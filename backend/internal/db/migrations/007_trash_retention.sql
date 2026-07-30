-- Give papers deleted by older releases a complete recovery window from the
-- moment this migration is installed. Active taxonomy counters are rebuilt
-- because older soft-delete code did not decrement them.
UPDATE papers
SET deleted_at = UTC_TIMESTAMP(6), updated_at = UTC_TIMESTAMP(6)
WHERE deleted_at IS NOT NULL;

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

-- Keep DDL last: MySQL commits ALTER TABLE implicitly. The metadata check also
-- makes a retry safe if the process stops after ALTER but before recording the
-- migration in schema_migrations.
SET @trash_index_exists = (
  SELECT COUNT(*) FROM information_schema.statistics
  WHERE table_schema = DATABASE()
    AND table_name = 'papers'
    AND index_name = 'idx_papers_deleted_at'
);
SET @trash_index_sql = IF(
  @trash_index_exists = 0,
  'ALTER TABLE papers ADD INDEX idx_papers_deleted_at (deleted_at)',
  'SELECT 1'
);
PREPARE trash_index_stmt FROM @trash_index_sql;
EXECUTE trash_index_stmt;
DEALLOCATE PREPARE trash_index_stmt;
