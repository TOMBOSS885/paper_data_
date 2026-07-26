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

	"paper-knowledge-base/backend/internal/citation"
	"paper-knowledge-base/backend/internal/pdfmeta"
)

func (s *Server) extractPapers(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(s.cfg.UploadMaxBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
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
	if !s.validCSRF(r) {
		writeError(w, http.StatusForbidden, "forbidden", "csrf validation failed")
		return
	}

	var fileKey string
	err := s.db.QueryRowContext(r.Context(), `SELECT object_key FROM paper_files WHERE paper_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&fileKey)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "paper file not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		}
		return
	}
	
	path := filepath.Join(s.cfg.UploadDir, fileKey)
	raw, err := os.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "fs_error", err.Error())
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
		writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) citePaper(w http.ResponseWriter, r *http.Request, id string) {
	formatName := r.URL.Query().Get("format")
	if formatName == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "format required")
		return
	}
	
	var tmpl string
	err := s.db.QueryRowContext(r.Context(), `SELECT template FROM citation_formats WHERE name=?`, formatName).Scan(&tmpl)
	if err != nil {
		if err == sql.ErrNoRows {
			writeError(w, http.StatusNotFound, "not_found", "citation format not found")
		} else {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
		}
		return
	}
	
	var title, authorsJSON, journal string
	var doi, publishedAt sql.NullString
	err = s.db.QueryRowContext(r.Context(), `SELECT title, authors_json, doi, journal, published_at FROM papers WHERE id=?`, id).Scan(&title, &authorsJSON, &doi, &journal, &publishedAt)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "paper not found")
		return
	}
	
	var authors []string
	json.Unmarshal([]byte(authorsJSON), &authors)
	
	var year int
	if publishedAt.Valid && len(publishedAt.String) >= 4 {
		fmt.Sscanf(publishedAt.String[:4], "%d", &year)
	}
	
	info := citation.PaperInfo{
		Title:   title,
		Authors: authors,
		Year:    year,
		DOI:     doi.String,
		Journal: journal,
	}
	
	res, err := citation.Format(tmpl, info)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "format_error", err.Error())
		return
	}
	
	writeJSON(w, http.StatusOK, map[string]string{"citation": res})
}

func (s *Server) citationFormats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := s.db.QueryContext(r.Context(), `SELECT id, name, builtin, template FROM citation_formats ORDER BY builtin DESC, name ASC`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		defer rows.Close()
		
		var list []map[string]any
		for rows.Next() {
			var id int64
			var name string
			var builtin int
			var template string
			if err := rows.Scan(&id, &name, &builtin, &template); err != nil {
				continue
			}
			list = append(list, map[string]any{
				"id": id,
				"name": name,
				"builtin": builtin == 1,
				"template": template,
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": list})
		return
	}
	
	if r.Method == http.MethodPost {
		if !s.validCSRF(r) {
			writeError(w, http.StatusForbidden, "forbidden", "csrf validation failed")
			return
		}
		var payload struct {
			Name     string `json:"name"`
			Template string `json:"template"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if len(payload.Name) == 0 || len(payload.Name) > 60 {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid name length")
			return
		}
		if len(payload.Template) == 0 || len(payload.Template) > 5000 {
			writeError(w, http.StatusBadRequest, "invalid_request", "invalid template length")
			return
		}
		now := time.Now().UTC()
		res, err := s.db.ExecContext(r.Context(), `INSERT INTO citation_formats(name, builtin, template, created_at, updated_at) VALUES(?,0,?,?,?)`, payload.Name, payload.Template, now, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		id, _ := res.LastInsertId()
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": payload.Name, "template": payload.Template, "builtin": false})
		return
	}
	
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *Server) citationFormatByID(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/citation/formats/")
	if r.Method == http.MethodPatch {
		if !s.validCSRF(r) {
			writeError(w, http.StatusForbidden, "forbidden", "csrf validation failed")
			return
		}
		var payload struct {
			Name     *string `json:"name"`
			Template *string `json:"template"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		
		var builtin int
		if err := s.db.QueryRowContext(r.Context(), `SELECT builtin FROM citation_formats WHERE id=?`, idStr).Scan(&builtin); err != nil {
			writeError(w, http.StatusNotFound, "not_found", "format not found")
			return
		}
		
		var sets []string
		var params []any
		if payload.Name != nil && builtin == 0 {
			sets = append(sets, "name=?")
			params = append(params, *payload.Name)
		}
		if payload.Template != nil {
			sets = append(sets, "template=?")
			params = append(params, *payload.Template)
		}
		if len(sets) == 0 {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		
		sets = append(sets, "updated_at=?")
		params = append(params, time.Now().UTC())
		params = append(params, idStr)
		
		_, err := s.db.ExecContext(r.Context(), "UPDATE citation_formats SET "+strings.Join(sets, ",")+" WHERE id=?", params...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	
	if r.Method == http.MethodDelete {
		if !s.validCSRF(r) {
			writeError(w, http.StatusForbidden, "forbidden", "csrf validation failed")
			return
		}
		_, err := s.db.ExecContext(r.Context(), `DELETE FROM citation_formats WHERE id=? AND builtin=0`, idStr)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "db_error", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
		return
	}
	
	w.WriteHeader(http.StatusMethodNotAllowed)
}
