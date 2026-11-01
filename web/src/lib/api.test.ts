import { beforeEach, describe, expect, it, vi } from 'vitest'
import { api, APIError } from './api'

describe('API client', () => {
  beforeEach(() => { vi.restoreAllMocks() })
  it('uses same-origin admin session instead of a browser-stored API key', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"status":"ok"}', { status: 200, headers: { 'Content-Type': 'application/json' } }))
    await api('/admin/status')
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.credentials).toBe('same-origin')
    expect(new Headers(init.headers).has('Authorization')).toBe(false)
    expect(sessionStorage.getItem('muse2api.session-key')).toBeNull()
  })
  it('still times out when React Query supplies its own abort signal', async () => {
    vi.useFakeTimers()
    try {
      const external = new AbortController()
      vi.spyOn(globalThis, 'fetch').mockImplementation((_input, init) => new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), { once: true })
      }))
      const promise = api('/admin/accounts', { signal: external.signal })
      const assertion = expect(promise).rejects.toMatchObject({ message: '请求超时，请稍后重试', status: 0 } satisfies Partial<APIError>)
      await vi.advanceTimersByTimeAsync(60_000)
      await assertion
    } finally {
      vi.useRealTimers()
    }
  })
  it('normalizes OpenAI errors and request ID', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"error":{"message":"capability unavailable"}}', { status: 503, headers: { 'X-Request-ID': 'req_test' } }))
    const promise = api('/v1/models')
    await expect(promise).rejects.toMatchObject({ message: 'capability unavailable', status: 503, requestId: 'req_test' } satisfies Partial<APIError>)
  })
})
