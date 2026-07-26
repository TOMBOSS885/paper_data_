-- 002: 分类与标签（仅管理员自有，类别自带树形结构）。
--
-- 设计要点：
--   * tags：扁平、按名称去重（不区分大小写），可选颜色，限制名称长度与字符集以防控制字符。
--   * categories：自引用树（parent_id → categories.id），按 sort_order/display_name 排序。
--   * paper_tags / paper_categories：组合主键保证同一论文不会被重复打上同一标签/分类；
--     级联删除跟随论文或标签/分类的删除；手动删除时 ON DELETE CASCADE。
--   * 表字符集与现有库保持 utf8mb4 / utf8mb4_unicode_ci，索引在 name / parent_id 上。

CREATE TABLE IF NOT EXISTS tags (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(40) NOT NULL,
  normalized_name VARCHAR(40) NOT NULL,
  color VARCHAR(16) NOT NULL DEFAULT 'teal',
  usage_count INT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_tags_normalized (normalized_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS categories (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  parent_id BIGINT UNSIGNED NULL,
  name VARCHAR(60) NOT NULL,
  normalized_name VARCHAR(60) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  paper_count INT UNSIGNED NOT NULL DEFAULT 0,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_categories_parent_name (parent_id, normalized_name),
  KEY idx_categories_parent (parent_id),
  CONSTRAINT fk_categories_parent FOREIGN KEY (parent_id) REFERENCES categories(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS paper_tags (
  paper_id CHAR(36) NOT NULL,
  tag_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (paper_id, tag_id),
  KEY idx_paper_tags_tag (tag_id),
  CONSTRAINT fk_paper_tags_paper FOREIGN KEY (paper_id) REFERENCES papers(id) ON DELETE CASCADE,
  CONSTRAINT fk_paper_tags_tag FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS paper_categories (
  paper_id CHAR(36) NOT NULL,
  category_id BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (paper_id, category_id),
  KEY idx_paper_categories_category (category_id),
  CONSTRAINT fk_paper_categories_paper FOREIGN KEY (paper_id) REFERENCES papers(id) ON DELETE CASCADE,
  CONSTRAINT fk_paper_categories_category FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;