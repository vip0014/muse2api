import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Download, RefreshCw, Search } from 'lucide-react'
import { api, apiRaw } from '../lib/api'
import type { SystemEvent } from '../lib/types'
import { Badge, Button, Empty, ErrorState, Input, Loading, PageHeader, Panel, Select } from '../components/UI'

const levelText: Record<string, string> = { info: '信息', warning: '警告', error: '错误' }

export default function Logs() {
  const [level, setLevel] = useState(''); const [query, setQuery] = useState('')
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['logs', level], queryFn: ({ signal }) => api<{ events: SystemEvent[] }>(`/admin/logs${level ? `?level=${level}` : ''}`, { signal }), refetchInterval: 10_000 })
  const events = useMemo(() => (data?.events ?? []).filter(event => !query || `${event.message} ${event.request_id} ${event.task_id} ${event.account_id}`.toLowerCase().includes(query.toLowerCase())), [data, query])
  const download = async () => { const response = await apiRaw('/admin/logs/export'); if (!response.ok) return; const blob = await response.blob(); const url = URL.createObjectURL(blob); const anchor = document.createElement('a'); anchor.href = url; anchor.download = 'muse2api-redacted-logs.ndjson'; anchor.click(); URL.revokeObjectURL(url) }
  return <div className="page-stack"><PageHeader title="日志与审计" actions={<><Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button><Button onClick={download}><Download size={15} />导出</Button></>} />
    <Panel className="toolbar"><div className="search-box"><Search size={16} /><Input placeholder="搜索日志" value={query} onChange={e => setQuery(e.target.value)} /></div><Select value={level} onChange={e => setLevel(e.target.value)} aria-label="日志级别"><option value="">全部级别</option><option value="info">信息</option><option value="warning">警告</option><option value="error">错误</option></Select><span className="toolbar-count">{events.length} 条事件</span></Panel>
    <Panel className="table-panel">{isLoading ? <Loading /> : error ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : events.length === 0 ? <Empty title="暂无日志" description="调整筛选条件后重试。" /> : <div className="table-wrap"><table><thead><tr><th>时间</th><th>级别</th><th>事件</th><th>消息</th><th>请求 / 任务</th><th>上游状态</th><th>耗时</th></tr></thead><tbody>{events.map(event => <tr key={event.id}><td className="nowrap">{new Date(event.time).toLocaleString('zh-CN')}</td><td><Badge tone={event.level === 'error' ? 'danger' : event.level === 'warning' ? 'warning' : 'neutral'}>{levelText[event.level] ?? event.level}</Badge></td><td className="mono">{event.type}</td><td>{event.message}</td><td><div className="secondary-cell mono">{event.request_id || event.task_id || event.account_id || '—'}</div></td><td>{event.upstream_status || '—'}</td><td className="nowrap">{event.duration_ms ? `${event.duration_ms} ms` : '—'}</td></tr>)}</tbody></table></div>}</Panel>
  </div>
}
