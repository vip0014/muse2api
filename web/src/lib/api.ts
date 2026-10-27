export class APIError extends Error {
  constructor(message: string, public status: number, public requestId?: string) { super(message); this.name = 'APIError' }
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  let response: Response
  try { response = await fetch(path, { ...init, credentials: 'same-origin', headers }) } catch { throw new APIError('无法连接服务，请检查后端是否运行', 0) }
  const requestId = response.headers.get('X-Request-ID') ?? undefined
  const text = await response.text()
  let body: unknown = null
  if (text) { try { body = JSON.parse(text) } catch { body = text } }
  if (!response.ok) {
    const record = body as { detail?: string; error?: { message?: string } | string } | null
    const message = record?.detail ?? (typeof record?.error === 'string' ? record.error : record?.error?.message) ?? `请求失败（HTTP ${response.status}）`
    if (response.status === 401) window.dispatchEvent(new Event('muse2api:unauthorized'))
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
