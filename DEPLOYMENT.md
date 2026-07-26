# 部署文档（Docker 一键部署）

个人论文知识库：React + Go + MySQL 单管理员应用。

- 认证方式：邮箱 + 密码（**无邮箱验证码，无需 SMTP**）
- 依赖：**只需要服务器上已有的 MySQL**（不使用 Redis）
- 部署方式：Docker Compose 一键部署，API 通过 `.env` 中的配置直连服务器 MySQL

## 架构

```
浏览器 ──> web 容器 (Nginx, 宿主机 HTTP_PORT -> 8080)
              ├── 静态前端 (React)
              └── /api/ 反向代理 ──> api 容器 (Go, 8080)
                                        └── 直连服务器 MySQL (host.docker.internal:3306)
上传的论文文件保存在 Docker 卷 paper_uploads 中
```

## 一、前置条件

1. 服务器已安装 **Docker Engine 24+** 和 **Docker Compose v2**（`docker compose version` 能正常输出）。
2. 服务器上已运行 **MySQL 8.0+**（宝塔面板、系统包安装或另一台数据库服务器均可）。
3. 开放防火墙的 `HTTP_PORT`（默认 80）端口；**不要**把 3306 开放到公网。

## 二、准备 MySQL

MySQL 需要能被 Docker 网桥访问。若 MySQL 与本项目在同一台服务器：

1. 将 MySQL 的 `bind-address` 设置为 `0.0.0.0` 或服务器内网地址（宝塔：数据库 → 配置修改；或编辑 `/etc/my.cnf`），重启 MySQL。
2. 创建数据库和仅允许 Docker 子网访问的账号：

```sql
CREATE DATABASE IF NOT EXISTS paper_kb
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'paper_kb_app'@'172.30.0.%'
  IDENTIFIED BY '一个强密码（与 .env 中 MYSQL_PASSWORD 一致）';
GRANT ALL PRIVILEGES ON paper_kb.* TO 'paper_kb_app'@'172.30.0.%';
FLUSH PRIVILEGES;
```

> `172.30.0.%` 对应 `.env` 中的 `DOCKER_SUBNET=172.30.0.0/24`。若该网段与现有网络冲突，请同时修改 `DOCKER_SUBNET` 和 MySQL 账号的来源网段。
>
> 若 MySQL 在**另一台服务器**上，把来源网段换成本机的出口 IP，并在 `.env` 中把 `MYSQL_HOST` 填成那台服务器的 IP。

## 三、配置 .env

```bash
cp .env.example .env
vim .env
```

必须修改的 4 项：

| 变量 | 说明 |
| --- | --- |
| `MYSQL_PASSWORD` | 上一步为 `paper_kb_app` 设置的密码 |
| `JWT_SECRET` | 至少 32 字符的随机串，用于会话/CSRF 签名 |
| `SETUP_SECRET` | 至少 32 字符的随机串，首次初始化管理员时使用；**必须与 JWT_SECRET 不同** |
| `PUBLIC_BASE_URL` | 实际访问地址，如 `http://1.2.3.4` 或 `https://papers.example.com` |

生成随机密钥：

```bash
openssl rand -base64 48
```

按需调整的项：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `HTTP_PORT` | `80` | 对外服务端口 |
| `MYSQL_HOST` | `host.docker.internal` | MySQL 在本机时保持默认；在其他服务器时填其 IP |
| `COOKIE_SECURE` | `false` | 通过 HTTPS 域名访问时改为 `true`；用 `http://IP` 访问时必须保持 `false`，否则浏览器不回传 Cookie、无法登录 |
| `GO_IMAGE` 等 | Docker Hub 官方镜像 | 国内服务器拉取超时时换成镜像加速地址，如 `镜像加速域名/library/golang:1.22-alpine` |
| `AUTO_MIGRATE` | `true` | 启动时自动执行版本化数据库迁移（已应用的版本会跳过） |

## 四、一键部署

Linux：

```bash
bash deploy.sh
```

Windows Server（PowerShell）：

```powershell
.\deploy.ps1
```

脚本会依次：校验 `.env` 是否仍含示例值 → 校验 compose 配置 → 构建镜像并启动 api/web 容器 → 轮询 `/api/health` 直到就绪。失败时自动打印容器日志。

## 五、初始化管理员（只执行一次）

1. 打开 `http://服务器IP:HTTP_PORT/setup`。
2. 填写：
   - **初始化令牌**：`.env` 中的 `SETUP_SECRET`
   - 管理员邮箱、显示名称（邮箱仅作为登录账号，不发送任何邮件）
   - 密码（至少 12 位）
3. 提交后跳转登录页，用邮箱 + 密码登录即可。

初始化完成后 `/setup` 会自动失效（重复初始化返回 409）。

## 六、日常运维

```bash
# 查看状态 / 日志
docker compose --env-file .env -f deploy/docker-compose.yml ps
docker compose --env-file .env -f deploy/docker-compose.yml logs -f api

# 更新代码后重新部署（数据不受影响）
git pull && bash deploy.sh

# 停止（保留数据卷）
docker compose --env-file .env -f deploy/docker-compose.yml down

# 备份
mysqldump -u paper_kb_app -p paper_kb > paper_kb_$(date +%F).sql     # 数据库
docker run --rm -v paper-knowledge-base_paper_uploads:/data -v $(pwd):/backup alpine \
  tar czf /backup/uploads_$(date +%F).tar.gz -C /data .              # 上传的论文文件
```

## 七、常见问题

**api 容器反复重启，日志显示 `connect mysql: ...`**
- MySQL 的 `bind-address` 仍是 `127.0.0.1` → 改为 `0.0.0.0` 后重启 MySQL；
- 账号来源网段不对 → 确认用户是 `'paper_kb_app'@'172.30.0.%'` 且密码与 `.env` 一致；
- 服务器防火墙拦截了 Docker 子网 → 放行 `172.30.0.0/24` 访问 3306。

**能打开页面但登录后立刻退回登录页**
- 用 `http://IP` 访问时 `.env` 中 `COOKIE_SECURE` 必须为 `false`（改完后 `bash deploy.sh` 重新部署）。

**日志显示 `JWT_SECRET must contain at least 32 bytes`**
- `.env` 中密钥太短或未填写，按第三节生成后重新部署。

**镜像拉取超时**
- 在 `.env` 中把 `GO_IMAGE`/`NODE_IMAGE`/`NGINX_IMAGE` 换成云厂商镜像加速地址后重跑脚本。

**构建时 `npm ci` 或 `go mod download` 失败（`Exit handler never called` / 超时）**
- 服务器访问国外源慢导致依赖拉取中断。在 `.env` 中加入（或确认存在）：
  ```env
  NPM_REGISTRY=https://registry.npmmirror.com
  GOPROXY_URL=https://goproxy.cn,direct
  ```
  然后重跑 `bash deploy.sh`。
- 若仍失败,检查内存：`free -h`。1GB 及以下的服务器请先加 swap：
  ```bash
  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile
  ```

**80 端口被占用**
- 修改 `.env` 中 `HTTP_PORT`（如 8081）后重跑脚本，访问 `http://IP:8081`。

## 八、安全建议

- 生产环境建议用宿主机 Nginx/Caddy 或云负载均衡做 **HTTPS 终止**，然后把 `COOKIE_SECURE` 改为 `true`。
- `SETUP_SECRET` 在初始化完成后即失去作用，但仍不要泄露 `.env`（已在 `.gitignore` 中，勿提交）。
- 登录有速率限制（默认 10 分钟窗口内多次失败会锁定 10 分钟），限流状态保存在 API 进程内存中，容器重启后重置——单管理员场景下足够。
- 定期执行第六节的数据库与上传文件备份。
