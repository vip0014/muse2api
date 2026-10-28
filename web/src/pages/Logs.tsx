import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { ChevronDown, ChevronRight, Download, RefreshCw, Search, ShieldAlert } from 'lucide-react'
import { api, apiRaw } from '../lib/api'
import type { SystemEvent } from '../lib/types'
import { Badge, Button, Empty, ErrorState, Input, Loading, PageHeader, Select } from '../components/UI'

const levelText: Record<string, string> = { info: '信息', warning: '警告', error: '错误' }

function logTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date)
}

function elapsed(ms?: number) {
  if (!ms) return ''
  return ms >= 1000 ? `${(ms / 1000).toFixed(ms >= 10_000 ? 0 : 1)} 秒` : `${ms} ms`
}

export default function Logs() {
  const [level, setLevel] = useState('')
  const [query, setQuery] = useState('')
  const [expanded, setExpanded] = useState<string>()
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['logs', level], queryFn: ({ signal }) => api<{ events: SystemEvent[] }>(`/admin/logs${level ? `?level=${level}` : ''}`, { signal }), refetchInterval: 10_000 })
  const events = useMemo(() => (data?.events ?? []).filter(event => !query || `${event.message} ${event.request_id} ${event.task_id} ${event.account_id} ${event.type}`.toLowerCase().includes(query.toLowerCase())), [data, query])
  const download = async () => {
    const response = await apiRaw('/admin/logs/export')
    if (!response.ok) return
    const blob = await response.blob()
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = 'muse2api-redacted-logs.ndjson'
    anchor.click()
    URL.revokeObjectURL(url)
  }

  return <div className="page-stack">
    <PageHeader title="日志与审计" description="请求、账号、安全事件与上游状态；点击任意一条展开完整标识。" kicker="审计" actions={<><Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button><Button onClick={() => void download()}><Download size={15} />导出 NDJSON</Button></>} />
    <div className="toolbar panel">
      <div className="search-box"><Search size={16} /><Input placeholder="搜索消息、请求 ID、任务或事件" value={query} onChange={event => setQuery(event.target.value)} aria-label="搜索日志" /></div>
      <Select value={level} onChange={event => setLevel(event.target.value)} aria-label="日志级别"><option value="">全部级别</option><option value="info">信息</option><option value="warning">警告</option><option value="error">错误</option></Select>
      <span className="toolbar-count">{error ? '读取失败' : `${events.length} 条事件`}</span>
    </div>

    <div className="panel log-surface">
      {isLoading ? <Loading /> : error ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : events.length === 0 ? <Empty title="暂无日志" description="调整筛选条件后重试；管理员操作与任务执行会自动记录。" /> : <div className="log-list">
        {events.map(event => {
          const open = expanded === event.id
          return <div key={event.id}>
            <button className={`log-row ${open ? 'expanded' : ''}`} onClick={() => setExpanded(open ? undefined : event.id)} aria-expanded={open}>
              <span className="log-time">{logTime(event.time)}</span>
              <span className={`log-marker ${event.level}`} />
              <span className="log-main">
                <span className="log-message">{event.message}</span>
                <span className="log-meta"><Badge tone={event.level === 'error' ? 'danger' : event.level === 'warning' ? 'warning' : 'neutral'}>{levelText[event.level] ?? event.level}</Badge><span className="mono">{event.type}</span>{event.request_id && <span>· {event.request_id}</span>}</span>
              </span>
              <span className="log-side">{(event.upstream_status || event.duration_ms) && <span>{[event.upstream_status ? `上游 ${event.upstream_status}` : '', elapsed(event.duration_ms)].filter(Boolean).join(' · ')}</span>}{open ? <ChevronDown size={14} /> : <ChevronRight size={14} />}</span>
            </button>
            {open && <div className="log-details">
              <div><span>请求 ID</span><strong className="mono">{event.request_id || '—'}</strong></div>
              <div><span>任务 ID</span><strong className="mono">{event.task_id || '—'}</strong></div>
              <div><span>账号 ID</span><strong className="mono">{event.account_id || '—'}</strong></div>
              <div><span>上游状态 / 耗时</span><strong>{[event.upstream_status ? String(event.upstream_status) : '—', elapsed(event.duration_ms)].filter(Boolean).join(' · ')}</strong></div>
            </div>}
          </div>
        })}
      </div>}
    </div>
    <p className="secondary-cell"><ShieldAlert size={12} /> 日志导出只包含脱敏后的服务端事件；敏感凭据不会出现在此处。</p>
  </div>
}
