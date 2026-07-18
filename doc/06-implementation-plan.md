# 分步实施、测试与验收

## 1. 迭代顺序

### Sprint 0：工程基线

建立 monorepo、Go/React CI、Docker Compose、`.env.example`、迁移工具、统一响应和 request_id。完成 lint、format、依赖锁定和 pre-commit。

### Sprint 1：数据库与初始化认证

实现 `admins/system_settings/sessions/verification_codes/audit_logs`；完成 SMTP TLS、验证码哈希与 Redis 限流；完成 `/setup` 向导、登录、注销、刷新、改密；Cookie + CSRF；编写竞态和重放测试。

### Sprint 2：论文导入与文件安全

实现安全对象存储、上传分片/断点续传、magic/MIME 检查、扫描队列、解析 worker、导入确认和去重。先支持 PDF、DOCX、BibTeX/RIS/CSL-JSON，再扩展 ODT/LaTeX/TXT。

### Sprint 3：论文管理与阅读器

实现论文 CRUD、标签、分类树、收藏、阅读状态、笔记、PDF.js 预览、鉴权下载、软删除和审计。

### Sprint 4：本地/外部搜索

实现全文索引、搜索 DSL AST、游标分页、相关性排序、OpenAlex/Crossref/Semantic Scholar 适配器、缓存、SSRF 和版权策略。

### Sprint 5：界面打磨与部署

完成 Dashboard、Papers 工作区、Import Stepper、Reader、Taxonomy、Settings/Audit、深色/高对比模式；完成 Nginx/Caddy TLS、Compose 生产配置、备份和监控。

## 2. 测试分层

- Go 单元测试：密码、验证码哈希/过期/重放、限流、DSL 解析、路径安全、MIME/magic、SSRF/IP 判断。
- Go 集成测试：MySQL 迁移、事务初始化竞态、全文查询、session 撤销、上传下载鉴权。
- 前端测试：表单校验、错误状态、键盘焦点、搜索 URL 状态、上传进度和响应式布局。
- E2E：首次部署 -> 创建管理员 -> 登录二次验证 -> 导入 PDF -> 确认元数据 -> 搜索 -> 在线阅读 -> 下载 -> 修改标签 -> 删除/恢复。
- 安全测试：OWASP ZAP/Burp（认证、IDOR、CSRF、XSS、SQLi、SSRF）、压缩炸弹/超大请求 fuzz、Trivy、gosec、govulncheck、npm audit。
- 运维测试：容器重启不丢上传/数据库，Redis 重启后的 fail-closed 行为，备份恢复，密钥轮换和回滚。

## 3. 验收标准

### 功能

- 未初始化只能看到 `/setup`；成功创建后初始化接口返回 410/409，不能重复创建管理员。
- 邮箱验证码在 60 秒冷却、小时上限、错误次数和 TTL 规则下工作；成功后一次性消费。
- PDF、DOCX、BibTeX、RIS 至少可导入；解析失败可重试且不泄露文件路径。
- 可按标题、作者、DOI、年份、期刊、标签、分类、阅读状态和收藏组合搜索；搜索支持模糊词和短语。
- 可在线预览、下载、修改元数据/标签/分类、软删除；下载必须鉴权并产生审计记录。
- Docker Compose 新机器可按文档启动；容器重启后论文和数据库数据保留。

### 安全

- HTTPS/HSTS/CSP/CSRF/CORS/`nosniff`/`frame-ancestors` 已验证。
- 会话不出现在 localStorage/sessionStorage；修改密码会吊销旧会话。
- 上传无法执行、无法路径穿越、无法通过 IDOR 获取其他文件；解析 worker 非 root、无网络、有资源上限。
- MySQL/Redis 不暴露公网；API 非 root、只读根 FS、capabilities 已清理。
- 日志不含密码、验证码、token、SMTP 凭据和论文正文；备份可恢复且加密。

## 4. 交付物清单

- `frontend/` React 应用及锁定依赖。
- `backend/` Go API、worker、迁移和单元/集成测试。
- `deploy/docker-compose.yml`、反向代理配置、健康检查。
- `.env.example` 与 Secret 注入说明。
- OpenAPI 3.1 文件（由本接口文档实现后生成并作为 CI 契约测试输入）。
- 数据字典、备份恢复 Runbook、安全验收报告和已知限制清单。

## 5. 开发约束

- 每个端点先写 OpenAPI/契约测试，再实现 handler。
- 所有数据库变更使用迁移脚本，生产禁止无审计 AutoMigrate。
- 论文正文、用户输入和日志按“默认不信任”处理；任何解析器升级都要重新跑恶意样本回归。
- 任何新增外部来源都必须补充 SSRF、版权、限流、缓存和数据删除策略。
