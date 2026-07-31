# Personal Paper Knowledge Base

React + Go + MySQL 的单管理员论文知识库。部署步骤见 [`DEPLOYMENT.md`](DEPLOYMENT.md)，原始设计规范位于 [`doc/README.md`](doc/README.md)（其中 SMTP/Redis 部分已废弃）。

## 当前实现

- Go API：健康检查、一次性初始化（SETUP_SECRET）、邮箱 + 密码登录、修改密码（吊销全部会话）、Cookie 会话、CSRF、论文检索（关键词 + 阅读状态 + 年份 + 收藏 + 标签/分类 + 排序 + 分页）、上传、详情、乐观锁更新、预览/下载、默认 10 天且可配置的回收站与自动文件清理、Dashboard、分类聚合（`/api/facets`）、标签与分类 CRUD 与论文绑定。
- React：`/setup`、登录、概览、论文库（摘要、分类和标签概览，筛选条件写入 URL）、论文详情（编辑元数据/收藏/预览/下载/删除、标签与分类多选打标）、回收站恢复、导入（逐文件结果反馈）、分类与标签管理（创建/删除、树形展开、颜色选择）、安全设置；支持明暗主题和移动端筛选面板，使用 HttpOnly Cookie 和自动 CSRF 头，会话失效自动回登录页。
- Docker：`deploy/docker-compose.yml` 包含 API 和 Web 两个容器，均运行在宿主机网络上；API 按 `.env` 中的配置经 `127.0.0.1` 直连服务器 MySQL（无需改 bind-address 和防火墙），Web(Nginx) 托管前端并反代 `/api/`。
- 不依赖 Redis 和 SMTP：限流在 API 进程内存中实现，登录无需邮箱验证码。

## 快速部署

```bash
cp .env.example .env
# 填写 MYSQL_PASSWORD、JWT_SECRET、SETUP_SECRET、PUBLIC_BASE_URL
bash deploy.sh          # Linux
# 或 .\deploy.ps1       # Windows Server
```

在宿主机 TLS 反向代理中将 HTTPS 域名转发到 `http://127.0.0.1:HTTP_PORT`。完成后打开 `PUBLIC_BASE_URL/setup`，输入 `.env` 中的 `SETUP_SECRET` 创建管理员。详细说明（MySQL 准备、常见问题、备份）见 [`DEPLOYMENT.md`](DEPLOYMENT.md)。

## 本地开发

后端：

```powershell
cd backend
$env:JWT_SECRET='本地开发用的至少32字符随机串________'
$env:SETUP_SECRET='另一个至少32字符的随机串__________'
go test ./...
go run ./cmd/server
```

前端：

```powershell
cd frontend
npm ci
npm run dev
```

## 当前验证结果

- `backend`: `gofmt`、`go test ./...`、`go vet ./...` 已通过。
- `frontend`: `npm run build`、`npm test` 已通过。
- 本机未安装 Docker CLI，`docker compose up` 需在部署服务器上执行。

安全注意：不要提交 `.env`、上传目录或数据库凭据。生产环境强制使用 HTTPS 反向代理和安全 Cookie。
