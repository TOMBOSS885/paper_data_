# 安全基线与风险规避

本项目按 OWASP ASVS Level 2 设计，并以 OWASP Top 10 2021、NIST SP 800-63B、OWASP Cheat Sheet Series、CIS Docker Benchmark 和 MySQL Security Guide 做上线验收。

## 1. 威胁模型与资产

资产包括管理员账号/邮箱、论文原文和解析文本、标签分类、SMTP/MySQL 凭据、会话、备份和审计日志。攻击者包括公网未认证请求者、恶意上传者、窃取 Cookie 的脚本、被入侵容器和试图消耗 CPU/磁盘的滥用者。安全目标是机密性、完整性、可用性和可审计性。

## 2. 认证与会话

- 首次初始化仅在 `initialized=false` 时开放，使用部署 Secret `SETUP_SECRET` + 邮箱验证码；服务端不通过公开状态接口回传 Secret，成功写入唯一管理员并原子关闭入口，防并发竞态。禁止固定默认密码和公开初始化密钥。
- 密码优先 Argon2id（m=64MB,t=3,p=1，按压测调整）或 bcrypt cost>=12；至少 12 字符，拒绝常见/泄露密码和控制字符。
- Cookie：`HttpOnly; Secure; SameSite=Lax/Strict; Path=/api`。access session 15-30 分钟，refresh token 轮换、服务端只存哈希、支持重放检测；改密/注销立即吊销。
- JWT 必须校验固定 `alg`、`iss`、`aud`、`exp`、`nbf`，禁止 `none`；密钥至少 32 字节并通过 Secret 注入。
- Cookie 模式所有变更请求验证 CSRF token、Origin/Referer/Sec-Fetch-Site；CORS 仅允许显式 HTTPS 源。

## 3. SMTP 与验证码

- SMTP 587 强制 STARTTLS，465 强制 TLS 1.2+，校验证书和主机名；拒绝明文回退。配置 SPF/DKIM/DMARC，From 必须为已验证域名。
- 6-8 位 CSPRNG 验证码只存 HMAC-SHA256/Argon2 哈希，绑定 email、purpose、nonce，TTL 5-10 分钟、单次消费、错误 5 次失效、常数时间比较。
- 邮箱 60 秒冷却/小时 6 次，IP 小时 30 次；登录按 IP、账号和 IP+账号限流并指数退避。429 返回 `Retry-After`；响应时序和文案统一，防邮箱枚举。
- 验证码存 Redis（或 MySQL 事务表）共享状态，禁止只存进程内 map；Redis 故障时敏感认证接口 fail-closed 并告警。

## 4. 上传、解析、预览和下载

- 扩展名 + MIME + magic bytes 三重白名单；限制单文件/总配额、文件数、解压大小、页数、文件名长度；拒绝双扩展、控制字符和符号链接。
- 使用 UUID object key，存储目录 Web 根目录外、权限 0700/0640、挂载 `noexec,nodev,nosuid`（可行时）。数据库只存 key。
- PDF/Office/TeX/压缩包解析在非 root、无网络、只读文件系统 worker 中执行，限制 CPU、内存、进程数和超时；禁宏、XXE、外部链接、Zip Slip、PDF JavaScript、嵌套压缩炸弹；先做 AV/ClamAV 扫描。
- 预览只允许 PDF.js 渲染；下载先验证管理员会话和 paper/file 关联，再返回 10 分钟签名 URL/流，设置 `Content-Disposition` 和 `nosniff`，记录审计与限速。

## 5. 搜索、导入和数据库

- MySQL 全部查询使用参数化 SQL/GORM 占位符；字段、排序、分页、LIKE 通配符均白名单/转义。
- 查询长度、复杂度、超时、结果数和并发受限；禁止用户正则直接下推数据库。
- 外部 DOI/URL 导入只允许 HTTPS 白名单；解析 DNS 后阻断 localhost、RFC1918、link-local、云元数据地址和重定向内网，限制响应大小/超时。
- MySQL 使用独立最小权限账号、内网监听、TLS、连接池上限；生产关闭 AutoMigrate；敏感字段应用层加密或仅存哈希。
- 备份遵循 3-2-1，备份加密并定期恢复演练；论文原文可选 AES-256-GCM 信封加密，密钥与数据库分离。

## 6. 浏览器与 API 防护

- 统一错误格式和 request_id；生产不返回 stack trace、SQL、路径、邮箱是否存在。
- CSP：`default-src 'self'`、`object-src 'none'`、`frame-ancestors 'none'`、禁止 `unsafe-eval`；同时设置 `nosniff`、HSTS、Referrer-Policy、Permissions-Policy。
- 日志记录登录、验证码、初始化、上传/解析/下载/删除、分类变更、限流和拒绝事件；禁止密码、验证码、token、SMTP 内容和论文正文入日志。保留 90-180 天并限制访问。

## 7. Docker、主机和可用性

- 镜像多阶段、固定 digest、非 root、只读根文件系统、drop ALL capabilities、no-new-privileges、seccomp/AppArmor；限制 CPU/内存/PIDs。
- 只对外暴露 80/443；MySQL/Redis/API 私有网络，Redis 启用密码/TLS；`.env` 通过 Secret 挂载。
- 反代设置 body/连接/读取超时、请求速率限制、现代 TLS；主机最小开放端口和安全更新。
- 上传、解析、搜索、下载分别限速和并发上限；队列有最大长度、超时、重试和熔断；监控磁盘/CPU/内存/DB/SMTP/429/解析失败。

## 8. 风险与控制矩阵

| 风险 | 控制 | 验证方式 |
|---|---|---|
| 暴力登录/验证码轰炸 | 三维限流、指数退避、账号锁定 | 并发压测、429/423 检查 |
| 邮箱枚举 | 统一响应、统一时序、模糊邮箱 | 不存在邮箱与存在邮箱对比 |
| 会话窃取/XSS | HttpOnly Cookie、CSP、Sanitizer、CSRF | ZAP/Burp、存储型 XSS |
| 任意文件上传/RCE | 白名单、魔数、扫描、沙箱、noexec | 恶意 Office/PDF/脚本/压缩包 |
| 路径遍历/IDOR | UUID key、归属校验、禁止绝对路径 | `../`、符号链接、跨 ID 下载 |
| SQL 注入/查询 DoS | 参数化、AST、分页/超时/复杂度限制 | SQLi payload、长查询 fuzz |
| SSRF | HTTPS/域名/IP 白名单、禁内网和重定向 | localhost/metadata/IPv6 测试 |
| 密钥泄露 | Secret 注入、日志脱敏、最小权限 | 镜像/日志/备份扫描 |
| 容器逃逸/横向移动 | 非 root、只读、私网、cap drop | `docker inspect`、Trivy |
| 数据丢失/勒索 | 加密备份、3-2-1、恢复演练 | 定期 restore 演练 |

## 9. 应急响应

发现泄露时立即吊销全部会话、轮换 JWT/SMTP/DB 密钥、隔离上传和备份、保全审计日志、关闭高风险外部导入，完成修补与恢复后再开放服务。预先维护回滚、恢复和密钥轮换 Runbook。
