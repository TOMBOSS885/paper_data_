# 数据模型与搜索设计

## 1. MySQL 设计原则

使用 MySQL 8.0+、`utf8mb4`、InnoDB、UTC 存储时间。生产采用版本化迁移，不依赖自动迁移。应用账号只拥有目标库 CRUD 和执行预定义过程的权限；迁移账号独立。

## 2. 核心表

### `admins`

`id BIGINT PK`、`email VARCHAR(254) UNIQUE`、`display_name`、`password_hash`、`password_changed_at`、`token_version`、`failed_login_attempts`、`locked_until`、`email_verified_at`、`created_at`、`updated_at`。

### `system_settings`

`key VARCHAR(64) PK`、`value_json JSON`、`updated_at`。初始化使用 `initialized`、`initialized_at`、`schema_version`；通过唯一键和事务保证只初始化一次。

### `papers`

`id BINARY(16) PK`（UUID）、`title VARCHAR(1000)`、`abstract_text MEDIUMTEXT`、`authors_json JSON`、`doi VARCHAR(255)`、`journal VARCHAR(500)`、`publisher VARCHAR(255)`、`language VARCHAR(16)`、`published_at DATE`、`added_at DATETIME`、`reading_status ENUM`、`is_favorite`、`parse_status`、`source_type`、`version INT`、`deleted_at`。

建议唯一约束：`normalized_doi`（可空唯一）以及应用层的标题+作者+年份候选去重。

### `paper_files`

`id BINARY(16) PK`、`paper_id`、`object_key`、`original_name`、`media_type`、`size_bytes`、`sha256`、`page_count`、`scan_status`、`storage_class`、`created_at`。`object_key` 唯一；数据库不保存绝对路径。

### `tags`、`paper_tags`

标签字段：`name`、`normalized_name`、`color`、`aliases_json`、`merged_into_id`。关联表使用复合主键 `(paper_id, tag_id)`。

### `categories`、`paper_categories`

分类字段：`scheme`、`external_id`、`version`、`parent_id`、`name`、`sort_order`、`is_custom`。同一 scheme/version 下 external_id 唯一；论文可关联多个节点。

### `paper_notes`

`id`、`paper_id`、`content_markdown`、`content_plaintext`、`position_json`、`version`、`created_at`、`updated_at`。渲染前使用 allowlist sanitizer。

### `verification_codes`、`sessions`、`audit_logs`

验证码只存 `code_hash`、`purpose`、`email_normalized`、`nonce_hash`、`attempts`、`expires_at`、`consumed_at`；session 只存 refresh token 哈希、设备信息和过期时间；审计日志禁止正文、密码、验证码和完整 token。

## 3. 索引与全文搜索

- `papers(normalized_doi)`、`papers(published_at)`、`papers(added_at)`、`papers(reading_status,is_favorite)`。
- `paper_tags(tag_id,paper_id)`、`paper_categories(category_id,paper_id)`。
- 英文全文：`FULLTEXT(title, abstract_text, plaintext_content) WITH PARSER ngram` 视语言启用；中文先做 ngram/规范化字段，数据量大时迁移 Elasticsearch/OpenSearch。
- 搜索查询先解析为 AST，字段和运算符白名单化；LIKE 查询对 `%`、`_` 转义；禁止任意正则和用户提供排序表达式。
- 结果默认游标分页，最多 100 条；相关性排序使用全文 score + 时间衰减 + 收藏/阅读状态可选权重。

## 4. 解析管线

```text
上传 -> UUID 文件落盘 -> magic/MIME 校验 -> 病毒扫描
     -> 解析 worker（隔离、只读、无网络、超时）
     -> 元数据规范化（DOI/作者/日期）
     -> 去重候选 -> 用户确认 -> 入库与索引
```

解析失败不覆盖原文件，进入隔离区；重试次数和队列长度有限。PDF/Office/压缩包禁止宏、外部链接、XXE、Zip Slip、PDF JavaScript 和嵌套压缩炸弹。

## 5. 元数据标准

内部字段兼容 Dublin Core 与 CSL-JSON：`title, author, issued, container-title, DOI, URL, abstract, keyword`。导出格式优先支持 BibTeX、RIS、CSL-JSON、JSON、CSV；导入格式支持 BibTeX/RIS/CSL-JSON 元数据文件和论文正文文件。

分类内置 OECD FOS，可选导入 ACM CCS 2012、JEL、MeSH；标准分类与自定义分类通过 `scheme` 分离。

## 6. 外部论文搜索策略

- OpenAlex：适合开放学术元数据、作者/机构/主题过滤。
- Crossref：适合 DOI、期刊和出版元数据校验。
- Semantic Scholar：适合引用、相似论文和影响力补充（需遵守其 API 限额和条款）。
- Go 端统一适配器、超时 5-10 秒、响应大小上限、缓存 5-30 分钟；来源异常只影响对应来源卡片。
- 导入优先 metadata-only；只有开放获取且通过 SSRF/版权策略校验时才下载正文。
