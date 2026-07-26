const API_BASE = import.meta.env.VITE_API_BASE_URL ?? '/api'

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const method = (init.method ?? 'GET').toUpperCase()
  if (typeof document !== 'undefined' && ['POST', 'PUT', 'PATCH', 'DELETE'].includes(method)) {
    const csrf = document.cookie.split('; ').find((item) => item.startsWith('pkb_csrf='))?.split('=').slice(1).join('=')
    if (csrf) headers.set('X-CSRF-Token', decodeURIComponent(csrf))
  }
  if (init.body && !(init.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  const response = await fetch(`${API_BASE}${path}`, { ...init, headers, credentials: 'include' })
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new Error(body?.error?.message || `请求失败 (${response.status})`)
  }
  if (response.status === 204) return undefined as T
  return response.json()
}

export const setupStatus = () => api<{ data: { initialized: boolean } }>('/setup/status')
export const createAdmin = (payload: Record<string, string>) => api('/setup/admin', { method: 'POST', body: JSON.stringify(payload) })
export const login = (payload: Record<string, string>) => api('/auth/login', { method: 'POST', body: JSON.stringify(payload) })
export const me = () => api<{ data: { displayName: string; email: string } }>('/auth/me')
export const logout = () => api('/auth/logout', { method: 'POST' })
export type Paper = { id: string; title: string; authors?: { name: string }[]; year?: number; journal?: string; doi?: string; abstractSnippet?: string; tags?: { id: string; name: string; color?: string }[]; categories?: { id: string; name: string }[]; readingStatus?: string; isFavorite?: boolean; parseStatus?: string; addedAt?: string }
export const papers = (params = '') => api<{ data: { items: Paper[]; total: number } }>(`/papers${params}`)
export const dashboard = () => api<{ data: { totalPapers?: number; importedLast30Days?: number; unread?: number; storageBytes?: number; recent?: Paper[]; stats?: { totalPapers?: number; importedLast30Days?: number; unread?: number; storageBytes?: number } } }>('/dashboard')
