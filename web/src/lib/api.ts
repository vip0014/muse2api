export class APIError extends Error {
  constructor(message: string, public status: number, public requestId?: string) { super(message); this.name = 'APIError' }
}

const REQUEST_TIMEOUT_MS = 60_000

// React Query always supplies its own abort signal, which previously replaced
// the timeout signal entirely: a stuck backend left the page loading forever.
// Merge both so caller cancellation still wins while every request keeps a
// hard deadline.
function mergeSignals(signals: AbortSignal[]): AbortSignal {
  if (typeof AbortSignal.any === 'function') return AbortSignal.any(signals)
  const merged = new AbortController()
  for (const signal of signals) {
    if (signal.aborted) { merged.abort(); break }
    signal.addEventListener('abort', () => merged.abort(), { once: true })
  }
  return merged.signal
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  // Never let the UI spin forever on a slow backend path (browser startup,
  // generation wait, quota refresh queued behind other work).
  const timeout = new AbortController()
  const timer = window.setTimeout(() => timeout.abort(), REQUEST_TIMEOUT_MS)
  const signal = init.signal ? mergeSignals([init.signal, timeout.signal]) : timeout.signal
  let response: Response
  try {
    response = await fetch(path, { ...init, credentials: 'same-origin', headers, signal })
  } catch {
    if (timeout.signal.aborted) throw new APIError('请求超时，请稍后重试', 0)
    // Keep the caller's cancellation as an AbortError so React Query can
    // discard the obsolete result instead of surfacing a network failure.
    if (init.signal?.aborted) throw new DOMException('The operation was aborted.', 'AbortError')
    throw new APIError('无法连接服务，请检查后端是否运行', 0)
  } finally {
    window.clearTimeout(timer)
  }
  const requestId = response.headers.get('X-Request-ID') ?? undefined
  const text = await response.text()
  let body: unknown = null
  if (text) { try { body = JSON.parse(text) } catch { body = text } }
  if (!response.ok) {
    const record = body as { detail?: string; error?: { message?: string } | string } | null
    const message = record?.detail ?? (typeof record?.error === 'string' ? record.error : record?.error?.message) ?? `请求失败（HTTP ${response.status}）`
    // A wrong password on the login form is also HTTP 401. Only a 401 from
    // an authenticated admin route means the session itself expired.
    if (response.status === 401 && path !== '/admin/login') window.dispatchEvent(new Event('muse2api:unauthorized'))
    throw new APIError(message, response.status, requestId)
  }
  return body as T
}

export async function apiRaw(path: string, init: RequestInit = {}) {
  const headers = new Headers(init.headers)
  if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  return fetch(path, { ...init, credentials: 'same-origin', headers })
}
export const json = (value: unknown) => JSON.stringify(value)
