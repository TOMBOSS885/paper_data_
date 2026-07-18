# React 前端界面与交互规范

## 1. 框架选型结论

推荐：React 19 + Vite + TypeScript + Tailwind CSS + shadcn/ui（Radix 原语）+ TanStack Query/Table/Virtual + React Hook Form/Zod + PDF.js + Uppy + lucide-react。该组合保留现有个人博客项目的 React/Vite/Tailwind/Lucide 习惯，同时允许论文阅读器保持独立视觉。

备选：Ant Design 6.x + ProComponents，适合快速交付和高密度表格；或 MUI 9.x/Mantine 9.x。若使用 MUI X DataGrid Pro/Premium，需单独评估商业许可。组件版本必须锁定小版本并提交 lockfile。

## 2. 信息架构与路由

```text
/setup
/auth/login        /auth/verify        /auth/forgot
/app/dashboard
/app/papers        /app/papers/:id
/app/import        /app/search
/app/taxonomy      /app/settings/security
/app/settings/storage             /app/audit
```

首次未初始化只允许 `/setup`、健康检查和静态资源；初始化后访问 `/setup` 返回 410 并跳转登录。

## 3. 视觉方向

- 工作型、信息密度高、8px 以内圆角；冷灰背景、青绿色主操作色、少量暖色状态色，避免整页紫色渐变。
- 桌面端：左侧 240-256px 导航 + 中央工作区 + 可折叠右侧详情抽屉。
- 移动端：导航变为 Sheet，筛选变为底部/侧边 Sheet，详情从底部打开。
- 支持深色模式、高对比模式和 `prefers-reduced-motion`；颜色不能是唯一状态表达方式。

## 4. 页面规格

### Dashboard

显示总论文数、近 30 天导入、待读、存储用量、解析失败、最近打开列表和标签/分类分布。所有卡片提供可点击下钻链接，不做营销型 Hero。

### Papers 工作区

顶部全局搜索和命令面板；左侧 facets（分类、标签、年份、阅读状态、来源）；中间支持表格/紧凑列表切换；右侧详情抽屉展示标题、作者、摘要、来源、标签、分类和快捷动作。表格使用 TanStack Table，超过 1 万条启用 Virtual。

### Import 导入向导

Stepper：选择文件 -> 安全扫描 -> 解析预览 -> 去重提示 -> 分类/标签 -> 确认。显示每个文件状态、可重试错误和扫描结果；客户端校验只是提示，最终以服务端校验为准。

### Reader 阅读器

左侧页缩略图，中间 PDF.js 画布，右侧元数据/标签/分类/笔记。提供下载、收藏、阅读状态和页码定位；预览内容使用隔离 iframe/worker，禁止原文 HTML 执行脚本。

### Search 搜索

URLSearchParams 持久化查询；支持 `title:`、`author:`、`year:`、`tag:`、`category:`、`doi:`、`journal:`、`type:`、`is:favorite`，引号短语和 AND/OR/NOT。输入防抖 200-300ms，AbortController 取消旧请求；外部来源分别显示 loading/error/rate-limit。

### Taxonomy

标准分类树和自定义分类并列显示，节点标注 scheme/version；标签支持颜色、别名、合并和批量应用。删除分类必须显示影响论文数量并要求确认。

## 5. 交互与无障碍

- 目标符合 WCAG 2.2 AA：正文对比度至少 4.5:1，大字 3:1，触摸目标至少 44px。
- 每个表单输入有可见 label、`aria-describedby` 错误说明和 `aria-live` 状态；Dialog/Sheet focus trap，关闭后还原焦点。
- 所有图标按钮使用 lucide 图标并提供 tooltip/aria-label；熟悉动作不使用“文字胶囊”替代图标。
- 上传、解析和搜索状态提供键盘可达的进度、取消和重试；不依赖 hover 才能获得关键信息。

## 6. 前端安全与性能

- 会话只由 HttpOnly Secure Cookie 管理，前端不读取 token；写请求携带 CSRF token。
- 标题、摘要、笔记、搜索高亮全部经过 DOMPurify/allowlist；CSP 禁止 `unsafe-eval`。
- 路由懒加载、TanStack Query 缓存、服务端筛选、PDF 按页渲染和 Web Worker；构建启用 Brotli/gzip 和 bundle analyzer。
- 依赖使用 npm lockfile、Dependabot/SCA；Vitest + Testing Library 做组件测试，Playwright 做关键流程和响应式回归。
