-- Bind each session to the administrator token version so a password change
-- invalidates sessions even if an explicit revocation update is interrupted.
SET @session_token_version_exists = (
  SELECT COUNT(*) FROM information_schema.columns
  WHERE table_schema = DATABASE()
    AND table_name = 'sessions'
    AND column_name = 'token_version'
);
SET @session_token_version_sql = IF(
  @session_token_version_exists = 0,
  'ALTER TABLE sessions ADD COLUMN token_version INT UNSIGNED NOT NULL DEFAULT 1 AFTER admin_id',
  'SELECT 1'
);
PREPARE session_token_version_stmt FROM @session_token_version_sql;
EXECUTE session_token_version_stmt;
DEALLOCATE PREPARE session_token_version_stmt;
