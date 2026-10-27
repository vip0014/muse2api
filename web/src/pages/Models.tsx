import { useQuery } from '@tanstack/react-query'
import { RefreshCw } from 'lucide-react'
import { api } from '../lib/api'
import type { Capability } from '../lib/types'
import { fullDate } from '../lib/format'
import { Badge, Button, Empty, ErrorState, Loading, PageHeader, Panel } from '../components/UI'

const statusMeta: Record<string, { label: string; tone: 'success' | 'warning' | 'neutral' }> = {
  VERIFIED: { label: '已验证', tone: 'success' },
  IMPLEMENTED: { label: '已实现', tone: 'success' },
  UNAVAILABLE: { label: '不可用', tone: 'warning' },
  UNVERIFIED: { label: '未验证', tone: 'neutral' },
}

export default function Models() {
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['capabilities'], queryFn: ({ signal }) => api<{ data: Capability[] }>('/admin/capabilities', { signal }) })
  return <div className="page-stack"><PageHeader title="模型与能力" actions={<Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button>} />
    <Panel className="table-panel">{isLoading ? <Loading /> : error ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : !data?.data.length ? <Empty title="暂无能力数据" /> : <div className="table-wrap"><table><thead><tr><th>对外能力</th><th>上游映射</th><th>声明范围</th><th>状态</th><th>最近验证</th><th>限制</th></tr></thead><tbody>{data.data.map(cap => { const meta = statusMeta[cap.status] ?? { label: cap.status, tone: 'neutral' as const }; return <tr key={cap.id}><td><div className="model-name">{cap.alias}</div><div className="secondary-cell mono">{cap.id}</div></td><td className="mono">{cap.upstream_model}</td><td><div className="feature-list">{cap.features.map(feature => <span key={feature}>{feature}</span>)}</div></td><td><Badge tone={meta.tone}>{meta.label}</Badge></td><td className="nowrap">{fullDate(cap.last_verified)}</td><td className="reason-cell">{cap.reason || '—'}</td></tr> })}</tbody></table></div>}</Panel>
  </div>
}
