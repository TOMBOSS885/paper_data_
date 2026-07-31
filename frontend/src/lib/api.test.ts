import { afterEach, expect, it, vi } from 'vitest'
import { listCategories, listTags, papers } from './api'

afterEach(() => {
  vi.unstubAllGlobals()
})

it.each([
  ['tags', listTags],
  ['categories', listCategories],
])('bypasses the browser HTTP cache for %s', async (path, request) => {
  const fetchMock = vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ data: { items: [] } }),
    { status: 200, headers: { 'Content-Type': 'application/json' } },
  ))
  vi.stubGlobal('fetch', fetchMock)

  await request()

  expect(fetchMock).toHaveBeenCalledWith(`/api/${path}`, expect.objectContaining({
    cache: 'no-store',
    credentials: 'include',
  }))
})

it('passes AbortSignal to fetch and preserves cancellation errors', async () => {
  const controller = new AbortController()
  const fetchMock = vi.fn((_url: string, init?: RequestInit) => new Promise<Response>((_resolve, reject) => {
    init?.signal?.addEventListener('abort', () => reject(new DOMException('cancelled', 'AbortError')), { once: true })
  }))
  vi.stubGlobal('fetch', fetchMock)

  const request = papers('?q=cancelled', controller.signal)
  controller.abort()

  await expect(request).rejects.toMatchObject({ name: 'AbortError' })
  expect(fetchMock).toHaveBeenCalledWith('/api/papers?q=cancelled', expect.objectContaining({
    credentials: 'include',
    signal: controller.signal,
  }))
})
