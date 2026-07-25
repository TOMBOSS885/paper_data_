# 总体架构与部署规范

## 1. 范围与非目标

### 1.1 本期范围

- 只有一个管理员账号，不做多租户和普通用户注册。
- 首次部署通过向导创建管理员邮箱、密码和显示名。
- SMTP 邮箱验证码用于初始化、登录二次校验、找回/修改密码。
- 导入 PDF、DOCX、DOC、ODT、LaTeX、TXT、HTML（可配置白名单）；提取标题、作者、摘要、DOI、年份、关键词等。
- 论文标签、分类、收藏、阅读状态、笔记、原文在线预览和下载。
- 本地模糊搜索、高级字段搜索、排序和筛选；可选 OpenAlex/Crossref/Semantic Scholar 外部搜索。
- Docker Compose 一键启动，支持 MySQL/Redis/应用/反向代理分层。

### 1.2 暂不包含

- 多用户协作、细粒度角色、公开分享链接。
- 自动购买付费论文、绕过版权墙或抓取受限站点。
- 以 Elasticsearch 为强依赖。数据量超过约 10 万篇时再增加独立搜索服务。

## 2. 逻辑架构

```text
Browser (React SPA)
        |
 HTTPS  |  Nginx/Caddy: TLS, CSP, body limit, rate limit
        v
 Go API (Gin)
   |       |         |             |
 MySQL  Redis     File Store    Search Adapters
 metadata codes    private       OpenAlex/Crossref/S2
 fulltext limits   volume
        |
 Parser workers (非 root、无网络、CPU/内存/超时限制)
```

后端按 `handler -> service -> repository -> storage/parser` 分层。handler 只处理 HTTP，service 处理事务和业务规则，repository 负责 MySQL/Redis，storage 负责安全文件名和对象 key，parser 负责沙箱中的文本/元数据提取。

## 3. 推荐目录

```text
paper-knowledge-base/
  frontend/
    src/{app,components,features,lib,pages,styles,types}
  backend/
    cmd/server/main.go
    internal/{config,db,model,repository,service,handler,middleware,
              storage,parser,search,mailer,job,response}
    migrations/
  deploy/{docker-compose.yml,nginx.conf}
  doc/
  uploads/                 # 仅挂载卷，不提交 Git
  .env.example
```

## 4. 服务与环境变量

生产配置只通过 Docker Secret、主机 Secret 或 `.env` 注入，`.env` 永不提交 Git、永不复制到镜像、永不写入日志。

| 变量 | 必填 | 说明与安全约束 |
|---|---:|---|
| `APP_ENV` | 是 | `development`/`production`，生产拒绝弱默认值 |
| `SERVER_HOST`/`SERVER_PORT` | 是 | 容器内监听；对外只通过反代 |
| `PUBLIC_BASE_URL` | 是 | HTTPS 公网地址，用于邮件链接和签名 URL |
| `MYSQL_HOST/PORT/DATABASE/USERNAME/PASSWORD` | 是 | 使用独立应用账号，禁止 root 远程登录 |
| `MYSQL_TLS_MODE` | 生产是 | `verify_identity` 或等效 TLS 校验 |
| `REDIS_ADDR/REDIS_PASSWORD/REDIS_TLS` | 推荐 | 验证码、限流、refresh 撤销共享状态 |
| `JWT_SECRET` | 是 | 至少 32 字节高熵随机值；更推荐仅用于签名并周期轮换 |
| `SETUP_SECRET` | 是 | 首次初始化令牌，必须与 JWT_SECRET 分离；创建管理员后仍保留在 Secret 中 |
| `COOKIE_SECURE`/`COOKIE_SAMESITE` | 生产是 | `true` / `lax` 或 `strict` |
| `CORS_ALLOWED_ORIGINS` | 生产是 | 显式 HTTPS 源，禁止 `*` + credentials |
| `SMTP_HOST/PORT/USERNAME/PASSWORD/FROM/FROM_NAME` | 是 | 凭据使用授权码；587 STARTTLS，465 TLS |
| `SMTP_TLS_MODE` | 是 | `starttls`/`tls`；禁止明文回退 |
| `EMAIL_CODE_TTL_SECONDS` | 是 | 600 推荐，允许范围 300-900 |
| `UPLOAD_MAX_BYTES` | 是 | 默认单文件 200 MiB，按磁盘容量调整 |
| `UPLOAD_QUOTA_BYTES` | 是 | 管理员总配额，超额拒绝 |
| `PARSE_TIMEOUT_SECONDS` | 是 | 默认 120；解析 worker 超时即隔离 |
| `SEARCH_MAX_PAGE_SIZE` | 是 | 默认 100；游标分页优先 |
| `RATE_LIMIT_ENABLED` | 是 | 生产必须 `true` |
| `TRUST_PROXY_CIDRS` | 是 | 仅填写实际反代网段，防伪造客户端 IP |

## 5. Docker Compose 规范

服务划分为 `proxy`、`api`、`worker`、`mysql`、`redis`。只有 `proxy` 映射宿主机 80/443；MySQL、Redis、API 使用内部网络，不映射公网端口。数据库数据、上传卷、备份卷分开管理。

API 镜像采用多阶段构建，最终镜像使用 distroless 或 Alpine、非 root UID、`read_only: true`、`cap_drop: [ALL]`、`security_opt: [no-new-privileges:true]`，仅 `/app/uploads` 和 `/tmp` 可写。固定基础镜像 digest，CI 使用 Trivy 扫描。

健康检查分为：

- `GET /api/health`：公开，仅返回 `{ "status": "ok" }`。
- `GET /api/health/ready`：反代/编排使用，检查数据库和 Redis 可用性，不返回凭据。
- `GET /api/health/full`：仅管理员或 localhost，返回组件状态摘要。

## 6. 首次部署流程

1. 复制 `.env.example` 为 `.env`，生成随机 `JWT_SECRET`、数据库密码和 Redis 密码，填写 SMTP 授权码。
2. 执行 `docker compose up -d mysql redis`，等待健康检查通过。
3. 保持 `AUTO_MIGRATE=true`，启动时执行版本化 SQL；已记录在 `schema_migrations` 的版本会自动跳过。本项目不使用 GORM 结构反射式 AutoMigrate。
4. 启动 `api`、`worker` 和 `proxy`，访问 `/setup`。
5. 页面调用 `GET /api/setup/status`；只有 `initialized=false` 才展示向导。
6. 输入邮箱、密码、显示名，发送并提交邮箱验证码；服务端在事务中写入管理员和初始化标记。
7. 初始化成功后立即关闭 `/setup`，签发短期会话并跳转 `/app/dashboard`。
8. 运行备份、恢复演练、依赖扫描和安全验收清单。

## 7. 反向代理基线

- 强制 HTTPS、HSTS（确认域名后再加入 preload）、现代 TLS 套件。
- `client_max_body_size` 与 API 上传上限一致；请求头、连接、读取超时有上限。
- 设置 `Content-Security-Policy`、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Permissions-Policy`、`frame-ancestors 'none'`。
- `/uploads/` 不直接暴露目录索引；论文下载统一走 API 鉴权和短期签名，不返回物理路径。
- 反代只信任配置的 `X-Forwarded-*` 来源；错误页面不暴露版本和内部路径。
