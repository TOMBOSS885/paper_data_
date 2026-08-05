CREATE TABLE IF NOT EXISTS sync_clients (
  id CHAR(36) NOT NULL PRIMARY KEY,
  admin_id BIGINT UNSIGNED NOT NULL,
  instance_id CHAR(36) NOT NULL,
  display_name VARCHAR(120) NOT NULL,
  last_seen_at DATETIME(6) NULL,
  revoked_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_sync_client_instance (admin_id, instance_id),
  CONSTRAINT fk_sync_client_admin FOREIGN KEY (admin_id) REFERENCES admins(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_tokens (
  id CHAR(36) NOT NULL PRIMARY KEY,
  client_id CHAR(36) NOT NULL,
  token_prefix VARCHAR(16) NOT NULL,
  token_hash CHAR(64) NOT NULL UNIQUE,
  scopes_json JSON NOT NULL,
  expires_at DATETIME(6) NULL,
  last_used_at DATETIME(6) NULL,
  revoked_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_sync_token_client FOREIGN KEY (client_id) REFERENCES sync_clients(id) ON DELETE CASCADE,
  INDEX idx_sync_token_prefix (token_prefix)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_links (
  id CHAR(36) NOT NULL PRIMARY KEY,
  admin_id BIGINT UNSIGNED NOT NULL,
  paper_id CHAR(36) NOT NULL,
  external_library_key VARCHAR(160) NOT NULL,
  external_item_key VARCHAR(32) NOT NULL,
  base_metadata_json JSON NOT NULL,
  base_metadata_hash CHAR(64) NOT NULL,
  base_file_sha256 CHAR(64) NULL,
  server_version INT UNSIGNED NOT NULL DEFAULT 1,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_sync_link_item (admin_id, external_library_key, external_item_key),
  UNIQUE KEY uq_sync_link_paper (admin_id, external_library_key, paper_id),
  INDEX idx_sync_link_paper (paper_id),
  CONSTRAINT fk_sync_link_admin FOREIGN KEY (admin_id) REFERENCES admins(id) ON DELETE CASCADE,
  CONSTRAINT fk_sync_link_paper FOREIGN KEY (paper_id) REFERENCES papers(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_sessions (
  id CHAR(36) NOT NULL PRIMARY KEY,
  client_id CHAR(36) NOT NULL,
  external_library_key VARCHAR(160) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_sync_session_client FOREIGN KEY (client_id) REFERENCES sync_clients(id) ON DELETE CASCADE,
  INDEX idx_sync_session_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_session_items (
  session_id CHAR(36) NOT NULL,
  row_key VARCHAR(220) NOT NULL,
  external_item_key VARCHAR(32) NOT NULL,
  metadata_json JSON NOT NULL,
  metadata_hash CHAR(64) NOT NULL,
  file_sha256 CHAR(64) NULL,
  file_size BIGINT UNSIGNED NULL,
  PRIMARY KEY (session_id, row_key),
  CONSTRAINT fk_sync_session_item FOREIGN KEY (session_id) REFERENCES sync_sessions(id) ON DELETE CASCADE,
  INDEX idx_sync_session_item_key (session_id, external_item_key)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sync_operations (
  id CHAR(36) NOT NULL PRIMARY KEY,
  session_id CHAR(36) NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  operation_type VARCHAR(24) NOT NULL,
  status VARCHAR(24) NOT NULL,
  paper_id CHAR(36) NULL,
  result_json JSON NULL,
  request_hash CHAR(64) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_sync_operation_idempotency (session_id, idempotency_key),
  CONSTRAINT fk_sync_operation_session FOREIGN KEY (session_id) REFERENCES sync_sessions(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
