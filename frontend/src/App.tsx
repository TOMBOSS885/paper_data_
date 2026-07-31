import { Component, memo, useDeferredValue } from 'react'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, DragEvent, ReactNode } from 'react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { useQueryClient } from '@tanstack/react-query'
import { AlertCircle, ArrowLeft, BookOpen, CheckCircle2, ChevronDown, ChevronRight, Clock3, Database, Download, Eye, Filter, FolderPlus, Info, LayoutDashboard, LogOut, Menu, Moon, RotateCcw, Save, Search, Settings2, Star, Sun, Tag as TagIcon, Tags, TrendingUp, Trash2, TriangleAlert, Upload, X } from 'lucide-react'
import {
  ApiError,
  bulkDeletePapers,
  bulkSetFavorite,
  bulkUpdatePaperCategories,
  bulkUpdatePaperTags,
  changePassword,
  createAdmin,
  createCategory,
  createTag,
  dashboard,
  deleteCategory,
  deletePaperWithPassword,
  deleteTag,
  fileUrl,
  listCategories,
  useCategories,
  listTags,
  useTags,
  login,
  logout,
  me,
  paper as fetchPaper,
  papers as fetchPapers,
  reextractPaper,
  restoreTrashPaper,
  setupStatus,
  trashPapers,
  updatePaper,
  updatePaperCategories,
  updatePaperTags,
  uploadPapers,
} from './lib/api'
import type { Category, Paper, PaperDetail, Tag, TrashPaper, UploadResult } from './lib/api'
import { extractPapersPreview } from './lib/api'
import { removeCategoryFromTree } from './lib/taxonomy'

// 标签颜色映射：与 styles.css 中 .tag.<color> 的类名一致。
const TAG_COLORS: { value: TagColor; label: string }[] = [
  { value: 'teal', label: '青绿' },
  { value: 'blue', label: '蓝' },
  { value: 'amber', label: '琥珀' },
  { value: 'rose', label: '玫瑰' },
  { value: 'slate', label: '中性灰' },
  { value: 'green', label: '草绿' },
  { value: 'violet', label: '紫罗兰' },
]
type TagColor = Tag['color']

const PAGE_SIZE = 20
const ALLOWED_EXTENSIONS = ['.pdf', '.doc', '.docx', '.odt', '.tex', '.txt']
const MAX_UPLOAD_BYTES = 200 * 1024 * 1024
const STATUS_LABELS: Record<string, string> = { unread: '未读', reading: '阅读中', read: '已读' }

const errorMessage = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : '操作失败，请重试')
const statusLabel = (v?: string) => STATUS_LABELS[v ?? 'unread'] ?? '未读'
const authorLine = (p: Paper) => {
  const names = (p.authors ?? []).map((a) => (typeof a === 'string' ? a : a?.name)).filter(Boolean)
  return `${names.length ? names.join(', ') : '作者信息待补充'} · ${p.year ? `${p.year} 年` : '年份未知'}`
}

type ThemeMode = 'light' | 'dark'

function initialTheme(): ThemeMode {
  if (typeof window === 'undefined') return 'dark'
  const stored = window.localStorage.getItem('paper-atlas-theme')
  if (stored === 'light' || stored === 'dark') return stored
  return 'dark'
}

function ThemeToggle({ compact = false }: { compact?: boolean }) {
  const [theme, setTheme] = useState<ThemeMode>(initialTheme)

  useEffect(() => {
    document.documentElement.dataset.theme = theme
    window.localStorage.setItem('paper-atlas-theme', theme)
  }, [theme])

  const next = theme === 'dark' ? 'light' : 'dark'
  return (
    <button
      className={compact ? 'icon-button theme-toggle compact' : 'theme-toggle'}
      type="button"
      onClick={() => setTheme(next)}
      aria-label={next === 'dark' ? '切换到深色模式' : '切换到浅色模式'}
      title={next === 'dark' ? '深色模式' : '浅色模式'}
    >
      {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
      {!compact && <span>{theme === 'dark' ? '浅色模式' : '深色模式'}</span>}
    </button>
  )
}

// 全局错误边界：阻止单个子组件的运行时崩溃导致整页变白。
// 捕获到错误后展示降级 UI 与原始错误信息，同时提供重新载入入口。
interface ErrorBoundaryState { error: Error | null }
class PageErrorBoundary extends Component<{ children: ReactNode }, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }
  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }
  componentDidCatch(error: Error, info: unknown) {
    // eslint-disable-next-line no-console
    console.error('[PageErrorBoundary]', error, info)
  }
  render() {
    if (this.state.error) {
      const message = this.state.error.message || '未知错误'
      return (
        <div className="splash" style={{ padding: 24, textAlign: 'left' }}>
          <div style={{ maxWidth: 560, background: '#fff', border: '1px solid #dfe7e4', borderRadius: 10, padding: 24 }}>
            <h1 style={{ margin: '0 0 8px', color: '#a8433d' }}>页面加载失败</h1>
            <p style={{ color: '#6d7b83', margin: '0 0 16px' }}>下方是该错误的原文。复制后可以贴给开发者，或用「重试」再次尝试。</p>
            <pre style={{ background: '#fff5f4', border: '1px solid #f4cac5', padding: 12, borderRadius: 6, fontSize: 12, overflow: 'auto', maxHeight: 240, whiteSpace: 'pre-wrap', wordBreak: 'break-word', color: '#a8433d' }}>{message}</pre>
            <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
              <button className="button primary" onClick={() => this.setState({ error: null })}>重试</button>
              <button className="button secondary" onClick={() => (window.location.href = '/app/dashboard')}>回到概览</button>
            </div>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

export function Shell({ children }: { children: ReactNode }) {
  const nav = useNavigate()
  const loc = useLocation()
  const [open, setOpen] = useState(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const links = [
    { to: '/app/dashboard', label: '概览', icon: LayoutDashboard },
    { to: '/app/papers', label: '论文库', icon: BookOpen },
    { to: '/app/import', label: '导入论文', icon: Upload },
    { to: '/app/taxonomy', label: '分类与标签', icon: Tags },
    { to: '/app/trash', label: '回收站', icon: Trash2 },
    { to: '/app/settings/security', label: '安全设置', icon: Settings2 },
  ]

  // ⌘K / Ctrl+K 聚焦全局搜索框。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const signOut = async () => {
    await logout().catch(() => {})
    // 整页跳转，确保清掉内存里的已登录状态。
    window.location.href = '/auth/login'
  }

  return (
    <div className="app-shell">
      <button className={open ? 'sidebar-backdrop open' : 'sidebar-backdrop'} onClick={() => setOpen(false)} aria-label="关闭导航遮罩" />
      <aside className={open ? 'sidebar open' : 'sidebar'}>
        <div className="brand">
          <span className="brand-mark">PA</span>
          <span className="brand-copy"><strong>Paper Atlas</strong><small>Research library</small></span>
          <button className="icon-button mobile-close" onClick={() => setOpen(false)} aria-label="关闭导航"><X size={18} /></button>
        </div>
        <nav>
          {links.map(({ to, label, icon: Icon }) => (
            <Link key={to} to={to} onClick={() => setOpen(false)} aria-current={loc.pathname.startsWith(to) ? 'page' : undefined} className={loc.pathname.startsWith(to) ? 'nav-link active' : 'nav-link'}>
              <Icon size={18} /><span>{label}</span>
            </Link>
          ))}
        </nav>
        <div className="sidebar-foot">
          <div className="status-dot"><span /> 本地知识库</div>
          <button className="nav-link" onClick={signOut}><LogOut size={18} />退出登录</button>
        </div>
      </aside>
      <div className="main-area">
        <header className="topbar">
          <button className="icon-button mobile-menu" onClick={() => setOpen(true)} aria-label="打开导航"><Menu size={20} /></button>
          <div className="global-search search-inputbox">
            <Search className="search-leading-icon" size={17} />
            <input
              ref={searchRef}
              aria-label="搜索论文"
              placeholder=" "
              onKeyDown={(e) => {
                if (e.key !== 'Enter') return
                const value = e.currentTarget.value.trim()
                nav(value ? `/app/papers?q=${encodeURIComponent(value)}` : '/app/papers')
              }}
            />
            <span className="search-label">搜索标题、作者、DOI…</span>
            <span className="search-fill" aria-hidden="true" />
          </div>
          <div className="top-actions"><ThemeToggle compact /><span className="avatar">A</span></div>
        </header>
        <main>{children}</main>
      </div>
    </div>
  )
}

function Setup() {
  const nav = useNavigate()
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [setupSecret, setSetupSecret] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setError('')
    if (!email.includes('@')) return setError('请输入合法的邮箱地址')
    if (!name.trim()) return setError('请填写显示名称')
    if (password.length < 12) return setError('密码至少 12 位')
    if (!setupSecret.trim()) return setError('请填写 .env 中的 SETUP_SECRET')
    setBusy(true)
    try {
      await createAdmin({ email: email.trim(), displayName: name.trim(), password, setupNonce: setupSecret.trim() })
      nav('/auth/login', { replace: true })
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="auth-page">
      <div className="auth-theme"><ThemeToggle /></div>
      <div className="auth-panel">
        <div className="brand large"><span className="brand-mark">PA</span><span className="brand-copy"><strong>Paper Atlas</strong><small>Research library</small></span></div>
        <p className="eyebrow">首次部署</p>
        <h1>创建管理员</h1>
        <p className="muted">初始化只执行一次。请输入部署时设置的初始化令牌。</p>
        <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
        <label>初始化令牌<input type="password" value={setupSecret} onChange={(e) => setSetupSecret(e.target.value)} placeholder="SETUP_SECRET" autoComplete="one-time-code" /></label>
        <label>管理员邮箱<input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" autoComplete="email" /></label>
        <label>显示名称<input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：林研究员" /></label>
        <label>设置密码<input type="password" value={password} onChange={(e) => setPassword(e.target.value)} placeholder="至少 12 位" autoComplete="new-password" /></label>
        <button className="button primary full" disabled={busy} onClick={submit}>{busy ? '正在初始化…' : '完成初始化'}</button>
        <p className="fineprint">初始化令牌是部署时写入 .env 的 SETUP_SECRET。</p>
      </div>
    </div>
  )
}

function Login() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setError('')
    if (!email.includes('@') || !password) return setError('请输入邮箱和密码')
    setBusy(true)
    try {
      await login({ email: email.trim(), password })
      window.location.href = '/app/dashboard'
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
    }
  }

  return (
    <div className="auth-page">
      <div className="auth-theme"><ThemeToggle /></div>
      <div className="auth-panel">
        <div className="brand large"><span className="brand-mark">PA</span><span className="brand-copy"><strong>Paper Atlas</strong><small>Research library</small></span></div>
        <p className="eyebrow">管理员入口</p>
        <h1>欢迎回来</h1>
        <p className="muted">你的论文、笔记和研究脉络都在这里。</p>
        <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
        <form
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <label>邮箱<input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" /></label>
          <label>密码<input type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" /></label>
          <button className="button primary full" type="submit" disabled={busy}>{busy ? '正在登录…' : '登录'}</button>
        </form>
        <div className="auth-links"><Link to="/setup">首次部署？创建管理员</Link><span>登录受到速率限制保护</span></div>
      </div>
    </div>
  )
}

function Dashboard() {
  const [data, setData] = useState<{ totalPapers: number; importedLast30Days: number; unread: number; storageBytes: number; recent: Paper[] } | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    dashboard()
      .then((r) =>
        setData({
          totalPapers: r.data.totalPapers ?? 0,
          importedLast30Days: r.data.importedLast30Days ?? 0,
          unread: r.data.unread ?? 0,
          storageBytes: r.data.storageBytes ?? 0,
          recent: r.data.recent ?? [],
        }),
      )
      .catch((e) => setError(errorMessage(e)))
  }, [])

  const stats = [
    { label: '论文总数', value: data?.totalPapers ?? '—', unit: '篇', icon: BookOpen, tone: 'blue' },
    { label: '近 30 天导入', value: data?.importedLast30Days ?? '—', unit: '篇', icon: TrendingUp, tone: 'green' },
    { label: '待阅读', value: data?.unread ?? '—', unit: '篇', icon: Clock3, tone: 'amber' },
    { label: '存储占用', value: data ? ((data.storageBytes || 0) / 1073741824).toFixed(2) : '—', unit: 'GB', icon: Database, tone: 'rose' },
  ]

  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">研究工作台</p>
          <h1>概览</h1>
          <p className="muted">快速了解知识库的积累与下一步阅读。</p>
        </div>
        <Link className="button primary" to="/app/import"><Upload size={17} />导入论文</Link>
      </div>
      <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
      <section className="stat-grid">
        {stats.map(({ label, value, unit, icon: Icon, tone }) => (
          <div className={`stat-card tone-${tone}`} key={label}>
            <div className="stat-card-head"><span>{label}</span><span className="stat-icon"><Icon size={19} /></span></div>
            <div><strong>{value}</strong><small>{unit}</small></div>
          </div>
        ))}
      </section>
      <section className="dashboard-banner">
        <div className="dashboard-banner-icon"><TrendingUp size={22} /></div>
        <div><h2>继续构建你的研究脉络</h2><p>集中整理新论文，再通过阅读状态、分类和标签保持资料库可检索。</p></div>
        <div className="dashboard-banner-actions">
          <Link to="/app/import">导入论文</Link>
          <Link to="/app/papers">浏览论文库</Link>
        </div>
      </section>
      <section className="content-grid">
        <div className="panel">
          <div className="panel-head"><h2>最近更新</h2><Link to="/app/papers">查看全部</Link></div>
          {(data?.recent ?? []).length === 0 ? (
            <Empty title="还没有论文" description="导入第一篇论文，建立你的研究地图。" action={<Link className="button secondary" to="/app/import"><Upload size={16} />开始导入</Link>} />
          ) : (
            (data?.recent ?? []).map((p) => <PaperRow key={p.id} paper={p} />)
          )}
        </div>
        <div className="panel">
          <div className="panel-head"><h2>快捷入口</h2></div>
          <div className="quick-grid">
            <Link to="/app/papers?status=unread"><BookOpen size={19} /><span>继续阅读</span><small>查看未读论文</small></Link>
            <Link to="/app/papers?favorite=true"><Star size={19} /><span>我的收藏</span><small>标记为重点的论文</small></Link>
            <Link to="/app/taxonomy"><Tags size={19} /><span>分类浏览</span><small>按年份与期刊统计</small></Link>
            <Link to="/app/settings/security"><Settings2 size={19} /><span>安全设置</span><small>账号与密码</small></Link>
          </div>
        </div>
      </section>
    </Shell>
  )
}

// memo 化后，列表项只在 paper 引用或 selected 变化时 re-render。
// 父层 `Papers` 把 onToggle 用 useCallback 包好，memo 浅比较才有效。
const PaperRow = memo(function PaperRow({ paper, selectable, selected, onToggle }: { paper: Paper; selectable?: boolean; selected?: boolean; onToggle?: (id: string) => void }) {
  const tags = useMemo(() => paper.tags ?? [], [paper.tags])
  const cats = useMemo(() => paper.categories ?? [], [paper.categories])
  const nav = useNavigate()
  const goDetail = useCallback(() => nav(`/app/papers/${paper.id}`), [nav, paper.id])
  const onKey = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault()
      goDetail()
    }
  }, [goDetail])
  const stop = (e: React.MouseEvent | React.ChangeEvent) => e.stopPropagation()
  return (
    <div className={`paper-row${selectable ? ' selectable' : ''}${selected ? ' selected' : ''}`} role="link" tabIndex={0} onClick={goDetail} onKeyDown={onKey}>
      {selectable && (
        <label className="row-check" onClick={stop}>
          <input
            type="checkbox"
            checked={!!selected}
            onChange={() => onToggle?.(paper.id)}
            aria-label={`选择 ${paper.title}`}
          />
        </label>
      )}
      <div className="paper-icon"><BookOpen size={18} /></div>
      <div className="paper-main">
        <strong>{paper.isFavorite ? '★ ' : ''}{paper.title}</strong>
        <span className="paper-byline">{authorLine(paper)}{paper.journal ? ` · ${paper.journal}` : ''}</span>
        <p className={paper.abstract?.trim() ? 'paper-abstract' : 'paper-abstract empty-copy'}>
          {paper.abstract?.trim() || '暂无摘要，可进入详情页补充论文概览。'}
        </p>
        <div className="row-taxonomy" aria-label="论文分类与标签">
          <div className="taxonomy-group">
            <span className="taxonomy-label">分类</span>
            {cats.length > 0 ? cats.map((c) => (
              <button type="button" className="taxonomy-chip cat" key={`c-${c.id}`} onClick={(e) => { stop(e); nav(`/app/papers?category=${c.id}`) }}>{c.name}</button>
            )) : <span className="taxonomy-empty">未分类</span>}
          </div>
          <div className="taxonomy-group">
            <span className="taxonomy-label">标签</span>
            {tags.length > 0 ? tags.map((t) => (
              <button type="button" className={`taxonomy-chip tag tag-${t.color}`} key={`t-${t.id}`} onClick={(e) => { stop(e); nav(`/app/papers?tag=${t.id}`) }}>#{t.name}</button>
            )) : <span className="taxonomy-empty">无标签</span>}
          </div>
        </div>
      </div>
      <span className="row-status">{statusLabel(paper.readingStatus)}</span>
    </div>
  )
})

function Empty({ title, description, action }: { title: string; description: string; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="empty-icon"><BookOpen size={24} /></div>
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  )
}

type NoticeVariant = 'error' | 'success' | 'info' | 'warning'
const activeNoticeIds: symbol[] = []
const noticeStackSubscribers = new Set<() => void>()

function syncNoticeStack() {
  noticeStackSubscribers.forEach((subscriber) => subscriber())
}

function NoticeModal({
  open,
  variant = 'info',
  title,
  message,
  onClose,
}: {
  open: boolean
  variant?: NoticeVariant
  title?: string
  message: string
  onClose: () => void
}) {
  const onCloseRef = useRef(onClose)
  const noticeId = useRef(Symbol('notice'))
  const [stackIndex, setStackIndex] = useState(0)

  useEffect(() => {
    onCloseRef.current = onClose
  }, [onClose])

  useEffect(() => {
    if (!open) return
    const id = noticeId.current
    const syncIndex = () => setStackIndex(Math.max(0, activeNoticeIds.indexOf(id)))
    if (!activeNoticeIds.includes(id)) activeNoticeIds.push(id)
    noticeStackSubscribers.add(syncIndex)
    syncNoticeStack()
    return () => {
      const index = activeNoticeIds.indexOf(id)
      if (index >= 0) activeNoticeIds.splice(index, 1)
      noticeStackSubscribers.delete(syncIndex)
      syncNoticeStack()
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    const closeTimer = window.setTimeout(() => onCloseRef.current(), 4200)
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onCloseRef.current()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      window.clearTimeout(closeTimer)
      window.removeEventListener('keydown', onKey)
    }
  }, [open])

  if (!open) return null

  const Icon = variant === 'error' ? AlertCircle : variant === 'success' ? CheckCircle2 : variant === 'warning' ? TriangleAlert : Info
  const fallbackTitle = variant === 'error' ? '操作失败' : variant === 'success' ? '操作完成' : variant === 'warning' ? '请确认' : '提示'

  return (
    <div
      className={`notice-toast notice-${variant}`}
      role={variant === 'error' ? 'alert' : 'status'}
      aria-live={variant === 'error' ? 'assertive' : 'polite'}
      style={{ '--notice-offset': `${stackIndex * 118}px` } as CSSProperties}
    >
      <div className="notice-icon"><Icon size={21} /></div>
      <div className="notice-copy">
        <h2>{title ?? fallbackTitle}</h2>
        <p>{message}</p>
      </div>
      <button type="button" className="icon-button notice-close" aria-label="关闭提示" onClick={onClose}><X size={15} /></button>
      <span className="notice-progress" />
    </div>
  )
}

function LoadingView({ text = '加载中…' }: { text?: string }) {
  return (
    <div className="loading" role="status" aria-live="polite">
      <div className="wifi-loader" aria-hidden="true">
        <svg className="circle-outer" viewBox="0 0 86 86">
          <circle className="back" cx="43" cy="43" r="40" />
          <circle className="front" cx="43" cy="43" r="40" />
          <circle className="new" cx="43" cy="43" r="40" />
        </svg>
        <svg className="circle-middle" viewBox="0 0 60 60">
          <circle className="back" cx="30" cy="30" r="27" />
          <circle className="front" cx="30" cy="30" r="27" />
        </svg>
        <svg className="circle-inner" viewBox="0 0 34 34">
          <circle className="back" cx="17" cy="17" r="14" />
          <circle className="front" cx="17" cy="17" r="14" />
        </svg>
        <span className="wifi-dot" />
      </div>
      <span className="loading-text" data-text={text}>{text}</span>
    </div>
  )
}

function ConfirmModal({
  open,
  title,
  description,
  confirmLabel = '确认',
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}) {
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!open) return
    const timer = setTimeout(() => cancelRef.current?.focus(), 30)
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy) onCancel()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      clearTimeout(timer)
      window.removeEventListener('keydown', onKey)
    }
  }, [open, busy, onCancel])

  if (!open) return null

  return (
    <div className="modal-mask" onClick={busy ? undefined : onCancel}>
      <div className={`modal-panel confirm-panel${danger ? ' danger' : ''}`} role="alertdialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <div className="notice-icon"><TriangleAlert size={22} /></div>
        <div className="notice-copy">
          <h2>{title}</h2>
          <p>{description}</p>
        </div>
        <div className="modal-actions notice-actions">
          <button ref={cancelRef} type="button" className="button secondary" onClick={onCancel} disabled={busy}>取消</button>
          <button type="button" className={danger ? 'button danger' : 'button primary'} onClick={onConfirm} disabled={busy}>{busy ? '处理中…' : confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}

// 通用密码确认 modal：用于批量删除、批量打标和单篇删除等敏感操作。
// 受控 open / busy，提交时调用 onConfirm(password)。
function PasswordModal({
  open, title, description, confirmLabel = '确认', busy, onConfirm, onCancel,
}: {
  open: boolean
  title: string
  description: string
  confirmLabel?: string
  busy?: boolean
  onConfirm: (password: string) => void
  onCancel: () => void
}) {
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!open) {
      setPassword('')
      setErr('')
      return
    }
    // 打开时聚焦输入框；按键 ESC 关闭。
    const t = setTimeout(() => inputRef.current?.focus(), 30)
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !busy) onCancel()
    }
    window.addEventListener('keydown', onKey)
    return () => {
      clearTimeout(t)
      window.removeEventListener('keydown', onKey)
    }
  }, [open, busy, onCancel])

  if (!open) return null
  const submit = () => {
    if (!password) {
      setErr('请输入当前管理员密码')
      return
    }
    setErr('')
    onConfirm(password)
  }
  return (
    <div className="modal-mask" onClick={busy ? undefined : onCancel}>
      <div className="modal-panel" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h2>{title}</h2>
        <p className="muted">{description}</p>
        {err && <p className="modal-note error">{err}</p>}
        <label className="field">当前管理员密码
          <input
            ref={inputRef}
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter') submit() }}
            autoComplete="current-password"
          />
        </label>
        <div className="modal-actions">
          <button type="button" className="button secondary" onClick={onCancel} disabled={busy}>取消</button>
          <button type="button" className="button primary" onClick={submit} disabled={busy}>{busy ? '处理中…' : confirmLabel}</button>
        </div>
      </div>
    </div>
  )
}

// 批量打标/批量分类 modal：先选一组标签或分类，确认后再走密码确认。
function BatchTaxonomyModal({
  open, kind, items, onConfirm, onCancel,
}: {
  open: boolean
  kind: 'tags' | 'categories'
  items: { id: number; name: string; color?: string }[]
  onConfirm: (ids: number[]) => void
  onCancel: () => void
}) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  useEffect(() => {
    if (open) setSelected(new Set())
  }, [open])
  if (!open) return null
  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }
  const submit = () => onConfirm(Array.from(selected))
  return (
    <div className="modal-mask" onClick={onCancel}>
      <div className="modal-panel wide" role="dialog" aria-modal="true" onClick={(e) => e.stopPropagation()}>
        <h2>{kind === 'tags' ? '批量打标签' : '批量分类'}</h2>
        <p className="muted">替换模式：所选论文最终拥有下方勾选的标签/分类（其它会被移除）。</p>
        {items.length === 0 ? (
          <p className="muted">还没有{kind === 'tags' ? '标签' : '分类'}，先去「分类与标签」页面创建。</p>
        ) : (
          <div className="chip-grid">
            {items.map((it) => (
              <label key={it.id} className={`chip-pick${selected.has(it.id) ? ' on' : ''} ${kind === 'tags' ? `tag-${it.color ?? 'teal'}` : ''}`}>
                <input type="checkbox" checked={selected.has(it.id)} onChange={() => toggle(it.id)} />
                {kind === 'tags' ? `#${it.name}` : it.name}
              </label>
            ))}
          </div>
        )}
        <div className="modal-actions">
          <button type="button" className="button secondary" onClick={onCancel}>取消</button>
          <button type="button" className="button primary" onClick={submit} disabled={items.length === 0}>下一步</button>
        </div>
      </div>
    </div>
  )
}

function Papers() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const status = params.get('status') ?? ''
  const sort = params.get('sort') ?? 'newest'
  const yearFrom = params.get('yearFrom') ?? ''
  const yearTo = params.get('yearTo') ?? ''
  const favorite = params.get('favorite') === 'true'
  const tagFilter = params.get('tag') ?? ''
  const categoryFilter = params.get('category') ?? ''
  const page = Math.max(1, Number(params.get('page') ?? '1') || 1)

  const [items, setItems] = useState<Paper[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState(q)
  const [filtersOpen, setFiltersOpen] = useState(false)
  const { data: allTags = [] } = useTags()
  const { data: allCategories = [] } = useCategories()
  // 批量选择：跨页会被自动清空，避免误操作。点击"全选本页"一键勾选当前 20 篇。
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  // 批量操作需要的 modal 状态机：null=无；先打开 taxonomy 选择 modal，再打开 password modal。
  const [bulkState, setBulkState] = useState<null | {
    kind: 'delete' | 'favorite-on' | 'favorite-off' | 'tags' | 'categories'
    pendingIDs?: number[]
  }>(null)
  const [bulkBusy, setBulkBusy] = useState(false)
  const [batchError, setBatchError] = useState('')

  // 翻页 / 改筛选时清空选择（防止跨页批量）。
  useEffect(() => {
    setSelectedIds(new Set())
  }, [page, q, status, sort, yearFrom, yearTo, favorite ? '1' : '0', tagFilter, categoryFilter])

  const toggleOne = useCallback((id: string) => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else {
        if (next.size >= 100) {
          setBatchError('单次最多选择 100 篇')
          return prev
        }
        next.add(id)
      }
      return next
    })
  }, [])
  const selectAllOnPage = useCallback(() => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const it of items) {
        if (next.size >= 100) break
        next.add(it.id)
      }
      if (items.length > 100 && prev.size === 0) setBatchError(`一次最多选 100 篇；本页 ${items.length} 篇仅勾选了前 100。`)
      return next
    })
  }, [items])
  const clearSelection = useCallback(() => setSelectedIds(new Set()), [])
  const selectedCount = selectedIds.size
  const allOnPageSelected = items.length > 0 && items.every((it) => selectedIds.has(it.id))

  const update = useCallback(
    (patch: Record<string, string | null>) => {
      const next = new URLSearchParams(params)
      Object.entries(patch).forEach(([key, value]) => {
        if (value === null || value === '') next.delete(key)
        else next.set(key, value)
      })
      if (!('page' in patch)) next.delete('page') // 改动筛选条件时回到第一页
      setParams(next, { replace: true })
    },
    [params, setParams],
  )

  useEffect(() => setDraft(q), [q])

  // useDeferredValue 让 React 19 把这个值标为低优先级，让出主线程给输入；
  // 配合 300ms debounce 既保证 UI 不卡、又保证请求不爆。
  const deferredDraft = useDeferredValue(draft)
  useEffect(() => {
    const value = deferredDraft.trim()
    if (value === q) return
    const timer = setTimeout(() => update({ q: value }), 300)
    return () => clearTimeout(timer)
  }, [deferredDraft, q, update])

  const queryString = useMemo(() => {
    const search = new URLSearchParams()
    if (q) search.set('q', q)
    if (status) search.set('status', status)
    if (favorite) search.set('favorite', 'true')
    if (yearFrom) search.set('yearFrom', yearFrom)
    if (yearTo) search.set('yearTo', yearTo)
    if (tagFilter) search.set('tag', tagFilter)
    if (categoryFilter) search.set('category', categoryFilter)
    if (sort && sort !== 'newest') search.set('sort', sort)
    search.set('pageSize', String(PAGE_SIZE))
    if (page > 1) search.set('page', String(page))
    return `?${search.toString()}`
  }, [q, status, favorite, yearFrom, yearTo, tagFilter, categoryFilter, sort, page])

  // Categories and tags are now managed by React Query

  useEffect(() => {
    let alive = true
    setLoading(true)
    setError('')
    fetchPapers(queryString)
      .then((r) => {
        if (!alive) return
        setItems(r.data.items ?? [])
        setTotal(r.data.total ?? 0)
      })
      .catch((e) => {
        if (!alive) return
        setItems([])
        setTotal(0)
        setError(errorMessage(e))
      })
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [queryString])

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const r = await fetchPapers(queryString)
      setItems(r.data.items ?? [])
      setTotal(r.data.total ?? 0)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setLoading(false)
    }
  }, [queryString])

  // 批量操作入口：把 id 转成数组，调用后端 endpoint，处理 missing；成功后清选 + 重拉。
  const runBulk = async (kind: 'delete' | 'favorite-on' | 'favorite-off' | 'tags' | 'categories', password: string, pendingIDs?: number[]) => {
    if (selectedIds.size === 0) return
    const ids = Array.from(selectedIds)
    setBulkBusy(true)
    setBatchError('')
    try {
      let res: { data: { deleted?: number; updated?: number; missing: string[] } }
      if (kind === 'delete') {
        res = await bulkDeletePapers(ids, password)
      } else if (kind === 'favorite-on' || kind === 'favorite-off') {
        res = await bulkSetFavorite(ids, kind === 'favorite-on', password)
      } else if (kind === 'tags') {
        res = await bulkUpdatePaperTags(ids, pendingIDs ?? [], password)
      } else {
        res = await bulkUpdatePaperCategories(ids, pendingIDs ?? [], password)
      }
      const missing = res.data.missing ?? []
      const note = missing.length
        ? `操作完成：${res.data.deleted ?? res.data.updated ?? ids.length} 篇成功，${missing.length} 篇已被删除或不存在`
        : `操作完成：${res.data.deleted ?? res.data.updated ?? ids.length} 篇`
      setSelectedIds(new Set())
      await reload()
      setError('')
      setBatchError(note)
      setBulkState(null)
    } catch (e) {
      // 失败也保留选中状态，方便重试
      setBatchError(errorMessage(e))
    } finally {
      setBulkBusy(false)
    }
  }

  const lastPage = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const filtered = Boolean(q || status || favorite || yearFrom || yearTo || tagFilter || categoryFilter)

  // 当 batchError 表示成功提示时（包含 "操作完成"），复用现有 alert.error 渲染。
  const batchNotice = batchError.startsWith('操作完成') ? batchError : ''
  const batchErr = batchError && !batchNotice ? batchError : ''

  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">资料库</p>
          <h1>论文库</h1>
          <p className="muted">共 {total} 篇{filtered ? '（已应用筛选）' : ''}。关键词会匹配标题、作者、期刊、DOI 和摘要。</p>
        </div>
        <Link className="button primary" to="/app/import"><Upload size={17} />导入论文</Link>
      </div>
      {selectedCount > 0 && (
        <div className="batch-bar" role="region" aria-label="批量操作">
          <span className="batch-count">已选 <strong>{selectedCount}</strong> 篇</span>
          <button type="button" className="button secondary" onClick={clearSelection}>清空选择</button>
          <div className="batch-spacer" />
          <button type="button" className="button secondary" onClick={() => setBulkState({ kind: 'favorite-on' })}><Star size={15} />批量加收藏</button>
          <button type="button" className="button secondary" onClick={() => setBulkState({ kind: 'favorite-off' })}><Star size={15} />批量取消收藏</button>
          <button type="button" className="button secondary" onClick={() => setBulkState({ kind: 'tags' })}><TagIcon size={15} />批量打标签</button>
          <button type="button" className="button secondary" onClick={() => setBulkState({ kind: 'categories' })}><FolderPlus size={15} />批量分类</button>
          <button type="button" className="button danger" onClick={() => setBulkState({ kind: 'delete' })}><Trash2 size={15} />移入回收站</button>
        </div>
      )}
      <div className="toolbar">
        <div className="search-field search-inputbox">
          <Search className="search-leading-icon" size={17} />
          <input aria-label="搜索论文库" value={draft} onChange={(e) => setDraft(e.target.value)} placeholder=" " />
          <span className="search-label">搜索标题、作者、DOI、期刊…</span>
          {draft && <button className="icon-button" aria-label="清空搜索" onClick={() => setDraft('')}><X size={15} /></button>}
          <span className="search-fill" aria-hidden="true" />
        </div>
        <button type="button" className={filtered ? 'button secondary filter-toggle active' : 'button secondary filter-toggle'} onClick={() => setFiltersOpen((value) => !value)} aria-expanded={filtersOpen}>
          <Filter size={16} />筛选{filtered ? ' · 已启用' : ''}
        </button>
        <select aria-label="排序" value={sort} onChange={(e) => update({ sort: e.target.value })}>
          <option value="newest">最近添加</option>
          <option value="oldest">最早添加</option>
          <option value="updated">最近更新</option>
          <option value="year">发表年份</option>
          <option value="title">标题</option>
        </select>
      </div>
      <div className="papers-layout">
        <aside className={filtersOpen ? 'filters open' : 'filters'}>
          <div className="filter-head"><h3>筛选</h3><button type="button" className="icon-button filter-close" aria-label="关闭筛选" onClick={() => setFiltersOpen(false)}><X size={16} /></button></div>
          <label>
            阅读状态
            <select value={status} onChange={(e) => update({ status: e.target.value })}>
              <option value="">全部状态</option>
              <option value="unread">未读</option>
              <option value="reading">阅读中</option>
              <option value="read">已读</option>
            </select>
          </label>
          <label>起始年份<input type="number" inputMode="numeric" placeholder="2020" value={yearFrom} onChange={(e) => update({ yearFrom: e.target.value })} /></label>
          <label>截止年份<input type="number" inputMode="numeric" placeholder="2025" value={yearTo} onChange={(e) => update({ yearTo: e.target.value })} /></label>
          <label className="check-row">
            <input type="checkbox" checked={favorite} onChange={(e) => update({ favorite: e.target.checked ? 'true' : null })} />
            只看收藏
          </label>
          <label className="field-tight">
            按分类
            <select value={categoryFilter} onChange={(e) => update({ category: e.target.value })}>
              <option value="">全部分类</option>
              {allCategories.flatMap((c) => [
                <option key={`c-${c.id}`} value={String(c.id)}>{c.name} ({c.paperCount})</option>,
                ...(c.children ?? []).map((sc) => <option key={`sc-${sc.id}`} value={String(sc.id)}>{c.name} / {sc.name} ({sc.paperCount})</option>),
              ])}
            </select>
          </label>
          <label className="field-tight">
            按标签
            <select value={tagFilter} onChange={(e) => update({ tag: e.target.value })}>
              <option value="">全部标签</option>
              {allTags.map((t) => <option key={t.id} value={String(t.id)}>{t.name} ({t.usageCount ?? 0})</option>)}
            </select>
          </label>
          {filtered && (
            <button className="button secondary full" onClick={() => setParams(new URLSearchParams(), { replace: true })}>重置筛选</button>
          )}
          <div className="filter-note">筛选条件会写入网址，可直接收藏或分享该链接。</div>
        </aside>
        <div className="panel paper-list">
          <NoticeModal open={Boolean(batchErr)} variant="error" message={batchErr} onClose={() => setBatchError('')} />
          <NoticeModal open={Boolean(batchNotice)} variant="success" message={batchNotice} onClose={() => setBatchError('')} />
          {!loading && items.length > 0 && (
            <div className="select-all">
              <label className="check-row">
                <input
                  type="checkbox"
                  checked={allOnPageSelected}
                  onChange={(e) => (e.target.checked ? selectAllOnPage() : clearSelection())}
                />
                全选本页 {items.length} 篇
              </label>
              <small className="muted">单次批量最多 100 篇</small>
            </div>
          )}
          <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
          {loading ? (
            <LoadingView text="正在查询论文…" />
          ) : items.length === 0 ? (
            <Empty
              title={filtered ? '没有匹配的论文' : '论文库还是空的'}
              description={filtered ? '调整关键词或筛选条件再试一次。' : '先导入一篇论文，之后即可在这里检索。'}
              action={<Link className="button secondary" to="/app/import"><Upload size={16} />导入论文</Link>}
            />
          ) : (
            <>
              {items.map((p) => (
                <PaperRow
                  key={p.id}
                  paper={p}
                  selectable
                  selected={selectedIds.has(p.id)}
                  onToggle={toggleOne}
                />
              ))}
              {lastPage > 1 && (
                <div className="pager">
                  <button className="button secondary" disabled={page <= 1} onClick={() => update({ page: String(page - 1) })}>上一页</button>
                  <span>{page} / {lastPage}</span>
                  <button className="button secondary" disabled={page >= lastPage} onClick={() => update({ page: String(page + 1) })}>下一页</button>
                </div>
              )}
            </>
          )}
        </div>
      </div>
      {bulkState && (
        <BatchTaxonomyModal
          open={bulkState.kind === 'tags' || bulkState.kind === 'categories'}
          kind={bulkState.kind === 'tags' ? 'tags' : 'categories'}
          items={
            bulkState.kind === 'tags'
              ? allTags.map((t) => ({ id: t.id, name: t.name, color: t.color }))
              : allCategories.flatMap((c) => [
                  { id: c.id, name: c.name },
                  ...((c.children ?? []).map((sc) => ({ id: sc.id, name: `${c.name} / ${sc.name}` }))),
                ])
          }
          onConfirm={(ids) => setBulkState({ kind: bulkState.kind, pendingIDs: ids })}
          onCancel={() => setBulkState(null)}
        />
      )}
      {bulkState && bulkState.kind !== 'tags' && bulkState.kind !== 'categories' && (
        <PasswordModal
          open
          title={
            bulkState.kind === 'delete' ? `将 ${selectedCount} 篇论文移入回收站`
              : bulkState.kind === 'favorite-on' ? `批量加收藏（${selectedCount} 篇）`
              : `批量取消收藏（${selectedCount} 篇）`
          }
          description={bulkState.kind === 'delete' ? '论文将移入回收站，保留期内可以恢复；到期后服务器会永久删除论文文件。请输入当前管理员密码以确认。' : '请输入当前管理员密码以确认。'}
          confirmLabel={bulkState.kind === 'delete' ? '确认移入回收站' : '确认执行'}
          busy={bulkBusy}
          onConfirm={(password) => runBulk(bulkState.kind, password)}
          onCancel={() => { setBulkState(null); setBatchError('') }}
        />
      )}
      {bulkState && (bulkState.kind === 'tags' || bulkState.kind === 'categories') && bulkState.pendingIDs && (
        <PasswordModal
          open
          title={bulkState.kind === 'tags' ? `批量打标签（${selectedCount} 篇）` : `批量分类（${selectedCount} 篇）`}
          description="替换模式：所选论文最终拥有上方勾选的标签/分类。其它将被移除。"
          confirmLabel="确认"
          busy={bulkBusy}
          onConfirm={(password) => runBulk(bulkState.kind, password, bulkState.pendingIDs)}
          onCancel={() => setBulkState(null)}
        />
      )}
    </Shell>
  )
}

const remainingLabel = (purgeAt: string) => {
  const expires = new Date(purgeAt).getTime()
  if (!Number.isFinite(expires)) return '清理时间待确认'
  const remaining = Math.ceil((expires - Date.now()) / 86400000)
  if (remaining <= 0) return '等待永久清理'
  if (remaining === 1) return '不足 1 天后清理'
  return `剩余 ${remaining} 天`
}

const TrashPaperRow = memo(function TrashPaperRow({ paper, busy, onRestore }: { paper: TrashPaper; busy: boolean; onRestore: (id: string) => void }) {
  const tags = paper.tags ?? []
  const cats = paper.categories ?? []
  return (
    <article className="paper-row trash-row">
      <div className="paper-icon trash-icon"><Trash2 size={18} /></div>
      <div className="paper-main">
        <strong>{paper.title}</strong>
        <span className="paper-byline">{authorLine(paper)}{paper.journal ? ` · ${paper.journal}` : ''}</span>
        <p className={paper.abstract?.trim() ? 'paper-abstract' : 'paper-abstract empty-copy'}>
          {paper.abstract?.trim() || '暂无摘要。'}
        </p>
        <div className="row-taxonomy" aria-label="论文分类与标签">
          <div className="taxonomy-group">
            <span className="taxonomy-label">分类</span>
            {cats.length > 0
              ? cats.map((category) => <span className="taxonomy-chip cat" key={`c-${category.id}`}>{category.name}</span>)
              : <span className="taxonomy-empty">未分类</span>}
          </div>
          <div className="taxonomy-group">
            <span className="taxonomy-label">标签</span>
            {tags.length > 0
              ? tags.map((tag) => <span className={`taxonomy-chip tag tag-${tag.color}`} key={`t-${tag.id}`}>#{tag.name}</span>)
              : <span className="taxonomy-empty">无标签</span>}
          </div>
        </div>
      </div>
      <div className="trash-actions">
        <span className="trash-deadline" title={`预计永久删除：${new Date(paper.purgeAt).toLocaleString()}`}>
          <Clock3 size={14} />{remainingLabel(paper.purgeAt)}
        </span>
        <button type="button" className="button secondary" disabled={busy} onClick={() => onRestore(paper.id)}>
          <RotateCcw size={15} />{busy ? '恢复中…' : '恢复'}
        </button>
      </div>
    </article>
  )
})

function Trash() {
  const [items, setItems] = useState<TrashPaper[]>([])
  const [total, setTotal] = useState(0)
  const [retentionDays, setRetentionDays] = useState(10)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [restoringID, setRestoringID] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = useCallback(async () => {
    setLoading(true)
    setError('')
    try {
      const query = `?page=${page}&pageSize=${PAGE_SIZE}`
      const response = await trashPapers(query)
      setItems(response.data.items ?? [])
      setTotal(response.data.total ?? 0)
      setRetentionDays(response.data.retentionDays ?? 10)
    } catch (e) {
      setError(errorMessage(e))
      setItems([])
    } finally {
      setLoading(false)
    }
  }, [page])

  useEffect(() => {
    void load()
  }, [load])

  const restore = useCallback(async (id: string) => {
    if (restoringID) return
    setRestoringID(id)
    setError('')
    setNotice('')
    try {
      await restoreTrashPaper(id)
      setNotice('论文已恢复，可在论文库中继续查看和编辑。')
      if (items.length === 1 && page > 1) setPage((value) => value - 1)
      else await load()
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setRestoringID('')
    }
  }, [items.length, load, page, restoringID])

  const lastPage = Math.max(1, Math.ceil(total / PAGE_SIZE))
  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">文件保护</p>
          <h1>回收站</h1>
          <p className="muted">共 {total} 篇。删除的论文保留 {retentionDays} 天，到期后服务器会自动永久删除对应文件。</p>
        </div>
        <Link className="button secondary" to="/app/papers"><BookOpen size={16} />返回论文库</Link>
      </div>
      <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
      <NoticeModal open={Boolean(notice)} variant="success" message={notice} onClose={() => setNotice('')} />
      <div className="panel trash-list">
        {loading ? (
          <LoadingView text="正在读取回收站…" />
        ) : items.length === 0 ? (
          <Empty title="回收站是空的" description="移入回收站的论文会在这里保留，期间可以随时恢复。" />
        ) : (
          <>
            {items.map((paper) => (
              <TrashPaperRow key={paper.id} paper={paper} busy={restoringID === paper.id} onRestore={restore} />
            ))}
            {lastPage > 1 && (
              <div className="pager">
                <button className="button secondary" disabled={page <= 1 || loading} onClick={() => setPage((value) => value - 1)}>上一页</button>
                <span>{page} / {lastPage}</span>
                <button className="button secondary" disabled={page >= lastPage || loading} onClick={() => setPage((value) => value + 1)}>下一页</button>
              </div>
            )}
          </>
        )}
      </div>
    </Shell>
  )
}

function PaperDetailPage() {
  const { id = '' } = useParams()
  const nav = useNavigate()
  const [data, setData] = useState<PaperDetail | null>(null)
  const [form, setForm] = useState({ title: '', abstract: '', doi: '', readingStatus: 'unread', authorsStr: '', journal: '', year: '' })
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const { data: allTags = [] } = useTags()
  const { data: allCategories = [] } = useCategories()
  const [selectedTagIDs, setSelectedTagIDs] = useState<number[]>([])
  const [selectedCategoryIDs, setSelectedCategoryIDs] = useState<number[]>([])
  const [missingDismissed, setMissingDismissed] = useState(false)

  const apply = (detail: PaperDetail) => {
    setData(detail)
    setForm({ 
      title: detail.title ?? '', 
      abstract: detail.abstract ?? '', 
      doi: detail.doi ?? '', 
      readingStatus: detail.readingStatus ?? 'unread',
      authorsStr: (detail.authors || []).map(a => typeof a === 'string' ? a : a.name).join(', '),
      journal: detail.journal ?? '',
      year: detail.year ? detail.year.toString() : ''
    })
    setSelectedTagIDs((detail.tags ?? []).map((t) => t.id))
    setSelectedCategoryIDs((detail.categories ?? []).map((c) => c.id))
  }

  useEffect(() => {
    let alive = true
    setLoading(true)
    Promise.all([
      fetchPaper(id),
    ])
      .then(([paper]) => {
        if (!alive) return
        apply(paper.data)
      })
      .catch((e) => alive && setError(errorMessage(e)))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [id])

  const flatCategories = useMemo(() => {
    const out: { id: number; name: string }[] = []
    const walk = (list: Category[] | null | undefined, prefix = '') => {
      if (!list) return
      list.forEach((c) => {
        if (!c || c.id == null) return
        out.push({ id: c.id, name: prefix ? `${prefix} / ${c.name}` : c.name })
        walk(c.children, prefix ? `${prefix} / ${c.name}` : c.name)
      })
    }
    walk(allCategories)
    return out
  }, [allCategories])

  const toggleTag = (id: number) => {
    setSelectedTagIDs((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
  }
  const toggleCategory = (id: number) => {
    setSelectedCategoryIDs((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]))
  }
  const saveTaxonomy = async () => {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await updatePaperTags(id, selectedTagIDs)
      await updatePaperCategories(id, selectedCategoryIDs)
      // 重新拉详情，确保计数更新
      const r = await fetchPaper(id)
      apply(r.data)
      setNotice('已更新标签和分类')
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const patch = async (payload: Record<string, unknown>, message: string) => {
    if (!data) return
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const r = await updatePaper(id, { version: data.version, ...payload })
      apply(r.data)
      setNotice(message)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  // 单篇删除走密码再认证 modal，不可直接 confirm 跳过。
  const [deleteOpen, setDeleteOpen] = useState(false)
  const destroyWithPassword = async (password: string) => {
    setBusy(true)
    try {
      await deletePaperWithPassword(id, password)
      setDeleteOpen(false)
      nav('/app/papers', { replace: true })
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
      setDeleteOpen(false)
    }
  }

  if (loading) return <Shell><LoadingView text="正在加载论文…" /></Shell>
  if (!data) {
    return (
      <Shell>
        <NoticeModal open={!missingDismissed} variant="error" message={error || '论文不存在'} onClose={() => { setError(''); setMissingDismissed(true) }} />
        <Link className="button secondary" to="/app/papers"><ArrowLeft size={16} />返回论文库</Link>
      </Shell>
    )
  }

  return (
    <Shell>
      <div className="page-head">
        <div>
          <Link className="back-link" to="/app/papers"><ArrowLeft size={15} />返回论文库</Link>
          <h1>{data.title}</h1>
          <p className="muted">{authorLine(data)}{data.journal ? ` · ${data.journal}` : ''} · 解析状态：{data.parseStatus ?? 'queued'}</p>
        </div>
        <div className="head-actions">
          <button className={data.isFavorite ? 'button primary' : 'button secondary'} disabled={busy} onClick={() => patch({ isFavorite: !data.isFavorite }, data.isFavorite ? '已取消收藏' : '已加入收藏')}>
            <Star size={16} />{data.isFavorite ? '已收藏' : '收藏'}
          </button>
          {data.file?.previewable && <a className="button secondary" href={fileUrl(id, 'preview')} target="_blank" rel="noreferrer"><Eye size={16} />预览</a>}
          {data.file && <a className="button secondary" href={fileUrl(id, 'download')}><Download size={16} />下载</a>}
        </div>
      </div>
      <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
      <NoticeModal open={Boolean(notice)} variant="success" message={notice} onClose={() => setNotice('')} />
      <div className="content-grid">
        <div className="panel">
          <div className="panel-head"><h2>元数据</h2></div>
          <label className="field">标题<input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} /></label>
          <label className="field">作者 (逗号分隔)<input value={form.authorsStr} onChange={(e) => setForm({ ...form, authorsStr: e.target.value })} placeholder="John Doe, Jane Smith" /></label>
          <label className="field">期刊 / 会议<input value={form.journal} onChange={(e) => setForm({ ...form, journal: e.target.value })} placeholder="Nature" /></label>
          <label className="field">发表年份<input type="number" value={form.year} onChange={(e) => setForm({ ...form, year: e.target.value })} placeholder="2023" /></label>
          <label className="field">DOI<input value={form.doi} onChange={(e) => setForm({ ...form, doi: e.target.value })} placeholder="10.xxxx/xxxxx" /></label>
          <label className="field">
            阅读状态
            <select value={form.readingStatus} onChange={(e) => setForm({ ...form, readingStatus: e.target.value })}>
              <option value="unread">未读</option>
              <option value="reading">阅读中</option>
              <option value="read">已读</option>
            </select>
          </label>
          <label className="field">摘要 / 笔记<textarea rows={9} value={form.abstract} onChange={(e) => setForm({ ...form, abstract: e.target.value })} placeholder="粘贴摘要，或记录自己的阅读笔记" /></label>
          <div className="import-actions">
            <button className="button primary" disabled={busy} onClick={() => patch({ 
              title: form.title.trim() || data.title, 
              authors: form.authorsStr.split(',').map(s => s.trim()).filter(Boolean),
              journal: form.journal.trim(),
              year: parseInt(form.year) || null,
              doi: form.doi.trim(), 
              abstract: form.abstract, 
              readingStatus: form.readingStatus 
            }, '已保存')}>
              <Save size={16} />{busy ? '保存中…' : '保存修改'}
            </button>
            <button className="button secondary" disabled={busy} onClick={async () => {
              if (busy) return;
              setBusy(true); setError(''); setNotice('');
              try {
                const res = await reextractPaper(id)
                setNotice('重新识别完成');
                if (res) {
                  const toUpdate: Partial<PaperDetail> = {}
                  if (res.Title) toUpdate.title = res.Title
                  if (res.Year) toUpdate.year = res.Year
                  if (res.Subject) toUpdate.journal = res.Subject
                  if (res.Authors && res.Authors.length) toUpdate.authors = res.Authors.map(name => ({ name }))
                  if (Object.keys(toUpdate).length > 0) apply({ ...data, ...toUpdate })
                }
              } catch(e) {
                setError(errorMessage(e));
              } finally {
                setBusy(false);
              }
            }}><BookOpen size={16} />重新识别</button>
            <button className="button secondary" disabled={busy} onClick={() => setDeleteOpen(true)}><Trash2 size={16} />移入回收站</button>
          </div>
          <div className="taxonomy-editor">
            <h3>分类</h3>
            {flatCategories.length === 0 ? (
              <p className="muted">还没有分类，可到「分类与标签」页面创建。</p>
            ) : (
              <div className="chip-grid">
                {flatCategories.map((c) => (
                  <label key={c.id} className={`chip-pick${selectedCategoryIDs.includes(c.id) ? ' on' : ''}`}>
                    <input type="checkbox" checked={selectedCategoryIDs.includes(c.id)} onChange={() => toggleCategory(c.id)} />
                    {c.name}
                  </label>
                ))}
              </div>
            )}
            <h3>标签</h3>
            {allTags.length === 0 ? (
              <p className="muted">还没有标签，可到「分类与标签」页面创建。</p>
            ) : (
              <div className="chip-grid">
                {allTags.map((t) => (
                  <label key={t.id} className={`chip-pick chip-${t.color}${selectedTagIDs.includes(t.id) ? ' on' : ''}`}>
                    <input type="checkbox" checked={selectedTagIDs.includes(t.id)} onChange={() => toggleTag(t.id)} />
                    #{t.name}
                  </label>
                ))}
              </div>
            )}
            <button className="button primary" disabled={busy} onClick={saveTaxonomy}>
              <Save size={16} />{busy ? '保存中…' : '保存分类和标签'}
            </button>
          </div>
        </div>
        <div className="panel">
          <div className="panel-head"><h2>文件信息</h2></div>
          {data.file ? (
            <dl className="meta-list">
              <div><dt>原始文件名</dt><dd>{data.file.originalName}</dd></div>
              <div><dt>大小</dt><dd>{(data.file.sizeBytes / 1048576).toFixed(2)} MB</dd></div>
              <div><dt>类型</dt><dd>{data.file.mediaType || '未知'}</dd></div>
              <div><dt>导入时间</dt><dd>{data.addedAt ? new Date(data.addedAt).toLocaleString() : '—'}</dd></div>
              <div><dt>最后修改</dt><dd>{data.updatedAt ? new Date(data.updatedAt).toLocaleString() : '—'}</dd></div>
              <div><dt>版本号</dt><dd>{data.version}</dd></div>
            </dl>
          ) : (
            <p className="muted">这条记录没有关联文件。</p>
          )}
        </div>
      </div>
      <PasswordModal
        open={deleteOpen}
        title="将这篇论文移入回收站"
        description="移入后在回收站保留期内可以恢复；到期未恢复时，服务器会永久删除论文文件。请输入当前管理员密码以确认。"
        confirmLabel="确认移入回收站"
        busy={busy}
        onConfirm={destroyWithPassword}
        onCancel={() => setDeleteOpen(false)}
      />
    </Shell>
  )
}

function Import() {
  const [files, setFiles] = useState<File[]>([])
  const [results, setResults] = useState<UploadResult[]>([])
  const [previewResults, setPreviewResults] = useState<{fileName: string, meta: {Title?: string, Authors?: string[], Year?: number, Subject?: string}}[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const [previewing, setPreviewing] = useState(false)
  const [isDragging, setIsDragging] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)
  const dragDepthRef = useRef(0)

  const pick = (list: FileList | null) => {
    setError('')
    setNotice('')
    setResults([])
    const chosen = Array.from(list ?? [])
    const bad = chosen.find((f) => !ALLOWED_EXTENSIONS.some((ext) => f.name.toLowerCase().endsWith(ext)))
    if (bad) {
      setError(`不支持的文件类型：${bad.name}（仅支持 ${ALLOWED_EXTENSIONS.join('、')}）`)
      return
    }
    const tooBig = chosen.find((f) => f.size > MAX_UPLOAD_BYTES)
    if (tooBig) {
      setError(`${tooBig.name} 超过 200 MB 上限`)
      return
    }
    setFiles(chosen)
    setPreviewResults([])
  }

  const handleDragEnter = (event: DragEvent<HTMLLabelElement>) => {
    event.preventDefault()
    event.stopPropagation()
    if (!event.dataTransfer.types.includes('Files')) return
    dragDepthRef.current += 1
    setIsDragging(true)
  }

  const handleDragOver = (event: DragEvent<HTMLLabelElement>) => {
    event.preventDefault()
    event.stopPropagation()
    if (event.dataTransfer.types.includes('Files')) event.dataTransfer.dropEffect = 'copy'
  }

  const handleDragLeave = (event: DragEvent<HTMLLabelElement>) => {
    event.preventDefault()
    event.stopPropagation()
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1)
    if (dragDepthRef.current === 0) setIsDragging(false)
  }

  const handleDrop = (event: DragEvent<HTMLLabelElement>) => {
    event.preventDefault()
    event.stopPropagation()
    dragDepthRef.current = 0
    setIsDragging(false)
    if (event.dataTransfer.files.length > 0) pick(event.dataTransfer.files)
  }

  const preview = async () => {
    if (!files.length) return
    setPreviewing(true)
    setError('')
    try {
      const r = await extractPapersPreview(files)
      setPreviewResults(r)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setPreviewing(false)
    }
  }

  const submit = async () => {
    if (!files.length) return
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const r = await uploadPapers(files)
      setResults(r.data.items ?? [])
      setNotice(`成功导入 ${r.data.accepted} 个文件${r.data.rejected ? `，${r.data.rejected} 个被拒绝` : ''}。`)
      setFiles([])
      if (inputRef.current) inputRef.current.value = ''
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">安全导入</p>
          <h1>导入论文</h1>
          <p className="muted">支持 PDF、DOC、DOCX、ODT、LaTeX 和 TXT，单文件最大 200 MB。</p>
        </div>
      </div>
      <div className="import-panel">
        <label
          className={`dropzone${isDragging ? ' dragging' : ''}`}
          onDragEnter={handleDragEnter}
          onDragOver={handleDragOver}
          onDragLeave={handleDragLeave}
          onDrop={handleDrop}
        >
          <Upload size={28} />
          <strong>{isDragging ? '释放文件以添加' : '点击选择文件，或把文件拖到这里'}</strong>
          <span>服务端会校验扩展名、大小和 PDF 文件头。</span>
          <input
            ref={inputRef}
            type="file"
            multiple
            accept={ALLOWED_EXTENSIONS.join(',')}
            onChange={(event) => {
              pick(event.target.files)
              event.target.value = ''
            }}
          />
        </label>
        <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
        {files.length > 0 && (
          <div className="file-list">
            {files.map((f) => (
              <div className="file-item" key={f.name}>
                <BookOpen size={17} /><span>{f.name}</span><small>{(f.size / 1048576).toFixed(1)} MB</small>
              </div>
            ))}
          </div>
        )}
        <div className="import-actions">
          <button className="button primary" disabled={!files.length || busy || previewing} onClick={submit}><Upload size={17} />{busy ? '正在上传…' : '开始导入'}</button>
          {files.length > 0 && !busy && !previewing && <button className="button secondary" onClick={preview}>试识别</button>}
          {files.length > 0 && !busy && !previewing && <button className="button secondary" onClick={() => { setFiles([]); setPreviewResults([]); if (inputRef.current) inputRef.current.value = '' }}>清空</button>}
          <NoticeModal open={Boolean(notice)} variant="success" message={notice} onClose={() => setNotice('')} />
          {results.length > 0 && <Link to="/app/papers">前往论文库 →</Link>}
        </div>
        {previewResults.length > 0 && (
          <div className="file-list">
            <h4>识别预览结果：</h4>
            {previewResults.map((r, i) => (
              <div className="file-item" key={i} style={{ flexDirection: 'column', alignItems: 'flex-start', gap: 4 }}>
                <div><BookOpen size={17} style={{ verticalAlign: 'middle', marginRight: 8 }}/><strong>{r.fileName}</strong></div>
                {r.meta && Object.keys(r.meta).length > 0 ? (
                  <div style={{ fontSize: '13px', color: '#666', paddingLeft: 24 }}>
                    {r.meta.Title && <div>标题：{r.meta.Title}</div>}
                    {r.meta.Authors && r.meta.Authors.length > 0 && <div>作者：{r.meta.Authors.join('; ')}</div>}
                    {r.meta.Year && <div>年份：{r.meta.Year}</div>}
                    {r.meta.Subject && <div>期刊/主题：{r.meta.Subject}</div>}
                  </div>
                ) : <div style={{ fontSize: '13px', color: '#999', paddingLeft: 24 }}>未能识别出元数据</div>}
              </div>
            ))}
          </div>
        )}
        {results.length > 0 && (
          <div className="file-list">
            {results.map((r) => (
              <div className="file-item" key={`${r.filename}-${r.status}`}>
                <BookOpen size={17} />
                <span>{r.filename}</span>
                <small className={r.status === 'rejected' ? 'danger-text' : 'success-text'}>{r.status === 'rejected' ? `失败：${r.reason ?? '未知原因'}` : '已入库'}</small>
              </div>
            ))}
          </div>
        )}
      </div>
    </Shell>
  )
}

function Taxonomy() {
  const queryClient = useQueryClient()
  const [tab, setTab] = useState<'tags' | 'categories'>('categories')
  const { data: tags = [], refetch: refetchTags } = useTags()
  const { data: categories = [], refetch: refetchCategories } = useCategories()
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [newTag, setNewTag] = useState({ name: '', color: 'teal' })
  const [newCat, setNewCat] = useState({ name: '', parentId: '' as string })
  const [busy, setBusy] = useState(false)
  const [expanded, setExpanded] = useState<Record<number, boolean>>({})
  const [pendingDelete, setPendingDelete] = useState<null | { kind: 'category' | 'tag'; id: number; name: string }>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)

  const reload = useCallback(async () => {
    const [t, c] = await Promise.all([refetchTags(), refetchCategories()])
    return t.error || c.error
  }, [refetchTags, refetchCategories])

  // 成功提示 3 秒后自动消失，避免堆叠；切换 Tab 时也会被清空。
  useEffect(() => {
    if (!notice) return
    const t = setTimeout(() => setNotice(''), 3000)
    return () => clearTimeout(t)
  }, [notice])

  const submitTag = async () => {
    if (!newTag.name.trim()) return setError('请输入标签名')
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await createTag(newTag.name.trim(), newTag.color)
      setNewTag({ name: '', color: 'teal' })
      // 列表刷新失败也不能影响本次创建的成功提示；reload 内部已经用 allSettled 隔离。
      const refreshErr = await reload()
      if (refreshErr) {
        // 仅作为参考提示，不覆盖创建成功的状态条
        // eslint-disable-next-line no-console
        console.warn('刷新标签列表失败', refreshErr)
      }
      setNotice(`已创建标签：${newTag.name.trim()}`)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const submitCategory = async () => {
    if (!newCat.name.trim()) return setError('请输入分类名')
    setBusy(true)
    setError('')
    setNotice('')
    try {
      await createCategory({
        name: newCat.name.trim(),
        parentId: newCat.parentId ? Number(newCat.parentId) : null,
        sortOrder: 0,
      })
      setNewCat({ name: '', parentId: '' })
      const refreshErr = await reload()
      if (refreshErr) {
        // eslint-disable-next-line no-console
        console.warn('刷新分类列表失败', refreshErr)
      }
      setNotice(`已创建分类：${newCat.name.trim()}`)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const confirmDeleteTaxonomy = async () => {
    if (!pendingDelete) return
    setDeleteBusy(true)
    setError('')
    setNotice('')
    try {
      if (pendingDelete.kind === 'category') {
        await deleteCategory(pendingDelete.id)
        queryClient.setQueryData<Category[]>(['categories'], (current) => removeCategoryFromTree(current ?? [], pendingDelete.id))
        const refresh = await refetchCategories()
        if (refresh.error) console.warn('刷新分类失败', refresh.error)
        setNotice(`已删除分类：${pendingDelete.name}`)
      } else {
        await deleteTag(pendingDelete.id)
        queryClient.setQueryData<Tag[]>(['tags'], (current) => (current ?? []).filter((item) => item.id !== pendingDelete.id))
        const refresh = await refetchTags()
        if (refresh.error) console.warn('刷新标签失败', refresh.error)
        setNotice(`已删除标签：${pendingDelete.name}`)
      }
      setPendingDelete(null)
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setDeleteBusy(false)
    }
  }

  const renderCategoryNode = (c: Category, depth = 0): ReactNode => {
    const isOpen = expanded[c.id] ?? true
    const remove = (e: React.MouseEvent) => {
      e.preventDefault()
      e.stopPropagation()
      setPendingDelete({ kind: 'category', id: c.id, name: c.name })
    }
    return (
      <div key={c.id} className="cat-node" style={{ paddingLeft: depth * 14 }}>
        <div className="cat-row">
          <button className="icon-button" type="button" aria-label={isOpen ? '收起' : '展开'} onClick={(e) => { e.stopPropagation(); setExpanded((m) => ({ ...m, [c.id]: !(m[c.id] ?? true) })) }}>
            {(c.children ?? []).length > 0 ? (isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />) : <span style={{ width: 14 }} />}
          </button>
          <Link to={`/app/papers?category=${c.id}`}>{c.name}</Link>
          <small>{c.paperCount ?? 0} 篇</small>
          <button className="icon-button danger" type="button" aria-label={`删除分类 ${c.name}`} onClick={remove}><Trash2 size={14} /></button>
        </div>
        {isOpen && (c.children ?? []).map((sc) => renderCategoryNode(sc, depth + 1))}
      </div>
    )
  }

  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">知识组织</p>
          <h1>分类与标签</h1>
          <p className="muted">分类用于分层组织论文，标签用于横向标注主题。删除分类会同时移除其子分类与对应论文关联。</p>
        </div>
      </div>
      <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
      <NoticeModal open={Boolean(notice)} variant="success" message={notice} onClose={() => setNotice('')} />
      <div className="tab-bar">
        <button className={tab === 'categories' ? 'tab active' : 'tab'} onClick={() => { setTab('categories'); setError(''); setNotice('') }}>分类树</button>
        <button className={tab === 'tags' ? 'tab active' : 'tab'} onClick={() => { setTab('tags'); setError(''); setNotice('') }}>标签</button>
      </div>
      {tab === 'categories' && (
        <div className="taxonomy-grid single">
          <div className="panel">
            <div className="panel-head">
              <h2>分类</h2>
              <small>共 {categories.reduce((sum, c) => sum + 1 + ((c.children ?? []).length), 0)} 项</small>
            </div>
            {categories.length === 0 ? (
              <p className="muted">还没有分类。下方创建第一个根分类。</p>
            ) : (
              <div className="cat-tree">{categories.map((c) => renderCategoryNode(c))}</div>
            )}
          </div>
          <div className="panel">
            <div className="panel-head"><h2>新建分类</h2></div>
            <label className="field">分类名<input value={newCat.name} onChange={(e) => setNewCat({ ...newCat, name: e.target.value })} placeholder="例如：机器学习" /></label>
            <label className="field">
              父分类
              <select value={newCat.parentId} onChange={(e) => setNewCat({ ...newCat, parentId: e.target.value })}>
                <option value="">无（顶级分类）</option>
                {categories.map((c) => (
                  <option key={c.id} value={String(c.id)}>{c.name}</option>
                ))}
              </select>
            </label>
            <div className="import-actions">
              <button className="button primary" onClick={submitCategory} disabled={busy}><FolderPlus size={16} />{busy ? '创建中…' : '创建分类'}</button>
            </div>
            <div className="filter-note">同名分类在同一个父分类下会自动合并，已合并的分类下论文计数会汇总。</div>
          </div>
        </div>
      )}
      {tab === 'tags' && (
        <div className="taxonomy-grid single">
          <div className="panel">
            <div className="panel-head"><h2>现有标签</h2><small>共 {tags.length} 个</small></div>
            {tags.length === 0 ? (
              <p className="muted">还没有标签。右侧创建一个吧。</p>
            ) : (
              <div className="tag-cloud">
                {[...tags].sort((a, b) => (b.usageCount ?? 0) - (a.usageCount ?? 0) || a.name.localeCompare(b.name)).map((t) => (
                  <span className={`tag tag-${t.color}`} key={t.id}>
                    <Link to={`/app/papers?tag=${t.id}`}>#{t.name} <small>{t.usageCount ?? 0}</small></Link>
                    <button
                      type="button"
                      className="tag-remove"
                      aria-label={`删除标签 ${t.name}`}
                      onClick={async (e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        setPendingDelete({ kind: 'tag', id: t.id, name: t.name })
                      }}
                    >
                      <X size={11} />
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>
          <div className="panel">
            <div className="panel-head"><h2>新建标签</h2></div>
            <label className="field">标签名<input value={newTag.name} onChange={(e) => setNewTag({ ...newTag, name: e.target.value })} placeholder="例如：survey" maxLength={40} /></label>
            <label className="field">颜色<div className="color-row">{TAG_COLORS.map((c) => <button key={c.value} className={`color-swatch tag-${c.value}${newTag.color === c.value ? ' on' : ''}`} type="button" aria-label={c.label} onClick={() => setNewTag({ ...newTag, color: c.value })} />)}</div></label>
            <div className="import-actions">
              <button className="button primary" onClick={submitTag} disabled={busy}><TagIcon size={16} />{busy ? '创建中…' : '创建标签'}</button>
            </div>
            <div className="filter-note">同名标签会自动合并、保留最新颜色。可在论文详情里多选打标。</div>
          </div>
        </div>
      )}
      <ConfirmModal
        open={Boolean(pendingDelete)}
        title={pendingDelete?.kind === 'category' ? `删除分类「${pendingDelete.name}」` : `删除标签「${pendingDelete?.name ?? ''}」`}
        description={pendingDelete?.kind === 'category' ? '会同时移除其子分类与已关联论文的绑定。此操作不可直接撤销。' : '所有论文的该标签关联都会被移除。此操作不可直接撤销。'}
        confirmLabel="确认删除"
        danger
        busy={deleteBusy}
        onConfirm={confirmDeleteTaxonomy}
        onCancel={() => setPendingDelete(null)}
      />
    </Shell>
  )
}

function Security() {
  const [account, setAccount] = useState<{ email: string; displayName: string; createdAt: string } | null>(null)
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    me()
      .then((r) => setAccount(r.data))
      .catch((e) => setError(errorMessage(e)))
  }, [])

  const submit = async () => {
    setError('')
    setNotice('')
    if (next.length < 12) return setError('新密码至少 12 位')
    if (next !== confirm) return setError('两次输入的新密码不一致')
    if (next === current) return setError('新密码不能与当前密码相同')
    setBusy(true)
    try {
      await changePassword(current, next)
      setNotice('密码已更新，所有会话已退出，即将返回登录页…')
      setTimeout(() => {
        window.location.href = '/auth/login'
      }, 1500)
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
    }
  }

  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">账号</p>
          <h1>安全设置</h1>
          <p className="muted">修改密码后所有已登录设备都会被强制退出。</p>
        </div>
      </div>
      <div className="content-grid">
        <div className="panel">
          <div className="panel-head"><h2>修改密码</h2></div>
          <NoticeModal open={Boolean(error)} variant="error" message={error} onClose={() => setError('')} />
          <NoticeModal open={Boolean(notice)} variant="success" message={notice} onClose={() => setNotice('')} />
          <form
            onSubmit={(e) => {
              e.preventDefault()
              void submit()
            }}
          >
            <label className="field">当前密码<input type="password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" /></label>
            <label className="field">新密码<input type="password" value={next} onChange={(e) => setNext(e.target.value)} placeholder="至少 12 位" autoComplete="new-password" /></label>
            <label className="field">确认新密码<input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" /></label>
            <button className="button primary" type="submit" disabled={busy || !current || !next}>{busy ? '提交中…' : '更新密码'}</button>
          </form>
        </div>
        <div className="panel">
          <div className="panel-head"><h2>账号信息</h2></div>
          <dl className="meta-list">
            <div><dt>登录邮箱</dt><dd>{account?.email ?? '—'}</dd></div>
            <div><dt>显示名称</dt><dd>{account?.displayName ?? '—'}</dd></div>
            <div><dt>创建时间</dt><dd>{account?.createdAt ? new Date(account.createdAt).toLocaleString() : '—'}</dd></div>
          </dl>
          <div className="filter-note">
            登录失败次数过多会临时锁定账号；会话有效期由 .env 中的 SESSION_TTL_SECONDS 控制。忘记密码时可清空数据库 admins 表后重新运行 /setup。
          </div>
        </div>
      </div>
    </Shell>
  )
}

function PreviewDashboard() {
  const [notice, setNotice] = useState<{ variant: NoticeVariant; message: string } | null>(null)
  const stats = [
    { label: '论文总数', value: '1,284', unit: '篇', icon: BookOpen, tone: 'blue' },
    { label: '近 30 天导入', value: '76', unit: '篇', icon: TrendingUp, tone: 'green' },
    { label: '待阅读', value: '18', unit: '篇', icon: Clock3, tone: 'amber' },
    { label: '存储占用', value: '3.42', unit: 'GB', icon: Database, tone: 'rose' },
  ]

  return (
    <div className="preview-mode">
      <Shell>
        <div className="page-head">
          <div>
            <p className="eyebrow">研究工作台</p>
            <h1>概览</h1>
            <p className="muted">快速了解知识库的积累与下一步阅读。</p>
          </div>
          <button className="button primary" type="button" onClick={() => setNotice({ variant: 'success', message: '预览模式：导入入口工作正常。' })}><Upload size={17} />导入论文</button>
        </div>

        <section className="stat-grid">
          {stats.map(({ label, value, unit, icon: Icon, tone }) => (
            <div className={`stat-card tone-${tone}`} key={label}>
              <div className="stat-card-head"><span>{label}</span><span className="stat-icon"><Icon size={19} /></span></div>
              <div><strong>{value}</strong><small>{unit}</small></div>
            </div>
          ))}
        </section>

        <section className="dashboard-banner">
          <div className="dashboard-banner-icon"><TrendingUp size={22} /></div>
          <div><h2>继续构建你的研究脉络</h2><p>集中整理新论文，再通过阅读状态、分类和标签保持资料库可检索。</p></div>
          <div className="dashboard-banner-actions">
            <a href="#import" onClick={(event) => event.preventDefault()}>导入论文</a>
            <a href="#papers" onClick={(event) => event.preventDefault()}>浏览论文库</a>
          </div>
        </section>

        <section className="content-grid">
          <div className="panel preview-recent-panel">
            <div className="panel-head"><h2>最近更新</h2><a href="#all" onClick={(event) => event.preventDefault()}>查看全部</a></div>
            <article className="paper-row">
              <div className="paper-icon"><BookOpen size={18} /></div>
              <div className="paper-main">
                <strong>Attention Is All You Need: A Structured Review of Transformer Research</strong>
                <span className="paper-byline">A. Researcher, B. Scholar · 2026 年 · Journal of AI Research</span>
                <p className="paper-abstract">从模型结构、训练策略与实际应用三个维度整理近期研究进展，并给出可复用的知识脉络。</p>
                <div className="row-taxonomy">
                  <div className="taxonomy-group"><span className="taxonomy-label">分类</span><span className="taxonomy-chip cat">人工智能</span></div>
                  <div className="taxonomy-group"><span className="taxonomy-label">标签</span><span className="taxonomy-chip tag tag-teal">#Transformer</span><span className="taxonomy-chip tag tag-slate">#综述</span></div>
                </div>
              </div>
              <span className="row-status">阅读中</span>
            </article>
            <article className="paper-row">
              <div className="paper-icon"><Database size={18} /></div>
              <div className="paper-main">
                <strong>Building Reliable Local-First Research Knowledge Bases</strong>
                <span className="paper-byline">C. Author · 2025 年 · Data Systems Review</span>
                <p className="paper-abstract">讨论本地优先的数据组织、全文检索、元数据一致性和安全边界。</p>
                <div className="row-taxonomy">
                  <div className="taxonomy-group"><span className="taxonomy-label">分类</span><span className="taxonomy-chip cat">知识管理</span></div>
                  <div className="taxonomy-group"><span className="taxonomy-label">标签</span><span className="taxonomy-chip tag tag-amber">#Local-first</span></div>
                </div>
              </div>
              <span className="row-status">未读</span>
            </article>
          </div>

          <div className="panel">
            <div className="panel-head"><h2>快捷入口</h2></div>
            <div className="quick-grid">
              <a href="#reading" onClick={(event) => event.preventDefault()}><BookOpen size={19} /><span>继续阅读</span><small>查看未读论文</small></a>
              <a href="#favorites" onClick={(event) => event.preventDefault()}><Star size={19} /><span>我的收藏</span><small>标记为重点的论文</small></a>
              <a href="#taxonomy" onClick={(event) => event.preventDefault()}><Tags size={19} /><span>分类浏览</span><small>按年份与期刊统计</small></a>
              <a href="#security" onClick={(event) => event.preventDefault()}><Settings2 size={19} /><span>安全设置</span><small>账号与密码</small></a>
            </div>
          </div>
        </section>

        <NoticeModal open={Boolean(notice)} variant={notice?.variant} message={notice?.message ?? ''} onClose={() => setNotice(null)} />
      </Shell>
    </div>
  )
}

function ShowcasePage() {
  const [notice, setNotice] = useState<{ variant: NoticeVariant; message: string } | null>(null)

  return (
    <div className="showcase-page">
      <header className="showcase-topbar">
        <div className="brand">
          <span className="brand-mark">PA</span>
          <span className="brand-copy"><strong>Paper Atlas</strong><small>Interface preview</small></span>
        </div>
        <ThemeToggle />
      </header>

      <main className="showcase-main">
        <section className="showcase-intro">
          <div>
            <p className="eyebrow">视觉系统</p>
            <h1>界面效果预览</h1>
            <p className="muted">黑白基调、克制的青绿强调，以及清晰的交互反馈。</p>
          </div>
          <div className="showcase-actions">
            <button className="button primary" type="button" onClick={() => setNotice({ variant: 'success', message: '论文信息已保存到知识库。' })}><Save size={16} />保存论文</button>
            <button className="button secondary" type="button" onClick={() => setNotice({ variant: 'info', message: '同步任务已经加入后台队列。' })}><Download size={16} />同步资料</button>
            <button className="button danger" type="button" onClick={() => setNotice({ variant: 'warning', message: '此操作需要进一步确认。' })}><Trash2 size={16} />删除记录</button>
          </div>
        </section>

        <section className="showcase-band showcase-demo-grid">
          <div className="showcase-search-stage">
            <div className="showcase-section-head">
              <p className="eyebrow">资料检索</p>
              <h2>快速定位研究内容</h2>
            </div>
            <div className="search-field search-inputbox showcase-search">
              <Search className="search-leading-icon" size={18} />
              <input aria-label="预览搜索框" placeholder=" " />
              <span className="search-label">搜索标题、作者、DOI、期刊…</span>
              <span className="search-fill" aria-hidden="true" />
            </div>
            <div className="showcase-filter-row">
              <span className="tag tag-teal">机器学习</span>
              <span className="tag tag-slate">2026</span>
              <span className="tag tag-amber">待读</span>
            </div>
          </div>
          <div className="showcase-loader-stage">
            <LoadingView text="正在同步知识库…" />
          </div>
        </section>

        <section className="showcase-band">
          <div className="showcase-section-head">
            <p className="eyebrow">知识概览</p>
            <h2>研究数据</h2>
          </div>
          <div className="showcase-card-grid">
            <article className="stat-card">
              <div className="stat-card-head"><span>全部论文</span><BookOpen size={18} /></div>
              <strong>1,284</strong><small>篇</small>
            </article>
            <article className="stat-card tone-green">
              <div className="stat-card-head"><span>本周新增</span><TrendingUp size={18} /></div>
              <strong>36</strong><small>篇</small>
            </article>
            <article className="stat-card tone-amber">
              <div className="stat-card-head"><span>待读资料</span><Clock3 size={18} /></div>
              <strong>18</strong><small>篇</small>
            </article>
          </div>
        </section>

        <section className="showcase-band">
          <div className="showcase-section-head">
            <p className="eyebrow">最近收录</p>
            <h2>论文卡片</h2>
          </div>
          <div className="showcase-paper-list">
            <article className="paper-row">
              <div className="paper-icon"><BookOpen size={18} /></div>
              <div className="paper-main">
                <strong>Attention Is All You Need: A Structured Review of Transformer Research</strong>
                <span>A. Researcher, B. Scholar · 2026 年</span>
                <p className="paper-abstract">从模型结构、训练策略与实际应用三个维度整理近期研究进展，并给出可复用的知识脉络。</p>
              </div>
              <span className="row-status">阅读中</span>
            </article>
            <article className="paper-row">
              <div className="paper-icon"><Database size={18} /></div>
              <div className="paper-main">
                <strong>Building Reliable Local-First Research Knowledge Bases</strong>
                <span>C. Author · 2025 年</span>
                <p className="paper-abstract">讨论本地优先的数据组织、全文检索、元数据一致性和安全边界。</p>
              </div>
              <span className="row-status">未读</span>
            </article>
          </div>
        </section>
      </main>

      <NoticeModal
        open={Boolean(notice)}
        variant={notice?.variant}
        message={notice?.message ?? ''}
        onClose={() => setNotice(null)}
      />
    </div>
  )
}

export default function App() {
  const [boot, setBoot] = useState<{ initialized: boolean; authed: boolean } | null>(null)

  useEffect(() => {
    let alive = true
    // 两个请求都完成后再决定路由，避免先跳登录页再跳回来的闪烁。
    Promise.all([
      setupStatus()
        .then((r) => r.data.initialized)
        .catch(() => true),
      me(true)
        .then(() => true)
        .catch(() => false),
    ]).then(([initialized, authed]) => alive && setBoot({ initialized, authed }))
    return () => {
      alive = false
    }
  }, [])

  if (import.meta.env.DEV && window.location.pathname === '/showcase') return <ShowcasePage />
  if (import.meta.env.DEV && window.location.pathname === '/preview/dashboard') return <PreviewDashboard />

  if (!boot) return <div className="splash"><LoadingView text="正在连接知识库…" /></div>
  const { initialized, authed } = boot
  const guard = (element: ReactNode) => (authed ? element : <Navigate to="/auth/login" replace />)
  const fallback = initialized ? (authed ? '/app/dashboard' : '/auth/login') : '/setup'

  return (
    <PageErrorBoundary>
      <Routes>
        <Route path="/setup" element={initialized ? <Navigate to="/auth/login" replace /> : <Setup />} />
        <Route path="/auth/login" element={authed ? <Navigate to="/app/dashboard" replace /> : <Login />} />
        <Route path="/app/dashboard" element={guard(<Dashboard />)} />
        <Route path="/app/papers" element={guard(<Papers />)} />
        <Route path="/app/papers/:id" element={guard(<PaperDetailPage />)} />
        <Route path="/app/trash" element={guard(<Trash />)} />
        <Route path="/app/import" element={guard(<Import />)} />
        <Route path="/app/taxonomy" element={guard(<Taxonomy />)} />
        <Route path="/app/settings/security" element={guard(<Security />)} />
        <Route path="*" element={<Navigate to={fallback} replace />} />
      </Routes>
    </PageErrorBoundary>
  )
}
