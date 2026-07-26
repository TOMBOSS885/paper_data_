import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Link, Navigate, Route, Routes, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ArrowLeft, BookOpen, Download, Eye, LayoutDashboard, LogOut, Menu, Save, Search, Settings2, Star, Tags, Trash2, Upload, X } from 'lucide-react'
import {
  ApiError,
  createAdmin,
  changePassword,
  dashboard,
  facets,
  fileUrl,
  login,
  logout,
  me,
  paper as fetchPaper,
  papers as fetchPapers,
  removePaper,
  setupStatus,
  updatePaper,
  uploadPapers,
} from './lib/api'
import type { Facets, Paper, PaperDetail, UploadResult } from './lib/api'

const PAGE_SIZE = 20
const ALLOWED_EXTENSIONS = ['.pdf', '.doc', '.docx', '.odt', '.tex', '.txt']
const MAX_UPLOAD_BYTES = 200 * 1024 * 1024
const STATUS_LABELS: Record<string, string> = { unread: '未读', reading: '阅读中', read: '已读' }

const errorMessage = (e: unknown) => (e instanceof ApiError || e instanceof Error ? e.message : '操作失败，请重试')
const statusLabel = (v?: string) => STATUS_LABELS[v ?? 'unread'] ?? '未读'
const authorLine = (p: Paper) => {
  const names = (p.authors ?? []).map((a) => a?.name).filter(Boolean)
  return `${names.length ? names.join(', ') : '作者信息待补充'} · ${p.year ? `${p.year} 年` : '年份未知'}`
}

function Shell({ children }: { children: ReactNode }) {
  const nav = useNavigate()
  const loc = useLocation()
  const [open, setOpen] = useState(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const links = [
    { to: '/app/dashboard', label: '概览', icon: LayoutDashboard },
    { to: '/app/papers', label: '论文库', icon: BookOpen },
    { to: '/app/import', label: '导入论文', icon: Upload },
    { to: '/app/taxonomy', label: '分类浏览', icon: Tags },
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
      <aside className={open ? 'sidebar open' : 'sidebar'}>
        <div className="brand">
          <span className="brand-mark">PA</span>
          <span>Paper Atlas</span>
          <button className="icon-button mobile-close" onClick={() => setOpen(false)} aria-label="关闭导航"><X size={18} /></button>
        </div>
        <nav>
          {links.map(({ to, label, icon: Icon }) => (
            <Link key={to} to={to} onClick={() => setOpen(false)} className={loc.pathname.startsWith(to) ? 'nav-link active' : 'nav-link'}>
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
          <div className="global-search">
            <Search size={17} />
            <input
              ref={searchRef}
              aria-label="搜索论文"
              placeholder="搜索标题、作者、DOI…"
              onKeyDown={(e) => {
                if (e.key !== 'Enter') return
                const value = e.currentTarget.value.trim()
                nav(value ? `/app/papers?q=${encodeURIComponent(value)}` : '/app/papers')
              }}
            />
            <kbd>⌘ K</kbd>
          </div>
          <div className="top-actions"><span className="avatar">A</span></div>
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
      <div className="auth-panel">
        <div className="brand large"><span className="brand-mark">PA</span><span>Paper Atlas</span></div>
        <p className="eyebrow">首次部署</p>
        <h1>创建管理员</h1>
        <p className="muted">初始化只执行一次。请输入部署时设置的初始化令牌。</p>
        {error && <div className="alert error">{error}</div>}
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
      <div className="auth-panel">
        <div className="brand large"><span className="brand-mark">PA</span><span>Paper Atlas</span></div>
        <p className="eyebrow">管理员入口</p>
        <h1>欢迎回来</h1>
        <p className="muted">你的论文、笔记和研究脉络都在这里。</p>
        {error && <div className="alert error">{error}</div>}
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

  const stats: [string, string | number, string][] = [
    ['论文总数', data?.totalPapers ?? '—', '篇'],
    ['近 30 天导入', data?.importedLast30Days ?? '—', '篇'],
    ['待阅读', data?.unread ?? '—', '篇'],
    ['存储占用', data ? ((data.storageBytes || 0) / 1073741824).toFixed(2) : '—', 'GB'],
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
      {error && <div className="alert error">{error}</div>}
      <section className="stat-grid">
        {stats.map(([label, value, unit]) => (
          <div className="stat-card" key={label}><span>{label}</span><strong>{value}</strong><small>{unit}</small></div>
        ))}
      </section>
      <section className="content-grid">
        <div className="panel">
          <div className="panel-head"><h2>最近更新</h2><Link to="/app/papers">查看全部</Link></div>
          {(data?.recent ?? []).length === 0 ? (
            <Empty title="还没有论文" description="导入第一篇论文，建立你的研究地图。" action={<Link className="button secondary" to="/app/import"><Upload size={16} />开始导入</Link>} />
          ) : (
            data?.recent.map((p) => <PaperRow key={p.id} paper={p} />)
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

function PaperRow({ paper }: { paper: Paper }) {
  return (
    <Link className="paper-row" to={`/app/papers/${paper.id}`}>
      <div className="paper-icon"><BookOpen size={18} /></div>
      <div className="paper-main">
        <strong>{paper.isFavorite ? '★ ' : ''}{paper.title}</strong>
        <span>{authorLine(paper)}{paper.journal ? ` · ${paper.journal}` : ''}</span>
      </div>
      <span className="row-status">{statusLabel(paper.readingStatus)}</span>
    </Link>
  )
}

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

function Papers() {
  const [params, setParams] = useSearchParams()
  const q = params.get('q') ?? ''
  const status = params.get('status') ?? ''
  const sort = params.get('sort') ?? 'newest'
  const yearFrom = params.get('yearFrom') ?? ''
  const yearTo = params.get('yearTo') ?? ''
  const favorite = params.get('favorite') === 'true'
  const page = Math.max(1, Number(params.get('page') ?? '1') || 1)

  const [items, setItems] = useState<Paper[]>([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [draft, setDraft] = useState(q)

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

  // 输入停止 300ms 后再写入 URL，避免每敲一个字就请求一次。
  useEffect(() => {
    if (draft.trim() === q) return
    const timer = setTimeout(() => update({ q: draft.trim() }), 300)
    return () => clearTimeout(timer)
  }, [draft, q, update])

  const queryString = useMemo(() => {
    const search = new URLSearchParams()
    if (q) search.set('q', q)
    if (status) search.set('status', status)
    if (favorite) search.set('favorite', 'true')
    if (yearFrom) search.set('yearFrom', yearFrom)
    if (yearTo) search.set('yearTo', yearTo)
    if (sort && sort !== 'newest') search.set('sort', sort)
    search.set('pageSize', String(PAGE_SIZE))
    if (page > 1) search.set('page', String(page))
    return `?${search.toString()}`
  }, [q, status, favorite, yearFrom, yearTo, sort, page])

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

  const lastPage = Math.max(1, Math.ceil(total / PAGE_SIZE))
  const filtered = Boolean(q || status || favorite || yearFrom || yearTo)

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
      <div className="toolbar">
        <div className="search-field">
          <Search size={17} />
          <input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder="搜索标题、作者、DOI、期刊…" />
          {draft && <button className="icon-button" aria-label="清空搜索" onClick={() => setDraft('')}><X size={15} /></button>}
        </div>
        <select aria-label="排序" value={sort} onChange={(e) => update({ sort: e.target.value })}>
          <option value="newest">最近添加</option>
          <option value="oldest">最早添加</option>
          <option value="updated">最近更新</option>
          <option value="year">发表年份</option>
          <option value="title">标题</option>
        </select>
      </div>
      <div className="papers-layout">
        <aside className="filters">
          <h3>筛选</h3>
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
          {filtered && (
            <button className="button secondary full" onClick={() => setParams(new URLSearchParams(), { replace: true })}>重置筛选</button>
          )}
          <div className="filter-note">筛选条件会写入网址，可直接收藏或分享该链接。</div>
        </aside>
        <div className="panel paper-list">
          {error && <div className="alert error">{error}</div>}
          {loading ? (
            <div className="loading">正在查询论文…</div>
          ) : items.length === 0 ? (
            <Empty
              title={filtered ? '没有匹配的论文' : '论文库还是空的'}
              description={filtered ? '调整关键词或筛选条件再试一次。' : '先导入一篇论文，之后即可在这里检索。'}
              action={<Link className="button secondary" to="/app/import"><Upload size={16} />导入论文</Link>}
            />
          ) : (
            <>
              {items.map((p) => <PaperRow key={p.id} paper={p} />)}
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
    </Shell>
  )
}

function PaperDetailPage() {
  const { id = '' } = useParams()
  const nav = useNavigate()
  const [data, setData] = useState<PaperDetail | null>(null)
  const [form, setForm] = useState({ title: '', abstract: '', doi: '', readingStatus: 'unread' })
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)

  const apply = (detail: PaperDetail) => {
    setData(detail)
    setForm({ title: detail.title ?? '', abstract: detail.abstract ?? '', doi: detail.doi ?? '', readingStatus: detail.readingStatus ?? 'unread' })
  }

  useEffect(() => {
    let alive = true
    setLoading(true)
    fetchPaper(id)
      .then((r) => alive && apply(r.data))
      .catch((e) => alive && setError(errorMessage(e)))
      .finally(() => alive && setLoading(false))
    return () => {
      alive = false
    }
  }, [id])

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

  const destroy = async () => {
    if (!window.confirm('确认删除这篇论文？删除后不会出现在列表中。')) return
    setBusy(true)
    try {
      await removePaper(id)
      nav('/app/papers', { replace: true })
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
    }
  }

  if (loading) return <Shell><div className="loading">正在加载论文…</div></Shell>
  if (!data) {
    return (
      <Shell>
        <div className="alert error">{error || '论文不存在'}</div>
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
      {error && <div className="alert error">{error}</div>}
      {notice && <div className="alert ok">{notice}</div>}
      <div className="content-grid">
        <div className="panel">
          <div className="panel-head"><h2>元数据</h2></div>
          <label className="field">标题<input value={form.title} onChange={(e) => setForm({ ...form, title: e.target.value })} /></label>
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
            <button className="button primary" disabled={busy} onClick={() => patch({ title: form.title.trim() || data.title, doi: form.doi.trim(), abstract: form.abstract, readingStatus: form.readingStatus }, '已保存')}>
              <Save size={16} />{busy ? '保存中…' : '保存修改'}
            </button>
            <button className="button secondary" disabled={busy} onClick={destroy}><Trash2 size={16} />删除论文</button>
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
    </Shell>
  )
}

function Import() {
  const [files, setFiles] = useState<File[]>([])
  const [results, setResults] = useState<UploadResult[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [busy, setBusy] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

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
        <label className="dropzone">
          <Upload size={28} />
          <strong>点击选择文件，或把文件拖到这里</strong>
          <span>服务端会校验扩展名、大小和 PDF 文件头。</span>
          <input ref={inputRef} type="file" multiple accept={ALLOWED_EXTENSIONS.join(',')} onChange={(e) => pick(e.target.files)} />
        </label>
        {error && <div className="alert error">{error}</div>}
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
          <button className="button primary" disabled={!files.length || busy} onClick={submit}><Upload size={17} />{busy ? '正在上传…' : '开始导入'}</button>
          {files.length > 0 && !busy && <button className="button secondary" onClick={() => { setFiles([]); if (inputRef.current) inputRef.current.value = '' }}>清空</button>}
          {notice && <span className="success-text">{notice}</span>}
          {results.length > 0 && <Link to="/app/papers">前往论文库 →</Link>}
        </div>
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
  const [data, setData] = useState<Facets | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    facets()
      .then((r) => setData(r.data))
      .catch((e) => setError(errorMessage(e)))
  }, [])

  const statuses = data?.statuses ?? {}
  const empty = data && data.years.length === 0 && data.journals.length === 0 && !Object.values(statuses).some(Boolean)

  return (
    <Shell>
      <div className="page-head">
        <div>
          <p className="eyebrow">知识组织</p>
          <h1>分类浏览</h1>
          <p className="muted">按发表年份、期刊和阅读状态查看知识库分布，点击任意条目进入筛选结果。</p>
        </div>
      </div>
      {error && <div className="alert error">{error}</div>}
      {!data ? (
        <div className="loading">正在统计…</div>
      ) : empty ? (
        <Empty title="还没有可统计的数据" description="导入论文后，这里会按年份和期刊自动归类。" action={<Link className="button secondary" to="/app/import"><Upload size={16} />导入论文</Link>} />
      ) : (
        <div className="taxonomy-grid">
          <div className="panel">
            <div className="panel-head"><h2>按年份</h2></div>
            <div className="tree">
              {data.years.length === 0 && <p className="muted">还没有带发表年份的论文。</p>}
              {data.years.map((y) => (
                <Link className="tree-item" key={y.year} to={`/app/papers?yearFrom=${y.year}&yearTo=${y.year}`}>{y.year} 年<small>{y.count} 篇</small></Link>
              ))}
              {data.missingYear > 0 && <div className="tree-item">年份未填写<small>{data.missingYear} 篇</small></div>}
            </div>
          </div>
          <div className="panel">
            <div className="panel-head"><h2>阅读进度</h2></div>
            <div className="tree">
              <Link className="tree-item root" to="/app/papers?status=unread">未读<small>{statuses.unread ?? 0} 篇</small></Link>
              <Link className="tree-item root" to="/app/papers?status=reading">阅读中<small>{statuses.reading ?? 0} 篇</small></Link>
              <Link className="tree-item root" to="/app/papers?status=read">已读<small>{statuses.read ?? 0} 篇</small></Link>
              <Link className="tree-item root" to="/app/papers?favorite=true">收藏<small>{data.favorites} 篇</small></Link>
            </div>
          </div>
          <div className="panel">
            <div className="panel-head"><h2>按期刊 / 来源</h2></div>
            <div className="tag-cloud">
              {data.journals.length === 0 && <p className="muted">期刊字段为空，可在论文详情页补充。</p>}
              {data.journals.map((j) => (
                <Link className="tag teal" key={j.name} to={`/app/papers?q=${encodeURIComponent(j.name)}`}>{j.name} · {j.count}</Link>
              ))}
            </div>
          </div>
        </div>
      )}
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
          {error && <div className="alert error">{error}</div>}
          {notice && <div className="alert ok">{notice}</div>}
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

  if (!boot) return <div className="splash">正在连接知识库…</div>
  const { initialized, authed } = boot
  const guard = (element: ReactNode) => (authed ? element : <Navigate to="/auth/login" replace />)
  const fallback = initialized ? (authed ? '/app/dashboard' : '/auth/login') : '/setup'

  return (
    <Routes>
      <Route path="/setup" element={initialized ? <Navigate to="/auth/login" replace /> : <Setup />} />
      <Route path="/auth/login" element={authed ? <Navigate to="/app/dashboard" replace /> : <Login />} />
      <Route path="/app/dashboard" element={guard(<Dashboard />)} />
      <Route path="/app/papers" element={guard(<Papers />)} />
      <Route path="/app/papers/:id" element={guard(<PaperDetailPage />)} />
      <Route path="/app/import" element={guard(<Import />)} />
      <Route path="/app/taxonomy" element={guard(<Taxonomy />)} />
      <Route path="/app/settings/security" element={guard(<Security />)} />
      <Route path="*" element={<Navigate to={fallback} replace />} />
    </Routes>
  )
}
