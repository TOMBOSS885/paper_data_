# 论文管理系统 (Paper Knowledge Base) 性能与架构优化指南

本文档基于对项目前端（React + Vite）和后端（Go + MySQL）代码库的深度审查，旨在解决**当论文、标签、用户等数据量激增时**可能出现的卡顿、内存泄漏和响应缓慢问题。

---

## 1. 随着数据量级增大可能出现的“卡顿”与崩溃场景预警

1. **浏览器主线程阻塞（白屏/卡死）**：
   - 当前系统的标签云、多选框（`chip-grid`）和下拉菜单中，若标签和分类数量达到数千级别，前端直接渲染成千上万个 DOM 节点，会导致严重的渲染卡顿。
   - 每次在搜索框输入内容时，全局状态的即时更新会触发整个论文列表和过滤面板的**全量重渲染**。
2. **后端 OOM（内存溢出）与 CPU 飙升**：
   - 在请求 `/tags` 和 `/categories` 等接口时，后端全量拉取数据并转为庞大的 JSON 数组；同时缺乏 Gzip 压缩，这将占用巨大的内存和网络带宽。
   - Go 内存中的限流器（Rate Limiter）实现较为简陋，存在死锁或内存泄漏风险。
3. **数据库连接池耗尽与慢查询**：
   - 全文检索正在使用低效的 `LIKE %kw%` 而不是已建立的 `FULLTEXT` 索引。在大表上，这会导致全表扫描。
   - Dashboard 页面存在多个连续串行的 `COUNT(*)` 查询，高并发下会迅速耗尽数据库连接池。

---

## 2. 前端 (React + Vite) 深度优化方案

### 2.1 渲染性能优化 (React Rendering)
- **输入防抖 (Debounce) 与解耦**：
  - *问题定位*：`src/App.tsx` (第511, 707行) 中的搜索框输入直接触发 `setDraft(e.target.value)`，导致整个页面（包括大列表）在每次按键时重渲染。
  - *解决方案*：引入 `useDeferredValue` 或 `lodash.debounce`。将搜索输入框的状态与外部大列表的数据请求状态解耦，确保打字时不会卡顿。
- **组件记忆化 (Memoization)**：
  - *问题定位*：单纯的展示组件 `PaperRow` (第321, 529行) 未包裹在 `React.memo` 中；传递给它的 `toggleOne` 方法每次渲染都重新生成（未用 `useCallback`）。
  - *解决方案*：使用 `React.memo` 包裹 `PaperRow` 组件；使用 `useCallback` 包装传入的事件处理函数，确保浅比较生效，避免列表项的非必要更新。
- **长列表虚拟滚动 (Virtualization)**：
  - *问题定位*：论文列表、标签和分类下拉框（`chip-grid`, `select` 等, 见第741, 1030, 1331行）缺乏虚拟化支持。
  - *解决方案*：引入 `react-window` 或 `react-virtuoso`，无论数据有多少条，只在 DOM 中保留当前视口可见的数十个节点。

### 2.2 数据获取与状态管理
- **接口分页与按需加载**：
  - *问题定位*：`src/lib/api.ts` (第142, 147行) 中的分类和标签接口直接拉取所有数据（Over-fetching）。
  - *解决方案*：将前端获取逻辑改造为基于光标 (Cursor) 或页码 (Page) 的无限滚动（Infinite Scrolling）获取。
- **全局状态缓存**：
  - *问题定位*：多个组件（`Papers`, `PaperDetailPage`, `Taxonomy` 等，见第597, 876, 1176行）在 `useEffect` 中重复手动拉取 `listTags()` 和 `listCategories()`。
  - *解决方案*：引入 `React Query` (TanStack Query) 或 `SWR`。这不仅能避免重复请求，还能实现数据的后台静默更新和内存缓存。

### 2.3 构建与打包体积优化 (Bundle Size)
- **路由代码分割 (Code Splitting)**：
  - *问题定位*：整个 React 应用（约1491行代码）集中在单个 `App.tsx` (第1476-1488行) 中，导致首屏 JS 体积庞大。
  - *解决方案*：将 `Dashboard`, `Papers`, `PaperDetailPage`, `Taxonomy` 等页面组件拆分到独立文件，并使用 `React.lazy()` 和 `<Suspense>` 进行按需懒加载。

---

## 3. 后端 (Go + MySQL) 深度优化方案

### 3.1 数据库与查询优化
- **弃用 `LIKE`，启用全文索引 (FULLTEXT)**：
  - *问题定位*：`internal/httpapi/server.go:1307` 中，`listPapers` 方法对 `title`, `journal`, `doi`, `abstract_text` 等字段使用 `LIKE %kw%`，导致严重的全表扫描。而迁移脚本 `001_init.sql` 其实已经定义了 `FULLTEXT KEY ft_papers_text(title,abstract_text)`。
  - *解决方案*：将查询语句重构为 `MATCH(title, abstract_text) AGAINST(? IN BOOLEAN MODE)`。
- **合并 Dashboard 的 N+1 `COUNT(*)` 查询**：
  - *问题定位*：`internal/httpapi/server.go:294-300` 处的仪表盘触发了 5 个独立的 `COUNT(*)` 串行查询。
  - *解决方案*：合并为单条包含多个 `SUM(CASE WHEN ... THEN 1 ELSE 0 END)` 的聚合查询，或者引入 Redis 缓存统计结果。
- **添加缺失的索引**：
  - *问题定位*：`internal/httpapi/server.go:350, 360` 处的 facets API 中，`SELECT YEAR(published_at)` 未使用索引，`GROUP BY journal` 也没有 `journal` 字段专属索引。
- **实装分页策略**：
  - *问题定位*：`internal/httpapi/server.go:481, 561` 处，`listTags` 写死了 `LIMIT 500` 缺乏 offset，而 `listCategories` 完全没有 LIMIT。
  - *解决方案*：给 `/tags` 和 `/categories` 等高频读取接口添加 `limit` 和 `offset`，或者使用更高效的 `Cursor-based` 分页。

### 3.2 Go 代码与业务逻辑优化
- **并发批量处理**：
  - *问题定位*：`internal/httpapi/server.go:1533-1541` 中的批量上传，循环解析并同步调用 `saveUpload`，阻塞式 I/O 严重限制吞吐量。
  - *解决方案*：引入 Goroutine 池（如 `errgroup`），并发处理文件读取和解析。
- **优化字符串处理与循环**：
  - *问题定位*：`internal/httpapi/server.go:457` 的 `normalizeName` 方法中存在 `for strings.Contains(s, "  ") { s = strings.ReplaceAll(s, "  ", " ") }` 的 O(N²) 暴力循环，遇极端输入会导致 CPU 飙升。
  - *解决方案*：使用正则表达式替换 `regexp.MustCompile(\`\s+\`).ReplaceAllString(s, " ")`。
- **限流器 (Rate Limiter) 改造**：
  - *问题定位*：`internal/httpapi/server.go:61-75` 的自定义 `limiter` 在清理过期 key 时锁住了整个 map (`l.mu.Lock()`) 且遍历全部数据，大流量下有死锁或内存泄漏风险。
  - *解决方案*：替换为成熟的基于 Redis 的分布式限流或 `golang.org/x/time/rate`。

### 3.3 API 设计与传输层优化
- **开启 Gzip/Brotli 压缩**：
  - *问题定位*：`internal/httpapi/server.go:103` 处缺少响应压缩中间件。返回大列表时带宽压力巨大。
  - *解决方案*：引入并注册 Gzip 压缩中间件。
- **HTTP 缓存控制**：
  - *问题定位*：中间件遗漏了 `Cache-Control` 响应头配置。对于 tags, categories, facets 等低频变动数据缺乏客户端缓存支持。
  - *解决方案*：为对应接口增加如 `Cache-Control: public, max-age=3600` 或 ETag 控制。
