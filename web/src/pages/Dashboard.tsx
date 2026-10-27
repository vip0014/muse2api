import { useQuery } from '@tanstack/react-query'
import { ArrowUpRight, CheckCircle2, Clock3, Images, ListTodo, RefreshCw, Users, Video } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Overview } from '../lib/types'
import { dateTime, percent, taskStatusText } from '../lib/format'
import { Badge, Button, Empty, ErrorState, Loading, PageHeader, Panel } from '../components/UI'

export default function Dashboard() {
  const { data, isLoading, error, refetch, isFetching } = useQuery({ queryKey: ['overview'], queryFn: ({ signal }) => api<Overview>('/admin/overview', { signal }), refetchInterval: 15_000 })
  if (isLoading) return <Loading />
  if (error || !data) return <ErrorState message={error instanceof Error ? error.message : '暂无数据'} retry={() => refetch()} />
  const accounts = data.account_stats
  const tasks = data.task_stats
  const failed = data.tasks.filter(t => t.status === 'failed').length
  const browser = data.browser_enabled ? (data.browser_running ? '已连接' : '待启动') : '已禁用'
  return <div className="page-stack">
    <PageHeader title="服务总览" actions={<Button variant="secondary" onClick={() => refetch()} disabled={isFetching}><RefreshCw size={15} className={isFetching ? 'spin' : ''} />刷新</Button>} />
    <Panel className="status-strip">
      <div className="status-main"><span className="pulse-dot" /><strong>服务运行正常</strong><span className="status-version">v{data.version}</span></div>
      <div className="status-meta"><span>浏览器 <strong>{browser}</strong></span></div>
    </Panel>
    <div className="metric-grid">
      <Link to="/accounts" className="metric"><div className="metric-top"><span>可用账号</span><span className="metric-icon"><Users size={15} /></span></div><div className="metric-value">{accounts.active ?? 0}<small>/ {accounts.total ?? 0}</small></div><div className="metric-foot"><span>{accounts.error ?? 0} 个异常</span><ArrowUpRight size={14} /></div></Link>
      <Link to="/tasks" className="metric"><div className="metric-top"><span>任务负载</span><span className="metric-icon"><ListTodo size={15} /></span></div><div className="metric-value">{tasks.running ?? 0}<small>执行中</small></div><div className="metric-foot"><span>{tasks.queued ?? 0} 排队 · {failed} 失败</span><ArrowUpRight size={14} /></div></Link>
      <div className="metric"><div className="metric-top"><span>今日请求</span><span className="metric-icon"><Clock3 size={15} /></span></div><div className="metric-value">{tasks.requests_today ?? 0}<small>24 小时</small></div><div className="metric-foot"><span>{data.media_count} 个媒体文件</span></div></div>
      <div className="metric"><div className="metric-top"><span>生成成功率</span><span className="metric-icon"><CheckCircle2 size={15} /></span></div><div className="metric-value compact"><span><Images size={15} />{percent(tasks.image_succeeded ?? 0, tasks.image_total ?? 0)}</span><span><Video size={15} />{percent(tasks.video_succeeded ?? 0, tasks.video_total ?? 0)}</span></div><div className="metric-foot"><span>图片 / 视频</span></div></div>
    </div>
    <div className="dashboard-grid">
      <Panel><div className="section-head"><h2>最近任务</h2><Link className="text-link" to="/tasks">全部<ArrowUpRight size={13} /></Link></div>{data.tasks.length === 0 ? <Empty title="暂无任务" /> : <div className="table-wrap"><table><thead><tr><th>任务</th><th>类型</th><th>状态</th><th>提交时间</th></tr></thead><tbody>{data.tasks.slice(0, 6).map(task => <tr key={task.id}><td><div className="primary-cell">{task.prompt || task.id}</div><div className="secondary-cell mono">{task.id}</div></td><td>{task.kind === 'image' ? '图片' : task.kind === 'video' ? '视频' : '对话'}</td><td><Badge tone={task.status === 'failed' ? 'danger' : ['completed', 'succeeded'].includes(task.status) ? 'success' : 'neutral'}>{taskStatusText[task.status]}</Badge></td><td className="text-muted nowrap">{dateTime(task.created_at)}</td></tr>)}</tbody></table></div>}</Panel>
      <Panel><div className="section-head"><h2>系统事件</h2><Link className="text-link" to="/logs">日志<ArrowUpRight size={13} /></Link></div>{data.events.length === 0 ? <Empty title="暂无事件" /> : <div className="event-list">{data.events.slice(0, 6).map(event => <div className="event-item" key={event.id}><span className={`event-marker ${event.level}`} /><div><strong>{event.message}</strong><p>{event.type} · {new Date(event.time).toLocaleString('zh-CN')}</p></div></div>)}</div>}</Panel>
    </div>
  </div>
}
