# REST API 接口规范

## 1. 通用约定

- Base URL：`/api`，JSON 编码 UTF-8，时间使用 RFC3339。
- 认证：HttpOnly Cookie `pkb_session`；需要变更状态的请求同时提交 `X-CSRF-Token`。若采用 Authorization Header 的服务间调用，仍校验 `Origin`/`Sec-Fetch-Site`。
- 成功响应统一使用 `{ "data": ..., "requestId": "..." }`；分页使用 `{ "items": [], "page": 1, "pageSize": 20, "total": 0, "nextCursor": null }`。
- 错误响应：`{ "error": { "code": "machine_code", "message": "面向用户的通用消息", "fields": {}, "retryAfter": 60 }, "requestId": "..." }`。
- 不在响应中返回密码、验证码、token、SMTP/SQL 错误、磁盘路径或论文原文（预览接口除外）。
- 所有列表接口 `pageSize <= 100`，字符串有长度上限，排序字段使用枚举。

## 2. 初始化与认证

### `GET /api/setup/status`

公开、禁用缓存。返回 `{ "initialized": boolean }`，不返回邮箱或管理员信息。

### `POST /api/auth/send-code`

发送验证码。请求：

```json
{ "email": "admin@example.com", "purpose": "setup|login|reset|change_email" }
```

响应始终使用模糊成功消息，防止邮箱枚举：`202` + `{ "accepted": true, "expiresIn": 600, "maskedEmail": "a***@example.com" }`。邮箱维度 60 秒冷却、每小时最多 6 次；IP 每小时最多 30 次。SMTP 失败不消耗配额。

### `POST /api/setup/admin`

仅初始化状态为 false 时可用。请求：

```json
{
  "email": "admin@example.com",
  "displayName": "管理员",
  "password": "至少12字符的强密码",
  "code": "123456",
  "setupNonce": "部署时注入的 SETUP_SECRET（只在首次初始化使用）"
}
```

服务端验证邮箱验证码、密码策略和一次性 nonce，在同一事务中插入管理员并置 `system_settings.initialized=1`。并发请求一个成功，其他返回 `409 setup_already_completed`。

### `POST /api/auth/login`

请求：`{ "email": "...", "password": "...", "code": "可选6位验证码" }`。首次登录或风险 IP 未验证时返回 `202 verification_required`，不签发会话；提交验证码后返回 `200`。失败统一 `401 invalid_credentials`。IP、邮箱、IP+邮箱三维限流，5 次/10 分钟起指数退避；账号锁定返回 `423` 但不泄露额外信息。

### `POST /api/auth/logout`

需要会话和 CSRF。服务端撤销当前 session/refresh token，返回 `204`。

### `POST /api/auth/refresh`

需要 refresh Cookie。refresh token 轮换、重放检测，返回新的短期 access session；重放时撤销该设备全部会话并记录高危审计事件。

### `POST /api/auth/password/reset`

请求 `{ "email": "...", "code": "...", "newPassword": "..." }`。响应不泄露邮箱是否存在；成功吊销全部会话、递增 `token_version`。

### `GET /api/auth/me`

返回管理员公开资料、会话过期时间和安全提示，不返回密码哈希、SMTP 配置或原始 token。

## 3. 论文接口

### `GET /api/papers`

参数：

`q`、`query`（搜索 DSL）、`categoryId`、`tagId`、`status`、`yearFrom`、`yearTo`、`author`、`journal`、`doi`、`isFavorite`、`sort=relevance|published_at|added_at|title`、`page`、`pageSize`、`cursor`。

返回论文列表项：`id,title,authors,year,journal,doi,abstractSnippet,thumbnailUrl,tags,categories,readingStatus,isFavorite,parseStatus,addedAt,updatedAt`。

### `POST /api/papers/upload`

`multipart/form-data`，字段 `files[]`、可选 `importMode=metadata_only|fulltext`。服务端限制扩展名、MIME、magic bytes、大小、总配额和文件数量，返回 `202`：

```json
{ "jobId": "opaque-job-id", "items": [{ "uploadId": "...", "filename": "paper.pdf", "status": "queued" }] }
```

### `GET /api/papers/import-jobs/{jobId}`

返回解析进度、阶段、错误码和元数据预览。阶段：`queued -> scanning -> parsing -> awaiting_confirmation -> completed|failed|quarantined`。

### `POST /api/papers/import-jobs/{jobId}/confirm`

提交解析后的元数据和分类标签：

```json
{
  "items": [{
    "uploadId": "...",
    "title": "...",
    "authors": [{"name":"...","orcid":"..."}],
    "abstract": "...",
    "doi": "10.xxxx/xxxxx",
    "publishedAt": "2024-01-01",
    "tagIds": [1,2],
    "categoryIds": [10],
    "readingStatus": "unread|reading|read"
  }]
}
```

服务端按 DOI 优先、标题+第一作者+年份次之去重；冲突返回 `409 duplicate_candidate` 并提供合并选项。

### `GET /api/papers/{id}`

返回完整元数据、标签分类、文件摘要、笔记和版本号 `version`。

### `PATCH /api/papers/{id}`

允许字段白名单：标题、作者、摘要、DOI、期刊、年份、语言、阅读状态、收藏、备注。必须带 `version` 做乐观锁；版本冲突返回 `409 version_conflict`。

### `GET /api/papers/{id}/preview`

需要管理员会话。返回短期签名地址或流式响应，设置 `Content-Disposition: inline`、`X-Content-Type-Options: nosniff`。PDF 使用 PDF.js；不允许原文 HTML 执行脚本。

### `GET /api/papers/{id}/download`

需要管理员会话和资源归属校验，返回短期（默认 10 分钟）签名 URL或下载流，记录审计和限流。

### `DELETE /api/papers/{id}`

需要 CSRF 和二次确认；先软删除，异步清理物理文件和搜索索引，保留可审计的 tombstone。

## 4. 标签、分类和笔记

- `GET/POST/PATCH/DELETE /api/tags`
- `POST /api/tags/merge`：将来源标签合并到目标标签并更新关联。
- `GET/POST/PATCH/DELETE /api/categories`
- `GET /api/categories/tree?scheme=oecd|acm|jel|mesh|custom`
- `POST/PATCH/DELETE /api/papers/{id}/notes`

分类节点必须带 `scheme,externalId,version,parentId,name`; 自定义分类使用 `scheme=custom`。删除节点默认禁止级联删除论文，只移除关联或转移到新节点。

## 5. 搜索与外部来源

### `GET /api/search/local`

支持语法：`title:"graph neural" author:wang year:2020-2025 tag:survey category:cs doi:10... is:favorite`，支持 AND/OR/NOT 和短语。后端解析为 AST，再映射到参数化 SQL；不得直接把 DSL 拼到 SQL。

### `GET /api/search/external`

参数 `source=openalex|crossref|semanticscholar`、`q`、`doi`、`author`、`yearFrom`、`yearTo`、`cursor`。Go 后端代理请求、缓存热门结果、统一超时和限流；浏览器不接触第三方 API key。响应显示来源、DOI、开放获取状态和 `importable`。

### `POST /api/search/external/import`

请求第三方结果的 `source`、`externalId`、确认后的元数据和 `downloadPolicy=metadata_only|open_access_only`。仅允许 HTTPS 和来源白名单，执行 SSRF 防护；不自动下载受版权限制的正文。

## 6. 设置、审计与健康

- `GET/PATCH /api/settings/profile`
- `GET/PATCH /api/settings/security`：会话、验证码策略和设备列表；敏感操作仍需当前密码/邮箱验证码。
- `GET /api/audit-logs?event=&from=&to=&page=`：仅管理员，字段脱敏。
- `GET /api/health`、`GET /api/health/ready`、`GET /api/health/full`（详见架构文档）。

## 7. 状态码与错误码

`400 invalid_request`、`401 unauthenticated/invalid_credentials`、`403 forbidden/csrf_failed`、`404 not_found`、`409 version_conflict/setup_already_completed/duplicate_candidate`、`413 file_too_large`、`415 unsupported_media_type`、`423 account_locked`、`429 rate_limited`、`500 internal_error`、`503 dependency_unavailable`。
