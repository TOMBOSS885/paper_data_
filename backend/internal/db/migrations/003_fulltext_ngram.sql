-- 003: 升级 papers.ft_papers_text 为 ngram 解析器，支持中文/东亚语言全文检索。
-- 仅在 MySQL 自带 ngram 插件时执行；否则保留原 Latin FULLTEXT 索引（仍可工作，仅中文不分词）。
--
-- 用 information_schema 探测 + PREPARE 动态执行，跨 MySQL 5.7/8.0/8.4 都安全。
-- 整体幂等：插件缺失时 SELECT 1 是 no-op；插件存在时 DROP/ADD 在重复执行时会因为索引名变化报错，
-- 但迁移是顺序应用（schema_migrations 表），所以生产环境只跑一次。

SET @has_ngram := (
  SELECT COUNT(*) FROM information_schema.PLUGINS WHERE PLUGIN_NAME = 'ngram'
);
SET @sql := IF(@has_ngram > 0,
  'ALTER TABLE papers DROP INDEX ft_papers_text, ADD FULLTEXT INDEX ft_papers_text (title, abstract_text) WITH PARSER ngram',
  'SELECT 1');
PREPARE stmt FROM @sql;
EXECUTE stmt;
DEALLOCATE PREPARE stmt;