# Personal Paper Knowledge Base

React + Go + MySQL + Redis 的单管理员论文知识库。完整设计规范位于 [`doc/README.md`](doc/README.md)。

## 当前实现

- Go API：健康检查、一次性初始化、SMTP 验证码、Cookie 会话、CSRF、论文列表/全文查询、上传、详情、乐观锁更新、预览/下载、软删除、Dashboard。
- React：`/setup`、登录二次验证、Dashboard、论文库、导入、分类标签基础工作区；使用 HttpOnly Cookie 和自动 CSRF 头。
- Docker：`deploy/docker-compose.yml` 包含 API、Web、MySQL、Redis；数据库和 Redis 仅暴露在内部网络，Web 监听宿主机 80。

## 首次部署

1. 复制环境模板：

   ```powershell
   Copy-Item .env.example .env
   ```

2. 为 `MYSQL_PASSWORD`、`MYSQL_ROOT_PASSWORD`、`REDIS_PASSWORD`、`JWT_SECRET`、`SETUP_SECRET` 和 SMTP 配置填写真实值。可用 PowerShell 生成随机密钥：

   ```powershell
   $bytes = New-Object byte[] 48; [Security.Cryptography.RandomNumberGenerator]::Fill($bytes); [Convert]::ToBase64String($bytes)
   ```

3. 在安装 Docker Engine/Compose 的机器一键部署：

   ```bash
   bash deploy.sh
   ```

   Windows Server/PowerShell：

   ```powershell
   .\deploy.ps1
   ```

4. 脚本会校验 `.env`、构建镜像、启动 MySQL/Redis/API/Web、执行版本化数据库迁移并等待健康检查。完成后打开 `/setup`，输入 `.env` 中的 `SETUP_SECRET`、管理员邮箱、SMTP 验证码和强密码。

5. 生产环境必须使用 HTTPS 反向代理；当前 Compose Web 层提供安全响应头和 HTTP 入口，TLS 可由宿主机 Nginx/Caddy 或云负载均衡终止。

## Docker Hub 拉取超时

项目的所有基础镜像都可以通过 `.env` 覆盖。服务器无法访问 Docker Hub 时，使用云厂商提供的可信镜像加速地址：

```env
GO_IMAGE=镜像加速域名/library/golang:1.22-alpine
NODE_IMAGE=镜像加速域名/library/node:22-alpine
NGINX_IMAGE=镜像加速域名/library/nginx:1.27-alpine
MYSQL_IMAGE=镜像加速域名/library/mysql:8.4
REDIS_IMAGE=镜像加速域名/library/redis:7.4-alpine
```

修改后重新运行 `bash deploy.sh`。生产环境应优先使用服务器云厂商提供的专属 Docker Hub 加速地址，并在部署后记录镜像 digest。

## 本地开发

后端：

```powershell
cd backend
$env:GOCACHE="$PWD\..\.gocache"
$env:GOMODCACHE="$PWD\..\.gomodcache"
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
- `frontend`: `npm ci`、`npm run build` 已通过。
- 当前执行环境未安装 Docker CLI，因此尚未在本机执行 `docker compose up`；部署机器需要 Docker Engine/Compose。

安全注意：不要提交 `.env`、上传目录、Docker 凭据或 SMTP 授权码。上线前按 [`doc/05-security.md`](doc/05-security.md) 完成恶意上传、CSRF、IDOR、限流、备份恢复和镜像扫描验收。
