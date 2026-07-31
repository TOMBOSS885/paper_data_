package httpapi

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"testing"

	"paper-knowledge-base/backend/internal/config"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestLimitedPrefixWriterCapsRetainedBytes(t *testing.T) {
	w := &limitedPrefixWriter{remaining: 4}
	if n, err := w.Write([]byte("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("Write() = (%d, %v), want (8, nil)", n, err)
	}
	if got := string(w.data); got != "abcd" {
		t.Fatalf("retained prefix = %q, want %q", got, "abcd")
	}
}

func TestUploadWorkLimiterIsGlobalAndBounded(t *testing.T) {
	s := &Server{}
	for i := 0; i < maxUploadWorkers; i++ {
		if err := s.acquireUploadSlot(context.Background()); err != nil {
			t.Fatalf("acquire slot %d: %v", i, err)
		}
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.acquireUploadSlot(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire beyond capacity returned %v, want context.Canceled", err)
	}
	for i := 0; i < maxUploadWorkers; i++ {
		s.releaseUploadSlot()
	}
}

func TestReserveUploadQuotaIncludesConcurrentReservations(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	query := regexp.QuoteMeta(`SELECT COALESCE(SUM(size_bytes),0) FROM paper_files`)
	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(0))
	mock.ExpectQuery(query).WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(0))
	s := &Server{cfg: config.Config{UploadQuotaBytes: 10}, db: db}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- s.reserveUploadQuota(context.Background(), 6)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful reservations = %d, want 1", successes)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveUploadRollsBackAndRemovesStagingFile(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(size_bytes),0) FROM paper_files`)).
		WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO papers").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO paper_files").WillReturnError(errors.New("injected file row failure"))
	mock.ExpectRollback()

	uploadDir := t.TempDir()
	s := &Server{
		cfg: config.Config{UploadDir: uploadDir, UploadMaxBytes: 1024, UploadQuotaBytes: 1024},
		db:  db,
	}
	fh := multipartFileHeader(t, "paper.txt", []byte("paper body"))
	if _, err := s.saveUpload(context.Background(), fh); err == nil {
		t.Fatal("saveUpload succeeded after the paper_files insert failed")
	}
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("upload directory contains %d files after rollback, want 0", len(entries))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSaveUploadRemovesFinalFileWhenCommitFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(size_bytes),0) FROM paper_files`)).
		WillReturnRows(sqlmock.NewRows([]string{"used"}).AddRow(0))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO papers").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO paper_files").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit().WillReturnError(errors.New("injected commit failure"))

	uploadDir := t.TempDir()
	s := &Server{
		cfg: config.Config{UploadDir: uploadDir, UploadMaxBytes: 1024, UploadQuotaBytes: 1024},
		db:  db,
	}
	fh := multipartFileHeader(t, "paper.txt", []byte("paper body"))
	if _, err := s.saveUpload(context.Background(), fh); err == nil {
		t.Fatal("saveUpload succeeded after commit failed")
	}
	entries, err := os.ReadDir(uploadDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("upload directory contains %d files after commit failure, want 0", len(entries))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func multipartFileHeader(t *testing.T, name string, data []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/papers", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = req.MultipartForm.RemoveAll() })
	return req.MultipartForm.File["files"][0]
}
