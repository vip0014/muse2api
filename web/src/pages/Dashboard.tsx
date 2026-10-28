import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, ArrowRight, ArrowUpRight, CheckCircle2, Gauge, Image, ListChecks, RefreshCw, Sparkles, UserX, Users, Video } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Account, Overview, Task } from '../lib/types'
import { accountStatusText, dateTime, percent, taskStatusText } from '../lib/format'
import { Badge, Button, Empty, ErrorState, PageHeader, PageSkeleton, Panel, RefreshError } from '../components/UI'

type Tone = 'success' | 'warning' | 'danger' | 'info'

interface Attention {
  id: string
  tone: Tone
  title: string
  detail: string
  meta: string
  to: string
}

function remainingPercent(account: Account) {
  const quota = account.quota
  if (!quota) return undefined
  const weekly = typeof quota.weekly_used_pct === 'number' ? 100 - Math.max(0, Math.min(100, quota.weekly_used_pct)) : undefined
  const extra = typeof quota.extra_used_pct === 'number' ? 100 - Math.max(0, Math.min(100, quota.extra_used_pct)) : undefined
  return weekly ?? extra
}

function taskTone(status: string) {
  if (status === 'failed') return 'danger' as const
  if (['completed', 'succeeded'].includes(status)) return 'success' as const
  if (['running', 'processing'].includes(status)) return 'info' as const
  return 'neutral' as const
}

function TaskKindIcon({ kind }: { kind: Task['kind'] }) {
  if (kind === 'image') return <Image size={14} />
  if (kind === 'video') return <Video size={14} />
  return <Sparkles size={14} />
}

function AttentionIcon({ tone }: { tone: Tone }) {
  if (tone === 'danger') return <UserX size={15} />
  if (tone === 'warning') return <AlertTriangle size={15} />
  if (tone === 'success') return <CheckCircle2 size={15} />
  return <Gauge size={15} />
}

export default function Dashboard() {
  const { data, isLoading, error, refetch, isFetching } = useQuery({ queryKey: ['overview'], queryFn: ({ signal }) => api<Overview>('/admin/overview', { signal }), refetchInterval: 15_000 })
  const accountQuery = useQuery({ queryKey: ['accounts'], queryFn: ({ signal }) => api<{ accounts: Account[] }>('/admin/accounts', { signal }), staleTime: 15_000 })

  if (isLoading) return <PageSkeleton />
  if (!data) return <div className="page-stack"><PageHeader title="服务总览" description="账号、任务与上游运行状态" kicker="工作台" /><Panel><ErrorState message={error instanceof Error ? error.message : '暂无数据'} retry={() => refetch()} /></Panel></div>

  const accounts = data.account_stats ?? {}
  const tasks = data.task_stats ?? {}
  const allAccounts = accountQuery.data?.accounts ?? []
  const browser = data.browser_enabled ? (data.browser_running ? '已连接' : '待启动') : '已禁用'
  const failed = data.tasks.filter(task => task.status === 'failed').length
  const activeAccounts = accounts.active ?? 0
  const totalAccounts = accounts.total ?? 0
  const awaiting = totalAccounts === 0

  const attention: Attention[] = []
  if (totalAccounts === 0) attention.push({ id: 'accounts-empty', tone: 'warning', title: '还没有可用账号', detail: '导入已登录的 Muse 会话后，生成任务才能开始调度。', meta: '账号', to: '/accounts' })
  for (const account of allAccounts.filter(item => ['error', 'expired'].includes(item.status)).slice(0, 3)) {
    attention.push({ id: `account-${account.id}`, tone: account.status === 'expired' ? 'warning' : 'danger', title: `${account.label} 需要重新登录`, detail: account.last_error || (account.status === 'expired' ? '会话已失效，刷新凭据后恢复调度。' : '账号检测异常，查看详情后处理。'), meta: accountStatusText[account.status] ?? '账号', to: `/accounts?focus=${encodeURIComponent(account.id)}` })
  }
  for (const account of allAccounts) {
    const remaining = remainingPercent(account)
    if (remaining === undefined || remaining > 20 || ['error', 'expired', 'disabled'].includes(account.status)) continue
    attention.push({ id: `quota-${account.id}`, tone: remaining <= 8 ? 'danger' : 'warning', title: `${account.label} 额度偏低`, detail: `每周额度剩余 ${Math.round(remaining)}%，额度用完后任务会排队或失败。`, meta: '额度', to: `/accounts?focus=${encodeURIComponent(account.id)}` })
  }
  for (const task of data.tasks.filter(item => item.status === 'failed').slice(0, 3)) {
    attention.push({ id: `task-${task.id}`, tone: 'danger', title: task.prompt || '生成任务失败', detail: task.error || '任务执行失败，查看错误后重试。', meta: '任务', to: `/tasks?focus=${encodeURIComponent(task.id)}` })
  }
  if ((tasks.queued ?? 0) > 0) attention.push({ id: 'queue', tone: 'info', title: `${tasks.queued} 个任务在排队`, detail: '队列正在等待可用账号或并发槽位。', meta: '任务', to: '/tasks?status=queued' })
  if (data.browser_enabled === false) attention.push({ id: 'browser-off', tone: 'info', title: '浏览器运行时已禁用', detail: '当前只能查看数据；需要生成任务时请在服务端启用浏览器运行时。', meta: '运行时', to: '/settings' })

  const visibleAttention = attention.slice(0, 7)
  const imageRate = percent(tasks.image_succeeded ?? 0, tasks.image_total ?? 0)
  const videoRate = percent(tasks.video_succeeded ?? 0, tasks.video_total ?? 0)
  const statusLabel = awaiting ? '服务已就绪，等待账号' : data.status === 'ok' ? '服务运行正常' : '服务状态待检查'

  return <div className="page-stack">
    <PageHeader title="服务总览" description="账号、任务与上游运行状态；先处理阻塞项，再看实时执行。" kicker="工作台" actions={<>
      <Button variant="secondary" onClick={() => refetch()} disabled={isFetching}><RefreshCw size={14} className={isFetching ? 'spin' : ''} />刷新</Button>
      <Link className="button button-primary" to="/playground"><Sparkles size={14} />打开调试台</Link>
    </>} />
    {error && <RefreshError message={error instanceof Error ? error.message : '无法刷新'} retry={() => refetch()} />}

    <section className="command-band" aria-label="服务状态">
      <div className="command-band-lead">
        <span className="live-pill"><span className={`live-dot ${awaiting || failed > 0 ? 'warn' : ''}`} />{awaiting ? '待配置' : failed > 0 ? `${failed} 项失败` : '运行中'}</span>
        <h2>{statusLabel}</h2>
        <p>Chromium {browser} · <span className="mono">v{data.version}</span> · 每 15 秒同步</p>
      </div>
      <div className="command-band-stats">
        <div className="band-metric"><span>可用账号</span><strong>{activeAccounts}<small>/ {totalAccounts}</small></strong></div>
        <div className="band-metric"><span>执行中</span><strong>{tasks.running ?? 0}<small>{tasks.queued ?? 0} 排队</small></strong></div>
        <div className="band-metric"><span>今日请求</span><strong>{tasks.requests_today ?? 0}<small>24 小时</small></strong></div>
        <div className="band-metric"><span>生成成功率</span><strong>{imageRate}<small>视频 {videoRate}</small></strong></div>
      </div>
      <div className="command-band-actions">
        <span className="band-footnote"><strong>{failed}</strong> 个失败 · <strong>{attention.length}</strong> 项待处理</span>
        <Button size="sm" variant="secondary" onClick={() => refetch()} disabled={isFetching}><RefreshCw size={13} className={isFetching ? 'spin' : ''} />同步</Button>
      </div>
    </section>

    <div className="overview-grid">
      <Panel className="queue-panel">
        <div className="section-head"><div><h2>需要处理 <span className="section-title-count" aria-hidden="true">{attention.length}</span></h2><p>会影响账号可用性或任务结果的事项</p></div><Link className="text-link" to="/accounts">账号池<ArrowUpRight size={12} /></Link></div>
        {visibleAttention.length === 0
          ? <div className="queue-list"><div className="queue-row"><span className="icon-tile success"><CheckCircle2 size={15} /></span><div className="queue-main"><div className="queue-title">当前没有需要处理的事项</div><div className="queue-detail">账号、队列与最近任务都处于正常范围。</div></div><span className="queue-meta">正常</span><ArrowRight size={14} className="row-arrow" /></div></div>
          : <div className="queue-list">{visibleAttention.map(item => <Link className="queue-row" to={item.to} key={item.id}>
            <span className={`icon-tile ${item.tone}`}><AttentionIcon tone={item.tone} /></span>
            <div className="queue-main"><div className="queue-title">{item.title}</div><div className="queue-detail">{item.detail}</div></div>
            <span className="queue-meta">{item.meta}</span>
            <ArrowRight size={14} className="row-arrow" />
          </Link>)}</div>}
        <div className="panel-note">额度低于 20% 的账号会自动出现在这里；账号检测后同步官方额度。</div>
      </Panel>

      <Panel className="activity-panel">
        <div className="section-head"><div><h2>最近活动</h2><p>服务端事件与上游状态变化</p></div><Link className="text-link" to="/logs">审计日志<ArrowUpRight size={12} /></Link></div>
        {data.events.length === 0
          ? <Empty title="暂无事件" description="管理员操作、账号检测和任务执行会记录在这里。" />
          : <div className="activity-list">{data.events.slice(0, 7).map(event => <div className="activity-row" key={event.id}>
            <span className={`activity-marker ${event.level}`} />
            <div className="activity-main"><div className="activity-message">{event.message}</div><div className="activity-meta">{event.type}{event.upstream_status ? ` · 上游 ${event.upstream_status}` : ''}</div></div>
            <span className="activity-time">{dateTime(Date.parse(event.time) / 1000)}</span>
          </div>)}</div>}
      </Panel>
    </div>

    <Panel className="recent-panel">
      <div className="section-head"><div><h2>最近任务</h2><p>新提交的任务与执行结果</p></div><Link className="text-link" to="/tasks">全部任务<ArrowUpRight size={12} /></Link></div>
      {data.tasks.length === 0 ? <Empty title="暂无任务" description={awaiting ? '先导入一个已登录的 Muse 账号，生成任务才能开始调度。' : '通过 API 或调试台发起任务后，这里会显示执行状态。'} action={awaiting ? <Link className="button button-secondary" to="/accounts"><Users size={14} />导入第一个账号</Link> : <Link className="button button-secondary" to="/playground"><Sparkles size={14} />发起第一个任务</Link>} /> : <>
        <div className="recent-task-list">
          {data.tasks.slice(0, 6).map(task => <Link className="recent-task-row" to={`/tasks?focus=${encodeURIComponent(task.id)}`} key={task.id}>
            <span className={`task-kind-icon ${task.kind}`}><TaskKindIcon kind={task.kind} /></span>
            <div className="recent-task-main"><strong>{task.prompt || '无提示词'}</strong><span>{task.kind === 'image' ? '图片' : task.kind === 'video' ? '视频' : '对话'} · <span className="mono">{task.id}</span>{task.account ? ` · ${task.account}` : ''}</span></div>
            <span className="recent-task-time">{dateTime(task.created_at)}</span>
            <Badge tone={taskTone(task.status)}>{taskStatusText[task.status] ?? task.status}</Badge>
          </Link>)}
        </div>
        <div className="dashboard-foot"><span><Users size={12} />{allAccounts.length} 个账号已导入</span><span><Image size={12} />{data.media_count} 个媒体文件</span><span><ListChecks size={12} />{tasks.completed ?? 0} 个任务已完成</span></div>
      </>}
    </Panel>
  </div>
}
