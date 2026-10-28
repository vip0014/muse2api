import { useEffect, useRef, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Check, ChevronRight, Clipboard, KeyRound, Play, Square, Zap } from 'lucide-react'
import { api, apiRaw } from '../lib/api'
import { Button, Field, InlineError, PageHeader, Panel, Select } from '../components/UI'

interface Model { id: string; status: string; description?: string }
type Endpoint = 'chat' | 'responses' | 'image' | 'video'
const endpoints: Record<Endpoint, { path: string; label: string; hint: string }> = {
  chat: { path: '/v1/chat/completions', label: 'Chat Completions', hint: '对话与文本生成' },
  responses: { path: '/v1/responses', label: 'Responses API', hint: 'OpenAI Responses 兼容' },
  image: { path: '/v1/images/tasks', label: '图片任务', hint: '异步图片生成' },
  video: { path: '/v1/videos', label: '视频任务', hint: '单片段视频生成' },
}

export default function Playground() {
  const [endpoint, setEndpoint] = useState<Endpoint>('chat')
  const [model, setModel] = useState('muse-spark')
  const [prompt, setPrompt] = useState('请用一句话介绍 Muse2API。')
  const [stream, setStream] = useState(true)
  const [sending, setSending] = useState(false)
  const [controller, setController] = useState<AbortController>()
  const [status, setStatus] = useState<number>()
  const [elapsed, setElapsed] = useState<number>()
  const [requestID, setRequestID] = useState('')
  const [output, setOutput] = useState('')
  const [error, setError] = useState('')
  const [copied, setCopied] = useState(false)
  const [responseCopied, setResponseCopied] = useState(false)
  const outputRef = useRef<HTMLPreElement>(null)
  const { data } = useQuery({ queryKey: ['models'], queryFn: ({ signal }) => api<{ data: Model[] }>('/v1/models', { signal }) })

  useEffect(() => {
    if (!sending || !outputRef.current) return
    outputRef.current.scrollTop = outputRef.current.scrollHeight
  }, [output, sending])

  const changeEndpoint = (value: Endpoint) => {
    setEndpoint(value)
    setModel(value === 'image' ? 'muse-image' : value === 'video' ? 'muse-video' : 'muse-spark')
    setOutput(''); setStatus(undefined); setError(''); setRequestID('')
  }
  const body = () => {
    if (endpoint === 'responses') return { model, input: prompt, stream }
    if (endpoint === 'chat') return { model, messages: [{ role: 'user', content: prompt }], stream }
    return { model, prompt }
  }
  const curl = `curl ${location.origin}${endpoints[endpoint].path} \\\n  -H "Authorization: Bearer $MUSE2API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(body())}'`

  const send = async () => {
    if (!prompt.trim() || sending) return
    const abort = new AbortController()
    setController(abort); setSending(true); setOutput(''); setError(''); setStatus(undefined); setRequestID('')
    const started = performance.now()
    try {
      const response = await apiRaw(endpoints[endpoint].path, { method: 'POST', body: JSON.stringify(body()), signal: abort.signal })
      setStatus(response.status)
      setRequestID(response.headers.get('X-Request-ID') ?? '')
      if (stream && (endpoint === 'chat' || endpoint === 'responses') && response.ok && response.body) {
        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let text = ''
        while (true) { const { done, value } = await reader.read(); if (done) break; text += decoder.decode(value, { stream: true }); setOutput(text) }
      } else {
        setOutput(await response.text())
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') setError('请求已取消')
      else setError(err instanceof Error ? err.message : '请求失败')
    } finally {
      setElapsed(Math.round(performance.now() - started))
      setSending(false)
      setController(undefined)
    }
  }

  const copyCurl = async () => {
    await navigator.clipboard.writeText(curl)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1500)
  }
  const copyResponse = async () => {
    if (!output) return
    await navigator.clipboard.writeText(output)
    setResponseCopied(true)
    window.setTimeout(() => setResponseCopied(false), 1500)
  }

  const statusLabel = status === undefined ? (sending ? '请求中' : '待发送') : `${status}`
  const statusClass = status === undefined ? '' : status >= 400 ? 'bad' : 'ok'

  return <div className="page-stack">
    <PageHeader title="API 调试台" description="直接向本地服务发起请求，确认鉴权、模型与流式响应。" kicker="工具" actions={<span className="session-chip"><KeyRound size={12} />管理员会话</span>} />
    <div className="playground-grid">
      <Panel className="request-panel">
        <div className="section-head"><div><h2>请求</h2><p>{endpoints[endpoint].hint}</p></div><span className="secondary-cell mono">{endpoints[endpoint].path}</span></div>
        <div className="endpoint-switch" role="tablist" aria-label="接口类型">
          {(Object.keys(endpoints) as Endpoint[]).map(key => <button key={key} role="tab" aria-selected={endpoint === key} className={`endpoint-tab ${endpoint === key ? 'active' : ''}`} onClick={() => changeEndpoint(key)}><strong>{endpoints[key].label}</strong><span>POST</span></button>)}
        </div>
        <div className="playground-form">
          <Field label="模型">
            <Select value={model} onChange={event => setModel(event.target.value)}>
              {(data?.data ?? []).filter(item => endpoint === 'image' ? item.id.includes('image') : endpoint === 'video' ? item.id.includes('video') : item.id.includes('spark')).map(item => <option key={item.id} value={item.id}>{item.id} · {item.status}</option>)}
              {!data && <option value={model}>{model}</option>}
            </Select>
          </Field>
          <Field label={endpoint === 'chat' || endpoint === 'responses' ? '用户输入' : '提示词'} hint="⌘ / Ctrl + Enter 发送">
            <textarea className="input textarea prompt-input" rows={8} value={prompt} onChange={event => setPrompt(event.target.value)} onKeyDown={event => { if ((event.metaKey || event.ctrlKey) && event.key === 'Enter') { event.preventDefault(); void send() } }} spellCheck={false} />
          </Field>
          {endpoint === 'video' && <p className="field-hint">官网单片段约 10 秒、720p；duration 不控制上游；比例只能靠提示词；长视频请分段生成，本项目不做拼接。</p>}
          {(endpoint === 'chat' || endpoint === 'responses') && <label className="switch-row"><strong>SSE 流式响应</strong><input type="checkbox" checked={stream} onChange={event => setStream(event.target.checked)} /></label>}
          <div className="request-actions">
            <Button onClick={send} disabled={sending || !prompt.trim()}><Play size={15} />{sending ? '请求中…' : '发送'}</Button>
            {sending && <Button variant="secondary" onClick={() => controller?.abort()}><Square size={14} />取消</Button>}
            <Button variant="ghost" onClick={() => void copyCurl()}>{copied ? <Check size={15} /> : <Clipboard size={15} />}{copied ? '已复制' : '复制 cURL'}</Button>
          </div>
          <div className="endpoint-preview"><span>POST</span><code>{endpoints[endpoint].path}</code><ChevronRight size={12} /></div>
        </div>
      </Panel>

      <section className="console-panel" aria-label="响应">
        <div className="console-head">
          <div className="console-lights" aria-hidden="true"><i /><i /><i /></div>
          <strong>响应</strong>
          <span className="spacer" />
          {sending && <span className="console-status"><Zap size={10} />流式接收</span>}
          <span className={`console-status ${statusClass}`}>{statusLabel}</span>
        </div>
        <div className="console-meta">
          <div><span>状态</span><strong className={status && status >= 400 ? 'text-danger' : status ? 'text-success' : ''}>{status ?? '—'}</strong></div>
          <div><span>耗时</span><strong>{elapsed !== undefined ? `${elapsed} ms` : '—'}</strong></div>
          <div><span>请求 ID</span><strong className="mono" title={requestID || undefined}>{requestID || '—'}</strong></div>
        </div>
        {error && <div className="console-error"><InlineError>{error}</InlineError></div>}
        <pre ref={outputRef} className={`console-output ${!output ? 'is-empty' : ''} ${error ? 'is-error' : ''}`} aria-live="polite">{output || (sending ? '等待响应…' : '响应将显示在这里')}</pre>
        <div className="console-foot"><span>{output ? `${output.length} 字符` : '—'}</span><Button size="sm" variant="ghost" disabled={!output} onClick={() => void copyResponse()}><Clipboard size={13} />{responseCopied ? '已复制' : '复制响应'}</Button></div>
      </section>
    </div>
  </div>
}
