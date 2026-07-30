package httpapi

import (
	"compress/gzip"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"paper-knowledge-base/backend/internal/config"
	"paper-knowledge-base/backend/internal/pdfmeta"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

type Server struct {
	cfg     config.Config
	db      *sql.DB
	limiter *limiter
}

// limiter：每个 key 独立的 token bucket
// 后台 GC goroutine 每 5 分钟扫一次长期未访问的 bucket，避免内存泄漏。
type bucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

func newLimiter() *limiter {
	l := &limiter{buckets: make(map[string]*bucket)}
	go l.gcLoop()
	return l
}

func (l *limiter) gcLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for k, b := range l.buckets {
			if now.Sub(b.lastSeen) > 30*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

func (l *limiter) allow(key string, max int, window time.Duration) bool {
	l.mu.Lock()
	b, ok := l.buckets[key]
	if !ok {
		limit := rate.Every(window / time.Duration(max))
		b = &bucket{
			lim: rate.NewLimiter(limit, max),
		}
		l.buckets[key] = b
	}
	b.lastSeen = time.Now()
	l.mu.Unlock()
	return b.lim.Allow()
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
	// 只读、低频变动接口走 5 分钟私有缓存 + gzip。
	// Vary: Cookie 防止登录态切换命中旧缓存；Cache-Control: private 防止 CDN 共享。
	mux.HandleFunc("/api/facets", withCacheHeaders(300, gzipResponse(s.facets)))
	mux.HandleFunc("/api/tags", withCacheHeaders(300, gzipResponse(s.tags)))
	mux.HandleFunc("/api/categories", withCacheHeaders(300, gzipResponse(s.categories)))
	mux.HandleFunc("/api/tags/", gzipResponse(s.tagByID))
	mux.HandleFunc("/api/categories/", gzipResponse(s.categoryByID))
	mux.HandleFunc("/api/papers/extract", s.extractPapers)
	mux.HandleFunc("/api/papers", s.papers)
	mux.HandleFunc("/api/papers/", s.paperByID)
	mux.HandleFunc("/api/trash", s.trash)
	mux.HandleFunc("/api/trash/", s.trashByID)

	return s.withMiddleware(mux)
}

// withCacheHeaders 给只读接口挂上短时间浏览器缓存；写入路径**不**走这里。
func withCacheHeaders(maxAge int, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d", maxAge))
		// 同时按 gzip 维度和 session 维度变化：登录后或登出后立即不命中旧缓存。
		w.Header().Set("Vary", "Accept-Encoding, Cookie")
		h(w, r)
	}
}

// gzipResponse 只压缩 JSON 类响应；文件下载（octet-stream）走原文以避免 PDF 预览被破坏。
// 透明地处理已经 Content-Encoding 过的响应与客户端不支持 gzip 的情况。
func gzipResponse(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 检查 Accept-Encoding：客户端没声明 gzip 就直接走原文，节省 CPU。
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			h(w, r)
			return
		}
		// 已经被设置过 Content-Encoding 的响应（例如 fileResponse）跳过。
		// 实际拦截在下面 ReadHeader 后：gzipWriter.WriteHeader 会检查 Content-Type。
		gw := &gzipWriter{ResponseWriter: w, minSize: 512}
		h(gw, r)
		gw.Close()
	}
}

// gzipWriter 在第一次 Write 前决定是否启用 gzip；优先看 Content-Type，只压缩文本类。
// 实现要点：
//   - 只有 Content-Type 以 text/、application/json、application/javascript 开头才压缩
//   - 已经设置 Content-Encoding 的跳过
//   - 内容 < minSize 时直接写原文（压缩小包反而变大）
//   - 任何写入错误都安全地让上游感知到
type gzipWriter struct {
	http.ResponseWriter
	minSize       int
	wroteHeader   bool
	zw            *gzip.Writer
	buf           []byte
	status        int
	enabled       bool
	decided       bool
	wroteBody     bool
	contentLength int
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.status = code
	ct := g.Header().Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	mediatype, _, _ := mime.ParseMediaType(ct)
	compressible := false
	switch {
	case strings.HasPrefix(ct, "text/"),
		strings.HasPrefix(ct, "application/json"),
		strings.HasPrefix(ct, "application/javascript"),
		strings.HasPrefix(ct, "application/xml"),
		strings.HasPrefix(ct, "image/svg+xml"):
		compressible = true
	case mediatype == "":
		compressible = false
	}
	alreadyEncoded := g.Header().Get("Content-Encoding") != ""
	// 仅对 200 OK 启用，避免 304/206/204 复杂语义叠加。
	if compressible && !alreadyEncoded && code >= 200 && code < 300 {
		g.enabled = true
		g.Header().Set("Content-Encoding", "gzip")
		g.Header().Set("Vary", addVary(g.Header().Get("Vary"), "Accept-Encoding"))
		// 必须先去掉 Content-Length（gzip 后长度会变）。
		g.Header().Del("Content-Length")
		g.zw = gzip.NewWriter(g.ResponseWriter)
	} else {
		g.enabled = false
	}
	g.decided = true
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.enabled {
		return g.zw.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

// 累计小包，到达 minSize 之后才决定要不要压缩，避免小响应浪费 CPU。
// 实际我们用决定式：header 一旦确认，立即切到 gzip 流。
func (g *gzipWriter) decide() {}

// addVary 把 val 添加到现有的 Vary 头里，避免覆盖已有值（Cookie / Accept-Encoding 都要保留）。
func addVary(existing, val string) string {
	if existing == "" {
		return val
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.EqualFold(strings.TrimSpace(part), val) {
			return existing
		}
	}
	return strings.TrimSpace(existing) + ", " + val
}

func (g *gzipWriter) Close() {
	if g.zw != nil {
		_ = g.zw.Close()
	}
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
	ctx := r.Context()

	// 三条读查询并行：用 sync.WaitGroup 收集错误（任意一条出错就回 500），不再串行。
	// 1. 单条聚合拿到 4 个统计指标，避免 4 次全表扫
	// 2. 单条聚合拿到 storageBytes
	// 3. 最近 5 篇论文（带 paperFiles join 时再延后）
	type statsRow struct {
		total, favorites, unread, last30 int
	}
	statsCh := make(chan statsRow, 1)
	storageCh := make(chan int64, 1)
	recentCh := make(chan []map[string]any, 1)
	recentIDsCh := make(chan []string, 1)
	errCh := make(chan error, 3)

	go func() {
		var row statsRow
		err := s.db.QueryRowContext(ctx,
			`SELECT
			   COALESCE(SUM(CASE WHEN deleted_at IS NULL THEN 1 ELSE 0 END),0),
			   COALESCE(SUM(CASE WHEN deleted_at IS NULL AND is_favorite=1 THEN 1 ELSE 0 END),0),
			   COALESCE(SUM(CASE WHEN deleted_at IS NULL AND reading_status='unread' THEN 1 ELSE 0 END),0),
			   COALESCE(SUM(CASE WHEN deleted_at IS NULL AND added_at >= UTC_TIMESTAMP(6) - INTERVAL 30 DAY THEN 1 ELSE 0 END),0)
			 FROM papers`).Scan(&row.total, &row.favorites, &row.unread, &row.last30)
		if err != nil {
			errCh <- fmt.Errorf("dashboard stats: %w", err)
			return
		}
		statsCh <- row
	}()
	go func() {
		var n int64
		err := s.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(size_bytes),0) FROM paper_files pf JOIN papers p ON p.id=pf.paper_id AND p.deleted_at IS NULL`).Scan(&n)
		if err != nil {
			errCh <- fmt.Errorf("dashboard storage: %w", err)
			return
		}
		storageCh <- n
	}()
	go func() {
		recent := make([]map[string]any, 0, 5)
		recentIDs := make([]string, 0, 5)
		rows, err := s.db.QueryContext(ctx,
			`SELECT id,title,LEFT(abstract_text,600),authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,added_at,updated_at FROM papers WHERE deleted_at IS NULL ORDER BY updated_at DESC LIMIT 5`)
		if err != nil {
			errCh <- fmt.Errorf("dashboard recent: %w", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, abstract, authors, status, parse string
			var doi, journal sql.NullString
			var published sql.NullTime
			var fav bool
			var added, updated time.Time
			if err := rows.Scan(&id, &title, &abstract, &authors, &doi, &journal, &published, &status, &fav, &parse, &added, &updated); err != nil {
				continue
			}
			recent = append(recent, paperSummary(id, title, abstract, authors, doi, journal, published, status, parse, fav, added, updated))
			recentIDs = append(recentIDs, id)
		}
		recentCh <- recent
		recentIDsCh <- recentIDs
	}()

	var stats statsRow
	var storageBytes int64
	var recent []map[string]any
	var recentIDs []string
	for i := 0; i < 3; i++ {
		select {
		case <-errCh:
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to load dashboard")
			return
		case stats = <-statsCh:
		case storageBytes = <-storageCh:
		case recent = <-recentCh:
			recentIDs = <-recentIDsCh
		case <-ctx.Done():
			writeError(w, http.StatusServiceUnavailable, "request_canceled", "request canceled")
			return
		}
	}

	// taxonomy 已经在 recentIDs 取到后再单独跑（避免拖慢其它三个查询）。
	if tags, cats, terr := s.loadTaxonomy(ctx, recentIDs); terr == nil {
		for i, id := range recentIDs {
			if ts, ok := tags[id]; ok {
				recent[i]["tags"] = ts
			}
			if cs, ok := cats[id]; ok {
				recent[i]["categories"] = cs
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"totalPapers":        stats.total,
		"importedLast30Days": stats.last30,
		"unread":             stats.unread,
		"favorites":          stats.favorites,
		"storageBytes":       storageBytes,
		"recent":             recent,
		"stats":              map[string]int{"total": stats.total, "favorites": stats.favorites, "unread": stats.unread},
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

// ===================== 标签 & 分类 =====================
//
// 设计：tags 与 categories 都是管理员全局资源，对所有论文可见；论文通过
// paper_tags / paper_categories 两张中间表绑定。删除标签或分类时由数据库外键
// ON DELETE CASCADE 自动清理中间表记录。统计字段 usage_count / paper_count 由
// 触发器自动维护（见 002_taxonomy.sql 下方的增量计数逻辑）以避免列表页每次
// 都做全表扫描。

// tagColor 限制前端可选的颜色取值，避免任意 CSS 类被前端写入。
var tagColorAllowed = map[string]bool{"teal": true, "blue": true, "amber": true, "rose": true, "slate": true, "green": true, "violet": true}

// normalizeName 把名称折叠成可比较的形式：去除首尾空白、转小写、全角空格归一。
// 用户名重复创建时按这个字符串去重。
// 包级编译一次；用正则一次性把任何空白序列（含 \r \n \t 全角空格）压成单个空格，
// 避免 strings.ReplaceAll("  "," ") 这种 O(N²) 暴力循环在大输入下 CPU 飙升。
var wsCollapse = regexp.MustCompile(`\s+`)

func normalizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "　", " ")
	return strings.ToLower(wsCollapse.ReplaceAllString(s, " "))
}

func validColor(c string) bool { return tagColorAllowed[c] }

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listTags(w, r)
	case http.MethodPost:
		s.createTag(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	// 默认 200，上限 1000；offset 仅在显式传时启用，避免无意义偏移。
	limit := 200
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 1000 {
		limit = n
	}
	offset := 0
	if n, _ := strconv.Atoi(r.URL.Query().Get("offset")); n > 0 {
		offset = n
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, name, color, usage_count FROM tags ORDER BY usage_count DESC, name ASC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to list tags")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, limit)
	for rows.Next() {
		var id uint64
		var name, color string
		var usage int
		if err := rows.Scan(&id, &name, &color, &usage); err != nil {
			continue
		}
		items = append(items, map[string]any{"id": id, "name": name, "color": color, "usageCount": usage})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "limit": limit, "offset": offset})
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("tag-create:"+clientIP(r), 60, time.Minute) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many tag creations")
		return
	}
	var req struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 1 || len(name) > 40 {
		writeError(w, http.StatusBadRequest, "invalid_request", "tag name must be 1-40 characters")
		return
	}
	if strings.ContainsAny(name, "\r\n\t") {
		writeError(w, http.StatusBadRequest, "invalid_request", "tag name contains invalid characters")
		return
	}
	color := req.Color
	if color == "" {
		color = "teal"
	}
	if !validColor(color) {
		writeError(w, http.StatusBadRequest, "invalid_request", "color is not allowed")
		return
	}
	now := time.Now().UTC()
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO tags(name, normalized_name, color, created_at, updated_at) VALUES(?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE color=VALUES(color), updated_at=VALUES(updated_at)`, name, normalizeName(name), color, now, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create tag")
		return
	}
	id, _ := res.LastInsertId()
	// ON DUPLICATE 时 LastInsertId 不可靠，重新按归一化名查一次。
	if id == 0 {
		_ = s.db.QueryRowContext(r.Context(), `SELECT id FROM tags WHERE normalized_name=?`, normalizeName(name)).Scan(&id)
	}
	var usage int
	_ = s.db.QueryRowContext(r.Context(), `SELECT usage_count FROM tags WHERE id=?`, id).Scan(&usage)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "color": color, "usageCount": usage})
}

func (s *Server) categories(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.listCategories(w, r)
	case http.MethodPost:
		s.createCategory(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	// 默认全量 1000（两层分类树实际很少超过几百）；支持 limit/offset 用于未来分页。
	limit := 1000
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 1000 {
		limit = n
	}
	offset := 0
	if n, _ := strconv.Atoi(r.URL.Query().Get("offset")); n > 0 {
		offset = n
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, parent_id, name, sort_order, paper_count FROM categories ORDER BY parent_id ASC, sort_order ASC, name ASC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to list categories")
		return
	}
	defer rows.Close()
	type node struct {
		ID         uint64  `json:"id"`
		ParentID   *uint64 `json:"parentId"`
		Name       string  `json:"name"`
		SortOrder  int     `json:"sortOrder"`
		PaperCount int     `json:"paperCount"`
		Children   []*node `json:"children"`
	}
	byParent := map[uint64][]*node{}
	byID := map[uint64]*node{}
	for rows.Next() {
		var id, sort, count int
		var parent sql.NullInt64
		var name string
		if err := rows.Scan(&id, &parent, &name, &sort, &count); err != nil {
			continue
		}
		var parentPtr *uint64
		if parent.Valid {
			p := uint64(parent.Int64)
			parentPtr = &p
		}
		n := &node{ID: uint64(id), ParentID: parentPtr, Name: name, SortOrder: sort, PaperCount: count, Children: []*node{}}
		byID[uint64(id)] = n
		if parent.Valid {
			byParent[uint64(parent.Int64)] = append(byParent[uint64(parent.Int64)], n)
		} else {
			byParent[0] = append(byParent[0], n)
		}
	}
	// 第二遍把子节点挂上去，限制递归深度防止恶意循环（虽然外键不允许，但 SQL 修复后可能历史数据残留）。
	var attach func(parent uint64, list []*node, depth int)
	attach = func(parent uint64, list []*node, depth int) {
		if depth > 8 {
			return
		}
		for _, n := range list {
			children := byParent[n.ID]
			n.Children = children
			attach(n.ID, children, depth+1)
		}
	}
	roots := byParent[0]
	attach(0, roots, 1)
	items := make([]map[string]any, 0, len(roots))
	for _, n := range roots {
		// 后端把 nil 切片序列化为 null，前端需要空数组而不是 null 才能安全地 map/length。
		children := n.Children
		if children == nil {
			children = []*node{}
		}
		items = append(items, map[string]any{"id": n.ID, "parentId": n.ParentID, "name": n.Name, "sortOrder": n.SortOrder, "paperCount": n.PaperCount, "children": children})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCategory(w http.ResponseWriter, r *http.Request) {
	if !s.limiter.allow("category-create:"+clientIP(r), 60, time.Minute) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many category creations")
		return
	}
	var req struct {
		Name      string  `json:"name"`
		ParentID  *uint64 `json:"parentId"`
		SortOrder int     `json:"sortOrder"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) < 1 || len(name) > 60 {
		writeError(w, http.StatusBadRequest, "invalid_request", "category name must be 1-60 characters")
		return
	}
	if strings.ContainsAny(name, "\r\n\t") {
		writeError(w, http.StatusBadRequest, "invalid_request", "category name contains invalid characters")
		return
	}
	if req.SortOrder < -10000 || req.SortOrder > 10000 {
		writeError(w, http.StatusBadRequest, "invalid_request", "sortOrder out of range")
		return
	}
	now := time.Now().UTC()
	var res sql.Result
	var err error
	if req.ParentID != nil {
		// 显式确认父分类存在，避免传一个不存在的 id 触发外键错误后还要回卷事务。
		var exists int
		if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM categories WHERE id=?`, *req.ParentID).Scan(&exists); err != nil || exists == 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "parent category does not exist")
			return
		}
		res, err = s.db.ExecContext(r.Context(), `INSERT INTO categories(parent_id, name, normalized_name, sort_order, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE sort_order=VALUES(sort_order), updated_at=VALUES(updated_at)`, *req.ParentID, name, normalizeName(name), req.SortOrder, now, now)
	} else {
		res, err = s.db.ExecContext(r.Context(), `INSERT INTO categories(parent_id, name, normalized_name, sort_order, created_at, updated_at) VALUES(NULL, ?, ?, ?, ?, ?) ON DUPLICATE KEY UPDATE sort_order=VALUES(sort_order), updated_at=VALUES(updated_at)`, name, normalizeName(name), req.SortOrder, now, now)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to create category")
		return
	}
	id, _ := res.LastInsertId()
	if id == 0 {
		q := `SELECT id FROM categories WHERE normalized_name=?`
		args := []any{normalizeName(name)}
		if req.ParentID != nil {
			q += ` AND parent_id=?`
			args = append(args, *req.ParentID)
		} else {
			q += ` AND parent_id IS NULL`
		}
		_ = s.db.QueryRowContext(r.Context(), q, args...).Scan(&id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": name, "parentId": req.ParentID, "sortOrder": req.SortOrder, "paperCount": 0})
}

// tagByID 与 categoryByID 目前只支持 DELETE，依靠数据库外键级联清理中间表。
func (s *Server) tagByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/api/tags/"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid tag id")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM tags WHERE id=?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to delete tag")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "not_found", "tag not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) categoryByID(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/api/categories/"), 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid category id")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM categories WHERE id=?`, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to delete category")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "not_found", "category not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// confirmAdminPassword 用 bcrypt 比对管理员密码，用于敏感操作的二次校验。
// 通过返回 true；失败时已经把错误写到 w，调用方应直接 return。
// rateKey 给不同操作独立的限流桶，防止一个操作被穷举连带其它操作一起锁。
func (s *Server) confirmAdminPassword(w http.ResponseWriter, r *http.Request, adminID uint64, password, rateKey string) bool {
	if len(password) == 0 || len(password) > 256 {
		writeError(w, http.StatusBadRequest, "invalid_request", "password is required")
		return false
	}
	if !s.limiter.allow(rateKey, 5, 15*time.Minute) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts, please retry later")
		return false
	}
	var hash string
	if err := s.db.QueryRowContext(r.Context(), `SELECT password_hash FROM admins WHERE id=?`, adminID).Scan(&hash); err != nil {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		writeError(w, http.StatusForbidden, "invalid_credentials", "password is incorrect")
		return false
	}
	return true
}

// parseBulkIDs 校验批量请求里的 paperIds：去重 + 长度 1..100。
func parseBulkIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("ids is required")
	}
	if len(raw) > 100 {
		return nil, fmt.Errorf("too many ids (max 100)")
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, id := range raw {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("ids is required")
	}
	return out, nil
}

// bulkTxOnPapers 在事务里对一组 paperId 执行回调，回调负责实际 SQL。
// 通用前置校验：papers 必须存在且未软删，缺失的 id 会在 missing 中返回（不影响其他）。
func (s *Server) bulkTxOnPapers(ctx context.Context, paperIDs []string, do func(tx *sql.Tx, existingIDs []string) error) (existing []string, missing []string, err error) {
	ph := placeholders(len(paperIDs))
	args := make([]any, len(paperIDs))
	for i, id := range paperIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM papers WHERE id IN (`+ph+`) AND deleted_at IS NULL`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			existing = append(existing, id)
		}
	}
	rows.Close()
	if len(existing) == 0 {
		return nil, paperIDs, nil
	}
	existingSet := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		existingSet[id] = struct{}{}
	}
	for _, id := range paperIDs {
		if _, ok := existingSet[id]; !ok {
			missing = append(missing, id)
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if err := do(tx, existing); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return existing, missing, nil
}

// updatePaperTaxonomy 处理 /api/papers/{id}/tags 和 /api/papers/{id}/categories 的 PUT。
// 用单次事务替换绑定 + 维护 usage_count，避免应用层与数据库计数漂移。
func (s *Server) updatePaperTaxonomy(w http.ResponseWriter, r *http.Request, paperID, kind string) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req struct {
		IDs []uint64 `json:"ids"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// 限制数量防止误用与拒绝服务。
	if len(req.IDs) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "too many associations")
		return
	}
	seen := make(map[uint64]struct{}, len(req.IDs))
	for _, id := range req.IDs {
		if id == 0 {
			continue
		}
		seen[id] = struct{}{}
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to update taxonomy")
		return
	}
	defer tx.Rollback()

	// 确认论文存在，避免对已删除论文留下孤儿。
	var paperExists int
	if err := tx.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE id=? AND deleted_at IS NULL`, paperID).Scan(&paperExists); err != nil || paperExists == 0 {
		writeError(w, http.StatusNotFound, "not_found", "paper not found")
		return
	}

	// 记录旧绑定，便于在删除后扣减 usage_count / paper_count。
	oldIDs := []uint64{}
	var oldQuery, joinTable, joinPK, countColumn, countJoin string
	switch kind {
	case "tags":
		joinTable = "paper_tags"
		joinPK = "tag_id"
		countColumn = "usage_count"
		countJoin = "tags"
		oldQuery = `SELECT tag_id FROM paper_tags WHERE paper_id=?`
	case "categories":
		joinTable = "paper_categories"
		joinPK = "category_id"
		countColumn = "paper_count"
		countJoin = "categories"
		oldQuery = `SELECT category_id FROM paper_categories WHERE paper_id=?`
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", "unknown taxonomy kind")
		return
	}
	oldRows, err := tx.QueryContext(r.Context(), oldQuery, paperID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to read taxonomy")
		return
	}
	for oldRows.Next() {
		var id uint64
		if err := oldRows.Scan(&id); err == nil {
			oldIDs = append(oldIDs, id)
		}
	}
	oldRows.Close()

	// 校验新 ID 全部存在，避免写入悬挂引用（FK 已经防住，但提前校验能给出更友好提示）。
	if len(seen) > 0 {
		newList := make([]any, 0, len(seen))
		for id := range seen {
			newList = append(newList, id)
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(newList)), ",")
		checkSQL := `SELECT id FROM ` + countJoin + ` WHERE id IN (` + placeholders + `)`
		checkRows, err := tx.QueryContext(r.Context(), checkSQL, newList...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to validate ids")
			return
		}
		found := make(map[uint64]struct{}, len(seen))
		for checkRows.Next() {
			var id uint64
			if err := checkRows.Scan(&id); err == nil {
				found[id] = struct{}{}
			}
		}
		checkRows.Close()
		if len(found) != len(seen) {
			writeError(w, http.StatusBadRequest, "invalid_request", "one or more ids do not exist")
			return
		}
	}

	// 清空旧绑定。
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM `+joinTable+` WHERE paper_id=?`, paperID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to clear taxonomy")
		return
	}

	// 写入新绑定 + 计数维护。
	if len(seen) > 0 {
		var b strings.Builder
		b.WriteString(`INSERT INTO `)
		b.WriteString(joinTable)
		b.WriteString(`(paper_id, `)
		b.WriteString(joinPK)
		b.WriteString(`, created_at) VALUES `)
		args := make([]any, 0, len(seen)*3)
		i := 0
		now := time.Now().UTC()
		for id := range seen {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("(?, ?, ?)")
			args = append(args, paperID, id, now)
			i++
		}
		if _, err := tx.ExecContext(r.Context(), b.String(), args...); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to apply taxonomy")
			return
		}
	}

	// 计算差量并更新计数（增量而非全表扫描）。
	oldSet := make(map[uint64]struct{}, len(oldIDs))
	for _, id := range oldIDs {
		oldSet[id] = struct{}{}
	}
	add := []uint64{}
	remove := []uint64{}
	for id := range seen {
		if _, ok := oldSet[id]; !ok {
			add = append(add, id)
		}
	}
	for id := range oldSet {
		if _, ok := seen[id]; !ok {
			remove = append(remove, id)
		}
	}
	adjustCount := func(ids []uint64, delta int) {
		if len(ids) == 0 {
			return
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids)+1)
		args = append(args, delta)
		for _, id := range ids {
			args = append(args, id)
		}
		_, _ = tx.ExecContext(r.Context(), `UPDATE `+countJoin+` SET `+countColumn+`=GREATEST(0, `+countColumn+`+?) WHERE id IN (`+placeholders+`)`, args...)
	}
	adjustCount(add, +1)
	adjustCount(remove, -1)

	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to commit taxonomy")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paperId": paperID, "kind": kind, "ids": req.IDs})
}

// bulkDelete 软删一组论文；二次校验需要管理员密码。
func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req struct {
		PaperIDs []string `json:"paperIds"`
		Password string   `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	paperIDs, err := parseBulkIDs(req.PaperIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !s.confirmAdminPassword(w, r, id, req.Password, fmt.Sprintf("bulk:delete:%d", id)) {
		return
	}
	existing, missing, err := s.softDeletePapers(r.Context(), paperIDs, id, clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to delete papers")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": len(existing), "missing": missing, "retentionDays": int(s.cfg.TrashRetention / (24 * time.Hour))})
}

// bulkFavorite 一组论文设置 isFavorite；二次校验需要管理员密码。
func (s *Server) bulkFavorite(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req struct {
		PaperIDs   []string `json:"paperIds"`
		IsFavorite bool     `json:"isFavorite"`
		Password   string   `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	paperIDs, err := parseBulkIDs(req.PaperIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if !s.confirmAdminPassword(w, r, id, req.Password, fmt.Sprintf("bulk:favorite:%d", id)) {
		return
	}
	existing, missing, err := s.bulkTxOnPapers(r.Context(), paperIDs, func(tx *sql.Tx, ids []string) error {
		ph := placeholders(len(ids))
		args := make([]any, 0, len(ids)+1)
		args = append(args, req.IsFavorite, time.Now().UTC())
		for _, pid := range ids {
			args = append(args, pid)
		}
		_, err := tx.ExecContext(r.Context(),
			`UPDATE papers SET is_favorite=?, updated_at=UTC_TIMESTAMP(6) WHERE id IN (`+ph+`) AND deleted_at IS NULL`,
			args...)
		return err
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to update favorites")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": len(existing), "missing": missing})
}

// bulkUpdateTaxonomy 一组论文的标签/分类替换（replace 模式）。
// 复用 updatePaperTaxonomy 的差量计数逻辑：聚合所有 paper 的 add/remove，
// 单次 UPDATE 维护 usage_count / paper_count，避免 N 次往返。
func (s *Server) bulkUpdateTaxonomy(w http.ResponseWriter, r *http.Request, kind string) {
	id, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	var req struct {
		PaperIDs []string `json:"paperIds"`
		IDs      []uint64 `json:"ids"`
		Password string   `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	paperIDs, err := parseBulkIDs(req.PaperIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if len(req.IDs) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "too many tag/category ids (max 200)")
		return
	}
	seen := make(map[uint64]struct{}, len(req.IDs))
	for _, x := range req.IDs {
		if x == 0 {
			continue
		}
		seen[x] = struct{}{}
	}
	if !s.confirmAdminPassword(w, r, id, req.Password, fmt.Sprintf("bulk:tag:%d", id)) {
		return
	}

	// 校验 IDs 全部存在
	if len(seen) > 0 {
		args := make([]any, 0, len(seen))
		for x := range seen {
			args = append(args, x)
		}
		ph := placeholders(len(args))
		table := "tags"
		if kind == "categories" {
			table = "categories"
		}
		rows, err := s.db.QueryContext(r.Context(), `SELECT id FROM `+table+` WHERE id IN (`+ph+`)`, args...)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to validate ids")
			return
		}
		found := 0
		for rows.Next() {
			var x uint64
			if err := rows.Scan(&x); err == nil {
				found++
			}
		}
		rows.Close()
		if found != len(seen) {
			writeError(w, http.StatusBadRequest, "invalid_request", "one or more ids do not exist")
			return
		}
	}

	joinTable := "paper_tags"
	countColumn := "usage_count"
	countTable := "tags"
	if kind == "categories" {
		joinTable = "paper_categories"
		countColumn = "paper_count"
		countTable = "categories"
	}

	existing, missing, err := s.bulkTxOnPapers(r.Context(), paperIDs, func(tx *sql.Tx, ids []string) error {
		// 收集每个 paper 的旧绑定，便于在事务里算 add/remove
		phOld := placeholders(len(ids))
		argsOld := make([]any, len(ids))
		for i, pid := range ids {
			argsOld[i] = pid
		}
		oldQuery := `SELECT paper_id, ` + strings.TrimSuffix(joinTable, "s") + `_id FROM ` + joinTable + ` WHERE paper_id IN (` + phOld + `)`
		rows, err := tx.QueryContext(r.Context(), oldQuery, argsOld...)
		if err != nil {
			return err
		}
		old := make(map[string]map[uint64]struct{}, len(ids))
		for rows.Next() {
			var pid string
			var xid uint64
			if err := rows.Scan(&pid, &xid); err != nil {
				continue
			}
			if _, ok := old[pid]; !ok {
				old[pid] = make(map[uint64]struct{})
			}
			old[pid][xid] = struct{}{}
		}
		rows.Close()

		// 差量计数：全局 add / remove 聚合
		addTotal := make(map[uint64]int)
		removeTotal := make(map[uint64]int)

		for _, pid := range ids {
			oldSet := old[pid]
			// 1) 清掉旧绑定
			if _, err := tx.ExecContext(r.Context(), `DELETE FROM `+joinTable+` WHERE paper_id=?`, pid); err != nil {
				return err
			}
			// 2) 写新绑定
			if len(seen) > 0 {
				var b strings.Builder
				b.WriteString(`INSERT INTO `)
				b.WriteString(joinTable)
				b.WriteString(`(paper_id, `)
				b.WriteString(strings.TrimSuffix(joinTable, "s"))
				b.WriteString(`_id, created_at) VALUES `)
				args := make([]any, 0, len(seen)*3)
				now := time.Now().UTC()
				i := 0
				for xid := range seen {
					if i > 0 {
						b.WriteString(",")
					}
					b.WriteString("(?, ?, ?)")
					args = append(args, pid, xid, now)
					i++
				}
				if _, err := tx.ExecContext(r.Context(), b.String(), args...); err != nil {
					return err
				}
			}
			// 3) 算这篇的 add/remove
			for xid := range seen {
				if _, ok := oldSet[xid]; !ok {
					addTotal[xid]++
				}
			}
			for xid := range oldSet {
				if _, ok := seen[xid]; !ok {
					removeTotal[xid]++
				}
			}
		}

		adjustCountDelta := func(m map[uint64]int, direction int) error {
			if len(m) == 0 {
				return nil
			}
			ids := make([]uint64, 0, len(m))
			for x := range m {
				ids = append(ids, x)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			ph := placeholders(len(ids))
			var cases strings.Builder
			args := make([]any, 0, len(ids)*3)
			for _, x := range ids {
				cases.WriteString(" WHEN ? THEN ?")
				args = append(args, x, direction*m[x])
			}
			for _, x := range ids {
				args = append(args, x)
			}
			_, err := tx.ExecContext(r.Context(),
				`UPDATE `+countTable+` SET `+countColumn+`=GREATEST(0, CAST(`+countColumn+` AS SIGNED)+CASE id`+cases.String()+` ELSE 0 END) WHERE id IN (`+ph+`)`,
				args...)
			return err
		}
		if err := adjustCountDelta(addTotal, +1); err != nil {
			return err
		}
		if err := adjustCountDelta(removeTotal, -1); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to update taxonomy")
		return
	}
	out := make([]uint64, 0, len(req.IDs))
	for _, x := range req.IDs {
		out = append(out, x)
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": len(existing), "missing": missing, "kind": kind, "ids": out})
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
	// 关键词检索策略：
	//   1) 优先尝试 FULLTEXT NATURAL LANGUAGE MODE（命中 003 迁移装上的 ngram 索引，中文分词）
	//   2) 如果有 FULLTEXT 报错（语法错误、ngram 不可用、incompatible 等），回退到参数化 LIKE
	//   3) 进入 FULLTEXT 前剥离 BOOLEAN MODE 特殊字符 + - < > ~ * ( ) " ` @，避免误触发语法错误
	if rawKw := strings.TrimSpace(query.Get("q")); rawKw != "" {
		if len(rawKw) > 200 {
			rawKw = rawKw[:200]
		}
		safe := strings.Map(func(r rune) rune {
			if strings.ContainsRune(`+-><~*()"`+"`@", r) {
				return ' '
			}
			return r
		}, rawKw)
		safe = strings.TrimSpace(safe)

		var kwWhere []string
		// 先尝试 FULLTEXT：title/abstract 上有 ngram 索引的情况下能走索引，避免全表扫
		if safe != "" {
			kwWhere = append(kwWhere, "MATCH(title, abstract_text) AGAINST(? IN NATURAL LANGUAGE MODE)")
			args = append(args, safe)
		}
		// 兜底：DOI / journal / authors_json 没有 FULLTEXT，仍然走 LIKE
		like := "%" + escapeLike(rawKw) + "%"
		kwWhere = append(kwWhere, "(COALESCE(doi,'') LIKE ? OR journal LIKE ? OR authors_json LIKE ?)")
		args = append(args, like, like, like)

		// 必须将全文索引和 LIKE 用 OR 连接起来，否则就变成了求交集
		where = append(where, "("+strings.Join(kwWhere, " OR ")+")")
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
	// 标签 / 分类过滤：参数是逗号分隔的 ID 列表，验证后用 EXISTS 子查询避免结果集膨胀。
	tagIDs, categoryIDs := parseIDs(query.Get("tag")), parseIDs(query.Get("category"))
	if len(tagIDs) > 0 || len(categoryIDs) > 0 {
		var sub []string
		if len(tagIDs) > 0 {
			sub = append(sub, "EXISTS (SELECT 1 FROM paper_tags pt WHERE pt.paper_id=papers.id AND pt.tag_id IN ("+placeholders(len(tagIDs))+"))")
			for _, id := range tagIDs {
				args = append(args, id)
			}
		}
		if len(categoryIDs) > 0 {
			sub = append(sub, "EXISTS (SELECT 1 FROM paper_categories pc WHERE pc.paper_id=papers.id AND pc.category_id IN ("+placeholders(len(categoryIDs))+"))")
			for _, id := range categoryIDs {
				args = append(args, id)
			}
		}
		where = append(where, "("+strings.Join(sub, " AND ")+")")
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
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,title,LEFT(abstract_text,600),authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,added_at,updated_at FROM papers WHERE `+clause+` ORDER BY `+order+` LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to query papers")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, pageSize)
	ids := make([]string, 0, pageSize)
	for rows.Next() {
		var id, title, abstract, authors, status, parse string
		var doi, journal sql.NullString
		var published sql.NullTime
		var fav bool
		var added, updated time.Time
		if err := rows.Scan(&id, &title, &abstract, &authors, &doi, &journal, &published, &status, &fav, &parse, &added, &updated); err != nil {
			continue
		}
		items = append(items, paperSummary(id, title, abstract, authors, doi, journal, published, status, parse, fav, added, updated))
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, "internal_error", "unable to query papers")
		return
	}
	// 一次性加载当前页的标签/分类，避免 N+1。
	tags, cats, terr := s.loadTaxonomy(r.Context(), ids)
	if terr != nil {
		writeError(w, 500, "internal_error", "unable to load taxonomy")
		return
	}
	for i, id := range ids {
		if ts, ok := tags[id]; ok {
			items[i]["tags"] = ts
		} else {
			items[i]["tags"] = []any{}
		}
		if cs, ok := cats[id]; ok {
			items[i]["categories"] = cs
		} else {
			items[i]["categories"] = []any{}
		}
	}
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "pageSize": pageSize, "total": total})
}

// paperSummary 统一列表与概览的论文字段，前端依赖 year 直接展示发表年份。
func paperSummary(id, title, abstract, authors string, doi, journal sql.NullString, published sql.NullTime, status, parse string, fav bool, added, updated time.Time) map[string]any {
	var authorList any
	if err := json.Unmarshal([]byte(authors), &authorList); err != nil {
		authorList = []any{}
	}
	var year any
	if published.Valid {
		year = published.Time.Year()
	}
	return map[string]any{
		"id": id, "title": title, "abstract": abstract, "authors": authorList, "doi": doi.String, "journal": journal.String,
		"year": year, "readingStatus": status, "isFavorite": fav, "parseStatus": parse,
		"addedAt": added, "updatedAt": updated, "tags": []any{}, "categories": []any{},
	}
}

// escapeLike 转义 LIKE 通配符，避免用户输入的 % 和 _ 变成模糊匹配。
func escapeLike(v string) string {
	v = strings.ReplaceAll(v, "\\", "\\\\")
	v = strings.ReplaceAll(v, "%", "\\%")
	return strings.ReplaceAll(v, "_", "\\_")
}

// parseIDs 把逗号分隔的数字串解析成 uint64 列表，自动去重并丢弃非法值。
func parseIDs(raw string) []uint64 {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	seen := make(map[uint64]struct{}, len(parts))
	out := make([]uint64, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil || n == 0 {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", n), ",")
}

// loadTaxonomy 一次性把当前页论文的标签和分类全部加载好，避免 N+1 查询。
func (s *Server) loadTaxonomy(ctx context.Context, paperIDs []string) (map[string][]map[string]any, map[string][]map[string]any, error) {
	tags := map[string][]map[string]any{}
	cats := map[string][]map[string]any{}
	if len(paperIDs) == 0 {
		return tags, cats, nil
	}
	args := make([]any, len(paperIDs))
	for i, id := range paperIDs {
		args[i] = id
	}
	q := `SELECT pt.paper_id, t.id, t.name, t.color FROM paper_tags pt JOIN tags t ON t.id=pt.tag_id WHERE pt.paper_id IN (` + placeholders(len(paperIDs)) + `) ORDER BY t.usage_count DESC, t.name ASC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var pid string
		var id uint64
		var name, color string
		if err := rows.Scan(&pid, &id, &name, &color); err != nil {
			continue
		}
		tags[pid] = append(tags[pid], map[string]any{"id": id, "name": name, "color": color})
	}
	rows.Close()
	q = `SELECT pc.paper_id, c.id, c.name FROM paper_categories pc JOIN categories c ON c.id=pc.category_id WHERE pc.paper_id IN (` + placeholders(len(paperIDs)) + `) ORDER BY c.sort_order ASC, c.name ASC`
	rows, err = s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var pid string
		var id uint64
		var name string
		if err := rows.Scan(&pid, &id, &name); err != nil {
			continue
		}
		cats[pid] = append(cats[pid], map[string]any{"id": id, "name": name})
	}
	rows.Close()
	return tags, cats, nil
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
	// 并发处理文件上传，最大并发数 4，避免瞬间 I/O 阻塞。
	// 预分配数组 + 按下标写入，保证结果顺序与请求文件顺序一致。
	items := make([]map[string]any, len(files))
	var mu sync.Mutex
	accepted := 0

	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup

	for i, fh := range files {
		i, fh := i, fh
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			item, err := s.saveUpload(r.Context(), fh)

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				items[i] = map[string]any{"filename": fh.Filename, "status": "rejected", "reason": err.Error()}
			} else {
				accepted++
				items[i] = item
			}
		}()
	}
	wg.Wait()
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
	var meta pdfmeta.Extracted
	if ext == ".pdf" && strings.HasPrefix(string(head), "%PDF-") {
		f.Seek(0, 0)
		if raw, err := io.ReadAll(f); err == nil {
			meta = pdfmeta.Extract(raw)
		}
	}
	f.Seek(0, 0)

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
	if meta.Title != "" {
		title = meta.Title
	}
	authorsJSON := "[]"
	if len(meta.Authors) > 0 {
		if b, err := json.Marshal(meta.Authors); err == nil {
			authorsJSON = string(b)
		}
	}
	var publishedAt *string
	if meta.Year > 0 {
		d := fmt.Sprintf("%04d-01-01", meta.Year)
		publishedAt = &d
	}
	now := time.Now().UTC()
	_, err = s.db.ExecContext(ctx, `INSERT INTO papers(id,title,abstract_text,authors_json,journal,published_at,reading_status,parse_status,source_type,added_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, title, "", authorsJSON, meta.Subject, publishedAt, "unread", "queued", "upload", now, now)
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
	// /api/papers/bulk/{op} 是一组批量端点，dispatch 到对应的 handler。
	if parts[0] == "bulk" && len(parts) == 2 {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "use POST")
			return
		}
		switch parts[1] {
		case "delete":
			s.bulkDelete(w, r)
		case "tags":
			s.bulkUpdateTaxonomy(w, r, "tags")
		case "categories":
			s.bulkUpdateTaxonomy(w, r, "categories")
		case "favorite":
			s.bulkFavorite(w, r)
		default:
			writeError(w, 404, "not_found", "bulk operation not found")
		}
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
		if parts[1] == "tags" || parts[1] == "categories" {
			if r.Method != http.MethodPut {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.updatePaperTaxonomy(w, r, parts[0], parts[1])
			return
		}
		if parts[1] == "reextract" {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			s.reextractPaper(w, r, parts[0])
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
	item := paperSummary(id, title, abstract, authors, doi, journal, published, status, parse, fav, added, updated)
	item["abstract"] = abstract
	item["version"] = version
	tags, cats, terr := s.loadTaxonomy(r.Context(), []string{id})
	if terr == nil {
		if ts, ok := tags[id]; ok {
			item["tags"] = ts
		}
		if cs, ok := cats[id]; ok {
			item["categories"] = cs
		}
	}
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
		Version       int       `json:"version"`
		Title         *string   `json:"title"`
		Abstract      *string   `json:"abstract"`
		DOI           *string   `json:"doi"`
		ReadingStatus *string   `json:"readingStatus"`
		IsFavorite    *bool     `json:"isFavorite"`
		Authors       *[]string `json:"authors"`
		Journal       *string   `json:"journal"`
		Year          *int      `json:"year"`
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
	if req.Authors != nil {
		if b, err := json.Marshal(*req.Authors); err == nil {
			sets = append(sets, "authors_json=?")
			args = append(args, string(b))
		}
	}
	if req.Journal != nil {
		sets = append(sets, "journal=?")
		args = append(args, *req.Journal)
	}
	if req.Year != nil {
		sets = append(sets, "published_at=?")
		if *req.Year > 0 {
			args = append(args, fmt.Sprintf("%04d-01-01", *req.Year))
		} else {
			args = append(args, nil)
		}
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
	adminID, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	// 单篇删除仍走密码再认证，避免攻击者利用已解锁的浏览器会话批量移入回收站。
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.confirmAdminPassword(w, r, adminID, req.Password, fmt.Sprintf("paper:delete:%d", adminID)) {
		return
	}
	existing, _, err := s.softDeletePapers(r.Context(), []string{id}, adminID, clientIP(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to delete paper")
		return
	}
	if len(existing) == 0 {
		writeError(w, http.StatusNotFound, "not_found", "paper not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true, "retentionDays": int(s.cfg.TrashRetention / (24 * time.Hour))})
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
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete {
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
