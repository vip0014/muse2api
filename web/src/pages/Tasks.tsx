import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Ban, Clock3, Eye, Image, ListChecks, RefreshCw, Sparkles, Trash2, Video } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { Task } from '../lib/types'
import { dateTime, duration, fullDate, taskStatusText } from '../lib/format'
import { taskRefetchInterval } from '../lib/tasks'
import { listKeyDown, useMediaQuery } from '../lib/hooks'
import { Badge, Button, CopyButton, Empty, ErrorState, Loading, PageHeader, RefreshError, SearchField, Select } from '../components/UI'
import { Confirm } from '../components/Dialog'

const ACTIVE = ['queued', 'running', 'processing']
const tone = (status: string) => status === 'failed' ? 'danger' : ['completed', 'succeeded'].includes(status) ? 'success' : ['running', 'processing'].includes(status) ? 'info' : 'neutral'
const stateClass = (status: string) => status === 'failed' ? 'danger' : ['completed', 'succeeded'].includes(status) ? 'success' : ['running', 'processing'].includes(status) ? 'info' : ''
const meterTone = (status: string) => status === 'failed' ? 'danger' : ['completed', 'succeeded'].includes(status) ? '' : ['running', 'processing'].includes(status) ? 'accent' : 'warn'
const kindText = (kind: Task['kind']) => kind === 'image' ? '图片' : kind === 'video' ? '视频' : '对话'
const normalizeStatus = (value: string) => value === 'processing' ? 'running' : value === 'completed' ? 'succeeded' : value

const filters = [
  { key: '', label: '全部' },
  { key: 'queued', label: '排队' },
  { key: 'running', label: '执行中' },
  { key: 'succeeded', label: '成功' },
  { key: 'failed', label: '失败' },
  { key: 'cancelled', label: '已取消' },
  { key: 'unknown', label: '未知' },
]

function KindIcon({ kind }: { kind: Task['kind'] }) {
  if (kind === 'image') return <Image size={14} />
  if (kind === 'video') return <Video size={14} />
  return <Sparkles size={14} />
}

export default function Tasks({ initialSearch = '', initialStatus = '', initialKind = '', initialFocus }: { initialSearch?: string; initialStatus?: string; initialKind?: string; initialFocus?: string } = {}) {
  const [search, setSearch] = useState(initialSearch)
  const [kind, setKind] = useState(initialKind)
  const [status, setStatus] = useState(normalizeStatus(initialStatus))
  const [selectedId, setSelectedId] = useState<string | undefined>(initialFocus)
  const [cancel, setCancel] = useState<Task>()
  const [remove, setRemove] = useState<Task>()
  const [autoOpened, setAutoOpened] = useState(false)
  const isMobile = useMediaQuery('(max-width: 960px)')
  const isWide = useMediaQuery('(min-width: 961px)')
  const client = useQueryClient()

  const { data, isLoading, isFetching, error, refetch } = useQuery({ queryKey: ['tasks'], queryFn: ({ signal }) => api<{ tasks: Task[] }>('/admin/tasks?limit=500', { signal }), refetchInterval: query => taskRefetchInterval(query.state.data?.tasks) })
  useEffect(() => { if (initialFocus) setSelectedId(initialFocus) }, [initialFocus])
  useEffect(() => {
    if (!isMobile) return
    const handler = (event: KeyboardEvent) => { if (event.key === 'Escape') setSelectedId(undefined) }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [isMobile])
  useEffect(() => {
    if (!isWide || autoOpened || initialFocus || !data?.tasks.length) return
    setSelectedId(data.tasks[0].id)
    setAutoOpened(true)
  }, [autoOpened, data, initialFocus, isWide])

  const tasks = useMemo(() => (data?.tasks ?? []).filter(task => (!search || `${task.id} ${task.prompt}`.toLowerCase().includes(search.toLowerCase())) && (!kind || task.kind === kind) && (!status || (status === 'running' ? ['running', 'processing'].includes(task.status) : status === 'succeeded' ? ['succeeded', 'completed'].includes(task.status) : task.status === status))), [data, search, kind, status])
  const allTasks = data?.tasks ?? []
  const active = tasks.find(task => task.id === selectedId) ?? (isWide ? tasks[0] : undefined)
  const statusCount = (key: string) => {
    if (key === 'running') return allTasks.filter(task => ['running', 'processing'].includes(task.status)).length
    if (key === 'succeeded') return allTasks.filter(task => ['succeeded', 'completed'].includes(task.status)).length
    return key ? allTasks.filter(task => task.status === key).length : allTasks.length
  }
  const hasActive = allTasks.some(task => ACTIVE.includes(task.status))
  const loadError = Boolean(error && !data)
  const refreshError = Boolean(error && data)
  const single = Boolean(loadError || isLoading || tasks.length === 0)
  const mutation = useMutation({
    mutationFn: ({ task, action }: { task: Task; action: 'cancel' | 'delete' }) => api(`/admin/tasks/${task.id}${action === 'cancel' ? '/cancel' : ''}`, { method: action === 'cancel' ? 'POST' : 'DELETE' }),
    onSuccess: (_, variables) => {
      client.invalidateQueries({ queryKey: ['tasks'] })
      client.invalidateQueries({ queryKey: ['overview'] })
      setCancel(undefined); setRemove(undefined)
      if (variables.action === 'delete' && selectedId === variables.task.id) setSelectedId(undefined)
    },
  })

  const openResult = (task: Task) => {
    if (!task.url) return
    // Same-origin media endpoint: let the browser stream/seek instead of buffering through fetch + blob.
    window.open(task.url, '_blank', 'noopener,noreferrer')
  }

  return <div className="page-stack">
    <PageHeader title="任务中心" description="生成任务的排队、执行与结果；选中任意一条查看完整上下文。" kicker="运行" actions={<Button variant="secondary" onClick={() => refetch()} disabled={isFetching}><RefreshCw size={15} className={isFetching ? 'spin' : ''} />刷新</Button>} />
    {refreshError && <RefreshError message={error instanceof Error ? error.message : '无法刷新任务列表'} retry={() => refetch()} />}

    <section className={`workbench tasks-workbench ${single ? 'single' : ''}`} aria-label="任务工作台" aria-busy={isLoading}>
      <div className="collection-pane">
        <div className="collection-toolbar task-collection-toolbar">
          <div className="collection-toolbar-row">
            <SearchField value={search} onChange={setSearch} placeholder="搜索提示词或任务 ID" label="搜索任务" />
            <Select value={kind} onChange={event => setKind(event.target.value)} aria-label="任务类型"><option value="">全部类型</option><option value="chat">对话</option><option value="image">图片</option><option value="video">视频</option></Select>
          </div>
        </div>
        <div className="filter-chips" role="group" aria-label="任务状态">
          {filters.map(item => <button key={item.key || 'all'} aria-pressed={status === item.key} className={`filter-chip ${status === item.key ? 'active' : ''}`} onClick={() => setStatus(item.key)}>{item.label}<b>{statusCount(item.key)}</b></button>)}
        </div>
        <div className="collection-meta">
          <span>{isLoading ? '正在读取…' : loadError ? '读取失败' : `${tasks.length} 条记录`}</span>
          {isLoading ? <span>请稍候</span> : hasActive ? <span className="meta-live"><span className="live-dot" />实时同步</span> : <span>空闲时每 30 秒同步</span>}
        </div>
        {isLoading ? <Loading /> : loadError ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : tasks.length === 0 ? <Empty title="暂无任务" description={search || kind || status ? '调整筛选条件，或通过 API 发起任务。' : '通过 API 或调试台发起任务，执行状态会显示在这里。'} action={!search && !kind && !status ? <Link className="button button-secondary" to="/playground">去调试台发起</Link> : undefined} /> : <div className="collection-list" aria-label="任务列表" onKeyDown={listKeyDown}>
          {tasks.map(task => <div key={task.id} className={`task-row ${active?.id === task.id ? 'selected' : ''}`} data-list-row role="button" tabIndex={0} onClick={() => setSelectedId(task.id)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setSelectedId(task.id) } }} aria-label={`查看任务 ${task.prompt || task.id}`}>
            <span className={`task-kind-icon ${task.kind}`}><KindIcon kind={task.kind} /></span>
            <div className="task-row-main"><div className="task-prompt">{task.prompt || '无提示词'}</div><div className="task-meta"><span className="mono">{task.id}</span><span className="task-meta-sep">·</span><span className="task-meta-kind">{kindText(task.kind)}</span><span className="task-meta-sep task-meta-account-sep">·</span><span className="task-meta-account">{task.account || '未分配账号'}</span></div></div>
            <span className={`task-state ${stateClass(task.status)}`}><span className="status-dot" />{taskStatusText[task.status] ?? task.status}</span>
            <div className="task-progress"><div className={`meter ${meterTone(task.status)}`} aria-hidden="true"><span style={{ width: `${task.progress}%` }} /></div><small>{task.progress}%</small></div>
            <span className="task-time">{dateTime(task.created_at)}</span>
          </div>)}
        </div>}
        {!isLoading && !loadError && tasks.length > 0 && <div className="collection-foot"><span>↑↓ 切换 · Enter 查看</span><span>{search || kind || status ? '筛选结果' : '全部任务'}</span></div>}
      </div>

      {tasks.length > 0 && !loadError && <>
        {isMobile && active && <button className="detail-scrim" aria-label="关闭任务详情" onClick={() => setSelectedId(undefined)} />}
        <section className={`inspector-pane task-detail ${active ? 'open' : ''}`} aria-label="任务详情">
          {active ? <TaskInspector task={active} isMobile={isMobile} onClose={() => setSelectedId(undefined)} onCancel={setCancel} onRemove={setRemove} onOpenResult={openResult} /> : <div className="task-detail-empty"><span className="state-icon"><ListChecks size={20} /></span><h3>选择一条任务</h3><p>查看完整提示词、账号、耗时和生成结果。列表会随任务进度自动更新。</p></div>}
        </section>
      </>}
    </section>

    <Confirm open={Boolean(cancel)} onOpenChange={() => setCancel(undefined)} title="取消任务？" description="取消本地任务；上游已开始的生成可能继续。" busy={mutation.isPending} onConfirm={() => cancel && mutation.mutate({ task: cancel, action: 'cancel' })} />
    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title="删除任务记录？" description="仅删除任务记录，媒体文件保留。" busy={mutation.isPending} onConfirm={() => remove && mutation.mutate({ task: remove, action: 'delete' })} />
  </div>
}

function TaskInspector({ task, isMobile, onClose, onCancel, onRemove, onOpenResult }: { task: Task; isMobile: boolean; onClose: () => void; onCancel: (task: Task) => void; onRemove: (task: Task) => void; onOpenResult: (task: Task) => void }) {
  const steps = [
    { label: '提交', time: task.created_at },
    { label: '开始', time: task.started_at },
    { label: '完成', time: task.completed_at },
  ]

  return <>
    <div className="inspector-head">
      <div className="inspector-title-block">
        <span className="page-kicker">任务详情</span>
        <div className="inspector-title"><h2>{kindText(task.kind)}任务</h2></div>
        <div className="inspector-sub"><span className="mono">{task.id}</span><CopyButton value={task.id} label="复制 ID" /></div>
      </div>
      <div className="task-status-summary">
        <div className={`task-dial ${stateClass(task.status)}`} style={{ '--p': task.progress } as CSSProperties} role="img" aria-label={`进度 ${task.progress}%`}><div className="task-dial-copy"><strong>{task.progress}%</strong><span>进度</span></div></div>
        <div className="task-status-copy"><Badge tone={tone(task.status)}>{taskStatusText[task.status] ?? task.status}</Badge><small>{dateTime(task.updated_at)} 更新</small></div>
      </div>
      {isMobile && <div className="inspector-head-actions"><button className="button button-secondary button-sm detail-drawer-close" onClick={onClose}><ArrowLeft size={14} />返回</button></div>}
    </div>
    <div className="inspector-body">
      <section className="detail-section">
        <div className="detail-section-title"><span>执行状态</span><span className="title-hint">{task.progress}%</span></div>
        <div className="task-timeline">
          {steps.map(step => <div key={step.label} className={`timeline-step ${step.time ? 'done' : ''}`}><span>{step.label}</span><strong>{step.time ? dateTime(step.time) : '—'}</strong></div>)}
        </div>
        <div className="detail-grid" style={{ marginTop: 17 }}>
          <Detail label="类型" value={kindText(task.kind)} />
          <Detail label="账号" value={task.account || '未分配'} />
          <Detail label="耗时" value={duration(task.elapsed)} />
          <Detail label="提交时间" value={fullDate(task.created_at)} />
          <Detail label="开始时间" value={fullDate(task.started_at)} />
          <Detail label="完成时间" value={fullDate(task.completed_at)} />
          {task.kind === 'video' && task.result?.duration_ms ? <Detail label="视频时长" value={`${(Number(task.result.duration_ms) / 1000).toFixed(1)} 秒`} /> : null}
          {task.kind === 'video' && task.result?.width ? <Detail label="视频分辨率" value={`${String(task.result.width)}×${String(task.result.height)}`} /> : null}
        </div>
      </section>

      {task.error && <section className="detail-section"><div className="detail-section-title"><span>错误</span></div><div className="error-box">{task.error}</div></section>}

      {task.url && <section className="detail-section">
        <div className="detail-section-title"><span>结果</span><span className="title-hint">{kindText(task.kind)}</span></div>
        <div className="task-result-preview">{task.kind === 'image' ? <img src={task.url} alt="任务结果" loading="lazy" /> : task.kind === 'video' ? <video src={task.url} controls playsInline preload="metadata" /> : null}</div>
        <div className="task-result-actions"><Button variant="secondary" size="sm" onClick={() => onOpenResult(task)}><Eye size={13} />打开结果</Button></div>
      </section>}

      <section className="detail-section">
        <div className="detail-section-title"><span>提示词</span><CopyButton value={task.prompt || ''} label="复制" /></div>
        <div className="task-prompt-block">{task.prompt || '无提示词'}</div>
      </section>
    </div>
    <div className="inspector-foot">
      {ACTIVE.includes(task.status)
        ? (task.kind !== 'chat' ? <Button variant="secondary" size="sm" onClick={() => onCancel(task)}><Ban size={13} />取消任务</Button> : <span className="secondary-cell">对话任务随请求结束</span>)
        : <Button variant="danger" size="sm" onClick={() => onRemove(task)}><Trash2 size={13} />删除记录</Button>}
      <span className="spacer" />
      <span className="secondary-cell"><Clock3 size={11} />{dateTime(task.updated_at)} 更新</span>
    </div>
  </>
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span className="detail-label">{label}</span><div className="detail-value">{value}</div></div> }
