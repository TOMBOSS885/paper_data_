# Paper Atlas 前端

React 19 + Vite + TypeScript 的单管理员论文工作台。会话由后端 HttpOnly Cookie 管理，前端不会把 token 写入 `localStorage` 或 `sessionStorage`。

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

镜像构建由 `frontend/Dockerfile` 完成，最终容器使用非 root Nginx 监听 8080。反向代理规则位于 `deploy/nginx.conf`，由 Compose 将宿主机 80 映射到容器 8080。
