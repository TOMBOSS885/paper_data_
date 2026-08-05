# Zotero 论文同步插件与服务端适配规范

> 状态：设计基线（待实现）
> 目标客户端：Zotero 7.x，设计时预留 Zotero 8 兼容验证
> 适配项目：本仓库 Go API + MySQL + 文件系统存储
> 最后核对官方资料：2026-08-05

## 1. 目标、边界与关键结论

插件用于在 Zotero 桌面端和本项目部署的个人论文库之间进行**用户确认后的双向同步**：

1. 用户可从当前选中条目、当前集合或当前 Zotero 文库中选择要上传的论文。
2. 插件可列出服务器存在而当前 Zotero 同步范围内不存在的论文，并允许用户选择拉取。
3. 执行同步前必须展示统一差异清单，至少明确区分：
   - `local_only`：仅 Zotero 本地有；
   - `server_only`：仅服务器有；
   - `both_same`：两边都有且内容一致；
   - `both_changed`：两边都有但元数据或附件不同；
   - `possible_match`：疑似相同但证据不足；
   - `conflict`：双方自上次同步后都修改了同一字段或附件。
4. 默认行为必须非破坏性：比较动作不写数据，未勾选条目不操作，不自动传播删除，不静默覆盖冲突。
5. 第一版同步“书目元数据 + 标签 + 一个主论文附件”。Zotero 笔记、PDF 批注、网页快照、补充材料和集合树不混入第一版主协议；后续通过独立资源类型扩展。

本项目当前的 `/api/papers` 上传接口是“收到文件后立即创建论文”，鉴权依赖浏览器 Cookie 和 CSRF，并不具备跨端映射、幂等键、批量差异、断点续传或字段级同步基线。因此不能让插件直接复用现有网页上传流程。应新增隔离的 `/api/sync/v1` API，现有网页 API 保持兼容。

## 2. 非目标

- 不替代 Zotero 官方同步，也不直接读写 `zotero.sqlite`。
- 不在后台无提示地同步整个文库。
- 不把 Zotero 的内部数值 `item.id` 当作跨设备标识。
- 不依据标题相似度自动合并论文。
- 不把服务器账号密码保存在插件首选项中。
- 不承诺把 Zotero PDF 批注写回 PDF 文件。Zotero 批注通常是独立数据对象，并不等同于 PDF 内嵌批注。
- 不允许服务器响应中携带的字段驱动任意本地文件路径、脚本或界面标记。

## 3. 官方开发约束与技术选择

Zotero 插件运行在桌面客户端内，可以调用本地 JavaScript API 和 Firefox 平台 API。Zotero 官方同时明确不建议写本地 SQLite 数据库；本设计只通过 `Zotero.Item`、`Zotero.Items`、`Zotero.Attachments`、`Zotero.Notifier` 等应用层 API 操作数据。

插件采用 Zotero 7 的 bootstrapped plugin 结构：

- 根目录使用 WebExtension 风格的 `manifest.json`，且必须包含 `applications.zotero`。
- 根目录使用 `bootstrap.js`，实现 `startup()`、`shutdown()`、`install()`、`uninstall()`。
- 窗口相关注册放在 `onMainWindowLoad()`，清理放在 `onMainWindowUnload()`；禁用或卸载时必须移除菜单、事件监听、计时器和窗口引用。
- 默认首选项写入根目录 `prefs.js`。
- 设置页使用 `Zotero.PreferencePanes.register()`。
- 本地化使用 Fluent，并给文件名、ID 和 Fluent key 加插件命名空间。
- 使用官方 API 注册菜单/设置/状态展示，避免覆盖 Zotero 原型或依赖不稳定 DOM 层级。

建议插件 ID：`paper-kb-sync@example.invalid`。正式发布前替换为项目实际控制的域名，例如 `paper-kb-sync@your-domain.example`；插件 ID 一旦发布不应再改。

### 3.1 官方资料

- [Zotero Plugin Development](https://www.zotero.org/support/dev/client_coding/plugin_development)
- [Zotero 7 for Developers](https://www.zotero.org/support/dev/zotero_7_for_developers)
- [Zotero JavaScript API](https://www.zotero.org/support/dev/client_coding/javascript_api)
- [官方 Zotero 7 示例插件 make-it-red](https://github.com/zotero/make-it-red)
- [Zotero 7.0.32 客户端数据/附件源码](https://github.com/zotero/zotero/tree/7.0.32/chrome/content/zotero/xpcom)
- [Zotero 7.0.32 HTTP 实现](https://github.com/zotero/zotero/blob/7.0.32/chrome/content/zotero/xpcom/http.js)
- [Zotero 源码](https://github.com/zotero/zotero)
- [Zotero 8 for Developers](https://www.zotero.org/support/dev/zotero_8_for_developers)

Zotero 的客户端 JavaScript API 官方文档明确说明并不完整。实现时应固定一个经过测试的 Zotero 版本矩阵，并以对应 tag 的 Zotero 源码和官方示例作为补充依据，不能只凭第三方类型声明推断运行时行为。

### 3.2 当前项目审计结论

本文以代码而不是历史设计文档为准：后端是 Go 1.22 的 `net/http + database/sql + MySQL`，前端是 React 19 + TypeScript + Vite 8。与同步最相关的现状如下：

- `papers` 已有 UUID、常用元数据、`normalized_doi` 唯一约束、网页编辑乐观锁 `version` 和软删除时间。
- `paper_files` 已有文件大小、媒体类型和 SHA-256；数据库允许多文件，但当前详情/预览/下载只取最新文件。
- `POST /api/papers` 支持多文件 multipart，流式落临时文件并计算 SHA-256、校验 PDF magic bytes、事务写数据库后原子移动；这些底层 helper 可抽取复用。
- 当前上传不能携带完整 Zotero 元数据、外部 item key 或幂等键，重试可能重复建论文。
- `GET /api/papers` 最大页大小 100，列表不返回论文 version、文件 SHA、删除 tombstone 或同步游标，不能充当同步 manifest。
- `PATCH /api/papers/{id}` 已有版本前置条件，可借鉴冲突响应；DOI 唯一冲突目前需要从泛化 500 改为同步层的明确 409。
- 下载基于 `http.ServeContent`，已有 HEAD/Range 基础；仍需增加基于 SHA-256 的强 ETag、Digest/完整性信息和同步 token 鉴权。
- 现有登录只认 `pkb_session` Cookie，写操作需要 `pkb_csrf`，12 小时网页会话不适合桌面插件。
- 回收站负责短期恢复和物理文件清理，但永久删除后没有长期同步 tombstone。
- 当前 PDF 元数据提取只读有限前缀且能力有限；Zotero 同步应以 Zotero 条目元数据为权威，不能重新解析 PDF 后覆盖它。

## 4. 总体架构

```text
Zotero 主窗口
  -> 同步对话框（只生成计划）
  -> 本地扫描器（Item API / Attachment API）
  -> 差异引擎（稳定映射、DOI、SHA-256、元数据哈希）
  -> 操作队列（上传、下载、重试、取消）
  -> HTTP 客户端（Bearer token、幂等键、超时）
  -> /api/sync/v1
       -> 同步鉴权与限流
       -> 快照/差异服务
       -> 论文与映射事务
       -> Blob 上传/下载
       -> 审计日志
       -> MySQL + UPLOAD_DIR
```

同步分为两个严格阶段：

1. **计划阶段**：扫描本地、上传轻量清单、获取服务器清单、计算差异、呈现选择。禁止创建论文或写入附件。
2. **执行阶段**：用户确认后按操作计划执行。每个操作都有幂等键、前置版本和可重试状态；单条失败不影响其他条目，最终显示逐条结果。

## 5. 论文身份与匹配规则

### 5.1 Zotero 端稳定身份

本地对象使用以下逻辑组合键：

```text
serverInstanceID + externalLibraryKey + zoteroItemKey
```

- `item.key` 是文库内稳定的 8 字符 key；不能使用会随数据库重建变化的 `item.id`。
- `externalLibraryKey` 区分文库且尽量跨设备稳定：已登录的用户库用 `user:<zoteroUserID>`，群组库用 `group:<groupID>`；无法取得账号级身份的纯本地库才退化为 `local:<clientInstanceID>:<libraryID>`。
- `serverInstanceID` 由服务器首次部署生成，防止同一插件连接两个服务器时串用映射。
- 服务端按管理员、provider、`externalLibraryKey` 和 item key 持久化映射，不能按设备建立唯一映射。这样同一个 Zotero 文库在两台电脑上不会变成两个服务端对象。
- 插件本地只保存缓存和未完成操作日志。插件重装后仍可根据文库和 item key 恢复关联。
- 若无法取得稳定的 Zotero 用户身份，两台设备上的纯本地文库不会自动视作同一文库；应提供显式的“关联文库身份”流程，禁止根据相同数字 `libraryID` 猜测。

成功建立映射后，插件可在 Zotero 条目上写入命名空间关系（例如谓词 `papersync:paper`，对象为服务器 canonical paper URI）。该 relation 只作为本地恢复和人工检查的辅助证据，服务端的外部映射仍是权威；导入前必须验证 URI 的 `serverInstanceId` 和 token 所属账号，不能把服务端返回的 URI 当作 Zotero 本地 key。

### 5.2 精确和候选匹配优先级

按以下顺序判断，命中更高优先级后停止：

| 优先级 | 证据 | 结果 |
| --- | --- | --- |
| 1 | 服务端已有 `externalLibraryKey + itemKey` 映射 | 精确 `both_*` |
| 2 | 规范化 DOI 唯一且仅命中一条活跃论文 | 可自动建议为 `both_*`，首次执行前仍显示“按 DOI 匹配” |
| 3 | 主附件 SHA-256 唯一且仅命中一条 | 可自动建议为 `both_*`，首次执行前仍显示“按文件匹配” |
| 4 | PMID、arXiv ID、ISBN 等外部标识唯一 | 候选；第一版可暂不实现 |
| 5 | 规范化标题 + 年份 + 第一作者 | `possible_match`，禁止自动合并 |
| 6 | 无可靠证据 | `local_only` 或 `server_only` |

规范化规则必须在插件和服务端共享测试向量，但最终以服务端结果为准：

- DOI：去掉 `https://doi.org/`、`http://dx.doi.org/`、`doi:` 前缀，trim，Unicode NFKC，转小写；保留 DOI 内合法标点。
- 标题：NFKC、转小写、连续空白合一、去首尾标点；标题仅用于候选匹配。
- 元数据哈希：对固定字段顺序的规范 JSON 做 SHA-256，不能直接哈希任意对象序列化结果。
- 文件哈希：对原始文件字节做 SHA-256。

### 5.3 重复项与歧义

- 服务器现有 `papers.normalized_doi` 唯一索引会拒绝重复 DOI。同步 API 遇到该情况必须返回 `duplicate_doi` 和已有 `paperId`，不能转换成 500。
- 若一个 DOI 对应多个本地 Zotero 条目，所有条目标记 `possible_match/ambiguous_local_duplicates`。
- 若同一文件哈希对应多个服务端论文，视为服务端数据异常，禁止自动选中并记录审计日志。
- 用户确认合并时只建立映射；是否覆盖元数据仍由字段差异和用户选择决定。

## 6. 同步范围与本地扫描

同步对话框提供三个入口：

1. **同步选中的论文**：取 `Zotero.getActiveZoteroPane().getSelectedItems()`，将选中的附件归一到父级普通条目。
2. **同步当前集合**：取当前集合的直接条目，提供“包含子集合”开关。
3. **比较当前文库**：仅扫描当前文库中的普通、未删除条目。

默认排除：

- 笔记、批注、独立附件、附件子项、已删除条目、Feed 条目；
- 无编辑权限的群组文库（仍可允许只上传，拉取必须禁用）；
- linked URL、网页快照和缺失的 linked file；
- 正在由 Zotero 下载或处于不可用状态的附件。

每个普通条目收集：

```json
{
  "externalLibraryKey": "user:12345",
  "localLibraryId": 1,
  "itemKey": "AB12CD34",
  "itemType": "journalArticle",
  "dateModified": "2026-08-05T10:00:00Z",
  "title": "Example",
  "creators": [{"firstName": "A", "lastName": "Li", "creatorType": "author"}],
  "abstract": "...",
  "publicationTitle": "Journal",
  "date": "2025-06",
  "doi": "10.1000/example",
  "url": "https://example.test/paper",
  "tags": [{"tag": "review", "type": 0}],
  "collections": ["ZXCV1234"],
  "primaryAttachment": {
    "itemKey": "PDFKEY01",
    "contentType": "application/pdf",
    "linkMode": "imported_file",
    "filename": "paper.pdf",
    "size": 123456,
    "mtimeMs": 1785900000000,
    "sha256": null
  },
  "metadataHash": "sha256:..."
}
```

实现时使用公开/现有 Zotero 7 API：选中项来自 `Zotero.getActiveZoteroPane().getSelectedItems()`，附件通过 `item.getAttachments()` 获取，文件路径用 `attachment.getFilePathAsync()` 与 `attachment.fileExists()` 校验。批量创建/更新使用 `Zotero.DB.executeTransaction(async () => { ... await item.save() })`；普通单项可用 `saveTx()`。服务器 JSON 导入可先构造 `new Zotero.Item(itemType)`，只把白名单字段传给 `fromJSON(..., {strict: false})`，剔除远端 key、version、parentItem 和附件存储字段后再保存。

可以注册 `Zotero.Notifier` 监听 `item`、`collection`、`collection-item`、`file` 变化，但第一版只把缓存标记为过期并刷新差异，不要在 `modify` 事件里自动上传。执行远端导入时设置 `inRemoteApply` 标志并做 debounce，避免同步动作触发回环。

### 6.1 主附件选择

第一版每篇服务器论文只同步一个主附件，选择规则为：

1. 用户在差异页手动指定的 PDF；
2. Zotero 标记为最佳附件且文件存在的 PDF；
3. 第一个可用的 imported PDF；
4. 没有 PDF 时允许只同步元数据，并显示“无可上传附件”。

多个 PDF、补充材料、DOCX 等显示数量提示但不静默丢失。后续多附件协议应使用独立的 `attachmentId`，不能覆盖现有主文件。

### 6.2 哈希性能

- 初次比较不应读取所有大文件。先发送映射 key、DOI、大小、mtime 和元数据哈希。
- 仅对无映射且 DOI 不能唯一匹配、或附件疑似变化的条目计算 SHA-256。
- 本地缓存键为 `attachmentItemKey + size + mtimeMs`，缓存值为 SHA-256；任一输入变化即失效。
- 哈希和上传都使用流式读取，UI 主线程每处理约 8 MiB 主动让出；并发默认为 2。

## 7. 差异界面与用户操作

入口建议放在 `工具 -> Paper KB 同步`，并给条目右键菜单增加“与 Paper KB 比较”。设置页只保存连接和默认策略，不承担同步主流程。

差异对话框固定包含：

- 顶部：服务器、连接状态、同步范围、重新比较按钮。
- 分段筛选：全部、仅本地、仅服务器、双方都有、冲突；每项显示数量。
- 工具栏：搜索、全选当前筛选、清除选择、只看有附件。
- 表格：复选框、状态、题名、作者/年份、DOI、本地附件、服务器附件、建议动作。
- 右侧详情：仅在选中一行时显示字段级左右对比、匹配依据、版本、错误与操作选项。
- 底部：已选择上传数、拉取数、跳过数、预计流量、取消、开始同步。

不要用颜色作为唯一状态提示。状态同时使用图标、文字和可访问名称：

| 状态 | 默认选择 | 可选动作 |
| --- | --- | --- |
| `local_only` | 不勾选 | 上传到服务器、忽略 |
| `server_only` | 不勾选 | 拉取到 Zotero、忽略 |
| `both_same` | 不勾选且折叠 | 无操作、重新上传附件、重新拉取 |
| `both_changed` | 不勾选 | 本地覆盖服务器、服务器覆盖本地、逐字段合并 |
| `possible_match` | 不勾选 | 确认为同一篇、保持为两篇 |
| `conflict` | 不勾选且阻止批量覆盖 | 逐字段选择、保留两份、跳过 |

“双方都有”是一个集合筛选，内部仍要展示 `一致`、`本地较新`、`服务器较新`、`双方变化` 和 `附件不同`。用户要求的三类必须一眼可见，但不能把真实冲突压缩成一个模糊的“都有”。

### 7.1 选择规则

- “全选”只作用于当前筛选后的可见结果，并在按钮旁显示数量。
- `both_same` 不参与默认全选。
- 冲突项不能跟随批量“本地覆盖”或“服务器覆盖”，除非用户在二次确认中明确包含冲突。
- 关闭窗口前若已有选择或执行中任务，应提示；纯比较结果可直接关闭。
- 比较超过 500 条时服务端分页，表格虚拟化；选择状态按稳定行 ID 保存，不能依赖页码。

## 8. 服务端数据模型适配

建议新增迁移 `010_zotero_sync.sql`，不要修改历史迁移。表名可在实现时调整，但约束不可省略。

```sql
CREATE TABLE sync_clients (
  id CHAR(36) NOT NULL PRIMARY KEY,
  admin_id BIGINT UNSIGNED NOT NULL,
  client_type VARCHAR(32) NOT NULL,
  display_name VARCHAR(120) NOT NULL,
  instance_id CHAR(36) NOT NULL,
  last_ack_seq BIGINT UNSIGNED NOT NULL DEFAULT 0,
  last_seen_at DATETIME(6) NULL,
  inactive_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  revoked_at DATETIME(6) NULL,
  UNIQUE KEY uq_sync_client_instance (admin_id, instance_id),
  CONSTRAINT fk_sync_client_admin FOREIGN KEY (admin_id) REFERENCES admins(id)
);

CREATE TABLE sync_tokens (
  id CHAR(36) NOT NULL PRIMARY KEY,
  client_id CHAR(36) NOT NULL,
  token_prefix VARCHAR(16) NOT NULL,
  token_hash CHAR(64) NOT NULL UNIQUE,
  scopes_json JSON NOT NULL,
  expires_at DATETIME(6) NULL,
  last_used_at DATETIME(6) NULL,
  revoked_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_sync_token_client FOREIGN KEY (client_id) REFERENCES sync_clients(id)
);

CREATE TABLE paper_external_links (
  id CHAR(36) NOT NULL PRIMARY KEY,
  admin_id BIGINT UNSIGNED NOT NULL,
  paper_id CHAR(36) NOT NULL,
  provider VARCHAR(32) NOT NULL DEFAULT 'zotero',
  external_library_key VARCHAR(160) NOT NULL,
  external_item_key VARCHAR(32) NOT NULL,
  last_client_id CHAR(36) NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_external_item (admin_id, provider, external_library_key, external_item_key),
  UNIQUE KEY uq_external_paper_in_library (admin_id, provider, external_library_key, paper_id),
  KEY idx_external_paper (paper_id),
  CONSTRAINT fk_external_paper FOREIGN KEY (paper_id) REFERENCES papers(id) ON DELETE CASCADE,
  CONSTRAINT fk_external_admin FOREIGN KEY (admin_id) REFERENCES admins(id),
  CONSTRAINT fk_external_last_client FOREIGN KEY (last_client_id) REFERENCES sync_clients(id) ON DELETE SET NULL
);

CREATE TABLE paper_external_link_states (
  link_id CHAR(36) NOT NULL,
  client_id CHAR(36) NOT NULL,
  last_synced_paper_sync_revision BIGINT UNSIGNED NOT NULL,
  last_synced_local_version VARCHAR(64) NOT NULL,
  base_metadata_json JSON NOT NULL,
  base_metadata_hash CHAR(64) NOT NULL,
  base_file_sha256 CHAR(64) NULL,
  updated_at DATETIME(6) NOT NULL,
  PRIMARY KEY (link_id, client_id),
  CONSTRAINT fk_link_state_link FOREIGN KEY (link_id) REFERENCES paper_external_links(id) ON DELETE CASCADE,
  CONSTRAINT fk_link_state_client FOREIGN KEY (client_id) REFERENCES sync_clients(id) ON DELETE CASCADE
);

CREATE TABLE sync_link_reservations (
  id CHAR(36) NOT NULL PRIMARY KEY,
  operation_id CHAR(36) NOT NULL,
  admin_id BIGINT UNSIGNED NOT NULL,
  provider VARCHAR(32) NOT NULL,
  external_library_key VARCHAR(160) NOT NULL,
  paper_id CHAR(36) NOT NULL,
  status VARCHAR(24) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_active_link_reservation (admin_id, provider, external_library_key, paper_id),
  CONSTRAINT fk_reservation_admin FOREIGN KEY (admin_id) REFERENCES admins(id),
  CONSTRAINT fk_reservation_paper FOREIGN KEY (paper_id) REFERENCES papers(id) ON DELETE CASCADE
);

CREATE TABLE sync_changes (
  seq BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  admin_id BIGINT UNSIGNED NOT NULL,
  entity_type VARCHAR(32) NOT NULL,
  entity_id CHAR(36) NOT NULL,
  operation VARCHAR(24) NOT NULL,
  entity_revision BIGINT UNSIGNED NOT NULL,
  changed_fields_json JSON NOT NULL,
  created_at DATETIME(6) NOT NULL,
  KEY idx_sync_changes_admin_seq (admin_id, seq),
  CONSTRAINT fk_sync_change_admin FOREIGN KEY (admin_id) REFERENCES admins(id)
);

CREATE TABLE sync_tombstones (
  id CHAR(36) NOT NULL PRIMARY KEY,
  admin_id BIGINT UNSIGNED NOT NULL,
  entity_type VARCHAR(32) NOT NULL,
  entity_id CHAR(36) NOT NULL,
  external_links_json JSON NOT NULL,
  last_snapshot_json JSON NOT NULL,
  deleted_seq BIGINT UNSIGNED NOT NULL,
  deleted_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NULL,
  UNIQUE KEY uq_sync_tombstone_entity (admin_id, entity_type, entity_id),
  KEY idx_sync_tombstone_seq (admin_id, deleted_seq),
  CONSTRAINT fk_sync_tombstone_admin FOREIGN KEY (admin_id) REFERENCES admins(id)
);

CREATE TABLE sync_sessions (
  id CHAR(36) NOT NULL PRIMARY KEY,
  client_id CHAR(36) NOT NULL,
  scope_json JSON NOT NULL,
  snapshot_status VARCHAR(24) NOT NULL,
  server_cursor BIGINT UNSIGNED NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_sync_session_client FOREIGN KEY (client_id) REFERENCES sync_clients(id) ON DELETE CASCADE,
  KEY idx_sync_session_expiry (expires_at)
);

CREATE TABLE sync_operations (
  id CHAR(36) NOT NULL PRIMARY KEY,
  client_id CHAR(36) NOT NULL,
  session_id CHAR(36) NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  operation_type VARCHAR(32) NOT NULL,
  paper_id CHAR(36) NULL,
  request_hash CHAR(64) NOT NULL,
  status VARCHAR(24) NOT NULL,
  result_json JSON NULL,
  error_code VARCHAR(64) NULL,
  completed_at DATETIME(6) NULL,
  expires_at DATETIME(6) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  updated_at DATETIME(6) NOT NULL,
  UNIQUE KEY uq_sync_idempotency (client_id, idempotency_key),
  CONSTRAINT fk_sync_operation_client FOREIGN KEY (client_id) REFERENCES sync_clients(id),
  CONSTRAINT fk_sync_operation_session FOREIGN KEY (session_id) REFERENCES sync_sessions(id) ON DELETE SET NULL
);

CREATE TABLE sync_idempotency (
  client_id CHAR(36) NOT NULL,
  route VARCHAR(160) NOT NULL,
  idempotency_key VARCHAR(128) NOT NULL,
  request_hash CHAR(64) NOT NULL,
  status_code SMALLINT UNSIGNED NOT NULL,
  response_json JSON NOT NULL,
  created_at DATETIME(6) NOT NULL,
  expires_at DATETIME(6) NOT NULL,
  PRIMARY KEY (client_id, route, idempotency_key),
  CONSTRAINT fk_sync_idempotency_client FOREIGN KEY (client_id) REFERENCES sync_clients(id) ON DELETE CASCADE
);

CREATE TABLE blob_upload_sessions (
  id CHAR(36) NOT NULL PRIMARY KEY,
  client_id CHAR(36) NOT NULL,
  expected_sha256 CHAR(64) NOT NULL,
  size_bytes BIGINT UNSIGNED NOT NULL,
  media_type VARCHAR(255) NOT NULL,
  original_name VARCHAR(500) NOT NULL,
  status VARCHAR(24) NOT NULL,
  received_bytes BIGINT UNSIGNED NOT NULL DEFAULT 0,
  expires_at DATETIME(6) NOT NULL,
  completed_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL,
  CONSTRAINT fk_blob_upload_client FOREIGN KEY (client_id) REFERENCES sync_clients(id) ON DELETE CASCADE,
  KEY idx_blob_upload_expiry (expires_at)
);

CREATE TABLE blob_upload_parts (
  upload_id CHAR(36) NOT NULL,
  part_number INT UNSIGNED NOT NULL,
  start_byte BIGINT UNSIGNED NOT NULL,
  end_byte BIGINT UNSIGNED NOT NULL,
  sha256 CHAR(64) NOT NULL,
  size_bytes BIGINT UNSIGNED NOT NULL,
  object_key VARCHAR(255) NOT NULL,
  created_at DATETIME(6) NOT NULL,
  PRIMARY KEY (upload_id, part_number),
  CONSTRAINT fk_blob_part_upload FOREIGN KEY (upload_id) REFERENCES blob_upload_sessions(id) ON DELETE CASCADE
);
```

`sync_changes.seq` 是单调递增的全局 `serverCursor`，`papers.sync_revision` 是单篇论文的 `paperSyncRevision`，现有 `papers.version` 只叫 `webEditVersion`。所有同步可见的论文、文件、标签关系、恢复或删除操作都必须在业务写入的**同一数据库事务**中递增 `paperSyncRevision` 并追加 change；不能使用 `updated_at` 充当游标，否则同一时间的多次变更、分页和删除都会漏项。

`paper_external_links` 是文库级 canonical 映射，`paper_external_link_states` 才保存每台客户端的三方合并基线。设备 A 同步后不能改写设备 B 的 base。`uq_external_paper_in_library` 保证一篇服务端论文在同一 Zotero 文库只绑定一个 item；pull 计划在同一事务中创建短期 `sync_link_reservations`，ack 成功后转为正式 link 并释放 reservation。若另一设备已完成关联则返回 `linked_in_library`，等待 Zotero 官方同步把该 item 带到本机，禁止再次创建。

`sync_tombstones` 与现有 10 天回收站不是一回事。论文永久清理前，必须把最后快照和外部映射复制为 tombstone，再允许外键级联删除原映射。tombstone 至少保留 365 天；更稳妥的清理条件是“超过最短保留期，且所有活跃客户端的 `last_ack_seq` 都越过 `deleted_seq`”。长时间不活跃的客户端需先标为 inactive，不能永久阻止清理。

大量 manifest 不应只放在 Go 进程内存。实现时增加带 `session_id + row_id` 唯一键的 `sync_manifest_items` 暂存表，按会话过期清理。会话创建时应在事务中物化服务器侧清单，或从 `sync_changes` 重建 `<= serverCursor` 的历史快照；只记录起始游标后再查询实时表会让分页来自不同状态。分片表必须按 part 编号和 hash 幂等，以支持跨进程重启恢复。

`sync_idempotency` 覆盖 session 创建、manifest page、operation、blob prepare/complete 和 ack，不只覆盖 `sync_operations`。相同 route/key/hash 返回原响应；相同 key 不同 hash 返回 `409 idempotency_mismatch`；记录过期后返回 `410 operation_expired`，客户端先按外部映射、paper ID 和内容 hash 对账再新建动作。

长文件上传可以跨越 30 分钟比较会话：operation 创建后冻结需要的元数据快照、blob ID 和 `paperSyncRevision`，session 过期清理不能取消正在上传或已计划的 operation。Blob 状态至少为 `pending -> uploading -> complete/quarantined/failed/expired`；相同 part/range/hash 重传返回原成功，不同 range/hash 返回 `409 part_conflict`，complete 后禁止覆盖。

### 8.1 是否修改现有表

- `papers.version` 继续作为现有网页元数据乐观锁，但不能直接作为完整同步版本：当前标签、分类、重提取和软删除并不都递增它。应增加 `papers.sync_revision`，并改造所有同步可见的写路径在同一事务中递增它和追加 `sync_changes`。
- `paper_files.sha256` 已存在，可直接用于 Blob 去重和附件差异。
- `paper_files` 实际允许一篇论文多条文件，但现有读取只取最新一条；第一版明确只操作“主附件”，实现时建议增加 `role ENUM('primary','supplement')` 与唯一的活跃主附件约束。
- `papers.source_type` 对 Zotero 上传记录为 `zotero`。
- 建议给 `paper_files.sha256` 增加普通索引；是否全局唯一取决于是否允许同一 PDF 对应多条论文，第一版不设全局唯一。
- 现有 `normalized_doi` 唯一约束保留；同步接口把唯一约束错误转换为 `409 duplicate_doi`。还需定义命中回收站 DOI 时的 `deleted_match`，让用户选择恢复或保持两份。

## 9. 鉴权与连接

插件不能依赖 `pkb_session`/`pkb_csrf` 浏览器 Cookie。同步 API 使用：

```http
Authorization: Bearer pkb_zot_<prefix>.<secret>
X-PKB-Client-Version: 0.1.0
X-Request-ID: <uuid>
```

服务端只保存 token 的 HMAC-SHA-256 或专用慢哈希结果以及可检索 prefix，明文只显示一次。token scopes 至少拆分为：

- `papers:read`
- `papers:write`
- `files:read`
- `files:write`

第一版可在网页安全设置中让管理员创建并复制个人访问令牌；插件通过 Mozilla Login Manager 保存令牌，首选项只保存服务器 URL、token 引用和非敏感默认项。后续可实现 device-code 配对以避免复制令牌。

网页管理接口建议为：

```http
POST /api/auth/sync-tokens
GET /api/auth/sync-tokens
DELETE /api/auth/sync-tokens/{tokenId}
```

创建请求只接受名称、scope 和可选过期时间；响应仅在创建时返回一次完整 token，列表只返回 prefix、名称、scope、创建/最后使用/过期/撤销时间。创建和撤销继续走现有 Cookie + CSRF 管道，不能让 Bearer token 自己创建更高权限 token。

必要规则：

- 生产环境只允许 HTTPS；不提供“忽略证书错误”。自签名证书必须由操作系统/Zotero 信任链正确信任。
- 服务器 URL 必须是绝对 `https://` URL；开发模式可显式允许 `http://127.0.0.1` 或 `http://localhost`。
- 禁止重定向到不同 origin 后继续携带 Authorization。
- token 可撤销、可设置过期时间，修改管理员密码时可选择撤销全部同步 token。
- Bearer 请求不做 CSRF 校验；Cookie 请求继续执行现有 CSRF。中间件必须先识别鉴权类型，不能简单把 `/api/sync` 加入全局 CSRF 白名单后又接受 Cookie。
- CORS 对桌面插件通常不是鉴权机制。即使允许插件 origin，也必须验证 Bearer token；预检头需加入 `Authorization`、`Idempotency-Key`、`Content-Range`、`If-Match`、`If-Range`。

## 10. `/api/sync/v1` 协议

所有 JSON 成功响应沿用项目现有 `{ "data": ... }` 外壳；错误沿用 `{ "error": { "code", "message", "details" } }`。时间使用 UTC RFC 3339，大小使用字节，哈希写成小写十六进制并明确算法字段。

### 10.1 能力与连接测试

```http
GET /api/sync/v1/capabilities
```

```json
{
  "data": {
    "protocolVersion": "1.0",
    "serverInstanceId": "550e8400-e29b-41d4-a716-446655440000",
    "serverCursor": 42,
    "maxManifestItems": 500,
    "maxUploadBytes": 209715200,
    "simpleUploadThreshold": 16777216,
    "chunkSize": 8388608,
    "supportedItemTypes": ["journalArticle", "conferencePaper", "book", "bookSection", "thesis", "report", "preprint"],
    "supportedAttachmentTypes": ["application/pdf"],
    "features": ["metadata", "tags", "primaryAttachment", "rangeDownload", "resumableUpload"]
  }
}
```

`401` 区分 `token_invalid`、`token_expired`、`token_revoked`；插件对这些错误停止自动重试。

### 10.2 创建比较会话

```http
POST /api/sync/v1/sessions
Idempotency-Key: <uuid>
Content-Type: application/json
```

```json
{
  "clientInstanceId": "0ddf...",
  "scope": {"kind": "selected", "externalLibraryKey": "user:12345", "label": "3 selected items"},
  "manifestCompleteness": "partial",
  "items": [
    {
      "externalLibraryKey": "user:12345",
      "itemKey": "AB12CD34",
      "localVersion": "2026-08-05T10:00:00Z",
      "itemType": "journalArticle",
      "title": "Example",
      "year": 2025,
      "firstAuthor": "Li",
      "doi": "10.1000/example",
      "metadataHash": "5b8f...",
      "primaryFile": {"sizeBytes": 123456, "sha256": null}
    }
  ],
  "deletedItems": [],
  "page": 1,
  "isLastPage": true
}
```

清单很多时允许先创建空会话，再 `POST /sessions/{id}/manifest-pages`。服务端只有收到最后一页且数量、请求哈希一致后才把会话置为 `ready`。会话默认 30 分钟过期。

`manifestCompleteness` 只有完整扫描整个 external library 时才能为 `complete`。selected、collection、分页未完成或 Zotero 数据未加载完均为 `partial`，缺失项绝不能解释成删除。`deletedItems` 只包含插件已知映射且明确位于 Zotero Trash/本地 tombstone 的条目；只有显式 trash 记录或 complete manifest 才能生成 `local_trashed/local_missing`。

### 10.3 获取差异

```http
GET /api/sync/v1/sessions/{sessionId}/diff?cursor=<opaque>&pageSize=100
```

```json
{
  "data": {
    "sessionId": "...",
    "serverCursor": 42,
    "counts": {
      "localOnly": 2,
      "serverOnly": 4,
      "bothSame": 8,
      "bothChanged": 1,
      "possibleMatch": 1,
      "conflict": 0
    },
    "items": [
      {
        "rowId": "local:1:AB12CD34",
        "status": "both_changed",
        "matchBasis": "external_link",
        "localRef": {"externalLibraryKey": "user:12345", "itemKey": "AB12CD34", "localVersion": "..."},
        "serverRef": {"paperId": "...", "paperSyncRevision": 3},
        "fieldDiffs": [
          {"field": "title", "base": "Old", "local": "Local", "server": "Old", "resolution": "local"}
        ],
        "fileDiff": {"localSha256": "...", "serverSha256": "...", "state": "different"},
        "allowedActions": ["push", "pull", "merge", "skip"],
        "recommendedAction": "push"
      }
    ],
    "nextCursor": null
  }
}
```

服务器返回 opaque cursor，插件不得解析。`server_only` 必须相对于本次选择的语义显示：

- “比较当前文库”时表示当前文库无对应项；
- “同步选中项”时服务器通常会有大量未选中的论文，默认不应全部列出。此入口应提供“同时显示服务器可拉取论文”开关，开启后清楚标注不属于本地选择范围。

### 10.4 补充文件哈希

服务端若无法仅凭映射/DOI判断，返回：

```json
{"needFileHashes":[{"externalLibraryKey":"user:12345","itemKey":"AB12CD34","attachmentItemKey":"PDFKEY01"}]}
```

插件计算后调用：

```http
POST /api/sync/v1/sessions/{sessionId}/file-hashes
```

服务端重新计算受影响的 diff 行。一次最多提交 100 个哈希。

### 10.5 提交操作计划

```http
POST /api/sync/v1/sessions/{sessionId}/operations
Idempotency-Key: <uuid>
```

```json
{
  "basedOnServerCursor": 42,
  "operations": [
    {
      "operationId": "op-uuid",
      "rowId": "local:1:AB12CD34",
      "action": "push",
      "expectedLocalVersion": "2026-08-05T10:00:00Z",
      "expectedPaperSyncRevision": 3,
      "fieldPolicy": {"title":"local","abstract":"server","tags":"union"},
      "filePolicy": "local"
    }
  ]
}
```

若全局 `serverCursor` 变化，服务端不能一律拒绝：只要目标论文的 `paperSyncRevision` 没变可继续；目标项已变则返回 `409 sync_snapshot_stale` 和新的该行差异。每条 operation 独立返回 `planned`、`requires_upload`、`ready_to_download`、`skipped` 或错误。

### 10.6 上传 Blob

先探测，避免重复传输：

```http
POST /api/sync/v1/blobs/prepare
Idempotency-Key: <uuid>

{"sha256":"...","sizeBytes":123456,"mediaType":"application/pdf","filename":"paper.pdf"}
```

返回 `already_exists`，或返回 `uploadId`、`chunkSize`、`uploadedParts`、`expiresAt`。分片接口：

```http
PUT /api/sync/v1/blobs/{uploadId}/parts/{partNumber}
Content-Range: bytes 0-8388607/12345678
X-Content-SHA256: <本分片哈希>
```

完成接口：

```http
POST /api/sync/v1/blobs/{uploadId}/complete
Idempotency-Key: <uuid>

{"sha256":"<整文件哈希>","parts":[{"number":1,"sha256":"..."}]}
```

服务端必须在临时目录流式写入、限制总大小/分片数/并发、校验 PDF magic bytes、整文件大小和 SHA-256，然后原子移动。临时分片 24 小时后清理。客户端取消只调用取消接口，不假设连接断开就代表服务端已删除临时数据。

小于能力响应阈值时可以提供单请求 `POST /blobs/simple`，但最终仍返回统一 `blobId`。

### 10.7 提交上传论文

```http
POST /api/sync/v1/operations/{operationId}/commit-push
Idempotency-Key: <uuid>
If-Match: "paper-sync-revision-3"
```

正文包含完整、已验证的 Zotero 字段和可选 `blobId`。服务端在一个数据库事务中：

1. 校验操作、版本、token scope 和 blob 所有权；
2. 新建或更新 `papers`；
3. 关联主附件；
4. 建立/更新 `paper_external_links` 及三方合并基线；
5. 增加 `paperSyncRevision` 并追加新的 `serverCursor` change；
6. 写审计日志并提交。

文件移动与数据库无法共享事务，因此要采用 staging + 可恢复状态：数据库提交失败删除 staging；数据库成功但最终移动失败将文件记为 `storage_error` 并由恢复任务重试，不能返回虚假的成功。

### 10.8 拉取论文和附件

```http
GET /api/sync/v1/papers/{paperId}
GET /api/sync/v1/papers/{paperId}/primary-file
Range: bytes=0-
If-Range: "sha256:<hash>"
```

元数据响应必须包含 `paperSyncRevision`、完整哈希、文件大小和下载强 ETag。断点续传使用 `Range + If-Range`；ETag 不匹配时服务器返回完整 `200`，插件丢弃旧片段而不是追加。文件下载还需支持 `HEAD`、`206`、`Content-Length` 和 `Content-Range`；插件先下载到插件专用临时目录，完成后校验大小和 SHA-256，再通过 Zotero API 导入。

本地创建成功后调用：

```http
POST /api/sync/v1/operations/{operationId}/ack-pull
Idempotency-Key: <uuid>

{"externalLibraryKey":"user:12345","itemKey":"NEWKEY12","localVersion":"...","paperSyncRevision":3,"metadataHash":"...","fileSha256":"..."}
```

pull operation 在计划时冻结元数据快照、`paperSyncRevision` 和 blob ID，读取与 ack 必须绑定这一版本，避免把新元数据和旧附件混成共同基线。`ack-pull` 才建立外部映射。如果本地创建成功但确认请求失败，重试时插件先按 `operationId` 查询结果并补发 ack，不能再次创建 Zotero 条目。

### 10.9 操作状态与恢复

```http
GET /api/sync/v1/operations/{operationId}
POST /api/sync/v1/operations/{operationId}/cancel
```

同一 `Idempotency-Key` 搭配不同请求体必须返回 `409 idempotency_mismatch`。相同请求体返回原结果。执行结果至少保留 30 天，便于插件崩溃后恢复。

增量游标和 tombstone 确认接口：

```http
GET /api/sync/v1/changes?after=<serverCursor>&limit=500
POST /api/sync/v1/changes/ack

{"serverCursor": 1234}
```

变化响应包含 `nextCursor`、`minAvailableCursor` 和 upsert/trash/restore/delete 事件。游标早于 `minAvailableCursor` 时返回 `410 cursor_expired`，客户端丢弃增量假设并做一次完整比较；成功全量同步后才恢复 active 并推进 `last_ack_seq`。服务端可按明确的长期未使用策略设置 `inactive_at`，重新激活必须全量同步。

恢复论文必须在一个事务中恢复或重建 external link、递增 `paperSyncRevision`、追加 `restore` change，并把旧 tombstone 标记为 superseded；旧 tombstone 此后不得再参与匹配。

### 10.10 错误码

| HTTP | code | 插件行为 |
| --- | --- | --- |
| 400 | `invalid_manifest` | 标出具体条目/字段，不重试 |
| 401 | `token_invalid/expired/revoked` | 停止队列并要求重新连接 |
| 403 | `scope_denied` | 禁用对应动作 |
| 404 | `paper_not_found/blob_not_found` | 刷新该行差异 |
| 409 | `version_conflict/sync_snapshot_stale` | 重新比较该条目 |
| 409 | `duplicate_doi/ambiguous_match` | 转为人工匹配 |
| 409 | `idempotency_mismatch` | 视为客户端缺陷并停止该操作 |
| 409 | `linked_in_library/part_conflict` | 刷新映射或分片状态，不覆盖 |
| 410 | `cursor_expired/operation_expired` | 全量重对账，不直接重复创建 |
| 413 | `file_too_large/manifest_too_large` | 提示限制或改用分页 |
| 415 | `unsupported_media_type` | 允许仅同步元数据 |
| 422 | `hash_mismatch/invalid_pdf` | 删除临时文件并重新选择 |
| 429 | `rate_limited` | 遵守 `Retry-After`，带抖动退避 |
| 507 | `quota_exceeded` | 停止文件上传，元数据是否继续由用户确认 |
| 5xx | `internal_error/storage_unavailable` | 有上限重试并保留恢复状态 |

## 11. 元数据映射

| Zotero | 服务端现有字段 | 规则 |
| --- | --- | --- |
| `title` | `papers.title` | 必填；空值用附件名并标记质量警告 |
| `abstractNote` | `abstract_text` | 纯文本；限制长度，拒绝控制字符 |
| creators(author) | `authors_json` | 第一版保留姓名与 creatorType，服务端模型需从字符串数组升级为对象数组 |
| `DOI` | `doi/normalized_doi` | 保存展示值，同时服务端规范化 |
| `publicationTitle` | `journal` | 非期刊类型按映射表使用 `bookTitle`/`conferenceName`/`publisher` |
| `date` | `published_at` | Zotero 日期可能不完整；现有 DATE 会丢失“仅年份/年月”精度，建议新增原始日期与精度字段 |
| item type | `source_type` 不足 | 建议新增 `item_type`，不要把类型塞进 `source_type` |
| manual tags | `tags` | 默认仅同步手工标签；自动标签由设置显式开启 |
| collections | `categories` | 第一版不自动同步；两者语义和层级生命周期不同 |
| URL/ISBN/ISSN/PMID/arXiv | 当前缺失 | 建议新增 `identifiers_json` 与 `url` |
| `dateAdded/dateModified` | `added_at/updated_at` | 服务端自身时间不应被外部覆盖；原始值放 `external_dates_json` |

服务端当前年份写成 `YYYY-01-01`。为了往返不失真，建议新增：

```sql
ALTER TABLE papers
  ADD COLUMN item_type VARCHAR(64) NOT NULL DEFAULT 'journalArticle',
  ADD COLUMN published_raw VARCHAR(128) NOT NULL DEFAULT '',
  ADD COLUMN published_precision ENUM('unknown','year','month','day') NOT NULL DEFAULT 'unknown',
  ADD COLUMN identifiers_json JSON NULL,
  ADD COLUMN metadata_json JSON NULL,
  ADD COLUMN sync_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
  ADD COLUMN url VARCHAR(2048) NOT NULL DEFAULT '';
```

`authors_json` 当前约定为字符串数组，不能承载 Zotero 的 creatorType、机构作者和姓名模式；保留它供现有网页读取，同时在 `metadata_json` 中以带 schema 版本的 Zotero/CSL 结构保存完整 creators 和未结构化字段。迁移完成后再按读路径逐步切换，避免旧网页一次性崩溃。

若暂不迁移，插件拉取时只能保证本项目现有字段可逆，界面必须提示未支持字段不会上传，而不是默默宣称“完全同步”。

## 12. 三方合并和冲突策略

每条映射保存上次成功同步的 `base_metadata_json`。下次比较字段 `f`：

```text
local == base && server == base  -> 一致
local != base && server == base  -> 建议本地推送
local == base && server != base  -> 建议服务器拉取
local == server                  -> 一致并更新基线
local != base && server != base && local != server -> 冲突
```

默认字段策略：

- 标题、摘要、DOI、日期、期刊、作者：三方合并，冲突人工选择。
- 标签：默认集合并集；用户可选本地覆盖或服务器覆盖。
- 阅读状态、收藏：当前 Zotero 没有与项目完全等价的标准字段，默认只保留服务器端；如需同步应使用可配置标签映射，不能猜测。
- 主附件：哈希相同即一致；一侧变化则建议该侧；双方变化必须人工选择或保留两份。
- 空字符串是有效修改，不能当成“未提供”。缺失字段与显式清空必须在 JSON 中区分。

删除规则：

- 第一版不自动传播删除。
- 服务端论文在回收站时 diff 为 `server_trashed`，允许“恢复服务器论文”或“保持本地”；不得因为服务器回收站状态删除 Zotero 条目。
- 本地条目进入 Zotero 回收站时标为 `local_trashed`，允许“从服务器重新创建副本”或“在服务器移入回收站”，后者必须单独确认。
- 永久删除前把旧映射和最后快照写入 `sync_tombstones`；tombstone 至少保留 365 天，或直到所有活跃客户端的变更游标都越过删除序号，防止长期离线客户端把另一端对象重新当成新论文。

## 13. 插件工程结构

建议在仓库新增独立目录 `zotero-plugin/`：

```text
zotero-plugin/
  manifest.json
  bootstrap.js
  prefs.js
  package.json
  tsconfig.json
  scripts/
    build.mjs
    package.mjs
  src/
    main.ts
    lifecycle.ts
    commands.ts
    api/
      client.ts
      auth.ts
      contracts.ts
    zotero/
      scanner.ts
      mapper.ts
      attachments.ts
      importer.ts
      notifier.ts
    sync/
      compare.ts
      matcher.ts
      planner.ts
      executor.ts
      merge.ts
      recovery.ts
    storage/
      credentials.ts
      cache.ts
    ui/
      sync-dialog.xhtml
      sync-dialog.ts
      sync-dialog.css
      preferences.xhtml
      preferences.ts
      preferences.css
  locale/
    zh-CN/paper-kb-sync.ftl
    en-US/paper-kb-sync.ftl
  icons/
  test/
    unit/
    integration/
  dist/
```

职责边界：

- `scanner` 只读取 Zotero 对象并生成不可变 snapshot。
- `matcher` 实现与服务端一致的纯匹配规则，可用固定测试向量测试。
- `planner` 把 diff 和用户选择转为操作，不执行 I/O。
- `executor` 控制有限并发、取消、重试和进度。
- `importer` 是唯一允许写 Zotero 条目/附件的模块。
- `client` 是唯一发 HTTP 的模块，统一超时、认证、错误解析和敏感信息脱敏。
- `credentials` 只通过 Login Manager 读写 token；日志永远不打印 Authorization、完整 URL 查询密钥或论文正文。

### 13.1 `manifest.json` 基线

```json
{
  "manifest_version": 2,
  "name": "Paper KB Sync",
  "version": "0.1.0",
  "description": "Selectively synchronize papers with a private Paper KB server",
  "author": "Your Name",
  "icons": {"48": "icons/icon-48.png", "96": "icons/icon-96.png"},
  "applications": {
    "zotero": {
      "id": "paper-kb-sync@your-domain.example",
      "update_url": "https://your-domain.example/zotero/updates.json",
      "strict_min_version": "7.0",
      "strict_max_version": "7.0.*"
    }
  }
}
```

发布前用实际测试结果设置最大版本。官方建议最大版本写成已测试的最新 minor（例如 `7.0.*`），不要在未验证 Zotero 8 时写 `8.*`；若只是兼容范围更新，可通过 `updates.json` 调整而无需重新分发业务代码。

### 13.2 生命周期要求

`startup()`：

1. 初始化非窗口服务和偏好默认值；
2. 注册设置页；
3. 注册 Notifier 观察者（第一版只用于缓存失效，不自动上传）；
4. 对所有现有主窗口调用窗口加载逻辑。

`onMainWindowLoad()`：注册工具菜单、条目上下文菜单、命令和 Fluent 文件。必须支持窗口被反复打开关闭。

`onMainWindowUnload()`：移除属于该窗口的监听器、DOM、AbortController 和引用。

`shutdown()`：取消网络请求和计时器、注销 Notifier、移除所有窗口 UI、销毁运行时注册。应用退出原因下也不得启动新异步任务。

`uninstall()`：默认保留非敏感缓存以支持重装；用户从设置页选择“断开并清除”时才撤销 token 并清除凭据。卸载钩子不应假设网络可用。

Zotero 7 已移除 XUL overlay。窗口加载时用 `document.createXULElement('menuitem')` 等方式挂到 Tools 菜单和条目上下文菜单，所有节点 ID 加插件前缀并在 unload 时移除。差异窗口使用插件自己的 `sync-dialog.xhtml`，通过 `window.openDialog(rootURI + 'sync-dialog.xhtml', ..., 'chrome,dialog=no,modal,centerscreen,resizable=yes', io)` 打开；`.xul` 文件改为 `.xhtml`，复杂差异表使用 HTML `table`/`div` 和 checkbox，不依赖旧 XUL tree 的私有结构。

设置页调用 `Zotero.PreferencePanes.register({pluginID, src: 'preferences.xhtml', scripts: ['preferences.js'], stylesheets: ['preferences.css']})`。首选项表单直接绑定 `preference="extensions.paper-kb-sync.<key>"`，脚本通过 `Zotero.Prefs.get/set` 读写。共享主窗口中先调用 `window.MozXULElement.insertFTLIfNeeded('paper-kb-sync.ftl')`；所有 Fluent 文件名和 key 使用插件前缀，关闭窗口时清掉注入的 localization link。

### 13.3 Zotero 本地写入顺序

拉取一篇论文：

1. 校验目标文库可编辑和 item type 可用；
2. 在任何 Zotero 写入前原子落 recovery journal，记录 operation、server paper、冻结 revision/hash 和阶段；
3. 在 Zotero 事务中创建普通条目、设置字段/creators/tags，并写入 operation/server paper relation 后 `save()`；
4. 父条目保存后立即把生成的 item key 原子写回 journal；
5. 事务外用 `Zotero.HTTP.download()` 下载并校验临时附件；
6. 使用 Zotero 7 的 `Zotero.Attachments.importFromFile({file: tempPath, parentItemID, contentType: 'application/pdf', title})` 把文件作为子附件导入；目标版本升级时以对应 Zotero 源码确认实参；
7. 附件成功后更新 journal 并调用服务端 `ack-pull`；
8. ack 成功后删除 recovery journal 和临时文件。

附件导入 API 的实参在实现时必须对照目标 Zotero tag 的源码确认。不能用 `IOUtils.copy()` 直接写 Zotero storage 目录，也不能自行生成 Zotero key。

如果元数据创建成功但附件失败，保留元数据条目并在结果中显示“部分成功”；重试先按 journal 的 item key，再按 operation relation 查找相同父条目，只补附件，不再创建父条目。

## 14. 网络、队列和恢复

- 元数据请求超时建议 30 秒，单分片上传/下载空闲超时 60 秒；总任务不设过短硬超时。
- 默认同时处理 2 个文件、4 个纯元数据请求；由设置允许降为 1，不建议开放无上限并发。
- 只自动重试网络错误、`408`、`429` 和部分 `5xx`。退避为 1、2、4、8、16 秒加 0-30% 抖动，最多 5 次，并遵守 `Retry-After`。
- `400/401/403/404/409/413/415/422` 不盲重试。
- 每次执行生成本地 recovery journal，只保存 operation ID、阶段、临时文件路径、目标 library/key 和非敏感校验信息，不保存 token。
- Zotero 重启后发现未完成 journal 时，先询问服务端 operation 状态，再继续或清理。
- “取消”停止尚未开始的条目并中止网络；已提交成功的服务器事务不回滚，界面准确显示已完成数量。
- 系统睡眠/网络切换视作可恢复中断；恢复后先重查 operation 状态和文件 offset。

## 15. 安全要求

### 15.1 服务端

- 同步路由使用独立鉴权中间件、scope 检查和每 token/IP 限流。
- 所有 ID 都通过参数绑定查询；服务端不信任客户端给出的 `paperId` 与本地 key 归属关系。
- 上传文件名只作展示，存储 key 必须由服务端生成；下载头继续使用安全文件名处理。
- 临时和最终文件权限保持 `0600`，目录 `0700`。
- 限制 manifest 数量、JSON 深度、字符串长度、creator/tag 数量、单 tag 长度和解压行为。
- PDF magic bytes 只是基础检查；生产环境建议接入恶意文件扫描并在完成前保持隔离状态。
- 审计事件至少包含 token/client、operation、paper、动作、结果、请求 ID、IP 和时间，不记录 token 或摘要全文。
- Nginx 必须允许 `Authorization`、`Range`、`Content-Range`、`If-Match`、`If-Range`，并为分片路由配置与 Go 一致的 body 限制；禁用代理请求缓冲需经过磁盘与流控评估。

### 15.2 插件

- token 存入 Login Manager，绝不放 `prefs.js`、日志、异常详情或导出的诊断包。
- 所有服务器文本通过 `textContent` 或安全属性渲染，禁止 `innerHTML`。
- 网络只通过 `Zotero.HTTP.request()`/`Zotero.HTTP.download()`，统一设置 `responseType`、超时和成功状态；大文件不得先读成字符串。
- 只允许 `https` 与显式 localhost 开发例外；限制重定向和下载最大长度。
- 下载写入插件创建的临时目录，路径由本地生成；忽略服务端提供的目录部分。
- 日志默认仅记录 ID、状态码、耗时和字节数；开启诊断也对 DOI、标题和路径做可选脱敏。
- 不执行服务器返回的 JavaScript、CSS、XHTML、文件 URL 或 Zotero 搜索条件。

## 16. 异常场景清单

| 场景 | 必须行为 |
| --- | --- |
| 无网络/DNS/TLS 失败 | 保留选择与恢复记录，给出可重试错误，不降级明文 HTTP |
| token 过期或撤销 | 暂停整个队列，已完成项不回滚，要求重新连接 |
| 服务器升级导致协议不兼容 | 能力检查失败并显示支持的协议范围 |
| 比较后本地条目被编辑 | 执行前校验 `dateModified`/哈希，变为 stale 并重新比较 |
| 比较后服务器论文被编辑 | 版本前置条件失败，仅刷新该行 |
| 本地条目被删除或移入回收站 | 不创建空上传；显示 `local_missing/local_trashed` |
| 服务器论文在回收站 | 不当作新服务器论文；提供恢复或跳过 |
| 本地附件路径丢失 | 允许只同步元数据或重新定位；不上传零字节文件 |
| linked file 不在本机 | 标记不可用，不读取映射路径之外内容 |
| 多个 PDF | 要求主附件选择，保留未同步数量提示 |
| 超大文件 | 比较能力限制；禁用上传但不妨碍元数据 |
| 文件在哈希/上传期间变化 | 校验 size/mtime/最终哈希，丢弃该次传输并重新比较 |
| 磁盘空间不足 | 下载前检查合理余量；失败后清理临时文件 |
| 服务端配额不足 | 文件动作失败；不得误报论文完整同步 |
| DOI 冲突 | 返回已有论文候选，禁止新建重复或覆盖 |
| 标题相同但实际不同 | 只作为疑似项，保持两篇为默认 |
| 同一论文无 DOI 且 PDF 不同版 | 显示可能匹配与附件差异，人工选择 |
| Unicode/超长/非法日期 | 映射前规范化并逐字段报告，不让一字段拖垮整批 |
| 群组文库无写权限 | 允许比较/上传，禁用拉取和覆盖本地 |
| Zotero 官方同步同时运行 | 只通过 Zotero API/事务写入，让官方同步观察正常变更 |
| 插件禁用/窗口关闭 | 中止 UI 请求并释放引用；已提交操作可在下次恢复 |
| 服务器返回成功但客户端断线 | 用幂等键查询结果，禁止重复建论文 |
| 客户端创建成功但 ack 失败 | recovery journal 保存 item key，重试只补 ack |
| 分片重复或乱序 | 服务端按编号和 hash 幂等接收，complete 前验证全集 |
| Range 不支持/ETag 改变 | 从头下载新版本，不拼接两个版本 |
| 本地时钟错误 | 不依靠客户端当前时间决胜，以版本与基线哈希为准 |
| 两个 Zotero 设备同时同步 | 每个 profile 独立 client；依靠论文版本和三方基线发现冲突 |

## 17. 实施阶段

### Phase 0：协议冻结

- 确定第一版 item type、字段、标签、主附件范围。
- 将本文件中的 JSON 契约落入 `doc/openapi.yaml` 或拆分的 `sync-openapi.yaml`。
- 建立规范化与元数据哈希跨 Go/TypeScript 测试向量。

### Phase 1：服务端基础

- 新增迁移、Bearer token 管理、capabilities 和审计。
- 实现清单会话、映射精确匹配、DOI/SHA 候选与分页 diff。
- 先支持元数据 push/pull 和 ack，不含文件。

### Phase 2：插件比较界面

- 建立 Zotero 7 bootstrapped 工程、设置页、凭据存储和连接测试。
- 实现三种扫描范围、差异表、筛选/选择、字段详情。
- 此阶段“开始同步”只输出计划到调试日志，不写任一端，用真实库验收匹配准确性。

### Phase 3：可恢复执行

- 实现幂等 operation、元数据写入、三方基线、恢复日志。
- 增加 Blob 去重、分片上传、Range 下载、哈希校验和 Zotero 附件导入。
- 完成取消、失败重试、部分成功结果页。

### Phase 4：删除与高级资源

- 在单独开关和二次确认下加入回收站传播。
- 评估多附件、集合树、笔记、批注；每种资源独立版本化，不扩充成不透明“大 JSON”。

## 18. 测试与验收

### 18.1 服务端自动化

- 鉴权：有效、过期、撤销、scope 缺失、Cookie 不能冒充 Bearer。
- 匹配：映射、DOI、SHA、标题候选、所有歧义和重复组合。
- 三方合并：每字段的 5 种 base/local/server 组合。
- 幂等：相同键相同正文、相同键不同正文、响应丢失后重放。
- 并发：两个客户端用同一版本更新、比较后版本变化、并发完成同一 blob。
- 多设备：设备 A/B 使用同一 external library，各自 base 不互相覆盖；同一服务器论文的并发 pull 只能有一个 reservation，另一端收到 `linked_in_library`。
- 快照：清单分页期间插入、修改、软删论文，所有 diff 页仍来自同一物化 snapshot；游标过期返回 `410 cursor_expired` 并要求全量比较。
- 文件：断点、重复分片、乱序、错误 range、分片/整文件 hash 错、超限、伪 PDF、磁盘错误、配额竞争。
- 事务/恢复：文件移动前后故障注入、ack 丢失、过期会话和临时文件清理。
- 崩溃点：父条目事务提交后、附件导入后、ack 前分别重启 Zotero，journal 恢复不能重复创建父条目。
- 安全：路径穿越文件名、控制字符、超大 JSON、SQL 注入载荷、日志 token 脱敏。

### 18.2 插件自动化与手工矩阵

- 纯函数单测：规范化、hash canonicalization、diff 展示模型、计划器、重试分类。
- HTTP 合约测试：使用本地 mock server 覆盖所有错误码、分页和中断。
- Zotero 集成测试：独立 profile + 独立 data directory，测试普通条目、群组文库、附件缺失、多附件和恢复。
- 平台：Windows 11、macOS、Linux；至少各测一个 Zotero 7 最新补丁版本。
- 兼容：发布前单独验证 Zotero 8；通过才扩展 manifest 最大版本。
- 可访问性：键盘完成筛选、选中、字段比较和确认；屏幕阅读器能读出状态与进度。
- 大库：10,000 条本地论文、500 条差异，比较期间主窗口可交互、内存稳定、取消及时。

### 18.3 必须通过的产品验收

1. 三篇本地论文、一篇双方已有、两篇服务器独有时，数量和每行状态准确。
2. 用户只勾选一篇本地论文，服务器只新增这一篇；其余本地项不变化。
3. 用户只勾选一篇服务器论文，Zotero 只创建这一篇及其已校验附件。
4. 首次同步后再次比较显示 `both_same`，不重复上传文件。
5. 双方修改同一标题后显示冲突且默认不覆盖。
6. 传输中断并重启 Zotero 后可以恢复，不产生重复论文或孤立文件。
7. token 撤销后插件无法读取清单或文件，网页 Cookie 登录仍正常。
8. 任何单条失败都有行级错误；成功条目和失败条目不会被一个笼统 toast 混淆。

## 19. 运维与发布

- 开发使用独立 Zotero profile 和独立 data directory。关闭 Zotero 后，在 profile 的 `extensions/` 下创建名为插件 ID 的文本代理文件，内容是插件源码根目录绝对路径；首次注册后按官方说明清理 `prefs.js` 中的 `extensions.lastAppBuildId` 和 `extensions.lastAppVersion`。
- 调试启动可使用 `-purgecaches -ZoteroDebugText -jsconsole`，需要浏览器开发工具时再加 `-jsdebugger`。每次改动都先重启并检查错误控制台，不能把生产文库用于插件开发。
- 数据库迁移前备份 MySQL；新增表和索引先在生产数据副本评估时间。
- 为同步 API 增加请求数、错误码、传输字节、队列耗时、临时文件和 hash 失败指标，但不以论文标题作为标签。
- 设置页提供 token/client 列表、最后使用时间、创建时间、scope 和撤销按钮。
- XPI 本质是 ZIP，`manifest.json` 和 `bootstrap.js` 必须位于归档根目录；开发包可从 Zotero 的 Tools -> Plugins 界面安装验证。
- CI 生成 XPI 后计算 SHA-256，并生成 `updates.json` 的 `update_hash`；下载必须走 HTTPS。Zotero 官方资料支持直接安装自建 XPI，未要求经 Firefox AMO 签名；若组织有额外代码签名政策再叠加执行。
- 发布包不包含 source map 中的本机路径、测试 token、服务器 URL 或用户数据。
- 版本策略：插件 SemVer；协议使用独立 `protocolVersion`。破坏性协议变化新增 `/api/sync/v2`，不得原地改变 v1 语义。
- 服务端至少在插件升级窗口内保留前一个协议版本，并通过 capabilities 给出弃用日期。

## 20. 对当前仓库的具体改动清单

服务端：

- `backend/internal/db/migrations/010_zotero_sync.sql`：同步表、索引及论文补充字段。
- `backend/internal/httpapi/server.go`：只注册路由；同步实现拆到 `sync_*.go`，避免继续扩大单文件。
- `backend/internal/httpapi/sync_auth.go`：Bearer token、scope、last-used 节流更新。
- `backend/internal/httpapi/sync_sessions.go`：manifest、diff、cursor。
- `backend/internal/httpapi/sync_operations.go`：计划、幂等、commit、ack、恢复。
- `backend/internal/httpapi/sync_blobs.go`：流式 hash、分片、Range 和清理。
- `backend/internal/syncmatch/`：规范化、匹配和三方合并纯逻辑。
- `backend/internal/config/config.go`：同步会话 TTL、分片大小、token 限流、临时目录配置及校验。
- 建议新增环境变量：`SYNC_SESSION_TTL_SECONDS`（默认 1800）、`SYNC_OPERATION_RETENTION_DAYS`（默认 30）、`SYNC_TOMBSTONE_RETENTION_DAYS`（默认 365）、`SYNC_CHUNK_SIZE_BYTES`（默认 8388608）、`SYNC_MAX_MANIFEST_ITEMS`、`SYNC_MAX_CONCURRENT_BLOBS` 和 `SYNC_RATE_LIMIT_*`；生产校验要求 tombstone 保留期不小于 operation 保留期。
- `deploy/nginx.conf.template`：确认 `/api/sync/v1` 转发 `Authorization`、`Range`、`If-Range`、`Content-Range`、`Idempotency-Key`，分片 body 上限与服务端一致，并保留 `429 Retry-After`；不要为了大文件全局取消请求大小和超时保护。
- `doc/openapi.yaml`：追加或引用完整 v1 契约。
- `frontend/src/`：安全设置中增加同步客户端/token 管理；这不是插件同步差异主界面。

如果还希望服务器网页的“导入/上传”页显示同样的本地与服务器差异，应让网页导入组件复用 `sessions -> manifest -> diff -> operations` 契约，并由网页先上传一个轻量 manifest；不要把浏览器文件选择结果直接塞进现有 `POST /api/papers`。网页和 Zotero 插件可以共享状态枚举、匹配说明和错误码，但两者的凭据、文件读取和本地条目写入逻辑必须分开。

插件：

- 新增 `zotero-plugin/` 工程，按第 13 节拆分。
- CI 至少执行类型检查、单测、XPI 打包、包内容白名单检查、SHA-256 和更新清单校验。

在开始实现前仍需由产品所有者确认三项策略：

1. 第一版服务器拉取是否只进入“我的文库”，还是允许选择可写群组文库；
2. 标签采用并集、仅手工标签还是完全不双向同步；
3. 服务器论文只有元数据而没有附件时，是否允许拉取为无附件 Zotero 条目。

若未特别选择，本文建议默认分别为：允许选择任意可写目标文库、仅手工标签且合并取并集、允许创建无附件条目并清楚标记。
