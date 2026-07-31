-- Repair counters written by releases that replaced taxonomy links without
-- serializing updates or checking counter update errors.
UPDATE tags t
LEFT JOIN (
  SELECT pt.tag_id, COUNT(*) AS active_count
  FROM paper_tags pt
  JOIN papers p ON p.id = pt.paper_id AND p.deleted_at IS NULL
  GROUP BY pt.tag_id
) counts ON counts.tag_id = t.id
SET t.usage_count = COALESCE(counts.active_count, 0);

UPDATE categories c
LEFT JOIN (
  SELECT pc.category_id, COUNT(*) AS active_count
  FROM paper_categories pc
  JOIN papers p ON p.id = pc.paper_id AND p.deleted_at IS NULL
  GROUP BY pc.category_id
) counts ON counts.category_id = c.id
SET c.paper_count = COALESCE(counts.active_count, 0);

-- Each DDL statement is guarded because MySQL commits ALTER TABLE implicitly.
-- A retry after the DDL succeeds but before schema_migrations is updated must
-- not fail with a duplicate-index error.
SET @index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'papers'
    AND index_name = 'idx_papers_active_added'
);
SET @index_sql = IF(
  @index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_active_added (deleted_at, added_at, id)',
  'SELECT 1'
);
PREPARE index_stmt FROM @index_sql;
EXECUTE index_stmt;
DEALLOCATE PREPARE index_stmt;

SET @index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'papers'
    AND index_name = 'idx_papers_active_doi'
);
SET @index_sql = IF(
  @index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_active_doi (deleted_at, normalized_doi)',
  'SELECT 1'
);
PREPARE index_stmt FROM @index_sql;
EXECUTE index_stmt;
DEALLOCATE PREPARE index_stmt;

SET @index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'papers'
    AND index_name = 'idx_papers_active_updated'
);
SET @index_sql = IF(
  @index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_active_updated (deleted_at, updated_at, id)',
  'SELECT 1'
);
PREPARE index_stmt FROM @index_sql;
EXECUTE index_stmt;
DEALLOCATE PREPARE index_stmt;

SET @index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'papers'
    AND index_name = 'idx_papers_active_published'
);
SET @index_sql = IF(
  @index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_active_published (deleted_at, published_at, added_at, id)',
  'SELECT 1'
);
PREPARE index_stmt FROM @index_sql;
EXECUTE index_stmt;
DEALLOCATE PREPARE index_stmt;

SET @index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'papers'
    AND index_name = 'idx_papers_active_status_added'
);
SET @index_sql = IF(
  @index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_active_status_added (deleted_at, reading_status, added_at, id)',
  'SELECT 1'
);
PREPARE index_stmt FROM @index_sql;
EXECUTE index_stmt;
DEALLOCATE PREPARE index_stmt;

SET @index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE() AND table_name = 'papers'
    AND index_name = 'idx_papers_active_favorite_added'
);
SET @index_sql = IF(
  @index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_active_favorite_added (deleted_at, is_favorite, added_at, id)',
  'SELECT 1'
);
PREPARE index_stmt FROM @index_sql;
EXECUTE index_stmt;
DEALLOCATE PREPARE index_stmt;
