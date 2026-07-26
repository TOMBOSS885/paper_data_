-- 004: 针对 Dashboard / facets 接口优化增加的索引。
--
-- 移除了 IF NOT EXISTS 语法以兼容标准 MySQL 5.7/8.0。
-- idx_papers_published 已在 001_init.sql 定义，因此此处仅新增 idx_papers_journal。

CREATE INDEX idx_papers_journal ON papers(journal);