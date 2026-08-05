package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// syncMetadata is intentionally small and lossless fields are kept in Extra.
// The plugin sends this stable shape; the server can later add structured fields.
type syncMetadata struct {
	ItemType string            `json:"itemType"`
	Title    string            `json:"title"`
	Abstract string            `json:"abstract"`
	Authors  []string          `json:"authors"`
	DOI      string            `json:"doi"`
	Journal  string            `json:"journal"`
	Year     int               `json:"year"`
	Tags     []string          `json:"tags"`
	Extra    map[string]string `json:"extra,omitempty"`
}

type syncManifestItem struct {
	ExternalLibraryKey string       `json:"externalLibraryKey"`
	ItemKey            string       `json:"itemKey"`
	LocalVersion       string       `json:"localVersion"`
	Metadata           syncMetadata `json:"metadata"`
	MetadataHash       string       `json:"metadataHash"`
	FileSHA256         string       `json:"fileSha256"`
	FileSize           int64        `json:"fileSize"`
}

type syncSessionRequest struct {
	ClientInstanceID   string             `json:"clientInstanceId"`
	DisplayName        string             `json:"displayName"`
	ExternalLibraryKey string             `json:"externalLibraryKey"`
	Items              []syncManifestItem `json:"items"`
}

type syncLinkRequest struct {
	PaperID            string `json:"paperId"`
	ExternalLibraryKey string `json:"externalLibraryKey"`
	ItemKey            string `json:"itemKey"`
}

type syncPaper struct {
	ID           string         `json:"paperId"`
	Metadata     syncMetadata   `json:"metadata"`
	MetadataHash string         `json:"metadataHash"`
	File         map[string]any `json:"file"`
}

// sync_tokens.token_prefix is deliberately short because it is only a display
// identifier. The full token is hashed and is never stored in plaintext.
const syncTokenPrefixMaxLength = 16

func syncTokenPrefix(token string) string {
	if len(token) <= syncTokenPrefixMaxLength {
		return token
	}
	return token[:syncTokenPrefixMaxLength]
}

func (s *Server) registerZoteroRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/auth/sync-tokens", s.syncTokens)
	mux.HandleFunc("/api/auth/sync-tokens/", s.syncTokenByID)
	mux.HandleFunc("/api/sync/v1/capabilities", s.syncCapabilities)
	mux.HandleFunc("/api/sync/v1/sessions", s.syncSessions)
	mux.HandleFunc("/api/sync/v1/sessions/", s.syncSessionByID)
	mux.HandleFunc("/api/sync/v1/links", s.syncLinks)
	mux.HandleFunc("/api/sync/v1/papers", s.syncPapers)
	mux.HandleFunc("/api/sync/v1/papers/", s.syncPaperByID)
}

func (s *Server) syncPrincipal(r *http.Request, scope string) (adminID uint64, clientID string, ok bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return 0, "", false
	}
	var expires sql.NullTime
	err := s.db.QueryRowContext(r.Context(), `SELECT a.id,c.id,t.expires_at
		FROM sync_tokens t JOIN sync_clients c ON c.id=t.client_id AND c.revoked_at IS NULL
		JOIN admins a ON a.id=c.admin_id
		WHERE t.token_hash=? AND t.revoked_at IS NULL`, s.secureHash(parts[1])).Scan(&adminID, &clientID, &expires)
	if err != nil || (expires.Valid && !expires.Time.After(time.Now().UTC())) {
		return 0, "", false
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE sync_tokens SET last_used_at=UTC_TIMESTAMP(6) WHERE token_hash=?`, s.secureHash(parts[1]))
	if scope != "" {
		var scopes string
		if err := s.db.QueryRowContext(r.Context(), `SELECT scopes_json FROM sync_tokens WHERE token_hash=?`, s.secureHash(parts[1])).Scan(&scopes); err != nil {
			return 0, "", false
		}
		var values []string
		if json.Unmarshal([]byte(scopes), &values) != nil {
			return 0, "", false
		}
		found := false
		for _, value := range values {
			if value == scope {
				found = true
				break
			}
		}
		if !found {
			return 0, "", false
		}
	}
	return adminID, clientID, true
}

func (s *Server) syncCapabilities(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := s.syncPrincipal(r, "papers:read"); !ok {
		writeError(w, http.StatusUnauthorized, "token_invalid", "valid sync token required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"protocolVersion":          "1.0",
		"serverInstanceId":         s.cfg.PublicBaseURL,
		"simpleUploadThreshold":    16 << 20,
		"maxUploadBytes":           s.cfg.UploadMaxBytes,
		"supportedAttachmentTypes": []string{"application/pdf"},
		"features":                 []string{"metadata", "tags", "primaryAttachment", "rangeDownload"},
	})
}

func (s *Server) syncTokens(w http.ResponseWriter, r *http.Request) {
	adminID, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if r.Method == http.MethodGet {
		rows, err := s.db.QueryContext(r.Context(), `SELECT t.id,t.token_prefix,t.scopes_json,t.expires_at,t.last_used_at,t.revoked_at,t.created_at
			FROM sync_tokens t JOIN sync_clients c ON c.id=t.client_id WHERE c.admin_id=? ORDER BY t.created_at DESC`, adminID)
		if err != nil {
			writeError(w, 500, "internal_error", "unable to list sync tokens")
			return
		}
		defer rows.Close()
		items := []map[string]any{}
		for rows.Next() {
			var id, prefix, scopes string
			var expires, last, revoked, created sql.NullTime
			if rows.Scan(&id, &prefix, &scopes, &expires, &last, &revoked, &created) != nil {
				continue
			}
			var scopeList []string
			_ = json.Unmarshal([]byte(scopes), &scopeList)
			items = append(items, map[string]any{"id": id, "prefix": prefix, "scopes": scopeList, "expiresAt": expires, "lastUsedAt": last, "revokedAt": revoked, "createdAt": created})
		}
		writeJSON(w, 200, map[string]any{"items": items})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name             string   `json:"name"`
		ClientInstanceID string   `json:"clientInstanceId"`
		DisplayName      string   `json:"displayName"`
		Scopes           []string `json:"scopes"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Name == "" {
		req.Name = "Zotero"
	}
	if req.DisplayName == "" {
		req.DisplayName = req.Name
	}
	if req.ClientInstanceID == "" {
		req.ClientInstanceID = uuid.NewString()
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{"papers:read", "papers:write", "files:read", "files:write"}
	}
	allowed := map[string]bool{"papers:read": true, "papers:write": true, "files:read": true, "files:write": true}
	for _, scope := range req.Scopes {
		if !allowed[scope] {
			writeError(w, 400, "invalid_scope", "unsupported sync scope")
			return
		}
	}
	clientID := uuid.NewString()
	if err := func() error {
		tx, err := s.db.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var existing string
		err = tx.QueryRowContext(r.Context(), `SELECT id FROM sync_clients WHERE admin_id=? AND instance_id=?`, adminID, req.ClientInstanceID).Scan(&existing)
		if err == nil {
			clientID = existing
		} else if err == sql.ErrNoRows {
			_, err = tx.ExecContext(r.Context(), `INSERT INTO sync_clients(id,admin_id,instance_id,display_name,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))`, clientID, adminID, req.ClientInstanceID, req.DisplayName)
		} else {
			return err
		}
		if err != nil {
			return err
		}
		secret := randomToken(32)
		token := "pkb_zot_" + secret[:12] + "." + secret
		b, _ := json.Marshal(req.Scopes)
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO sync_tokens(id,client_id,token_prefix,token_hash,scopes_json,created_at) VALUES(?,?,?,?,?,UTC_TIMESTAMP(6))`, uuid.NewString(), clientID, syncTokenPrefix(token), s.secureHash(token), string(b)); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		writeJSON(w, http.StatusCreated, map[string]any{"token": token, "clientId": clientID, "scopes": req.Scopes})
		return nil
	}(); err != nil {
		log.Printf("unable to create sync token: %v", err)
		writeError(w, 500, "internal_error", "unable to create sync token")
	}
}

func (s *Server) syncTokenByID(w http.ResponseWriter, r *http.Request) {
	adminID, ok := s.authenticate(r)
	if !ok {
		writeError(w, 401, "unauthenticated", "authentication required")
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/auth/sync-tokens/"), "/")
	res, err := s.db.ExecContext(r.Context(), `UPDATE sync_tokens t JOIN sync_clients c ON c.id=t.client_id SET t.revoked_at=UTC_TIMESTAMP(6) WHERE t.id=? AND c.admin_id=?`, id, adminID)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to revoke sync token")
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		writeError(w, 404, "not_found", "sync token not found")
		return
	}
	writeJSON(w, 200, map[string]any{"revoked": true})
}

func (s *Server) syncSessions(w http.ResponseWriter, r *http.Request) {
	adminID, clientID, ok := s.syncPrincipal(r, "papers:read")
	if !ok {
		writeError(w, 401, "token_invalid", "valid papers:read token required")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req syncSessionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ExternalLibraryKey == "" || len(req.Items) > 500 {
		writeError(w, 400, "invalid_manifest", "externalLibraryKey and at most 500 items are required")
		return
	}
	sessionID := uuid.NewString()
	expires := time.Now().UTC().Add(30 * time.Minute)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to create sync session")
		return
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(r.Context(), `INSERT INTO sync_sessions(id,client_id,external_library_key,created_at,expires_at) VALUES(?,?,?,UTC_TIMESTAMP(6),?)`, sessionID, clientID, req.ExternalLibraryKey, expires)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to create sync session")
		return
	}
	for i, item := range req.Items {
		if item.ItemKey == "" {
			continue
		}
		if item.ExternalLibraryKey == "" {
			item.ExternalLibraryKey = req.ExternalLibraryKey
		}
		if item.MetadataHash == "" {
			item.MetadataHash = syncMetadataHash(item.Metadata)
		}
		b, _ := json.Marshal(item.Metadata)
		_, err = tx.ExecContext(r.Context(), `INSERT INTO sync_session_items(session_id,row_key,external_item_key,metadata_json,metadata_hash,file_sha256,file_size) VALUES(?,?,?,?,?,?,?)`, sessionID, fmt.Sprintf("%d:%s", i, item.ItemKey), item.ItemKey, string(b), item.MetadataHash, nullString(item.FileSHA256), nullableInt64(item.FileSize))
		if err != nil {
			writeError(w, 500, "internal_error", "unable to store sync manifest")
			return
		}
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "internal_error", "unable to create sync session")
		return
	}
	diff, err := s.buildSyncDiff(r, adminID, sessionID, req.ExternalLibraryKey)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to compare sync manifest")
		return
	}
	writeJSON(w, 201, map[string]any{"sessionId": sessionID, "expiresAt": expires, "diff": diff})
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullableInt64(v int64) any {
	if v <= 0 {
		return nil
	}
	return v
}

func (s *Server) syncSessionByID(w http.ResponseWriter, r *http.Request) {
	adminID, clientID, ok := s.syncPrincipal(r, "papers:read")
	if !ok {
		writeError(w, 401, "token_invalid", "valid sync token required")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sync/v1/sessions/"), "/")
	if path == "" {
		writeError(w, 404, "not_found", "sync session not found")
		return
	}
	var sessionClient, library string
	var expires time.Time
	if err := s.db.QueryRowContext(r.Context(), `SELECT client_id,external_library_key,expires_at FROM sync_sessions WHERE id=?`, path).Scan(&sessionClient, &library, &expires); err != nil || sessionClient != clientID || !expires.After(time.Now().UTC()) {
		writeError(w, 404, "sync_session_expired", "sync session not found or expired")
		return
	}
	if r.Method == http.MethodGet {
		diff, err := s.buildSyncDiff(r, adminID, path, library)
		if err != nil {
			writeError(w, 500, "internal_error", "unable to compare sync manifest")
			return
		}
		writeJSON(w, 200, map[string]any{"sessionId": path, "diff": diff})
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// syncLinks acknowledges that a server paper was imported into a Zotero item.
// The durable link makes later comparisons use the stable Zotero item key
// instead of falling back to a heuristic DOI match.
func (s *Server) syncLinks(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := s.syncPrincipal(r, "papers:write")
	if !ok {
		writeError(w, http.StatusUnauthorized, "token_invalid", "valid papers:write token required")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req syncLinkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.PaperID) == "" || strings.TrimSpace(req.ExternalLibraryKey) == "" || strings.TrimSpace(req.ItemKey) == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "paperId, externalLibraryKey, and itemKey are required")
		return
	}
	sp, err := s.syncPaperData(r, req.PaperID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "paper not found")
		return
	}
	fileHash, _ := sp.File["sha256"].(string)
	metadata, _ := json.Marshal(sp.Metadata)
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO sync_links(id,admin_id,paper_id,external_library_key,external_item_key,base_metadata_json,base_metadata_hash,base_file_sha256,server_version,updated_at)
		VALUES(?,?,?,?,?,?,?,?,1,UTC_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE paper_id=VALUES(paper_id),base_metadata_json=VALUES(base_metadata_json),base_metadata_hash=VALUES(base_metadata_hash),base_file_sha256=VALUES(base_file_sha256),server_version=server_version+1,updated_at=VALUES(updated_at)`,
		uuid.NewString(), adminID, req.PaperID, req.ExternalLibraryKey, req.ItemKey, string(metadata), sp.MetadataHash, nullString(fileHash))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			writeError(w, http.StatusConflict, "linked_in_library", "paper is already linked to another item in this Zotero library")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to save sync mapping")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"paperId": req.PaperID, "itemKey": req.ItemKey, "metadataHash": sp.MetadataHash})
}

func syncMetadataHash(m syncMetadata) string {
	// Zotero preserves tag insertion order while the server returns tags by name.
	// Hash a sorted copy so tag order alone never creates a false conflict.
	m.Tags = append([]string(nil), m.Tags...)
	sort.Strings(m.Tags)
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (s *Server) buildSyncDiff(r *http.Request, adminID uint64, sessionID, library string) (map[string]any, error) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT row_key,external_item_key,metadata_json,metadata_hash,COALESCE(file_sha256,''),COALESCE(file_size,0) FROM sync_session_items WHERE session_id=?`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	local := []syncManifestItem{}
	for rows.Next() {
		var row, key, raw, hash, fileHash string
		var size int64
		if err := rows.Scan(&row, &key, &raw, &hash, &fileHash, &size); err != nil {
			return nil, err
		}
		var md syncMetadata
		if json.Unmarshal([]byte(raw), &md) != nil {
			continue
		}
		local = append(local, syncManifestItem{ExternalLibraryKey: library, ItemKey: key, Metadata: md, MetadataHash: hash, FileSHA256: fileHash, FileSize: size})
	}
	items := []map[string]any{}
	matched := map[string]bool{}
	for _, li := range local {
		var paperID string
		var baseHash string
		err := s.db.QueryRowContext(r.Context(), `SELECT paper_id,base_metadata_hash FROM sync_links WHERE admin_id=? AND external_library_key=? AND external_item_key=?`, adminID, library, li.ItemKey).Scan(&paperID, &baseHash)
		basis := "none"
		if err == nil {
			basis = "external_link"
		} else if li.Metadata.DOI != "" {
			norm, valid := normalizeDOIQuery(li.Metadata.DOI)
			if valid {
				_ = s.db.QueryRowContext(r.Context(), `SELECT id FROM papers WHERE normalized_doi=? AND deleted_at IS NULL`, strings.ToLower(norm)).Scan(&paperID)
				if paperID != "" {
					basis = "doi"
				}
			}
		}
		if paperID == "" {
			items = append(items, map[string]any{"rowId": "local:" + li.ItemKey, "status": "local_only", "local": li})
			continue
		}
		matched[paperID] = true
		sp, err := s.syncPaperData(r, paperID)
		if err != nil {
			continue
		}
		status := "both_same"
		if li.MetadataHash != sp.MetadataHash {
			status = "both_changed"
		}
		serverFileHash, _ := sp.File["sha256"].(string)
		localHasFile := li.FileSize > 0 || li.FileSHA256 != ""
		serverHasFile := serverFileHash != ""
		if localHasFile != serverHasFile || (localHasFile && serverHasFile && li.FileSHA256 != "" && li.FileSHA256 != serverFileHash) {
			status = "both_changed"
		}
		if basis != "external_link" && status == "both_same" {
			status = "both_changed"
		}
		items = append(items, map[string]any{"rowId": "both:" + paperID + ":" + li.ItemKey, "status": status, "matchBasis": basis, "local": li, "server": sp})
	}
	rows2, err := s.db.QueryContext(r.Context(), `SELECT id FROM papers WHERE deleted_at IS NULL ORDER BY added_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var id string
		if rows2.Scan(&id) != nil || matched[id] {
			continue
		}
		sp, e := s.syncPaperData(r, id)
		if e == nil {
			items = append(items, map[string]any{"rowId": "server:" + id, "status": "server_only", "server": sp})
		}
	}
	counts := map[string]int{}
	for _, item := range items {
		if status, ok := item["status"].(string); ok {
			counts[status]++
		}
	}
	return map[string]any{"items": items, "counts": counts}, nil
}

func (s *Server) syncPaperData(r *http.Request, id string) (syncPaper, error) {
	var title, abstract, authors, doi, journal, status, parse string
	var published sql.NullTime
	var fav bool
	var version int
	err := s.db.QueryRowContext(r.Context(), `SELECT title,abstract_text,authors_json,COALESCE(doi,''),COALESCE(journal,''),reading_status,parse_status,published_at,is_favorite,version FROM papers WHERE id=? AND deleted_at IS NULL`, id).Scan(&title, &abstract, &authors, &doi, &journal, &status, &parse, &published, &fav, &version)
	if err != nil {
		return syncPaper{}, err
	}
	md := syncMetadata{ItemType: "journalArticle", Title: title, Abstract: abstract, DOI: doi, Journal: journal}
	if published.Valid {
		md.Year = published.Time.Year()
	}
	_ = json.Unmarshal([]byte(authors), &md.Authors)
	tagRows, err := s.db.QueryContext(r.Context(), `SELECT t.name
		FROM paper_tags pt JOIN tags t ON t.id=pt.tag_id
		WHERE pt.paper_id=? ORDER BY t.name`, id)
	if err != nil {
		return syncPaper{}, err
	}
	for tagRows.Next() {
		var name string
		if err := tagRows.Scan(&name); err != nil {
			tagRows.Close()
			return syncPaper{}, err
		}
		md.Tags = append(md.Tags, name)
	}
	if err := tagRows.Err(); err != nil {
		tagRows.Close()
		return syncPaper{}, err
	}
	if err := tagRows.Close(); err != nil {
		return syncPaper{}, err
	}
	var name, media, key, hash string
	var size int64
	file := map[string]any{}
	if err := s.db.QueryRowContext(r.Context(), `SELECT original_name,media_type,object_key,sha256,size_bytes FROM paper_files WHERE paper_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&name, &media, &key, &hash, &size); err == nil {
		file = map[string]any{"originalName": name, "mediaType": media, "sha256": hash, "sizeBytes": size, "available": fileExists(filepath.Join(s.cfg.UploadDir, filepath.Base(key)))}
	}
	return syncPaper{ID: id, Metadata: md, MetadataHash: syncMetadataHash(md), File: file}, nil
}

func (s *Server) syncPapers(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := s.syncPrincipal(r, "papers:write")
	if !ok {
		writeError(w, 401, "token_invalid", "valid papers:write token required")
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.UploadMaxBytes+2<<20)
	if err := r.ParseMultipartForm(2 << 20); err != nil {
		writeError(w, 413, "file_too_large", "request is too large")
		return
	}
	var md syncMetadata
	if err := json.Unmarshal([]byte(r.FormValue("metadata")), &md); err != nil || strings.TrimSpace(md.Title) == "" {
		writeError(w, 400, "invalid_metadata", "metadata is required")
		return
	}
	library, key := strings.TrimSpace(r.FormValue("externalLibraryKey")), strings.TrimSpace(r.FormValue("itemKey"))
	if library == "" || key == "" {
		writeError(w, 400, "invalid_request", "externalLibraryKey and itemKey are required")
		return
	}
	var paperID string
	err := s.db.QueryRowContext(r.Context(), `SELECT paper_id FROM sync_links WHERE admin_id=? AND external_library_key=? AND external_item_key=?`, adminID, library, key).Scan(&paperID)
	created := false
	if err == sql.ErrNoRows {
		paperID = uuid.NewString()
		created = true
	} else if err != nil {
		writeError(w, 500, "internal_error", "unable to read sync mapping")
		return
	}
	fileHeader := firstSyncFile(r.MultipartForm)
	var objectKey, fileHash string
	var fileSize int64
	if fileHeader != nil {
		objectKey, fileHash, fileSize, err = s.storeSyncFile(r.Context(), fileHeader)
		if err != nil {
			writeError(w, 422, "file_upload_failed", err.Error())
			return
		}
	}
	now := time.Now().UTC()
	authorsJSON, _ := json.Marshal(md.Authors)
	doi := strings.TrimSpace(md.DOI)
	normalized := ""
	if norm, valid := normalizeDOIQuery(doi); valid {
		normalized = strings.ToLower(norm)
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		if objectKey != "" {
			_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.Base(objectKey)))
		}
		writeError(w, 500, "internal_error", "unable to save sync paper")
		return
	}
	defer tx.Rollback()
	committed := false
	defer func() {
		if !committed && objectKey != "" {
			_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.Base(objectKey)))
		}
	}()
	if created {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO papers(id,title,abstract_text,authors_json,doi,normalized_doi,journal,published_at,source_type,added_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, paperID, md.Title, md.Abstract, string(authorsJSON), nullString(doi), nullString(normalized), md.Journal, nullableYear(md.Year), "zotero", now, now)
	} else {
		_, err = tx.ExecContext(r.Context(), `UPDATE papers SET title=?,abstract_text=?,authors_json=?,doi=?,normalized_doi=?,journal=?,published_at=?,source_type='zotero',updated_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=?`, md.Title, md.Abstract, string(authorsJSON), nullString(doi), nullString(normalized), md.Journal, nullableYear(md.Year), paperID)
	}
	if err != nil {
		tx.Rollback()
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			writeError(w, 409, "duplicate_doi", "another paper already uses this DOI")
			return
		}
		writeError(w, 500, "internal_error", "unable to save sync metadata")
		return
	}
	if fileHeader != nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO paper_files(id,paper_id,object_key,original_name,media_type,size_bytes,sha256,scan_status,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, uuid.NewString(), paperID, objectKey, fileHeader.Filename, fileHeader.Header.Get("Content-Type"), fileSize, fileHash, "pending", now)
		if err != nil {
			writeError(w, 500, "internal_error", "unable to link sync file")
			return
		}
	}
	if err = syncApplyTags(r.Context(), tx, paperID, md.Tags); err != nil {
		writeError(w, 400, "invalid_tags", err.Error())
		return
	}
	linkFileHash := fileHash
	if linkFileHash == "" {
		_ = tx.QueryRowContext(r.Context(), `SELECT sha256 FROM paper_files WHERE paper_id=? ORDER BY created_at DESC LIMIT 1`, paperID).Scan(&linkFileHash)
	}
	metaHash := syncMetadataHash(md)
	b, _ := json.Marshal(md)
	_, err = tx.ExecContext(r.Context(), `INSERT INTO sync_links(id,admin_id,paper_id,external_library_key,external_item_key,base_metadata_json,base_metadata_hash,base_file_sha256,server_version,updated_at) VALUES(?,?,?,?,?,?,?,?,1,?) ON DUPLICATE KEY UPDATE paper_id=VALUES(paper_id),base_metadata_json=VALUES(base_metadata_json),base_metadata_hash=VALUES(base_metadata_hash),base_file_sha256=VALUES(base_file_sha256),server_version=server_version+1,updated_at=VALUES(updated_at)`, uuid.NewString(), adminID, paperID, library, key, string(b), metaHash, nullString(linkFileHash), now)
	if err != nil {
		writeError(w, 500, "internal_error", "unable to save sync mapping")
		return
	}
	if err = tx.Commit(); err != nil {
		writeError(w, 500, "internal_error", "unable to commit sync paper")
		return
	}
	committed = true
	writeJSON(w, http.StatusCreated, map[string]any{"paperId": paperID, "created": created, "metadataHash": metaHash, "fileSha256": fileHash})
}

// syncApplyTags replaces a synced paper's manual tags and keeps the shared tag
// usage counts consistent with the taxonomy endpoints.
func syncApplyTags(ctx context.Context, tx *sql.Tx, paperID string, values []string) error {
	oldRows, err := tx.QueryContext(ctx, `SELECT tag_id FROM paper_tags WHERE paper_id=?`, paperID)
	if err != nil {
		return err
	}
	oldIDs := map[uint64]struct{}{}
	for oldRows.Next() {
		var id uint64
		if err := oldRows.Scan(&id); err != nil {
			oldRows.Close()
			return err
		}
		oldIDs[id] = struct{}{}
	}
	if err := oldRows.Err(); err != nil {
		oldRows.Close()
		return err
	}
	if err := oldRows.Close(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM paper_tags WHERE paper_id=?`, paperID); err != nil {
		return err
	}

	newIDs := map[uint64]struct{}{}
	seen := map[string]struct{}{}
	now := time.Now().UTC()
	for _, value := range values {
		name := strings.TrimSpace(value)
		if name == "" {
			continue
		}
		if len([]rune(name)) > 40 {
			return fmt.Errorf("tag is longer than 40 characters")
		}
		normalized := normalizeName(name)
		if normalized == "" {
			continue
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tags(name,normalized_name,color,created_at,updated_at)
			VALUES(?,?, 'teal',?,?) ON DUPLICATE KEY UPDATE updated_at=VALUES(updated_at)`, name, normalized, now, now); err != nil {
			return err
		}
		var tagID uint64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE normalized_name=?`, normalized).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO paper_tags(paper_id,tag_id,created_at) VALUES(?,?,?)`, paperID, tagID, now); err != nil {
			return err
		}
		newIDs[tagID] = struct{}{}
	}
	for id := range oldIDs {
		if _, retained := newIDs[id]; retained {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tags SET usage_count=GREATEST(0, usage_count-1) WHERE id=?`, id); err != nil {
			return err
		}
	}
	for id := range newIDs {
		if _, existed := oldIDs[id]; existed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tags SET usage_count=usage_count+1 WHERE id=?`, id); err != nil {
			return err
		}
	}
	return nil
}

func firstSyncFile(form *multipart.Form) *multipart.FileHeader {
	if form == nil {
		return nil
	}
	if files := form.File["file"]; len(files) > 0 {
		return files[0]
	}
	return nil
}
func nullableYear(year int) any {
	if year < 1000 || year > 3000 {
		return nil
	}
	return fmt.Sprintf("%04d-01-01", year)
}
func fileExists(path string) bool { info, err := os.Stat(path); return err == nil && !info.IsDir() }

func (s *Server) storeSyncFile(ctx context.Context, fh *multipart.FileHeader) (string, string, int64, error) {
	_ = ctx
	if fh.Size <= 0 || fh.Size > s.cfg.UploadMaxBytes {
		return "", "", 0, fmt.Errorf("file size is not allowed")
	}
	if strings.ToLower(filepath.Ext(fh.Filename)) != ".pdf" {
		return "", "", 0, fmt.Errorf("only PDF attachments are supported")
	}
	f, err := fh.Open()
	if err != nil {
		return "", "", 0, err
	}
	defer f.Close()
	id := uuid.NewString()
	staging := filepath.Join(s.cfg.UploadDir, ".sync-"+id+".tmp")
	final := filepath.Join(s.cfg.UploadDir, id+".pdf")
	if err := os.MkdirAll(s.cfg.UploadDir, 0700); err != nil {
		return "", "", 0, err
	}
	out, err := os.OpenFile(staging, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", 0, err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(f, s.cfg.UploadMaxBytes+1))
	_ = out.Close()
	if copyErr != nil {
		_ = os.Remove(staging)
		return "", "", 0, copyErr
	}
	if n != fh.Size || n > s.cfg.UploadMaxBytes {
		_ = os.Remove(staging)
		return "", "", 0, fmt.Errorf("file size changed or exceeds limit")
	}
	if err := os.Rename(staging, final); err != nil {
		_ = os.Remove(staging)
		return "", "", 0, err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return filepath.Base(final), sum, n, nil
}

func (s *Server) syncPaperByID(w http.ResponseWriter, r *http.Request) {
	adminID, _, ok := s.syncPrincipal(r, "papers:read")
	if !ok {
		writeError(w, 401, "token_invalid", "valid papers:read token required")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sync/v1/papers/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, 404, "not_found", "paper not found")
		return
	}
	id := parts[0]
	var owner uint64
	if err := s.db.QueryRowContext(r.Context(), `SELECT admin_id FROM sync_links WHERE paper_id=? AND admin_id=? LIMIT 1`, id, adminID).Scan(&owner); err != nil { // server-only papers may be read by the owner
		var exists int
		if s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE id=? AND deleted_at IS NULL`, id).Scan(&exists) != nil || exists == 0 {
			writeError(w, 404, "not_found", "paper not found")
			return
		}
	}
	if len(parts) > 1 && parts[1] == "file" {
		s.syncFile(w, r, id)
		return
	}
	sp, err := s.syncPaperData(r, id)
	if err != nil {
		writeError(w, 404, "not_found", "paper not found")
		return
	}
	writeJSON(w, 200, map[string]any{"paperId": sp.ID, "metadata": sp.Metadata, "metadataHash": sp.MetadataHash, "file": sp.File})
}

func (s *Server) syncFile(w http.ResponseWriter, r *http.Request, id string) {
	var key, name, media string
	var size int64
	var hash string
	if err := s.db.QueryRowContext(r.Context(), `SELECT object_key,original_name,media_type,size_bytes,sha256 FROM paper_files WHERE paper_id=? ORDER BY created_at DESC LIMIT 1`, id).Scan(&key, &name, &media, &size, &hash); err != nil {
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
	w.Header().Set("ETag", `"sha256:`+hash+`"`)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", media)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeDownloadName(name)+`"`)
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func safeDownloadName(name string) string {
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		if r < 32 || r == '"' || r == '\\' || r == '\r' || r == '\n' {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "paper.pdf"
	}
	return b.String()
}
