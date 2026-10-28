import { useQuery } from '@tanstack/react-query'
import { Database, RefreshCw } from 'lucide-react'
import { api } from '../lib/api'
import type { Capability } from '../lib/types'
import { fullDate } from '../lib/format'
import { Badge, Button, Empty, ErrorState, Loading, PageHeader } from '../components/UI'

const statusMeta: Record<string, { label: string; tone: 'success' | 'warning' | 'neutral' }> = {
  VERIFIED: { label: '已验证', tone: 'success' },
  IMPLEMENTED: { label: '已实现', tone: 'success' },
  UNAVAILABLE: { label: '不可用', tone: 'warning' },
  UNVERIFIED: { label: '未验证', tone: 'neutral' },
}

export default function Models() {
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['capabilities'], queryFn: ({ signal }) => api<{ data: Capability[] }>('/admin/capabilities', { signal }) })
  return <div className="page-stack">
    <PageHeader title="模型与能力" description="对外模型别名、上游映射与已经验证过的能力边界。" kicker="能力" actions={<><span className="metric-inline"><Database size={13} />{error ? '读取失败' : `${data?.data.length ?? 0} 项能力`}</span><Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button></>} />
    <div className="panel model-list">
      {isLoading ? <Loading /> : error ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : !data?.data.length ? <Empty title="暂无能力数据" description="服务端尚未返回模型能力清单。" /> : <>
        <div className="model-row header"><span>对外能力</span><span>上游映射</span><span>声明范围</span><span>状态</span><span>最近验证 / 限制</span></div>
        {data.data.map(cap => {
          const meta = statusMeta[cap.status] ?? { label: cap.status, tone: 'neutral' as const }
          return <div className="model-row" key={cap.id}>
            <div><div className="model-name">{cap.alias}</div><div className="secondary-cell mono">{cap.id}</div></div>
            <div className="mono secondary-cell">{cap.upstream_model}</div>
            <div className="feature-list">{cap.features.map(feature => <span key={feature}>{feature}</span>)}</div>
            <div><Badge tone={meta.tone}>{meta.label}</Badge></div>
            <div><div className="nowrap secondary-cell">{fullDate(cap.last_verified)}</div><div className="reason-cell">{cap.reason || '—'}</div></div>
          </div>
        })}
      </>}
    </div>
    <p className="secondary-cell">状态来自服务端能力清单；未验证不等于不可用，具体参数边界以实际请求结果为准。</p>
  </div>
}
