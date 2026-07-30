package httpapi

import (
	"path/filepath"
	"testing"
	"time"

	"paper-knowledge-base/backend/internal/config"
)

func TestSafeObjectPath(t *testing.T) {
	root := t.TempDir()
	path, err := safeObjectPath(root, "7f686b73-5725-4cb3-b7ae-d380ee4b1880.pdf")
	if err != nil {
		t.Fatalf("valid generated key was rejected: %v", err)
	}
	want := filepath.Join(root, "7f686b73-5725-4cb3-b7ae-d380ee4b1880.pdf")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
}

func TestSafeObjectPathRejectsTraversalAndNestedKeys(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"", "../secret.pdf", `..\secret.pdf`, "nested/file.pdf", `nested\file.pdf`, filepath.Join(root, "absolute.pdf")} {
		if _, err := safeObjectPath(root, key); err == nil {
			t.Errorf("unsafe key %q was accepted", key)
		}
	}
}

func TestTrashExpirationBoundary(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	retention := 10 * 24 * time.Hour
	if trashExpired(now.Add(-retention).Add(time.Microsecond), now, retention) {
		t.Fatal("paper inside the recovery window was considered expired")
	}
	if !trashExpired(now.Add(-retention), now, retention) {
		t.Fatal("paper at the retention boundary was not considered expired")
	}
}

func TestTrashStageDirRequiresUUID(t *testing.T) {
	s := &Server{cfg: config.Config{UploadDir: t.TempDir()}}
	if _, err := s.trashStageDir("../escape"); err == nil {
		t.Fatal("invalid paper id was accepted as a staging directory")
	}
	path, err := s.trashStageDir("7f686b73-5725-4cb3-b7ae-d380ee4b1880")
	if err != nil {
		t.Fatalf("valid UUID was rejected: %v", err)
	}
	if filepath.Base(path) != "7f686b73-5725-4cb3-b7ae-d380ee4b1880" {
		t.Fatalf("unexpected staging path: %q", path)
	}
}
