# 论文管理系统（Paper Knowledge Base）优化手册

> 本手册是 `doc/OPTIMIZATION_PLAN.md` 之后**完整的优化实战手册**。
> 它记录：已经落地的改动、剩余待修问题、修复方案、验证清单与回归测试矩阵。
> 任何接手此项目的人在改 backend 或 frontend 前，都应当先读完这份手册。

---

## 目录

1. [已落地的优化（baseline）](#1-已落地的优化baseline)
2. [剩余问题总览（按优先级）](#2-剩余问题总览按优先级)
3. [HIGH 问题修复方案](#3-high-问题修复方案)
4. [MEDIUM 问题修复方案](#4-medium-问题修复方案)
5. [LOW 问题修复方案](#5-low-问题修复方案)
6. [安全 / 稳定性原则（不可破坏）](#6-安全--稳定性原则不可破坏)
7. [验证与回归矩阵](#7-验证与回归矩阵)
8. [性能对照基线](#8-性能对照基线)

---

## 1. 已落地的优化（baseline）

> 这些是之前已经合并的优化，作为后续不能回退的 baseline。

### 1.1 后端

| ID | 改动 | 文件 | 风险等级 |
| --- | --- | --- | --- |
| B1 | **`Dashboard` 由 9 个串行 round-trip 改为 3 个并行 goroutine**（stats 单条 SUM(CASE) 聚合 + storage + recent 列表） | `server.go:dashboard` | LOW（已验证） |
| B2 | **`listTags` / `listCategories` 支持 `?limit=` `?offset=`**，默认 200 / 1000，上限 1000 | `server.go:listTags,listCategories` | MEDIUM（见 M4 分页缺陷） |
| B3 | **`normalizeName` 用 `regexp.MustCompile(\s+)` 替代 O(N²) 暴力循环** | `server.go:normalizeName` | LOW |
| B4 | **Gzip 中间件**（stdlib `compress/gzip`，无新增依赖；只压缩 JSON/JS/XML/SVG；不压缩 octet-stream/PDF） | `server.go:gzipResponse,gzipWriter` | MEDIUM（见 M10 gzipWriter 死字段） |
| B5 | **`Cache-Control: private, max-age=300` 只挂在 tags / categories / facets**，dashboard/papers/auth 全部不缓存；`Vary: Accept-Encoding, Cookie` | `server.go:withCacheHeaders` | LOW |
| B6 | **`limiter` 重构**：per-key bucket + 全局 RWMutex + 后台 5 分钟 GC goroutine；同时修复了原版「用 caller 的 window 判断其他 key 的过期」越界误删 bug | `server.go:limiter` | LOW |
| B7 | **FULLTEXT + ngram 迁移** `003_fulltext_ngram.sql`：探测 `information_schema.PLUGINS` 是否含 ngram，仅在可用时升级 `ft_papers_text` 为 `WITH PARSER ngram`；旧 MySQL 自动跳过 | `migrations/003_fulltext_ngram.sql` | LOW |
| B8 | **连接池** 增加 `SetConnMaxIdleTime(2 * time.Minute)` 防 MySQL `wait_timeout` 主动断开 | `db/db.go` | LOW |
| B9 | **`listPapers` 关键词处理** 优先 `MATCH(title, abstract_text) AGAINST(? IN NATURAL LANGUAGE MODE)`（命中 ngram 索引），回退 LIKE；进入 FULLTEXT 前剥离 BOOLEAN MODE 特殊字符 `+ - < > ~ * ( ) " \` @` 防语法报错 | `server.go:listPapers` | MEDIUM（见 M1 AND/OR 缺陷） |

### 1.2 前端

| ID | 改动 | 文件 | 风险等级 |
| --- | --- | --- | --- |
| F1 | **`PaperRow` 改 `React.memo` + props 全 `useCallback`/`useMemo`** | `App.tsx:PaperRow` | LOW |
| F2 | **`useDeferredValue(draft)`** 让搜索输入低优先级渲染，配合 300ms debounce | `App.tsx:Papers` | LOW |
| F3 | **`toggleOne` / `selectAllOnPage` / `clearSelection` 用 `useCallback`** | `App.tsx:Papers` | LOW |
| F4 | **`PageErrorBoundary` 顶层兜底**——任何子组件抛错不再全白屏 | `App.tsx:PageErrorBoundary` | LOW |
| F5 | **新增 `PasswordModal` / `BatchTaxonomyModal`** 支持批量删除 / 打标 / 收藏的密码再认证 | `App.tsx:PasswordModal` | LOW |

### 1.3 数据库

- 新增 `tags` / `categories` / `paper_tags` / `paper_categories` 四张表（迁移 002）。
- 新增 `003_fulltext_ngram.sql`（可选 ngram 升级）。

---

## 2. 剩余问题总览（按优先级）

> 探索 agent + 独立审计两份结果合并去重；HIGH/MEDIUM 是必修，LOW 是改进项。

| 等级 | 数量 | 修复目标 |
| --- | --- | --- |
| HIGH | 4 | 下一轮必须修 |
| MEDIUM | 16 | 第二轮迭代修 |
| LOW | 11 | 改善项 |

### 2.1 HIGH（必修）

| ID | 简述 | 文件 | 核心风险 |
| --- | --- | --- | --- |
| H1 | `adjustCount` 吞错，计数更新失败仍 commit | `server.go:updatePaperTaxonomy` | `usage_count` / `paper_count` 漂移 |
| H2 | `listCategories` 分页错位（offset 切断父子关系） | `server.go:listCategories` | 静默丢分类 / 错挂 |
| H3 | `saveUpload` fd 资源风险 | `server.go:uploadPapers` | 高并发打爆 ulimit |
| H4 | 批量操作时两个 modal 同时挂载 | `App.tsx:Papers` | UX 错乱 / 用户行为歧义 |

### 2.2 MEDIUM（应当修）

| ID | 简述 | 文件 |
| --- | --- | --- |
| M1 | FULLTEXT + LIKE 是 AND 不是 OR，结果集过窄 | `server.go:listPapers` |
| M2 | Dashboard goroutine 退出未 cancel ctx + recent 双 channel 可能阻塞 | `server.go:dashboard` |
| M3 | facets 5 个 query 错误全被吞，返回 200 + 空数据 | `server.go:facets` |
| M4 | `bulkTxOnPapers` 没检查 `rows.Err()` | `server.go:bulkTxOnPapers` |
| M5 | `bulkDelete` 没回传 `RowsAffected` | `server.go:bulkDelete` |
| M6 | `saveUpload` 两段 INSERT 不在事务里 | `server.go:saveUpload` |
| M7 | 多处 `rows.Err()` 缺失（taxonomy / listCategories） | `server.go` 多处 |
| M8 | 登录失败计数 / 会话吊销 / 改密重置用 `_, _ = ...` 吞错 | `server.go:login,logout,changePassword` |
| M9 | `facets` 的 favorites / missingYear 同样吞错 | `server.go:facets` |
| M10 | `runBulk` 闭包读 `selectedIds`，并发操作竞态 | `App.tsx:Papers` |
| M11 | `COOKIE_SECURE=true` + HTTP 部署下新标签页预览失败 | `App.tsx:PaperDetailPage` |
| M12 | multipart 解析不限制文件数 | `server.go:uploadPapers` |
| M13 | `out.Close()` 错误未检查，物理文件可能未完全落盘就插入 DB 行 | `server.go:saveUpload` |
| M14 | 仅 PDF 校验魔数，其他格式只校验扩展名 | `server.go:saveUpload` |
| M15 | `.gitignore` 没有 `**/.env` 全局匹配 | `.gitignore` |
| M16 | `listCategories` 忽略 `rows.Err()`（同 M7，归类） | `server.go:listCategories` |

### 2.3 LOW（改善项）

| ID | 简述 | 文件 |
| --- | --- | --- |
| L1 | `gzipWriter.minSize` 是死字段 | `server.go:gzipWriter` |
| L2 | Dashboard 顶栏头像硬编码 "A" | `App.tsx:Shell` |
| L3 | Security 改密后 setTimeout 未在 unmount 清理 | `App.tsx:Security` |
| L4 | Dashboard / Papers / PaperDetailPage fetch 没用 AbortController | `App.tsx` |
| L5 | `loadTaxonomy` 两个 query 仍串行 | `server.go:loadTaxonomy` |
| L6 | `update` 的 deps 含 `params` 在 URL 变化时重建 callback | `App.tsx:Papers` |
| L7 | DEPLOYMENT.md 未强调 host networking 是新前提 | `DEPLOYMENT.md` |
| L8 | `gzipWriter` 多余 `decided` / `wroteBody` / `buf` 字段 | `server.go:gzipWriter` |
| L9 | `bulkState` 状态机应改成 discriminated union | `App.tsx:Papers` |
| L10 | 仅 PDF 校验魔数（与 M14 重叠，已归类为 MEDIUM） | — |
| L11 | `out.Close()` 错误吞掉（同 M13） | — |

---

## 3. HIGH 问题修复方案

### H1. `adjustCount` 吞错导致计数漂移

**位置**：`backend/internal/httpapi/server.go`（`updatePaperTaxonomy` 内联闭包）

**症状**：标签被绑到论文时，`usage_count += 1` 或 `usage_count -= 1` 如果因网络抖动执行失败，事务仍然 `Commit()`，造成 `tags.usage_count` 永久少于实际使用数。

**修复**：让闭包返回 `error`，失败时 `tx.Rollback()` 并写 500。

```go
adjustCountDelta := func(m map[uint64]struct{}, delta int) error {
    if len(m) == 0 {
        return nil
    }
    ids := make([]uint64, 0, len(m))
    for x := range m {
        ids = append(ids, x)
    }
    ph := placeholders(len(ids))
    args := make([]any, 0, len(ids)+1)
    args = append(args, delta)
    for _, x := range ids {
        args = append(args, x)
    }
    if _, err := tx.ExecContext(r.Context(),
        `UPDATE `+countTable+` SET `+countColumn+`=GREATEST(0, `+countColumn+`+?) WHERE id IN (`+ph+`)`,
        args...); err != nil {
        return fmt.Errorf("counter update: %w", err)
    }
    return nil
}
if err := adjustCountDelta(addTotal, +1); err != nil {
    return err
}
if err := adjustCountDelta(removeTotal, -1); err != nil {
    return err
}
```

**回归**：连续创建 50 个标签、绑定、再删除、再创建，最终 `usage_count` 必须精确等于 `COUNT(paper_tags.tag_id)`。

---

### H2. `listCategories` 分页错位

**位置**：`backend/internal/httpapi/server.go:796-867`

**症状**：传 `?limit=10&offset=5`，分层之前 LIMIT/OFFSET 已经把数据库行切走。后果：
- 父分类在 offset 段外 → 子分类在 `byParent[id]` 但不会被 attach 到响应
- 父分类在 limit 段内但子分类在 limit 段外 → `Children` 不完整

**修复方案 A（推荐，按根分类分页）**：在 SQL 里只 LIMIT 根分类，然后用第二条 SQL 一次性把它们的子分类拿出来。

```go
// 第一条：根分类分页
roots, _ := s.db.QueryContext(ctx, `
    SELECT id, parent_id, name, sort_order, paper_count
    FROM categories WHERE parent_id IS NULL
    ORDER BY sort_order ASC, name ASC LIMIT ? OFFSET ?`, limit, offset)

// 第二条：根分类的所有子分类（不再次 LIMIT，因为子分类通常远少于根）
rootIDs := []uint64{}
for rows.Next() { ...; rootIDs = append(rootIDs, id) }
children, _ := s.db.QueryContext(ctx, `
    SELECT id, parent_id, name, sort_order, paper_count
    FROM categories WHERE parent_id IN (`+placeholders(len(rootIDs))+`)
    ORDER BY parent_id, sort_order ASC, name ASC`, rootIDs...)
```

**修复方案 B（更简单，去掉分页，保留硬上限）**：分类树本身规模有限（业务上很少超过 1000），直接不分页 + 硬上限 `LIMIT 1000`，文档明确说「分类超过 1000 后再做分页」。

**推荐**：先用 B，把 H2 降级成 LOW；等真的有人创建 5000 个分类再做 A。

---

### H3. `saveUpload` fd 资源风险

**位置**：`backend/internal/httpapi/server.go:saveUpload`

**症状**：单次上传 100 文件 × 并发 100 请求 = 同时打开 20,000 个 fd。

**修复**：在 `uploadPapers` 入口加文件数上限 + 用 worker pool。

```go
const maxFilesPerRequest = 100

func (s *Server) uploadPapers(w http.ResponseWriter, r *http.Request) {
    // ... authenticate ...
    files := r.MultipartForm.File["files[]"]
    if len(files) == 0 {
        files = r.MultipartForm.File["files"]
    }
    if len(files) == 0 {
        writeError(w, 400, "invalid_request", "files is required")
        return
    }
    if len(files) > maxFilesPerRequest {
        writeError(w, 400, "invalid_request", fmt.Sprintf("too many files (max %d)", maxFilesPerRequest))
        return
    }
    // ... 其余保持顺序逻辑（顺序处理对单管理员场景足够） ...
}
```

并发上限交给反向代理 / 前端控制；后端保证单请求 ≤ 100 文件。

**进阶**（如果未来要并发处理）：用 `errgroup.SetLimit(runtime.NumCPU())` + `g.Go(func() error { return s.saveUpload(...) })`，但要保证路径唯一性已经由 `uuid.NewString()` 保证。

---

### H4. 批量操作两个 modal 同时挂载

**位置**：`frontend/src/App.tsx:Papers`

**症状**：在「tags」/「categories」模式下，`<BatchTaxonomyModal>` 和 `<PasswordModal>` **同时挂载**（虽然 `PasswordModal` 有条件，但 `bulkState.pendingIDs` 在 `tags` 模式下也是 `undefined`，所以渲染条件是 `bulkState.kind !== 'tags' && bulkState.kind !== 'categories'`——逻辑上不会同时显示 PasswordModal，但**用户先选完 chip 进入 password 阶段时**两个 modal 短暂叠加）。

**修复**：状态机改成 discriminated union：

```ts
type BulkFlow =
  | { kind: 'delete' }      // 直接进密码
  | { kind: 'favorite-on' } // 直接进密码
  | { kind: 'favorite-off' }
  | { kind: 'tags'; step: 'pick' | 'password'; pendingIDs?: number[] }
  | { kind: 'categories'; step: 'pick' | 'password'; pendingIDs?: number[] }

const [flow, setFlow] = useState<BulkFlow | null>(null)

// 渲染：
{flow?.kind === 'tags' && flow.step === 'pick' && <BatchTaxonomyModal ... />}
{flow && (flow.kind === 'delete' || flow.step === 'password') && <PasswordModal ... />}
```

---

## 4. MEDIUM 问题修复方案

### M1. FULLTEXT + LIKE 改成 OR

**位置**：`backend/internal/httpapi/server.go:listPapers`（line 1552-1567）

当前写法：
```go
where = append(where, "MATCH(title, abstract_text) AGAINST(? IN NATURAL LANGUAGE MODE)")
where = append(where, "(COALESCE(doi,'') LIKE ? OR journal LIKE ? OR authors_json LIKE ?)")
```

这两条用 `AND` 串起来——title 命中但 DOI 命中（标题不含 "survey"，DOI 含 "10.xxxx/survey"）会被漏掉。

**修复**：把 LIKE 合并进 FULLTEXT 表达式，用 OR：

```go
where = append(where, "(MATCH(title, abstract_text) AGAINST(? IN NATURAL LANGUAGE MODE) OR COALESCE(doi,'') LIKE ? OR journal LIKE ? OR authors_json LIKE ?)")
args = append(args, safe, like, like, like)
```

---

### M2. Dashboard goroutine cancel + 双 channel 阻塞

**位置**：`backend/internal/httpapi/server.go:dashboard`

**症状**：
1. 第一个 goroutine 出错 → handler return，但另外两个 goroutine 仍在跑（ctx 没 cancel）
2. `recentCh` 发送后 `recentIDsCh` 才能发送——如果中间 handler 被 cancel，recentIDsCh 可能永久阻塞

**修复**：用 `context.WithCancel` 派生可取消 ctx，所有 goroutine 共用同一个 result 频道。

```go
ctx, cancel := context.WithCancel(r.Context())
defer cancel()

type statsResult struct {
    total, favorites, unread, last30 int
    storage                          int64
    recent                           []map[string]any
    recentIDs                        []string
    err                              error
}

results := make(chan statsResult, 3)

go func() {
    var r statsResult
    err := s.db.QueryRowContext(ctx, `SELECT ... FROM papers`).
        Scan(&r.total, &r.favorites, &r.unread, &r.last30)
    if err != nil { r.err = err; results <- r; return }
    results <- r
}()
go func() {
    var r statsResult
    err := s.db.QueryRowContext(ctx, `SELECT ...`).
        Scan(&r.storage)
    if err != nil { r.err = err; results <- r; return }
    results <- r
}()
go func() {
    var r statsResult
    rows, err := s.db.QueryContext(ctx, `SELECT id, title, ...`)
    if err != nil { r.err = err; results <- r; return }
    defer rows.Close()
    for rows.Next() { ... }
    if err := rows.Err(); err != nil { r.err = err; results <- r; return }
    results <- r
}()

var acc statsResult
for i := 0; i < 3; i++ {
    select {
    case <-ctx.Done():
        cancel()
        writeError(w, 503, "request_canceled", "request canceled")
        return
    case r := <-results:
        if r.err != nil { cancel(); writeError(w, 500, "internal_error", "unable to load dashboard"); return }
        acc.total += r.total
        acc.favorites += r.favorites
        // ...
    }
}
```

注意：`recent` + `recentIDs` 是同一个 goroutine 的两部分，所以放进同一个 struct 就不会产生跨 channel 阻塞问题。

---

### M3 + M9. `facets` 错误被吞

**位置**：`backend/internal/httpapi/server.go:facets`

**修复**：把所有 `if err == nil` 改成显式错误传播。失败时直接返回 503：

```go
func (s *Server) facets(w http.ResponseWriter, r *http.Request) {
    if _, ok := s.authenticate(r); !ok { ... }
    years, journals, statuses, favorites, missingYear, err := s.computeFacets(r.Context())
    if err != nil {
        writeError(w, 503, "dependency_unavailable", "facets unavailable")
        return
    }
    writeJSON(w, 200, map[string]any{
        "years": years, "journals": journals, "statuses": statuses,
        "favorites": favorites, "missingYear": missingYear,
    })
}

func (s *Server) computeFacets(ctx context.Context) (years, journals []map[string]any, statuses map[string]int, favorites, missingYear int, err error) {
    // ... 每个 QueryContext 错误都 return err，不吞 ...
}
```

---

### M4 / M7 / M16. `rows.Err()` 全检

**位置**：`backend/internal/httpapi/server.go` 多处

**修复**：在所有 `for rows.Next() { ... }` 循环后加 `if err := rows.Err(); err != nil { ... }`：

```go
// 通用模式
rows, err := s.db.QueryContext(ctx, q, args...)
if err != nil { ... }
defer rows.Close()
for rows.Next() {
    if err := rows.Scan(...); err != nil { ... continue }
}
if err := rows.Err(); err != nil {
    writeError(w, 500, "internal_error", "iteration failed")
    return
}
```

---

### M5. `bulkDelete` 回传 `RowsAffected`

**修复**：
```go
existing, missing, err := s.bulkTxOnPapers(r.Context(), paperIDs, func(tx *sql.Tx, ids []string) error {
    res, err := tx.ExecContext(r.Context(), `UPDATE papers SET deleted_at=... WHERE id IN (...) AND deleted_at IS NULL`, args...)
    if err != nil { return err }
    // 把 RowsAffected 通过闭包外变量带回
    affected, _ = res.RowsAffected()
    return nil
})
```

（闭包外声明 `var affected int64`，在闭包里赋值，回调外读 `affected` 而不是 `len(existing)`。）

---

### M6 + M13. `saveUpload` 事务化 + close 错误

**修复**：把 papers 插入和 paper_files 插入放进事务；插入前 `out.Close()` 之后还要确认 `out.Sync()` 成功。

```go
tx, err := s.db.BeginTx(ctx, nil)
if err != nil { os.Remove(path); return nil, err }
defer tx.Rollback()

_, err = tx.ExecContext(ctx, `INSERT INTO papers(...) VALUES(?,?,...)`, id, title, ...)
if err != nil { os.Remove(path); return nil, err }

_, err = tx.ExecContext(ctx, `INSERT INTO paper_files(...) VALUES(...)`, ...)
if err != nil { os.Remove(path); return nil, err }

if err := tx.Commit(); err != nil { os.Remove(path); return nil, err }

// 现在才确认物理文件已经 commit
if err := out.Close(); err != nil { os.Remove(path); return nil, err }
```

---

### M8. 安全敏感写错误不能再吞

**位置**：`server.go:409, 415, 429, 659`

涉及：
- `login` 里失败计数 + 锁定
- `logout` 里 sessions 吊销
- `changePassword` 里 sessions 吊销 + cookie 过期

**修复**：失败时至少 `log.Printf("[WARN] failed to update admin %d state: %v", id, err)`，并在 `logout` / `changePassword` 等**会话吊销失败**时返回 500（因为安全语义被破坏）。

---

### M10. `runBulk` 操作竞态

**位置**：`frontend/src/App.tsx:Papers` `runBulk`

**修复**：在 `runBulk` 入口快照 IDs，置 `busy`，完成后才能再次选择：

```ts
const runBulk = async (kind, password, pendingIDs) => {
    if (busy) return  // 防双击
    const idsSnapshot = Array.from(selectedIds)
    if (idsSnapshot.length === 0) return
    setBusy(true)
    // ... 调用 API ...
    // 用操作 token：进入前记录 token，回调里比对
    const token = Date.now()
    bulkTokenRef.current = token
    try {
        // 调用 API
    } catch (e) { ... }
    finally {
        if (bulkTokenRef.current === token) {
            setBusy(false)
        }
    }
}
```

---

### M11. COOKIE_SECURE / HTTP 部署检查

**修复**：启动时校验 `PUBLIC_BASE_URL` 是 https 还是 http，与 `COOKIE_SECURE` 必须一致：

```go
// config.go 校验
if cfg.CookieSecure {
    if !strings.HasPrefix(cfg.PublicBaseURL, "https://") {
        return fmt.Errorf("COOKIE_SECURE=true requires PUBLIC_BASE_URL to start with https://")
    }
}
```

---

### M12. multipart 文件数上限

**修复**（与 H3 合并）：在 `uploadPapers` 入口处 `if len(files) > 100 { reject }`。同时 `r.MultipartForm.File["files[]"]` 一次只取一个 key（避免恶意构造 10000 个 parts）。

---

### M14. 扩展名校验魔数

**修复**：每个允许的扩展名都给一个最小可验证的 magic bytes 检查：

```go
var magicByExt = map[string][]byte{
    ".pdf":  {'%', 'P', 'D', 'F', '-'},
    ".png":  {0x89, 0x50, 0x4E, 0x47},
    ".jpg":  {0xFF, 0xD8, 0xFF},
    // ...
}
```

实际支持的格式：PDF 用 `%PDF-`；DOCX/DOC/ODT 都是 ZIP 容器（`PK\x03\x04`）；TEX/TXT 文本文件没强校验（接受）。

**实现**：
```go
if ext != ".txt" && ext != ".tex" {
    if !bytes.HasPrefix(head, magicByExt[ext]) {
        return nil, fmt.Errorf("file signature mismatch")
    }
}
```

---

### M15. `.gitignore` 全局匹配

```gitignore
# Environment
.env
.env.*
backend/.env
frontend/.env
**/.env
!**/.env.example
```

---

## 5. LOW 问题修复方案

| ID | 修复 | 文件 |
| --- | --- | --- |
| L1 | 删除 `gzipWriter.minSize` 字段（决定时机改为 WriteHeader 一次性；或者改用 buffer 真的按大小判定） | `server.go:gzipWriter` |
| L2 | 在 App boot 里把 `me()` 数据存 Context 或顶层 state，Shell 头像字母取 `displayName[0]?.toUpperCase() ?? 'A'` | `App.tsx:Shell` |
| L3 | `Security` 改密后用 `useRef` 持有 timer id，cleanup 函数里 `clearTimeout` | `App.tsx:Security` |
| L4 | 在 `api()` 里支持 `AbortSignal`，组件 unmount / query 变化时 `controller.abort()` | `App.tsx + lib/api.ts` |
| L5 | `loadTaxonomy` 改为两个 goroutine 并行；同上 dashboard 模式 | `server.go:loadTaxonomy` |
| L6 | `update` callback deps 改为 `[]`（用 functional setter），或者用 useRef 持有 params | `App.tsx:Papers` |
| L7 | DEPLOYMENT.md 顶部加一段「重要前提：容器使用 host networking」 | `DEPLOYMENT.md` |
| L8 | 删 `decided` / `wroteBody` / `buf` | `server.go:gzipWriter` |

---

## 6. 安全 / 稳定性原则（不可破坏）

未来任何人改这个项目，必须遵守：

### 6.1 CSRF 不绕过

- 所有 `POST/PUT/PATCH/DELETE` 必须经过 CSRF 中间件
- 例外仅 `/api/auth/login` 与 `/api/setup/admin`（首次初始化）
- 不要新增「免 CSRF」路由；如有合理需要，**先**让 senior reviewer 加新白名单

### 6.2 密码再认证不可降级

- 任何**不可逆**或**安全敏感**的写操作必须二次校验密码
- 当前已覆盖：单篇删除、批量删除 / 打标 / 收藏、改密
- **新功能**：凡涉及「修改」「删除」「权限」——一律走 `confirmAdminPassword`

### 6.3 限流键命名要稳定

- 格式 `<operation>:<scope>`，scope 可以是 IP / email / adminID
- 现有键不要改名字（可能影响现有部署的限流状态）
- 新增限流键必须在 DEPLOYMENT.md 记录阈值

### 6.4 缓存策略只对只读接口

- `Cache-Control: private, max-age=...` **只能**挂在 GET tags / categories / facets 三个接口
- 含个人数据的接口（dashboard / papers / auth）**绝不**加缓存
- `Vary: Accept-Encoding, Cookie` 不可漏

### 6.5 Gzip 只压缩文本类

- 压缩白名单：`text/*` / `application/json` / `application/javascript` / `application/xml` / `image/svg+xml`
- 不压缩：`application/octet-stream` / `application/pdf` / 任何 `Content-Encoding` 已设的响应
- 仅 2xx 启用；错误响应走原文（方便调试）

### 6.6 软删除不变

- `papers.deleted_at IS NULL` 是「活跃」的唯一判定
- 所有读路径必须加这个 WHERE
- 物理文件不级联删除（保留审计可能）

### 6.7 数据库迁移只能加不能改

- 任何字段重命名 / 类型变更 / NOT NULL 收紧 → 都要写新的迁移 + 兼容期
- 迁移用 `CREATE TABLE IF NOT EXISTS` / `IF EXISTS` + `INFORMATION_SCHEMA` 探测
- `schema_migrations` 表保证只跑一次

---

## 7. 验证与回归矩阵

### 7.1 编译验证

```bash
cd backend && gofmt -l . && go build ./... && go vet ./... && go test ./...
cd frontend && npx tsc --noEmit && npm run build
```

### 7.2 部署

```bash
cd /home/www/paper_data_
bash deploy.sh
```

`Ctrl+Shift+R` 强刷浏览器清缓存。

### 7.3 端到端回归矩阵

> 每个 ✓ 表示「修完后必须仍然正常」。

| 流程 | 验证步骤 | 期望 | 关联优化 |
| --- | --- | --- | --- |
| 登录 | 邮箱 + 密码 | 进入 Dashboard | B1 |
| 错误密码 5 次 | 连错 5 次 | 第 6 次 429 | B6 |
| 改密 | 当前密码 + 新密码（≥12 位） | 成功，所有会话失效 | M8 |
| 导入 1 个 PDF | 上传 | Dashboard 计数 +1，最近更新出现 | M6/M13/M14 |
| 导入 100 个文件 | 上传 | 全部入库，无 fd 泄露 | H3/M12 |
| 上传非 PDF 改后缀为 .pdf | 上传 | 拒绝「file signature mismatch」 | M14 |
| 搜索中文 | 输入「机器学习」 | 返回含 ngram 命中的论文（ngram 可用时） | B7/M1 |
| 搜索特殊字符 | 输入 `+ - *` | 不 500，结果正常 | B9 |
| 创建标签 | 输入标签名 + 选颜色 | 出现新 chip | H1（计数维护） |
| 删除标签 | 点 × 确认 | 论文 chip 同步消失 | H1 |
| 创建分类（同名） | 创建「机器学习」两次 | 合并成一条，paper_count 累加 | H1 |
| 删除分类（带子） | 删除根分类 | 子分类 + 论文关联全部清理 | H2 |
| 论文库筛选 | 切年份 / 状态 / 收藏 / 标签 / 分类 | URL 同步、列表正确 | F1/F2/F3 |
| 论文库搜索 | 输入 → 300ms 后 URL 更新 | 列表更新；输入时 UI 不卡 | F2 |
| 论文库批量删除 | 勾 3 篇 → 批量删除 → 输密码 | 3 篇消失，红条 / 绿条正确 | H4/M10 |
| 论文库批量打标 | 勾 3 篇 → 选 `#survey` → 输密码 | 行上出现 `#survey` chip | H1/H4 |
| 论文库批量加/取消收藏 | 勾 → 选 → 输密码 | ★ 切换正确 | H4 |
| 论文库全选本页 | 20 篇 | 全部勾上；翻页后清空 | F1/F3 |
| 论文详情修改 | 改标题 / DOI / 笔记 | 保存成功 | F1 |
| 论文详情打标 | 勾几个 tag / cat → 保存 | 论文列表 chip 同步 | H1 |
| 论文详情删除 | 点删除 → 输密码 | 跳回列表 | M8 |
| 论文预览（新标签页） | 点预览按钮 | 新标签页打开 PDF | M11 |
| 预览（COOKIE_SECURE + HTTP 部署） | 点预览 | 报错或提前提示 | M11 |
| 切换分类 Tab | 分类树 ↔ 标签 | 数据刷新，无残留 | B6 |
| 切换 Tab 时删除按钮 | hover 才显示 | 键盘焦点也能看到 | F4 |
| Dashboard 并发 | 模拟 5 个浏览器同时打开 Dashboard | 全部 200，无 5xx 雪崩 | B1 |
| Dashboard 中途取消 | 关闭浏览器 | 后端 1-2s 内停止剩余 query | M2 |
| 上传时取消 | 中途断网 | 服务端检测 ctx 取消，写一半文件被清理 | M6 |
| gzip 大列表 | `curl -H "Accept-Encoding: gzip" /api/tags` | 看到 `Content-Encoding: gzip` | B4 |
| 缓存头 | `curl -i /api/tags` | 看到 `Cache-Control: private, max-age=300` | B5 |
| nginx 反代 gzip | `curl -i http://服务器/api/tags` | 看到 `Content-Encoding: gzip`（pass-through） | B4 |
| 直接改 `/api/papers/<id>` DELETE 不带 password | 旧客户端 / curl 直接调 | 返回 400「password is required」 | H4 |
| 升级后保留 admin 账号 | 重启 api 容器 | 仍然能登录 | B7 |

### 7.4 性能对照基线

| 操作 | 优化前（预估） | 优化后 | 数据量 |
| --- | --- | --- | --- |
| `/api/dashboard` P95 | 9 RTT × ~10ms = ~90ms | 3 并行 RTT × ~10ms = ~10ms | 1k papers |
| 列表搜索「机器学习」 | 全表 LIKE 5 列扫描 | FULLTEXT ngram 命中（≤10ms） | 10k papers |
| 限流器 1w+ keys | 每次 allow 都遍历全表 | per-key bucket O(1) | 高频攻击 |
| tags 接口响应 | 拉全量 | gzip + 5 分钟缓存 | 浏览器冷启 |
| `Papers` 输入搜索 | 每键全量重渲染 | memo + useDeferredValue 让出主线程 | 1k 行 |

---

## 8. 性能对照基线

### 8.1 优化前 / 后的指标对照（来自审查 + 改动记录）

```text
后端
  Dashboard 9 RTT → 3 RTT (parallel)
  listPapers 关键词 → 走 FULLTEXT ngram（中文分词可用时）
  normalizeName O(N²) → O(N) 单遍正则
  limiter 全表扫 → per-key O(1) + 后台 GC
  tags/categories/facets 无缓存 → 5 分钟浏览器私有缓存
  tags/categories/facets/facets 响应无压缩 → gzip（白名单文本类）

前端
  PaperRow 每次都 re-render → React.memo + useCallback
  搜索输入每键全量重渲染 → useDeferredValue 让出主线程
  切换 Tab 标签/分类 数据未分离 → state machine 用 discriminated union（建议 H4）

DB
  连接池 idle 不过期 → SetConnMaxIdleTime(2min)
  ft_papers_text 未启用 ngram → 003 迁移探测启用
```

### 8.2 不再追求的目标

- React Query / SWR（攻击面 + 复杂度增加，单管理员场景不需要）
- Virtual scrolling（当前数据规模不需要）
- Redis 分布式限流（单机 Go 服务不需要）
- HTTP/2 Push（nginx 反代已做）
- 大文件上传并发（顺序处理对单管理员场景足够）

---

## 附录 A：手动复现 HIGH 问题的方法

### H1 计数漂移

```sql
-- 在 MySQL 里人为把 tags.usage_count 改成 0
UPDATE tags SET usage_count = 0 WHERE id = 1;
```

后端日志会有 `counter update: ...` 错误；前端重试即可修复。

### H2 分页错位

```bash
# 创建 20 个根分类 + 每个 5 个子分类（共 120）
# 访问 /api/categories?limit=10&offset=5
# 看响应里子分类是否完整
```

### H3 fd 资源

```bash
ulimit -n 1024
# 上传 100 个文件并发 50 次
# 观察容器日志有没有 too many open files
```

### H4 modal 竞态

打开浏览器开发者工具 Performance 面板 → 在批量打标签流程里快速点多次 → 看是否有两个 modal 的 layout 都触发了。

---

## 附录 B：相关文档

- `doc/OPTIMIZATION_PLAN.md`：原始优化点检视
- `DEPLOYMENT.md`：部署手册（含 host networking 前提）
- `doc/README.md`：原始设计规范（SMTP / Redis 已废弃）
- `README.md`：项目介绍

---

**任何接手项目的人：在改 backend / frontend 之前先读这份手册。**