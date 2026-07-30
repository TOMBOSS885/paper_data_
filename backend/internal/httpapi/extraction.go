package httpapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"paper-knowledge-base/backend/internal/pdfmeta"
)

func (s *Server) extractPapers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if err := r.ParseMultipartForm(s.cfg.UploadMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "unable to read uploaded files")
		return
	}
	fhs := r.MultipartForm.File["files"]
	if len(fhs) == 0 {
		writeJSON(w, http.StatusOK, []any{})
		return
	}

	type result struct {
		FileName string            `json:"fileName"`
		Meta     pdfmeta.Extracted `json:"meta"`
	}
	var results []result

	for _, fh := range fhs {
		ext := strings.ToLower(filepath.Ext(fh.Filename))
		if ext != ".pdf" {
			results = append(results, result{FileName: fh.Filename})
			continue
		}
		if fh.Size <= 0 || fh.Size > s.cfg.UploadMaxBytes {
			results = append(results, result{FileName: fh.Filename})
			continue
		}
		f, err := fh.Open()
		if err != nil {
			results = append(results, result{FileName: fh.Filename})
			continue
		}

		head := make([]byte, 512)
		n, _ := io.ReadFull(f, head)
		head = head[:n]
		if !strings.HasPrefix(string(head), "%PDF-") {
			f.Close()
			results = append(results, result{FileName: fh.Filename})
			continue
		}
		f.Seek(0, 0)
		raw, _ := io.ReadAll(f)
		f.Close()

		meta := pdfmeta.Extract(raw)
		results = append(results, result{FileName: fh.Filename, Meta: meta})
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) reextractPaper(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}

	var fileKey string
	err := s.db.QueryRowContext(r.Context(), `SELECT object_key FROM paper_files WHERE paper_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&fileKey)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "paper file not found")
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to read paper file")
		}
		return
	}

	path := filepath.Join(s.cfg.UploadDir, fileKey)
	raw, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to read paper file")
		return
	}

	meta := pdfmeta.Extract(raw)

	var params []any
	var sets []string

	if meta.Title != "" {
		sets = append(sets, "title=?")
		params = append(params, meta.Title)
	}
	if len(meta.Authors) > 0 {
		b, _ := json.Marshal(meta.Authors)
		sets = append(sets, "authors_json=?")
		params = append(params, string(b))
	}
	if meta.Year > 0 {
		sets = append(sets, "published_at=?")
		params = append(params, fmt.Sprintf("%04d-01-01", meta.Year))
	}
	if meta.Subject != "" {
		sets = append(sets, "journal=?")
		params = append(params, meta.Subject)
	}

	if len(sets) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"status": "no_changes_detected"})
		return
	}

	sets = append(sets, "updated_at=?")
	params = append(params, time.Now().UTC())
	params = append(params, id)

	q := "UPDATE papers SET " + strings.Join(sets, ",") + " WHERE id=?"
	_, err = s.db.ExecContext(r.Context(), q, params...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to update paper metadata")
		return
	}
	writeJSON(w, http.StatusOK, meta)
}
