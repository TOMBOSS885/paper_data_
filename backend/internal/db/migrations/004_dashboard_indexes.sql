-- 004: 针对 Dashboard / facets 接口优化增加的索引。
--
-- 设计要点：
--   * `idx_papers_published` 已在 001_init.sql 里定义（来自 `INDEX idx_papers_published(published_at)`），
--     这里只补 IF NOT EXISTS 兜底兼容极端情况（曾经被手工 drop 掉），不会冲突。
--   * `idx_papers_journal` 是新增索引：facets 按 journal GROUP BY 排序，没有索引时全表扫。
--   * 使用 IF NOT EXISTS 保证幂等：升级时即使索引已存在也不会报错。
--   * 要求 MySQL 8.0+（IF NOT EXISTS 在 CREATE INDEX 上的支持从 8.0 开始）。

CREATE INDEX IF NOT EXISTS idx_papers_published ON papers(published_at);
CREATE INDEX IF NOT EXISTS idx_papers_journal ON papers(journal);