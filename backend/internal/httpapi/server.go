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
	"net/url"
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
	mux.HandleFunc("/api/auth/password", s.changePassword)
	mux.HandleFunc("/api/dashboard", s.dashboard)
	mux.HandleFunc("/api/facets", s.facets)
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
	http.SetCookie(w, &http.Cookie{Name: "pkb_csrf", Path: "/", MaxAge: -1})
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
	var last30 int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NULL AND added_at >= UTC_TIMESTAMP(6) - INTERVAL 30 DAY`).Scan(&last30)
	recent := make([]map[string]any, 0, 5)
	if rows, err := s.db.QueryContext(r.Context(), `SELECT id,title,authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,added_at,updated_at FROM papers WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT 5`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, title, authors, status, parse string
			var doi, journal sql.NullString
			var published sql.NullTime
			var fav bool
			var added, updated time.Time
			if err := rows.Scan(&id, &title, &authors, &doi, &journal, &published, &status, &fav, &parse, &added, &updated); err != nil {
				continue
			}
			recent = append(recent, paperSummary(id, title, authors, doi, journal, published, status, parse, fav, added, updated))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"totalPapers":        total,
		"importedLast30Days": last30,
		"unread":             unread,
		"favorites":          favorites,
		"storageBytes":       storageBytes,
		"recent":             recent,
		"stats":              map[string]int{"total": total, "favorites": favorites, "unread": unread},
	})
}

// facets 提供分类浏览页需要的聚合数据（按年份、期刊、阅读状态统计）。
func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	years := make([]map[string]any, 0, 20)
	if rows, err := s.db.QueryContext(r.Context(), `SELECT YEAR(published_at) AS y, COUNT(*) FROM papers WHERE deleted_at IS NULL AND published_at IS NOT NULL GROUP BY y ORDER BY y DESC LIMIT 24`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var y, c int
			if err := rows.Scan(&y, &c); err == nil {
				years = append(years, map[string]any{"year": y, "count": c})
			}
		}
	}
	journals := make([]map[string]any, 0, 12)
	if rows, err := s.db.QueryContext(r.Context(), `SELECT journal, COUNT(*) AS c FROM papers WHERE deleted_at IS NULL AND journal <> '' GROUP BY journal ORDER BY c DESC, journal ASC LIMIT 12`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var c int
			if err := rows.Scan(&name, &c); err == nil {
				journals = append(journals, map[string]any{"name": name, "count": c})
			}
		}
	}
	statuses := map[string]int{"unread": 0, "reading": 0, "read": 0}
	if rows, err := s.db.QueryContext(r.Context(), `SELECT reading_status, COUNT(*) FROM papers WHERE deleted_at IS NULL GROUP BY reading_status`); err == nil {
		defer rows.Close()
		for rows.Next() {
			var name string
			var c int
			if err := rows.Scan(&name, &c); err == nil {
				statuses[name] = c
			}
		}
	}
	var favorites, missingYear int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NULL AND is_favorite=1`).Scan(&favorites)
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NULL AND published_at IS NULL`).Scan(&missingYear)
	writeJSON(w, http.StatusOK, map[string]any{"years": years, "journals": journals, "statuses": statuses, "favorites": favorites, "missingYear": missingYear})
}

// changePassword 修改管理员密码，成功后吊销全部会话（含当前会话），前端需重新登录。
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < 12 || len(req.NewPassword) > 256 {
		writeError(w, http.StatusBadRequest, "invalid_request", "new password must be 12-256 characters")
		return
	}
	if req.NewPassword == req.CurrentPassword {
		writeError(w, http.StatusBadRequest, "invalid_request", "new password must differ from the current one")
		return
	}
	if !s.limiter.allow(fmt.Sprintf("password:%d", id), 5, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, please retry later")
		return
	}
	var hash string
	if err := s.db.QueryRowContext(r.Context(), `SELECT password_hash FROM admins WHERE id=?`, id).Scan(&hash); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)) != nil {
		writeError(w, http.StatusForbidden, "invalid_credentials", "current password is incorrect")
		return
	}
	next, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to secure password")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE admins SET password_hash=?, password_changed_at=UTC_TIMESTAMP(6), updated_at=UTC_TIMESTAMP(6), token_version=token_version+1, failed_login_attempts=0, locked_until=NULL WHERE id=?`, string(next), id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to update password")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE sessions SET revoked_at=UTC_TIMESTAMP(6) WHERE admin_id=? AND revoked_at IS NULL`, id)
	http.SetCookie(w, &http.Cookie{Name: "pkb_session", Path: "/", MaxAge: -1, HttpOnly: true})
	http.SetCookie(w, &http.Cookie{Name: "pkb_csrf", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"passwordChanged": true, "sessionsRevoked": true})
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
	query := r.URL.Query()
	pageSize := 20
	if n, _ := strconv.Atoi(query.Get("pageSize")); n > 0 {
		pageSize = n
	}
	if pageSize > s.cfg.SearchMaxPageSize {
		pageSize = s.cfg.SearchMaxPageSize
	}
	page := 1
	if n, _ := strconv.Atoi(query.Get("page")); n > 1 {
		page = n
	}
	where := []string{"deleted_at IS NULL"}
	args := []any{}
	// 关键词用参数化 LIKE 匹配，兼容中文并避免 FULLTEXT BOOLEAN MODE 对特殊字符报错。
	if kw := strings.TrimSpace(query.Get("q")); kw != "" {
		if len(kw) > 200 {
			kw = kw[:200]
		}
		like := "%" + escapeLike(kw) + "%"
		where = append(where, "(title LIKE ? OR journal LIKE ? OR COALESCE(doi,'') LIKE ? OR authors_json LIKE ? OR abstract_text LIKE ?)")
		args = append(args, like, like, like, like, like)
	}
	if st := query.Get("status"); st == "unread" || st == "reading" || st == "read" {
		where = append(where, "reading_status=?")
		args = append(args, st)
	}
	if query.Get("favorite") == "true" {
		where = append(where, "is_favorite=1")
	}
	if y, err := strconv.Atoi(query.Get("yearFrom")); err == nil && y >= 1000 && y <= 3000 {
		where = append(where, "published_at >= ?")
		args = append(args, fmt.Sprintf("%04d-01-01", y))
	}
	if y, err := strconv.Atoi(query.Get("yearTo")); err == nil && y >= 1000 && y <= 3000 {
		where = append(where, "published_at <= ?")
		args = append(args, fmt.Sprintf("%04d-12-31", y))
	}
	order := "added_at DESC"
	switch query.Get("sort") {
	case "oldest":
		order = "added_at ASC"
	case "title":
		order = "title ASC"
	case "year":
		order = "published_at IS NULL, published_at DESC, added_at DESC"
	case "updated":
		order = "updated_at DESC"
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE `+clause, args...).Scan(&total); err != nil {
		writeError(w, 500, "internal_error", "unable to query papers")
		return
	}
	listArgs := append(append([]any{}, args...), pageSize, (page-1)*pageSize)
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,title,authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,added_at,updated_at FROM papers WHERE `+clause+` ORDER BY `+order+` LIMIT ? OFFSET ?`, listArgs...)
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
		items = append(items, paperSummary(id, title, authors, doi, journal, published, status, parse, fav, added, updated))
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, "internal_error", "unable to query papers")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

// paperSummary 统一列表与概览的论文字段，前端依赖 year 直接展示发表年份。
func paperSummary(id, title, authors string, doi, journal sql.NullString, published sql.NullTime, status, parse string, fav bool, added, updated time.Time) map[string]any {
	var authorList any
	if err := json.Unmarshal([]byte(authors), &authorList); err != nil {
		authorList = []any{}
	}
	var year any
	if published.Valid {
		year = published.Time.Year()
	}
	return map[string]any{
		"id": id, "title": title, "authors": authorList, "doi": doi.String, "journal": journal.String,
		"year": year, "readingStatus": status, "isFavorite": fav, "parseStatus": parse,
		"addedAt": added, "updatedAt": updated,
	}
}

// escapeLike 转义 LIKE 通配符，避免用户输入的 % 和 _ 变成模糊匹配。
func escapeLike(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "%", "\\%")
	return strings.ReplaceAll(v, "_", "\\_")
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
	// 逐个文件汇报结果，单个文件失败不影响其余文件入库。
	items := make([]map[string]any, 0, len(files))
	accepted := 0
	for _, fh := range files {
		item, err := s.saveUpload(r.Context(), fh)
		if err != nil {
			items = append(items, map[string]any{"filename": fh.Filename, "status": "rejected", "reason": err.Error()})
			continue
		}
		accepted++
		items = append(items, item)
	}
	if accepted == 0 {
		reason, _ := items[0]["reason"].(string)
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", reason)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"jobId": uuid.NewString(), "accepted": accepted, "rejected": len(items) - accepted, "items": items})
}
func (s *Server) saveUpload(ctx context.Context, fh *multipart.FileHeader) (map[string]any, error) {
	if fh.Size <= 0 || fh.Size > s.cfg.UploadMaxBytes {
		return nil, fmt.Errorf("file size is not allowed")
	}
	ext := strings.ToLower(filepath.Ext(fh.Filename))
	allowed := map[string]bool{".pdf": true, ".docx": true, ".doc": true, ".odt": true, ".tex": true, ".txt": true}
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
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.fileResponse(w, r, parts[0])
			return
		}
		writeError(w, 404, "not_found", "paper not found")
		return
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
	var published sql.NullTime
	var version int
	var fav bool
	var added, updated time.Time
	err := s.db.QueryRowContext(r.Context(), `SELECT title,abstract_text,authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,version,added_at,updated_at FROM papers WHERE id=? AND deleted_at IS NULL`, id).Scan(&title, &abstract, &authors, &doi, &journal, &published, &status, &fav, &parse, &version, &added, &updated)
	if err != nil {
		writeError(w, 404, "not_found", "paper not found")
		return
	}
	item := paperSummary(id, title, authors, doi, journal, published, status, parse, fav, added, updated)
	item["abstract"] = abstract
	item["version"] = version
	var fileName, mediaType, objectKey string
	var fileSize int64
	if err := s.db.QueryRowContext(r.Context(), `SELECT original_name,media_type,object_key,size_bytes FROM paper_files WHERE paper_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&fileName, &mediaType, &objectKey, &fileSize); err == nil {
		item["file"] = map[string]any{"originalName": fileName, "mediaType": mediaType, "sizeBytes": fileSize, "previewable": strings.HasSuffix(strings.ToLower(objectKey), ".pdf")}
	}
	writeJSON(w, 200, item)
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
	info, err := f.Stat()
	if err != nil {
		writeError(w, 404, "not_found", "paper file not found")
		return
	}
	isPDF := strings.HasSuffix(strings.ToLower(key), ".pdf")
	disp := "attachment"
	if strings.HasSuffix(r.URL.Path, "/preview") {
		// 只允许内联预览服务端已校验过魔数的 PDF，其它类型必须下载。
		if !isPDF {
			writeError(w, http.StatusUnsupportedMediaType, "preview_not_supported", "this file type must be downloaded")
			return
		}
		disp = "inline"
	}
	if isPDF {
		media = "application/pdf"
	} else if media == "" {
		media = "application/octet-stream"
	}
	w.Header().Set("Content-Type", media)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	// 文件名只保留安全字符，中文等非 ASCII 用 RFC 5987 的 filename* 传递。
	w.Header().Set("Content-Disposition", fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, disp, asciiFilename(name), url.PathEscape(name)))
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// asciiFilename 去掉 Content-Disposition 中不安全或非 ASCII 的字符，避免响应头注入。
func asciiFilename(name string) string {
	var b strings.Builder
	for _, ch := range name {
		if ch < 32 || ch > 126 || ch == '"' || ch == '\\' || ch == ';' {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(ch)
	}
	if b.Len() == 0 {
		return "download"
	}
	return b.String()
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
