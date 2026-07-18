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

3. 在安装 Docker Engine/Compose 的机器执行：

   ```powershell
   docker compose --env-file .env -f deploy/docker-compose.yml up -d --build
   docker compose --env-file .env -f deploy/docker-compose.yml logs -f api
   ```

4. 打开 `http://localhost/setup`，输入 `.env` 中的 `SETUP_SECRET`、管理员邮箱、SMTP 验证码和强密码。初始化成功后 `/setup` 会自动关闭。

5. 生产环境必须使用 HTTPS 反向代理；当前 Compose Web 层提供安全响应头和 HTTP 入口，TLS 可由宿主机 Nginx/Caddy 或云负载均衡终止。

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
