CREATE TABLE IF NOT EXISTS admins (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email VARCHAR(254) NOT NULL UNIQUE,
  display_name VARCHAR(80) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  token_version INT UNSIGNED NOT NULL DEFAULT 1,
  failed_login_attempts INT UNSIGNED NOT NULL DEFAULT 0,
  locked_until DATETIME(6) NULL,
  email_verified_at DATETIME(6) NOT NULL,
  password_changed_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS system_settings (setting_key VARCHAR(64) NOT NULL PRIMARY KEY, value_json JSON NOT NULL, updated_at DATETIME(6) NOT NULL) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS verification_codes (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, email_normalized VARCHAR(254) NOT NULL, purpose VARCHAR(32) NOT NULL, code_hash CHAR(64) NOT NULL, attempts INT UNSIGNED NOT NULL DEFAULT 0, expires_at DATETIME(6) NOT NULL, consumed_at DATETIME(6) NULL, created_at DATETIME(6) NOT NULL, INDEX idx_codes_lookup (email_normalized,purpose,created_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS sessions (id CHAR(36) NOT NULL PRIMARY KEY, admin_id BIGINT UNSIGNED NOT NULL, token_hash CHAR(64) NOT NULL UNIQUE, csrf_hash CHAR(64) NOT NULL, ip_address VARCHAR(45) NOT NULL, user_agent VARCHAR(500) NOT NULL, expires_at DATETIME(6) NOT NULL, revoked_at DATETIME(6) NULL, created_at DATETIME(6) NOT NULL, CONSTRAINT fk_session_admin FOREIGN KEY (admin_id) REFERENCES admins(id), INDEX idx_sessions_expiry(expires_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS papers (id CHAR(36) NOT NULL PRIMARY KEY, title VARCHAR(1000) NOT NULL, abstract_text MEDIUMTEXT NOT NULL, authors_json JSON NOT NULL, doi VARCHAR(255) NULL, normalized_doi VARCHAR(255) NULL, journal VARCHAR(500) NOT NULL, language VARCHAR(16) NOT NULL DEFAULT '', published_at DATE NULL, reading_status ENUM('unread','reading','read') NOT NULL DEFAULT 'unread', is_favorite BOOLEAN NOT NULL DEFAULT FALSE, parse_status VARCHAR(32) NOT NULL DEFAULT 'queued', source_type VARCHAR(32) NOT NULL DEFAULT 'upload', version INT UNSIGNED NOT NULL DEFAULT 1, added_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL, deleted_at DATETIME(6) NULL, UNIQUE KEY uq_papers_doi(normalized_doi), INDEX idx_papers_added(added_at), INDEX idx_papers_published(published_at), INDEX idx_papers_state(reading_status,is_favorite), FULLTEXT KEY ft_papers_text(title,abstract_text)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS paper_files (id CHAR(36) NOT NULL PRIMARY KEY, paper_id CHAR(36) NOT NULL, object_key VARCHAR(255) NOT NULL UNIQUE, original_name VARCHAR(500) NOT NULL, media_type VARCHAR(255) NOT NULL, size_bytes BIGINT UNSIGNED NOT NULL, sha256 CHAR(64) NOT NULL, scan_status VARCHAR(32) NOT NULL DEFAULT 'pending', created_at DATETIME(6) NOT NULL, CONSTRAINT fk_file_paper FOREIGN KEY(paper_id) REFERENCES papers(id), INDEX idx_files_paper(paper_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE IF NOT EXISTS audit_logs (id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY, event_type VARCHAR(64) NOT NULL, actor_admin_id BIGINT UNSIGNED NULL, ip_address VARCHAR(45) NOT NULL, details_json JSON NOT NULL, created_at DATETIME(6) NOT NULL, INDEX idx_audit_event_time(event_type,created_at)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
