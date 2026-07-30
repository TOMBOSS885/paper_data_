# Paper Atlas 前端

React 19 + Vite + TypeScript 的单管理员论文工作台。论文条目展示摘要、分类和标签，删除后可在回收站的保留期内恢复。界面支持明暗主题、响应式侧栏和移动端筛选面板。会话由后端 HttpOnly Cookie 管理，前端不会把 token 写入 `localStorage` 或 `sessionStorage`。

## 本地开发

```powershell
npm install
npm run dev
```

可通过 `VITE_API_BASE_URL` 指向 API（默认 `/api`）。开发服务器需要后端同时运行；未连接后端时页面会显示安全的空态。

## 生产构建

```powershell
npm run build
```

镜像构建由 `frontend/Dockerfile` 完成，最终容器使用 Nginx 托管静态文件并反代 `/api/`。监听端口由根目录 `.env` 的 `HTTP_PORT` 控制，生产环境推荐由宝塔 Nginx 反向代理到 `127.0.0.1:HTTP_PORT`。
