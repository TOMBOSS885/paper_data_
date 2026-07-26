CREATE TABLE IF NOT EXISTS citation_formats (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(60) NOT NULL,
  builtin TINYINT(1) NOT NULL DEFAULT 0,
  template TEXT NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_citation_formats_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT IGNORE INTO citation_formats (name, builtin, template, created_at, updated_at) VALUES 
('APA', 1, '{authors} ({year}). {title}. *{journal}*.', NOW(), NOW()),
('MLA', 1, '{authors}. "{title}." *{journal}*, {year}.', NOW(), NOW()),
('Chicago', 1, '{authors}. {year}. "{title}." *{journal}*.', NOW(), NOW()),
('IEEE', 1, '{authors}, "{title}," *{journal}*, {year}.', NOW(), NOW()),
('GB/T 7714', 1, '[{authors}]. {title}[J]. {journal}, {year}.', NOW(), NOW()),
('BibTeX', 1, '@article{\n  title="{title}",\n  author="{authors}",\n  journal="{journal}",\n  year="{year}"\n}', NOW(), NOW());
