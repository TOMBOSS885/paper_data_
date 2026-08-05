# Zotero 同步部署与使用

## 服务器升级

1. 将新仓库文件上传到服务器，或在仓库目录执行 `git pull`。
2. 先备份 MySQL 数据库和 `paper_uploads` Docker volume。
3. 检查 `.env`：生产环境必须使用公开的 `https://` `PUBLIC_BASE_URL`、
   `COOKIE_SECURE=true`，并将 `UPLOAD_MAX_BYTES` 设为可接受 PDF 的大小。
4. Linux 服务器在仓库根目录执行 `bash deploy.sh`；Windows Server 执行
   `./deploy.ps1`。脚本会重新构建并滚动启动 `api`、`web` 容器。
5. `AUTO_MIGRATE=true` 时，API 会自动执行 `010_zotero_sync.sql`。执行
   `docker compose logs api`，并访问 `/api/health/ready` 验证服务就绪。
6. 登录网页端，进入 `安全设置`，为每台 Zotero 设备创建一个同步令牌。

The migration is additive. It creates sync clients, hashed tokens, external
links, comparison sessions, and operation records; it does not alter existing
papers or delete files.

插件清单已配置 GitHub Releases 更新地址。更新插件时，在开发机重新执行
`./zotero-plugin/build-xpi.ps1`，发布新的 XPI 和更新清单后，用户可从 Zotero
插件管理器检查更新；在更新清单发布前，也可以直接安装新的 XPI。

## Zotero setup

1. 在 Zotero 9 中安装 `zotero-plugin/dist/paper-kb-sync-0.1.9.xpi`。
2. 打开 `Edit -> Settings -> Paper KB Sync`。
3. 填入与 `PUBLIC_BASE_URL` 相同的公开服务器地址。
4. 粘贴网页端 `安全设置` 中只显示一次的令牌，并保存测试连接。
5. 选中文献后打开 `Tools -> Paper KB 同步`，可看到 `仅本地`、`仅服务器`、
   `双方都有/一致` 和 `双方都有/有差异` 四类记录。勾选后执行上传或拉取。
6. 若只想拉取服务器论文，不要选中任何条目，直接打开同步窗口即可；拉取完成后
   插件会保存 Zotero 条目与服务器论文的稳定映射，下一次比较不会依赖 DOI 猜测。

## Operational notes

- Create a different token per Zotero device and revoke it from `安全设置` if
  the device is lost.
- The plugin stores the token in the Mozilla login manager, not in its ordinary
  preferences.
- 打开同步窗口时只会扫描当前选中的本地文献；未选中时不会上传任何本地文献，
  但仍可看到并拉取服务器独有论文。
- Current v1 supports bibliographic metadata, manual tags, and the first
  accessible stored PDF. 冲突不会自动覆盖；选择“拉取”时会先确认以服务器
  元数据和主 PDF 更新本地条目。
- The server exposes `GET /api/sync/v1/capabilities` for connection tests and
  uses bearer tokens only for `/api/sync/v1/*`; the web UI keeps its existing
  Cookie + CSRF authentication.
