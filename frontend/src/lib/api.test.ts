import { afterEach, expect, it, vi } from 'vitest'
import { listCategories, listTags } from './api'

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
