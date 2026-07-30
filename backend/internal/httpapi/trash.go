package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const trashCleanupInterval = time.Hour

// RunTrashCleanup removes expired papers immediately on startup and then once
// per hour. Cancellation is controlled by main so shutdown does not leave an
// unmanaged background goroutine.
func (s *Server) RunTrashCleanup(ctx context.Context) {
	s.runTrashCleanup(ctx)
	ticker := time.NewTicker(trashCleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runTrashCleanup(ctx)
		}
	}
}

func (s *Server) runTrashCleanup(ctx context.Context) {
	operationCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	var migrationApplied int
	if err := s.db.QueryRowContext(operationCtx, `SELECT COUNT(*) FROM schema_migrations WHERE version='007_trash_retention.sql'`).Scan(&migrationApplied); err != nil || migrationApplied == 0 {
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("trash cleanup skipped because migration status is unavailable: %v", err)
		} else if migrationApplied == 0 {
			log.Printf("trash cleanup skipped until migration 007_trash_retention.sql is applied")
		}
		return
	}
	if err := s.reconcileTrashStaging(operationCtx); err != nil {
		if !errors.Is(err, context.Canceled) {
			log.Printf("trash staging reconciliation failed; cleanup skipped: %v", err)
		}
		return
	}
	removed, err := s.cleanupExpiredTrash(operationCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("trash cleanup completed with errors: %v", err)
	}
	if removed > 0 {
		log.Printf("trash cleanup permanently removed %d paper(s)", removed)
	}
}

func (s *Server) trash(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authenticate(r); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	page, pageSize := 1, 20
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = n
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("pageSize")); err == nil && n > 0 {
		pageSize = n
	}
	if pageSize > s.cfg.SearchMaxPageSize {
		pageSize = s.cfg.SearchMaxPageSize
	}
	cutoff := time.Now().UTC().Add(-s.cfg.TrashRetention)
	var total int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM papers WHERE deleted_at IS NOT NULL AND deleted_at>?`, cutoff).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to query trash")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,title,LEFT(abstract_text,600),authors_json,doi,journal,published_at,reading_status,is_favorite,parse_status,added_at,updated_at,deleted_at FROM papers WHERE deleted_at IS NOT NULL AND deleted_at>? ORDER BY deleted_at DESC LIMIT ? OFFSET ?`, cutoff, pageSize, (page-1)*pageSize)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to query trash")
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0, pageSize)
	ids := make([]string, 0, pageSize)
	for rows.Next() {
		var id, title, abstract, authors, status, parse string
		var doi, journal sql.NullString
		var published sql.NullTime
		var favorite bool
		var added, updated, deleted time.Time
		if err := rows.Scan(&id, &title, &abstract, &authors, &doi, &journal, &published, &status, &favorite, &parse, &added, &updated, &deleted); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to query trash")
			return
		}
		item := paperSummary(id, title, abstract, authors, doi, journal, published, status, parse, favorite, added, updated)
		item["deletedAt"] = deleted
		item["purgeAt"] = deleted.Add(s.cfg.TrashRetention)
		items = append(items, item)
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to query trash")
		return
	}
	tags, categories, err := s.loadTaxonomy(r.Context(), ids)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "unable to load trash taxonomy")
		return
	}
	for i, id := range ids {
		if values := tags[id]; values != nil {
			items[i]["tags"] = values
		}
		if values := categories[id]; values != nil {
			items[i]["categories"] = values
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items, "page": page, "pageSize": pageSize, "total": total,
		"retentionDays": int(s.cfg.TrashRetention / (24 * time.Hour)),
	})
}

func (s *Server) trashByID(w http.ResponseWriter, r *http.Request) {
	adminID, ok := s.authenticate(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "authentication required")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/trash/"), "/")
	parts := strings.Split(path, "/")
	if r.Method != http.MethodPost || len(parts) != 2 || parts[1] != "restore" || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found", "trash item not found")
		return
	}
	if _, err := uuid.Parse(parts[0]); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "trash item not found")
		return
	}
	if err := s.restorePaper(r.Context(), parts[0], adminID, clientIP(r)); err != nil {
		switch {
		case errors.Is(err, errTrashExpired):
			writeError(w, http.StatusGone, "trash_expired", "paper recovery period has expired")
		case errors.Is(err, sql.ErrNoRows):
			writeError(w, http.StatusNotFound, "not_found", "trash item not found")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "unable to restore paper")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": parts[0], "restored": true})
}

var errTrashExpired = errors.New("trash recovery period expired")

func (s *Server) softDeletePapers(ctx context.Context, paperIDs []string, adminID uint64, ip string) (existing, missing []string, err error) {
	if len(paperIDs) == 0 {
		return nil, nil, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	args := stringsToAny(paperIDs)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM papers WHERE id IN (`+placeholders(len(paperIDs))+`) AND deleted_at IS NULL ORDER BY id FOR UPDATE`, args...)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		existing = append(existing, id)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, err
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
	if len(existing) == 0 {
		return existing, missing, nil
	}
	if err := adjustTaxonomyCounts(ctx, tx, existing, -1); err != nil {
		return nil, nil, err
	}
	args = stringsToAny(existing)
	if _, err := tx.ExecContext(ctx, `UPDATE papers SET deleted_at=UTC_TIMESTAMP(6),updated_at=UTC_TIMESTAMP(6) WHERE id IN (`+placeholders(len(existing))+`) AND deleted_at IS NULL`, args...); err != nil {
		return nil, nil, err
	}
	if err := recordAuditTx(ctx, tx, "papers_soft_deleted", &adminID, ip, map[string]any{"paperIds": existing}); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return existing, missing, nil
}

func (s *Server) restorePaper(ctx context.Context, paperID string, adminID uint64, ip string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var deletedAt time.Time
	if err := tx.QueryRowContext(ctx, `SELECT deleted_at FROM papers WHERE id=? AND deleted_at IS NOT NULL FOR UPDATE`, paperID).Scan(&deletedAt); err != nil {
		return err
	}
	if trashExpired(deletedAt, time.Now().UTC(), s.cfg.TrashRetention) {
		return errTrashExpired
	}
	if err := adjustTaxonomyCounts(ctx, tx, []string{paperID}, 1); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE papers SET deleted_at=NULL,updated_at=UTC_TIMESTAMP(6),version=version+1 WHERE id=? AND deleted_at IS NOT NULL`, paperID)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		if err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err := recordAuditTx(ctx, tx, "paper_restored", &adminID, ip, map[string]any{"paperId": paperID}); err != nil {
		return err
	}
	return tx.Commit()
}

func trashExpired(deletedAt, now time.Time, retention time.Duration) bool {
	return !deletedAt.Add(retention).After(now)
}

func recordAuditTx(ctx context.Context, tx *sql.Tx, eventType string, adminID *uint64, ip string, details any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var actor any
	if adminID != nil {
		actor = *adminID
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO audit_logs(event_type,actor_admin_id,ip_address,details_json,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))`, eventType, actor, ip, payload)
	return err
}

func adjustTaxonomyCounts(ctx context.Context, tx *sql.Tx, paperIDs []string, delta int) error {
	if len(paperIDs) == 0 || (delta != -1 && delta != 1) {
		return nil
	}
	args := stringsToAny(paperIDs)
	tagExpression := "t.usage_count+counts.n"
	categoryExpression := "c.paper_count+counts.n"
	if delta < 0 {
		tagExpression = "IF(t.usage_count>=counts.n,t.usage_count-counts.n,0)"
		categoryExpression = "IF(c.paper_count>=counts.n,c.paper_count-counts.n,0)"
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tags t JOIN (SELECT tag_id,COUNT(*) AS n FROM paper_tags WHERE paper_id IN (`+placeholders(len(paperIDs))+`) GROUP BY tag_id) counts ON counts.tag_id=t.id SET t.usage_count=`+tagExpression, args...); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE categories c JOIN (SELECT category_id,COUNT(*) AS n FROM paper_categories WHERE paper_id IN (`+placeholders(len(paperIDs))+`) GROUP BY category_id) counts ON counts.category_id=c.id SET c.paper_count=`+categoryExpression, args...)
	return err
}

func stringsToAny(values []string) []any {
	args := make([]any, len(values))
	for i := range values {
		args[i] = values[i]
	}
	return args
}

func (s *Server) cleanupExpiredTrash(ctx context.Context) (int, error) {
	cutoff := time.Now().UTC().Add(-s.cfg.TrashRetention)
	removed := 0
	var failures []string
	var cursorDeletedAt time.Time
	var cursorID string
	for {
		query := `SELECT id,deleted_at FROM papers WHERE deleted_at IS NOT NULL AND deleted_at<=?`
		args := []any{cutoff}
		if !cursorDeletedAt.IsZero() {
			query += ` AND (deleted_at>? OR (deleted_at=? AND id>?))`
			args = append(args, cursorDeletedAt, cursorDeletedAt, cursorID)
		}
		query += ` ORDER BY deleted_at ASC,id ASC LIMIT 500`
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return removed, err
		}
		type candidate struct {
			id        string
			deletedAt time.Time
		}
		batch := make([]candidate, 0, 500)
		for rows.Next() {
			var item candidate
			if err := rows.Scan(&item.id, &item.deletedAt); err != nil {
				rows.Close()
				return removed, err
			}
			batch = append(batch, item)
		}
		if err := rows.Close(); err != nil {
			return removed, err
		}
		for _, item := range batch {
			purged, err := s.purgePaper(ctx, item.id, cutoff)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", item.id, err))
				continue
			}
			if purged {
				removed++
			}
		}
		if len(batch) < 500 {
			break
		}
		cursorDeletedAt = batch[len(batch)-1].deletedAt
		cursorID = batch[len(batch)-1].id
	}
	if len(failures) > 0 {
		return removed, fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return removed, nil
}

type stagedFile struct {
	original string
	staged   string
}

func (s *Server) purgePaper(ctx context.Context, paperID string, cutoff time.Time) (bool, error) {
	if _, err := uuid.Parse(paperID); err != nil {
		return false, fmt.Errorf("invalid paper id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var found string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM papers WHERE id=? AND deleted_at IS NOT NULL AND deleted_at<=? FOR UPDATE`, paperID, cutoff).Scan(&found); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT object_key FROM paper_files WHERE paper_id=?`, paperID)
	if err != nil {
		return false, err
	}
	var keys []string
	seen := map[string]struct{}{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return false, err
		}
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	if err := rows.Close(); err != nil {
		return false, err
	}
	sort.Strings(keys)

	stageDir, err := s.trashStageDir(paperID)
	if err != nil {
		return false, err
	}
	if len(keys) > 0 {
		if err := os.MkdirAll(stageDir, 0700); err != nil {
			return false, err
		}
	}
	staged := make([]stagedFile, 0, len(keys))
	rollbackFiles := func() {
		for i := len(staged) - 1; i >= 0; i-- {
			if err := os.Rename(staged[i].staged, staged[i].original); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("unable to restore staged file %q after purge rollback: %v", staged[i].original, err)
			}
		}
		_ = os.Remove(stageDir)
	}
	for _, key := range keys {
		original, err := safeObjectPath(s.cfg.UploadDir, key)
		if err != nil {
			rollbackFiles()
			return false, err
		}
		info, err := os.Lstat(original)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			rollbackFiles()
			return false, err
		}
		if !info.Mode().IsRegular() {
			rollbackFiles()
			return false, fmt.Errorf("refusing to purge non-regular object %q", key)
		}
		target := filepath.Join(stageDir, key)
		if err := os.Rename(original, target); err != nil {
			rollbackFiles()
			return false, err
		}
		staged = append(staged, stagedFile{original: original, staged: target})
	}
	// Existing deployments use a non-cascading paper_files foreign key, so
	// remove file metadata before deleting the parent paper in the same
	// transaction. A rollback restores both the rows and the staged files.
	if _, err := tx.ExecContext(ctx, `DELETE FROM paper_files WHERE paper_id=?`, paperID); err != nil {
		rollbackFiles()
		return false, err
	}
	result, err := tx.ExecContext(ctx, `DELETE FROM papers WHERE id=? AND deleted_at IS NOT NULL AND deleted_at<=?`, paperID, cutoff)
	if err != nil {
		rollbackFiles()
		return false, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		rollbackFiles()
		if err != nil {
			return false, err
		}
		return false, nil
	}
	if err := recordAuditTx(ctx, tx, "paper_auto_purged", nil, "", map[string]any{"paperId": paperID, "fileCount": len(staged)}); err != nil {
		rollbackFiles()
		return false, err
	}
	if err := tx.Commit(); err != nil {
		// A failed Commit can mean the server committed but the acknowledgement
		// was lost. Keep files staged so reconciliation can inspect the database
		// and either restore or remove them without creating orphaned files.
		return false, err
	}
	for _, file := range staged {
		if err := os.Remove(file.staged); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("unable to remove staged trash file %q: %v", file.staged, err)
		}
	}
	_ = os.Remove(stageDir)
	return true, nil
}

func safeObjectPath(uploadDir, objectKey string) (string, error) {
	if objectKey == "" || filepath.IsAbs(objectKey) || filepath.Base(objectKey) != objectKey || strings.ContainsAny(objectKey, `/\\`) {
		return "", fmt.Errorf("unsafe stored object key")
	}
	root, err := filepath.Abs(uploadDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, objectKey)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("stored object escapes upload directory")
	}
	return path, nil
}

func (s *Server) trashStageDir(paperID string) (string, error) {
	if _, err := uuid.Parse(paperID); err != nil {
		return "", fmt.Errorf("invalid paper id")
	}
	root, err := filepath.Abs(s.cfg.UploadDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, ".trash-purge", paperID), nil
}

// reconcileTrashStaging repairs the only crash window in physical deletion:
// staged files are restored when the database row still exists and discarded
// when the hard delete committed before the process stopped.
func (s *Server) reconcileTrashStaging(ctx context.Context) error {
	root, err := filepath.Abs(s.cfg.UploadDir)
	if err != nil {
		return err
	}
	stageRoot := filepath.Join(root, ".trash-purge")
	entries, err := os.ReadDir(stageRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		paperID := entry.Name()
		if _, err := uuid.Parse(paperID); err != nil {
			continue
		}
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM papers WHERE id=?`, paperID).Scan(&exists); err != nil {
			return err
		}
		dir := filepath.Join(stageRoot, paperID)
		files, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			staged := filepath.Join(dir, file.Name())
			info, err := os.Lstat(staged)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refusing to reconcile non-regular staged object %q", staged)
			}
			if exists == 0 {
				if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
				continue
			}
			original, err := safeObjectPath(s.cfg.UploadDir, file.Name())
			if err != nil {
				return err
			}
			if _, err := os.Lstat(original); err == nil {
				if err := os.Remove(staged); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			} else if errors.Is(err, os.ErrNotExist) {
				if err := os.Rename(staged, original); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		_ = os.Remove(dir)
	}
	_ = os.Remove(stageRoot)
	return nil
}
