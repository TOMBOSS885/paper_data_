const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api'

export class ApiError extends Error {
  status: number
  code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

const statusMessage = (status: number) => {
  if (status === 401) return '登录已过期，请重新登录'
  if (status === 403) return '没有权限或校验失败，请刷新页面后重试'
  if (status === 404) return '资源不存在或已被删除'
  if (status === 409) return '数据已被其他操作修改，请刷新后重试'
  if (status === 413) return '文件超过服务端允许的大小'
  if (status === 429) return '操作过于频繁，请稍后再试'
  if (status >= 500) return `服务端错误 (${status})，可用 docker compose logs api 查看日志`
  return `请求失败 (${status})`
}

// 会话失效时统一回到登录页，避免页面停留在空数据状态。
const redirectToLogin = () => {
  if (typeof window === 'undefined') return
  const path = window.location.pathname
  if (path.startsWith('/auth') || path.startsWith('/setup')) return
  window.location.replace('/auth/login')
}

type Options = RequestInit & { silent401?: boolean }

export async function api<T>(path: string, init: Options = {}): Promise<T> {
  const { silent401, ...rest } = init
  const headers = new Headers(rest.headers)
  const method = (rest.method ?? 'GET').toUpperCase()
  if (typeof document !== 'undefined' && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    const csrf = document.cookie.split('; ').find((item) => item.startsWith('pkb_csrf='))?.split('=').slice(1).join('=')
    if (csrf) headers.set('X-CSRF-Token', decodeURIComponent(csrf))
  }
  if (rest.body && !(rest.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  let response: Response
  try {
    response = await fetch(`${API_BASE}${path}`, { ...rest, headers, credentials: 'include' })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new ApiError(0, 'network_error', '无法连接服务，请确认容器正在运行')
  }
  if (!response.ok) {
    const body = await response.json().catch(() => ({}) as Record<string, never>)
    const detail = (body as { error?: { code?: string; message?: string } }).error
    if (response.status === 401 && !silent401) redirectToLogin()
    throw new ApiError(response.status, detail?.code ?? 'error', detail?.message || statusMessage(response.status))
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export type Author = { name: string } | string
export type Tag = { id: number; name: string; color: string; usageCount?: number }
export type Category = {
  id: number
  parentId: number | null
  name: string
  sortOrder: number
  paperCount: number
  children: Category[]
}
export type Paper = {
  id: string
  title: string
  abstract?: string
  authors?: Author[] | null
  year?: number | null
  journal?: string
  doi?: string
  readingStatus?: string
  isFavorite?: boolean
  parseStatus?: string
  addedAt?: string
  updatedAt?: string
  tags?: Tag[]
  categories?: { id: number; name: string }[]
}
export type TrashPaper = Paper & {
  deletedAt: string
  purgeAt: string
}
export type PaperDetail = Paper & {
  abstract?: string
  version: number
  file?: { originalName: string; mediaType: string; sizeBytes: number; previewable: boolean }
}
export type UploadResult = { filename: string; status: string; reason?: string; uploadId?: string }
export type Facets = {
  years: { year: number; count: number }[]
  journals: { name: string; count: number }[]
  statuses: Record<string, number>
  favorites: number
  missingYear: number
}

export const setupStatus = (signal?: AbortSignal) => api<{ data: { initialized: boolean } }>('/setup/status', { signal })
export const createAdmin = (payload: Record<string, string>) => api('/setup/admin', { method: 'POST', body: JSON.stringify(payload) })
export const login = (payload: Record<string, string>) => api('/auth/login', { method: 'POST', body: JSON.stringify(payload) })
export const me = (silent401 = false, signal?: AbortSignal) => api<{ data: { displayName: string; email: string; createdAt: string } }>('/auth/me', { silent401, signal })
export const logout = () => api('/auth/logout', { method: 'POST' })
export const changePassword = (currentPassword: string, newPassword: string) =>
  api('/auth/password', { method: 'POST', body: JSON.stringify({ currentPassword, newPassword }) })

export const papers = (params = '', signal?: AbortSignal) => api<{ data: { items: Paper[]; total: number; page: number; pageSize: number } }>(`/papers${params}`, { signal })
export const trashPapers = (params = '', signal?: AbortSignal) =>
  api<{ data: { items: TrashPaper[]; total: number; page: number; pageSize: number; retentionDays: number } }>(`/trash${params}`, { signal })
export const restoreTrashPaper = (id: string) =>
  api<{ data: { id: string; restored: boolean } }>(`/trash/${encodeURIComponent(id)}/restore`, { method: 'POST' })
export const paper = (id: string, signal?: AbortSignal) => api<{ data: PaperDetail }>(`/papers/${encodeURIComponent(id)}`, { signal })
export const updatePaper = (id: string, payload: Record<string, unknown>) =>
  api<{ data: PaperDetail }>(`/papers/${encodeURIComponent(id)}`, { method: 'PATCH', body: JSON.stringify(payload) })
export const removePaper = (id: string) => api(`/papers/${encodeURIComponent(id)}`, { method: 'DELETE' })
export const updatePaperTags = (id: string, ids: number[]) => api(`/papers/${encodeURIComponent(id)}/tags`, { method: 'PUT', body: JSON.stringify({ ids }) })
export const updatePaperCategories = (id: string, ids: number[]) => api(`/papers/${encodeURIComponent(id)}/categories`, { method: 'PUT', body: JSON.stringify({ ids }) })
export const deletePaperWithPassword = (id: string, password: string) =>
  api(`/papers/${encodeURIComponent(id)}`, { method: 'DELETE', body: JSON.stringify({ password }) })
export const bulkDeletePapers = (paperIds: string[], password: string) =>
  api<{ data: { deleted: number; missing: string[] } }>(
    '/papers/bulk/delete',
    { method: 'POST', body: JSON.stringify({ paperIds, password }) },
  )
export const bulkUpdatePaperTags = (paperIds: string[], tagIds: number[], password: string) =>
  api<{ data: { updated: number; missing: string[]; kind: string; ids: number[] } }>(
    '/papers/bulk/tags',
    { method: 'POST', body: JSON.stringify({ paperIds, ids: tagIds, password }) },
  )
export const bulkUpdatePaperCategories = (paperIds: string[], categoryIds: number[], password: string) =>
  api<{ data: { updated: number; missing: string[]; kind: string; ids: number[] } }>(
    '/papers/bulk/categories',
    { method: 'POST', body: JSON.stringify({ paperIds, ids: categoryIds, password }) },
  )
export const bulkSetFavorite = (paperIds: string[], isFavorite: boolean, password: string) =>
  api<{ data: { updated: number; missing: string[] } }>(
    '/papers/bulk/favorite',
    { method: 'POST', body: JSON.stringify({ paperIds, isFavorite, password }) },
  )
export const uploadPapers = (files: File[]) => {
  const form = new FormData()
  files.forEach((file) => form.append('files[]', file))
  return api<{ data: { accepted: number; rejected: number; items: UploadResult[] } }>('/papers', { method: 'POST', body: form })
}
export const fileUrl = (id: string, kind: 'preview' | 'download') => `${API_BASE}/papers/${encodeURIComponent(id)}/${kind}`

import { useQuery } from '@tanstack/react-query'

export const listTags = (signal?: AbortSignal) => api<{ data: { items: Tag[] } }>('/tags', { cache: 'no-store', signal })
export const useTags = () => useQuery<Tag[]>({ queryKey: ['tags'], queryFn: ({ signal }) => listTags(signal).then(r => r.data.items), placeholderData: [] })

export const createTag = (name: string, color: string) => api<Tag>('/tags', { method: 'POST', body: JSON.stringify({ name, color }) })
export const deleteTag = async (id: number) => {
  await api(`/tags/${id}`, { method: 'DELETE' })
}
export const listCategories = (signal?: AbortSignal) => api<{ data: { items: Category[] } }>('/categories', { cache: 'no-store', signal })
export const useCategories = () => useQuery<Category[]>({ queryKey: ['categories'], queryFn: ({ signal }) => listCategories(signal).then(r => r.data.items), placeholderData: [] })
export const createCategory = (payload: { name: string; parentId?: number | null; sortOrder?: number }) =>
  api<Category>('/categories', { method: 'POST', body: JSON.stringify(payload) })
export const deleteCategory = async (id: number) => {
  await api(`/categories/${id}`, { method: 'DELETE' })
}

export const facets = () => api<{ data: Facets }>('/facets')
export const dashboard = (signal?: AbortSignal) =>
  api<{ data: { totalPapers?: number; importedLast30Days?: number; unread?: number; favorites?: number; storageBytes?: number; recent?: Paper[] } }>('/dashboard', { signal })

export const TAG_COLORS: { value: string; label: string }[] = [
  { value: 'teal', label: '青绿' },
  { value: 'blue', label: '蓝' },
  { value: 'amber', label: '琥珀' },
  { value: 'rose', label: '玫瑰' },
  { value: 'slate', label: '中性灰' },
  { value: 'green', label: '草绿' },
	{ value: 'violet', label: '紫罗兰' },
]

export const reextractPaper = (id: string) =>
  api<{ data: { status?: string; Title?: string; Authors?: string[]; Year?: number; Subject?: string } }>(
    `/papers/${encodeURIComponent(id)}/reextract`,
    { method: 'POST' },
  ).then((response) => response.data)
export const extractPapersPreview = (files: File[]) => {
  const form = new FormData()
  files.forEach((file) => form.append('files', file))
  return api<{ data: { fileName: string; meta: { Title?: string; Authors?: string[]; Year?: number; Subject?: string } }[] }>(
    '/papers/extract',
    { method: 'POST', body: form },
  ).then((response) => response.data)
}
