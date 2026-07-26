package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"paper-knowledge-base/backend/internal/config"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type Server struct {
	cfg     config.Config
	db      *sql.DB
	limiter *limiter
}

type limiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func newLimiter() *limiter {
	return &limiter{attempts: make(map[string][]time.Time)}
}

func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	var kept []time.Time
	for _, t := range l.attempts[key] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		l.attempts[key] = kept
		return false
	}
	l.attempts[key] = append(kept, now)
	// 防止长期运行时 map 无限增长
	if len(l.attempts) > 10000 {
		for k, times := range l.attempts {
			expired := true
			for _, t := range times {
				if now.Sub(t) < window {
					expired = false
					break
				}
			}
			if expired {
				delete(l.attempts, k)
			}
		}
	}
	return true
}

func New(cfg config.Config, db *sql.DB) *Server {
	return &Server{cfg: cfg, db: db, limiter: newLimiter()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.health)
	mux.HandleFunc("/api/health/ready", s.ready)
	mux.HandleFunc("/api/setup/status", s.setupStatus)
	mux.HandleFunc("/api/setup/admin", s.setupAdmin)
	mux.HandleFunc("/api/auth/login", s.login)
	mux.HandleFunc("/api/auth/logout", s.logout)
	mux.HandleFunc("/api/auth/me", s.me)
	mux.HandleFunc("/api/dashboard", s.dashboard)
	mux.HandleFunc("/api/papers", s.papers)
	mux.HandleFunc("/api/papers/", s.paperByID)
	return s.withMiddleware(mux)
}

func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := randomToken(12)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'")
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := s.cfg.AllowedOrigins[origin]; ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Vary", "Origin")
			}
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type,X-CSRF-Token,X-Request-ID")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if requiresCSRF(r) && !s.validCSRF(r) {
			writeError(w, http.StatusForbidden, "csrf_failed", "request validation failed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "dependency_unavailable", "service is not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ready"})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	initialized, err := s.isInitialized(r.Context())
	if err != nil {
		writeError(w, 500, "internal_error", "unable to read setup state")
		return
	}
	data := map[string]any{"initialized": initialized}
	writeJSON(w, http.StatusOK, data)
}

type createAdminRequest struct{ Email, DisplayName, Password, SetupNonce string }

func (s *Server) setupAdmin(w http.ResponseWriter, r *http.Request) {
	var req createAdminRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.limiter.allow("setup-ip:"+clientIP(r), 10, time.Hour) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "please retry later")
		return
	}
	if subtleCompare(req.SetupNonce, s.cfg.SetupSecret) != 1 {
		writeError(w, 403, "invalid_setup_nonce", "setup authorization failed")
		return
	}
	if !validEmail(req.Email) || len(req.DisplayName) < 1 || len(req.DisplayName) > 80 || len(req.Password) < 12 || len(req.Password) > 256 {
		writeError(w, 400, "invalid_request", "invalid administrator fields")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), 12)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to secure password")
		return
	}
	now := time.Now().UTC()
	conn, err := s.db.Conn(r.Context())
	if err != nil {
		writeError(w, 500, "internal_error", "unable to initialize")
		return
	}
	defer conn.Close()
	tx, err := conn.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to initialize")
		return
	}
	defer tx.Rollback()
	var lockHeld int
	if err = tx.QueryRowContext(r.Context(), `SELECT GET_LOCK('paper_kb_setup', 10)`).Scan(&lockHeld); err != nil || lockHeld != 1 {
		writeError(w, http.StatusConflict, "setup_busy", "setup is already in progress")
		return
	}
	defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('paper_kb_setup')`)
	var count int
	if err = tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM admins FOR UPDATE`).Scan(&count); err != nil {
		writeError(w, 500, "internal_error", "unable to initialize")
		return
	}
	if count != 0 {
		writeError(w, 409, "setup_already_completed", "setup has already been completed")
		return
	}
	_, err = tx.ExecContext(r.Context(), `INSERT INTO admins(email,display_name,password_hash,email_verified_at,password_changed_at,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, strings.ToLower(strings.TrimSpace(req.Email)), req.DisplayName, string(hash), now, now, now, now)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO system_settings(setting_key,value_json,updated_at) VALUES('initialized', 'true', ?) ON DUPLICATE KEY UPDATE value_json='true', updated_at=VALUES(updated_at)`, now)
	}
	if err != nil {
		writeError(w, 409, "setup_already_completed", "setup has already been completed")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "internal_error", "unable to initialize")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"initialized": true})
}

type loginRequest struct{ Email, Password string }

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !validEmail(req.Email) || len(req.Password) == 0 {
		writeError(w, 401, "invalid_credentials", "invalid credentials")
		return
	}
	if !s.limiter.allow("login-ip:"+clientIP(r), s.cfg.LoginMaxFails*3, s.cfg.LoginWindow) || !s.limiter.allow("login-email:"+strings.ToLower(strings.TrimSpace(req.Email)), s.cfg.LoginMaxFails*3, s.cfg.LoginWindow) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "please retry later")
		return
	}
	var id uint64
	var display, passwordHash string
	var fails int
	var locked sql.NullTime
	err := s.db.QueryRowContext(r.Context(), `SELECT id,display_name,password_hash,failed_login_attempts,locked_until FROM admins WHERE email=?`, strings.ToLower(strings.TrimSpace(req.Email))).Scan(&id, &display, &passwordHash, &fails, &locked)
	if err != nil || (locked.Valid && locked.Time.After(time.Now())) || bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)) != nil {
		if err == nil {
			fails++
			_, _ = s.db.ExecContext(r.Context(), `UPDATE admins SET failed_login_attempts=?, locked_until=CASE WHEN ? >= ? THEN UTC_TIMESTAMP(6)+INTERVAL 10 MINUTE ELSE locked_until END WHERE id=?`, fails, fails, s.cfg.LoginMaxFails, id)
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
		return
	}
	now := time.Now().UTC()
	_, _ = s.db.ExecContext(r.Context(), `UPDATE admins SET failed_login_attempts=0, locked_until=NULL WHERE id=?`, id)
	token, csrf := randomToken(32), randomToken(32)
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO sessions(id,admin_id,token_hash,csrf_hash,ip_address,user_agent,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)`, uuid.NewString(), id, s.secureHash(token), s.secureHash(csrf), clientIP(r), r.UserAgent(), now.Add(s.cfg.SessionTTL), now)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to create session")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "pkb_session", Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: sameSite(s.cfg.CookieSameSite), MaxAge: int(s.cfg.SessionTTL.Seconds())})
	http.SetCookie(w, &http.Cookie{Name: "pkb_csrf", Value: csrf, Path: "/", HttpOnly: false, Secure: s.cfg.CookieSecure, SameSite: sameSite(s.cfg.CookieSameSite), MaxAge: int(s.cfg.SessionTTL.Seconds())})
	writeJSON(w, 200, map[string]any{"admin": map[string]any{"id": id, "displayName": display}})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if token, _ := r.Cookie("pkb_session"); token != nil {
		_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6) WHERE token_hash=?`, s.secureHash(token.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "pkb_session", Path: "/", MaxAge: -1, HttpOnly: true})
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(r)
	if !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var email, name string
	var created time.Time
	if err := s.db.QueryRowContext(r.Context(), `SELECT email,display_name,created_at FROM admins WHERE id=?`, id).Scan(&email, &name, &created); err != nil {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "email": email, "displayName": name, "createdAt": created})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var total, favorites, unread int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NULL`).Scan(&total)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NULL AND is_favorite=1`).Scan(&favorites)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NULL AND reading_status='unread'`).Scan(&unread)
	var storageBytes int64
	_ = s.db.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(size_bytes),0) FROM paper_files pf JOIN papers p ON p.id=pf.paper_id AND p.deleted_at IS NULL`).Scan(&storageBytes)
	writeJSON(w, http.StatusOK, map[string]any{
		"totalPapers":        total,
		"importedLast30Days": 0,
		"unread":             unread,
		"storageBytes":       storageBytes,
		"recent":             []any{},
		"stats":              map[string]int{"total": total, "favorites": favorites, "unread": unread},
	})
}

func (s *Server) papers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.listPapers(w, r)
		return
	}
	if r.Method == http.MethodPost {
		s.uploadPapers(w, r)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}
func (s *Server) listPapers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	pageSize := 20
	if n, _ := strconv.Atoi(r.URL.Query().Get("pageSize")); n > 0 {
		pageSize = n
	}
	if pageSize > s.cfg.SearchMaxPageSize {
		pageSize = s.cfg.SearchMaxPageSize
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	args := []any{}
	where := "deleted_at IS NULL"
	if q != "" {
		where += " AND MATCH(title, abstract_text) AGAINST(? IN BOOLEAN MODE)"
		args = append(args, q)
	}
	args = append(args, pageSize)
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,title,authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,added_at,updated_at FROM papers WHERE `+where+` ORDER BY added_at DESC LIMIT ?`, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to query papers")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, pageSize)
	for rows.Next() {
		var id, title, authors, status, parse string
		var doi, journal sql.NullString
		var published sql.NullTime
		var fav bool
		var added, updated time.Time
		if err := rows.Scan(&id, &title, &authors, &doi, &journal, &published, &status, &fav, &parse, &added, &updated); err != nil {
			continue
		}
		var authorList any
		_ = json.Unmarshal([]byte(authors), &authorList)
		items = append(items, map[string]any{"id": id, "title": title, "authors": authorList, "doi": doi.String, "journal": journal.String, "publishedAt": published, "readingStatus": status, "isFavorite": fav, "parseStatus": parse, "addedAt": added, "updatedAt": updated})
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": 1, "pageSize": pageSize, "total": len(items), "nextCursor": nil})
}

func (s *Server) uploadPapers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.UploadMaxBytes+1024*1024)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, 413, "file_too_large", "file exceeds configured limit")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	files := r.MultipartForm.File["files[]"]
	if len(files) == 0 {
		files = r.MultipartForm.File["files"]
	}
	if len(files) == 0 {
		writeError(w, 400, "invalid_request", "files is required")
		return
	}
	items := make([]map[string]any, 0, len(files))
	for _, fh := range files {
		item, err := s.saveUpload(r.Context(), fh)
		if err != nil {
			writeError(w, 400, "unsupported_media_type", err.Error())
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 202, map[string]any{"jobId": uuid.NewString(), "items": items})
}
func (s *Server) saveUpload(ctx context.Context, fh *multipart.FileHeader) (map[string]any, error) {
	if fh.Size <= 0 || fh.Size > s.cfg.UploadMaxBytes {
		return nil, fmt.Errorf("file size is not allowed")
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	allowed := map[string]bool{".pdf": true, ".docx": true, ".doc": true, ".odt": true, ".tex": true, ".txt": true, ".html": true}
	if !allowed[ext] {
		return nil, fmt.Errorf("unsupported file type")
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	if ext == ".pdf" && !strings.HasPrefix(string(head), "%PDF-") {
		return nil, fmt.Errorf("file signature mismatch")
	}
	if err := os.MkdirAll(s.cfg.UploadDir, 0700); err != nil {
		return nil, err
	}
	id := uuid.NewString()
	path := filepath.Join(s.cfg.UploadDir, id+ext)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(out, h), f); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	title := strings.TrimSuffix(filepath.Base(fh.Filename), ext)
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO papers(id,title,abstract_text,authors_json,journal,reading_status,parse_status,source_type,added_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, title, "", "[]", "", "unread", "queued", "upload", now, now)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO paper_files(id,paper_id,object_key,original_name,media_type,size_bytes,sha256,scan_status,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), id, id+ext, fh.Filename, fh.Header.Get("Content-Type"), fh.Size, hex.EncodeToString(h.Sum(nil)), "pending", now)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return map[string]any{"uploadId": id, "filename": fh.Filename, "status": "queued"}, nil
}

func (s *Server) paperByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/papers/")
	parts := strings.Split(strings.Trim(id, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, 404, "not_found", "paper not found")
		return
	}
	if len(parts) > 1 {
		if parts[1] == "download" || parts[1] == "preview" {
			s.fileResponse(w, r, parts[0])
			return
		}
	}
	if r.Method == http.MethodGet {
		s.getPaper(w, r, parts[0])
		return
	}
	if r.Method == http.MethodPatch {
		s.patchPaper(w, r, parts[0])
		return
	}
	if r.Method == http.MethodDelete {
		s.deletePaper(w, r, parts[0])
		return
	}
	w.WriteHeader(405)
}
func (s *Server) getPaper(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var title, abstract, authors, status, parse string
	var doi, journal sql.NullString
	var version int
	var fav bool
	var added, updated time.Time
	err := s.db.QueryRowContext(r.Context(), `SELECT title,abstract_text,authors_json,doi,journal,reading_status,is_favorite,parse_status,version,added_at,updated_at FROM papers WHERE id=? AND deleted_at IS NULL`, id).Scan(&title, &abstract, &authors, &doi, &journal, &status, &fav, &parse, &version, &added, &updated)
	if err != nil {
		writeError(w, 404, "not_found", "paper not found")
		return
	}
	var a any
	_ = json.Unmarshal([]byte(authors), &a)
	writeJSON(w, 200, map[string]any{"id": id, "title": title, "abstract": abstract, "authors": a, "doi": doi.String, "journal": journal.String, "readingStatus": status, "isFavorite": fav, "parseStatus": parse, "version": version, "addedAt": added, "updatedAt": updated})
}
func (s *Server) patchPaper(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var req struct {
		Version       int     `json:"version"`
		Title         *string `json:"title"`
		Abstract      *string `json:"abstract"`
		DOI           *string `json:"doi"`
		ReadingStatus *string `json:"readingStatus"`
		IsFavorite    *bool   `json:"isFavorite"`
	}
	if !decodeJSON(w, r, &req) || req.Version < 1 {
		return
	}
	sets := []string{}
	args := []any{}
	if req.Title != nil {
		sets = append(sets, "title=?")
		args = append(args, *req.Title)
	}
	if req.Abstract != nil {
		sets = append(sets, "abstract_text=?")
		args = append(args, *req.Abstract)
	}
	if req.DOI != nil {
		sets = append(sets, "doi=?")
		args = append(args, *req.DOI)
	}
	if req.ReadingStatus != nil {
		if *req.ReadingStatus != "unread" && *req.ReadingStatus != "reading" && *req.ReadingStatus != "read" {
			writeError(w, 400, "invalid_request", "invalid readingStatus")
			return
		}
		sets = append(sets, "reading_status=?")
		args = append(args, *req.ReadingStatus)
	}
	if req.IsFavorite != nil {
		sets = append(sets, "is_favorite=?")
		args = append(args, *req.IsFavorite)
	}
	if len(sets) == 0 {
		writeError(w, 400, "invalid_request", "no fields to update")
		return
	}
	sets = append(sets, "version=version+1", "updated_at=UTC_TIMESTAMP(6)")
	args = append(args, id, req.Version)
	res, err := s.db.ExecContext(r.Context(), `UPDATE papers SET `+strings.Join(sets, ",")+` WHERE id=? AND version=? AND deleted_at IS NULL`, args...)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to update paper")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, 409, "version_conflict", "paper was changed by another request")
		return
	}
	s.getPaper(w, r, id)
}
func (s *Server) deletePaper(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	_, err := s.db.ExecContext(r.Context(), `UPDATE papers SET deleted_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id=? AND deleted_at IS NULL`, id)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to delete paper")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) fileResponse(w http.ResponseWriter, r *http.Request, id string) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	var key, name, media string
	err := s.db.QueryRowContext(r.Context(), `SELECT f.object_key,f.original_name,f.media_type FROM paper_files f JOIN papers p ON p.id=f.paper_id AND p.deleted_at IS NULL WHERE f.paper_id=? ORDER BY f.created_at DESC LIMIT 1`, id).Scan(&key, &name, &media)
	if err != nil {
		writeError(w, 404, "not_found", "paper file not found")
		return
	}
	path := filepath.Join(s.cfg.UploadDir, filepath.Base(key))
	f, err := os.Open(path)
	if err != nil {
		writeError(w, 404, "not_found", "paper file not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", media)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	disp := "attachment"
	if strings.HasSuffix(r.URL.Path, "/preview") {
		if !strings.EqualFold(media, "application/pdf") || !strings.HasSuffix(strings.ToLower(key), ".pdf") {
			writeError(w, http.StatusUnsupportedMediaType, "preview_not_supported", "this file type must be downloaded")
			return
		}
		disp = "inline"
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"`, disp, strings.ReplaceAll(name, "\"", "")))
	io.Copy(w, f)
}

func (s *Server) isInitialized(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT JSON_UNQUOTE(value_json) FROM system_settings WHERE setting_key='initialized'`).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return value == "true" || value == "1", err
}
func (s *Server) authenticate(r *http.Request) (uint64, bool) {
	c, err := r.Cookie("pkb_session")
	if err != nil {
		return 0, false
	}
	var id uint64
	var expires time.Time
	err = s.db.QueryRowContext(r.Context(), `SELECT admin_id,expires_at FROM sessions WHERE token_hash=? AND revoked_at IS NULL`, s.secureHash(c.Value)).Scan(&id, &expires)
	return id, err == nil && expires.After(time.Now())
}
func validEmail(v string) bool {
	v = strings.TrimSpace(v)
	return len(v) >= 3 && len(v) <= 254 && strings.Contains(v, "@") && !strings.ContainsAny(v, "\r\n")
}
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func (s *Server) secureHash(v string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.JWTSecret))
	_, _ = mac.Write([]byte(v))
	return hex.EncodeToString(mac.Sum(nil))
}

// subtleCompare 以固定时间比较两个字符串,避免时序侧信道泄露 SETUP_SECRET。
func subtleCompare(a, b string) int {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	if hmac.Equal(ha[:], hb[:]) {
		return 1
	}
	return 0
}
func requiresCSRF(r *http.Request) bool {
	if r.Method != http.MethodPost && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		return false
	}
	switch r.URL.Path {
	case "/api/auth/login", "/api/setup/admin":
		return false
	}
	return true
}
func (s *Server) validCSRF(r *http.Request) bool {
	session, err := r.Cookie("pkb_session")
	if err != nil {
		return false
	}
	csrf, err := r.Cookie("pkb_csrf")
	if err != nil || csrf.Value == "" || !hmac.Equal([]byte(csrf.Value), []byte(r.Header.Get("X-CSRF-Token"))) {
		return false
	}
	var stored string
	err = s.db.QueryRowContext(r.Context(), `SELECT csrf_hash FROM sessions WHERE token_hash=? AND revoked_at IS NULL AND expires_at>UTC_TIMESTAMP(6)`, s.secureHash(session.Value)).Scan(&stored)
	return err == nil && hmac.Equal([]byte(stored), []byte(s.secureHash(csrf.Value)))
}
func sameSite(v string) http.SameSite {
	switch strings.ToLower(v) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	}
	return http.SameSiteLaxMode
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, 400, "invalid_request", "invalid JSON body")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "requestId": w.Header().Get("X-Request-ID")})
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}, "requestId": w.Header().Get("X-Request-ID")})
}
