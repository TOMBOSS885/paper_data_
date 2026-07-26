# 部署文档（Docker 一键部署）

个人论文知识库：React + Go + MySQL 单管理员应用。

- 认证方式：邮箱 + 密码（**无邮箱验证码，无需 SMTP**）
- 依赖：**只需要服务器上已有的 MySQL**（不使用 Redis）
- 部署方式：Docker Compose 一键部署，API 通过 `.env` 中的配置直连服务器 MySQL

## 架构

两个容器都运行在**宿主机网络**（`network_mode: host`）上，因此连接本机 MySQL 走 `127.0.0.1` 回环——**不需要修改 MySQL 的 bind-address，也不需要调整防火墙**。

```
浏览器 ──> web 容器 (Nginx, 监听 0.0.0.0:HTTP_PORT)
              ├── 静态前端 (React)
              └── /api/ 反向代理 ──> api 容器 (Go, 仅监听 127.0.0.1:API_PORT)
                                        └── 直连本机 MySQL (127.0.0.1:3306)
上传的论文文件保存在 Docker 卷 paper_uploads 中
```

## 一、前置条件

1. 服务器已安装 **Docker Engine 24+** 和 **Docker Compose v2**（`docker compose version` 能正常输出）。
2. 服务器上已运行 **MySQL 8.0+**（宝塔面板、系统包安装或另一台数据库服务器均可）。
3. 宿主机的 `HTTP_PORT`（默认 80）和 `API_PORT`（默认 8080）未被其他服务占用（`ss -tlnp | grep -E ':80|:8080'` 检查；被占用就在 `.env` 里换端口）。

## 二、准备 MySQL

由于容器使用宿主机网络，MySQL 保持默认的 `127.0.0.1` 监听即可，只需创建数据库和本机账号：

```sql
CREATE DATABASE IF NOT EXISTS paper_kb
  CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'paper_kb_app'@'localhost'
  IDENTIFIED BY '一个强密码（与 .env 中 MYSQL_PASSWORD 一致）';
CREATE USER IF NOT EXISTS 'paper_kb_app'@'127.0.0.1'
  IDENTIFIED BY '同一个密码';
GRANT ALL PRIVILEGES ON paper_kb.* TO 'paper_kb_app'@'localhost';
GRANT ALL PRIVILEGES ON paper_kb.* TO 'paper_kb_app'@'127.0.0.1';
FLUSH PRIVILEGES;
```

> 用宝塔面板建库时，账号的"访问权限"选 **本地服务器（localhost）** 即可。
>
> 若 MySQL 在**另一台服务器**上，在 `.env` 中把 `MYSQL_HOST` 填成那台服务器的 IP，并把账号来源改成本机的出口 IP。

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
| `HTTP_PORT` | `80` | Web 对外服务端口（宝塔/Nginx 反代时填反代目标端口） |
| `API_PORT` | `8080` | API 监听端口，仅绑定 `127.0.0.1` 不对外暴露；与其他项目冲突时修改 |
| `MYSQL_HOST` | `127.0.0.1` | MySQL 在本机时保持默认；在其他服务器时填其 IP |
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

## 六点五、怎么看日志（排查第一步）

进入项目目录（服务器上是 `/home/www/paper_data_`）执行：

```bash
cd /home/www/paper_data_

# 1. 先看两个容器是否都在 running
docker compose --env-file .env -f deploy/docker-compose.yml ps

# 2. 后端日志：接口报错、数据库连接、迁移都在这里
docker compose --env-file .env -f deploy/docker-compose.yml logs -f --tail=200 api

# 3. 前端/反代日志：能看到每个请求的状态码（499/502/404 都在这里）
docker compose --env-file .env -f deploy/docker-compose.yml logs -f --tail=200 web

# 4. 两个一起看
docker compose --env-file .env -f deploy/docker-compose.yml logs -f --tail=100
```

也可以直接用容器名（`docker ps` 查看实际名字，通常是 `paper_data_-api-1`）：

```bash
docker logs -f --tail=200 paper_data_-api-1
docker logs --since 10m paper_data_-api-1        # 只看最近 10 分钟
docker logs paper_data_-api-1 2>&1 | grep -i error
```

直接命中后端做健康检查（绕过 nginx，确认是前端还是后端问题）：

```bash
curl -i http://127.0.0.1:8089/api/health        # 8089 换成 .env 里的 API_PORT
curl -i http://127.0.0.1:8989/api/health        # 8989 换成 .env 里的 HTTP_PORT，走 nginx
```

**页面上按钮点了没反应时，最快的定位方式是浏览器端**：按 `F12` 打开开发者工具 →
- **Console** 标签：有红色报错说明是前端 JS 问题；
- **Network** 标签：点一次按钮，看有没有发出 `/api/...` 请求。
  - 没有请求 → 该控件没绑定行为或路由不存在；
  - 有请求但是 `401` → 会话失效，重新登录；
  - `403` → CSRF 校验失败，清掉站点 Cookie 重新登录；
  - `404`/`502` → 反向代理或后端没起来，回到上面的 `logs api`；
  - `500` → 后端异常，日志里搜同一时间的 `requestId`（响应头 `X-Request-ID` 与日志一致）。

## 七、常见问题

**api 容器反复重启，日志显示 `connect mysql: ...`**
- `connection refused` → 本机 MySQL 没在运行或端口不是 3306（`ss -tlnp | grep 3306` 检查）；
- `Access denied` → 账号/密码不对，确认存在 `'paper_kb_app'@'localhost'`（或 `'127.0.0.1'`/`'%'`）且密码与 `.env` 一致；
- `Unknown database` → 数据库还没建，按第二节执行建库 SQL。

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

**构建报 `dial tcp: lookup xxx on ...:53: i/o timeout`（容器内 DNS 解析失败）**
- compose 已对两个镜像的构建配置了 `network: host`（构建时直接用宿主机网络和 DNS），正常情况下不会再遇到此问题。若使用自定义 buildx builder 导致 `network: host` 被拒绝，改用默认 builder（`docker buildx use default`）或按下面方式给 Docker 守护进程指定 DNS：
  ```bash
  # 若 /etc/docker/daemon.json 已有内容，在原 JSON 中增加 "dns" 字段即可
  tee /etc/docker/daemon.json <<'EOF'
  {
    "dns": ["223.5.5.5", "119.29.29.29", "8.8.8.8"]
  }
  EOF
  systemctl restart docker
  docker run --rm alpine nslookup goproxy.cn   # 验证能解析后重跑 deploy.sh
  ```
- 仍不通则检查防火墙是否拦截了 Docker 网桥的出站 UDP 53（`ufw status`；必要时 `ufw allow out 53/udp`，或将 `/etc/default/ufw` 的 `DEFAULT_FORWARD_POLICY` 改为 `ACCEPT` 后 `ufw reload`）。

**80 或 8080 端口被占用（容器起不来 / `bind: address already in use`）**
- 容器用宿主机网络，`HTTP_PORT` 和 `API_PORT` 都实际占用宿主机端口。修改 `.env` 中对应端口后重跑脚本。

**部署成功但外网打不开页面**
- 宿主机防火墙（ufw/宝塔安全页）没放行 `HTTP_PORT`。推荐做法：不开端口，直接在宝塔给域名建站点开 HTTPS，反向代理到 `http://127.0.0.1:HTTP_PORT`（回环流量不受防火墙限制）；或临时 `ufw allow HTTP_PORT/tcp` 用 IP 直连测试。

## 八、安全建议

- 生产环境建议用宿主机 Nginx/Caddy 或云负载均衡做 **HTTPS 终止**，然后把 `COOKIE_SECURE` 改为 `true`。
- `SETUP_SECRET` 在初始化完成后即失去作用，但仍不要泄露 `.env`（已在 `.gitignore` 中，勿提交）。
- 登录有速率限制（默认 10 分钟窗口内多次失败会锁定 10 分钟），限流状态保存在 API 进程内存中，容器重启后重置——单管理员场景下足够。
- 定期执行第六节的数据库与上传文件备份。
