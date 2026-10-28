import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Ban, Clock3, Eye, Image, ListChecks, RefreshCw, Search, Sparkles, Trash2, Video, X } from 'lucide-react'
import { api } from '../lib/api'
import type { Task } from '../lib/types'
import { dateTime, duration, fullDate, taskStatusText } from '../lib/format'
import { taskRefetchInterval } from '../lib/tasks'
import { useMediaQuery } from '../lib/hooks'
import { Badge, Button, Empty, ErrorState, Input, Loading, PageHeader, Panel, Select } from '../components/UI'
import { Confirm } from '../components/Dialog'

const tone = (status: string) => status === 'failed' ? 'danger' : ['completed', 'succeeded'].includes(status) ? 'success' : ['running', 'processing'].includes(status) ? 'info' : 'neutral'
const progressTone = (status: string) => status === 'failed' ? 'danger' : ['completed', 'succeeded'].includes(status) ? 'success' : ['running', 'processing'].includes(status) ? '' : 'warning'

function KindIcon({ kind }: { kind: Task['kind'] }) {
  if (kind === 'image') return <Image size={14} />
  if (kind === 'video') return <Video size={14} />
  return <Sparkles size={14} />
}

export default function Tasks({ initialSearch = '', initialStatus = '', initialKind = '', initialFocus }: { initialSearch?: string; initialStatus?: string; initialKind?: string; initialFocus?: string } = {}) {
  const [search, setSearch] = useState(initialSearch)
  const [kind, setKind] = useState(initialKind)
  const [status, setStatus] = useState(initialStatus)
  const [selectedId, setSelectedId] = useState<string | undefined>(initialFocus)
  const [cancel, setCancel] = useState<Task>()
  const [remove, setRemove] = useState<Task>()
  const [autoOpened, setAutoOpened] = useState(false)
  const isMobile = useMediaQuery('(max-width: 960px)')
  const isWide = useMediaQuery('(min-width: 1100px)')
  const client = useQueryClient()

  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['tasks'], queryFn: ({ signal }) => api<{ tasks: Task[] }>('/admin/tasks?limit=500', { signal }), refetchInterval: query => taskRefetchInterval(query.state.data?.tasks) })
  useEffect(() => { if (initialFocus) setSelectedId(initialFocus) }, [initialFocus])
  useEffect(() => {
    if (!isWide || autoOpened || initialFocus || !data?.tasks.length) return
    setSelectedId(data.tasks[0].id)
    setAutoOpened(true)
  }, [autoOpened, data, initialFocus, isWide])

  const tasks = useMemo(() => (data?.tasks ?? []).filter(task => (!search || `${task.id} ${task.prompt}`.toLowerCase().includes(search.toLowerCase())) && (!kind || task.kind === kind) && (!status || (status === 'running' ? ['running', 'processing'].includes(task.status) : status === 'succeeded' ? ['succeeded', 'completed'].includes(task.status) : task.status === status))), [data, search, kind, status])
  const allTasks = data?.tasks ?? []
  const active = tasks.find(task => task.id === selectedId) ?? (isWide ? tasks[0] : undefined)
  const statusCount = (statuses: string[]) => allTasks.filter(task => statuses.includes(task.status)).length
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
    // The media endpoint is same-origin and protected by the admin cookie;
    // opening it directly lets the browser stream/Range instead of buffering
    // the whole file through fetch + blob.
    window.open(task.url, '_blank', 'noopener,noreferrer')
  }

  return <div className="page-stack">
    <PageHeader title="任务中心" description="生成任务的排队、执行与结果；选中任意一条查看完整上下文。" kicker="运行" actions={<Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button>} />
    <div className={`tasks-workbench ${error || (tasks.length === 0 && !isLoading) ? 'single' : ''}`}>
      <Panel className="task-list-panel">
        <div className="toolbar">
          <div className="search-box"><Search size={16} /><Input placeholder="搜索提示词或任务 ID" value={search} onChange={event => setSearch(event.target.value)} aria-label="搜索任务" /></div>
          <Select value={kind} onChange={event => setKind(event.target.value)} aria-label="任务类型"><option value="">全部类型</option><option value="chat">对话</option><option value="image">图片</option><option value="video">视频</option></Select>
          <Select value={status} onChange={event => setStatus(event.target.value)} aria-label="任务状态"><option value="">全部状态</option><option value="queued">排队中</option><option value="running">执行中</option><option value="succeeded">成功</option><option value="failed">失败</option><option value="cancelled">已取消</option><option value="unknown">结果未知</option></Select>
          <span className="toolbar-count">{error ? '读取失败' : `${tasks.length} 条记录`}</span>
        </div>
        {isLoading ? <Loading /> : error ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : tasks.length === 0 ? <Empty title="暂无任务" description={search || kind || status ? '调整筛选条件，或通过 API 发起任务。' : '通过 API 或调试台发起任务，执行状态会显示在这里。'} /> : <div className="task-list" aria-label="任务列表">
          {tasks.map(task => <div key={task.id} className={`task-row ${active?.id === task.id ? 'selected' : ''}`} role="button" tabIndex={0} onClick={() => setSelectedId(task.id)} onKeyDown={event => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); setSelectedId(task.id) } }}>
            <span className="task-kind-icon"><KindIcon kind={task.kind} /></span>
            <div className="task-row-main"><div className="task-prompt">{task.prompt || '无提示词'}</div><div className="task-meta"><span className="mono">{task.id}</span><span>·</span><span>{task.kind === 'image' ? '图片' : task.kind === 'video' ? '视频' : '对话'}</span><span>·</span><span>{task.account || '未分配账号'}</span></div></div>
            <Badge tone={tone(task.status)}>{taskStatusText[task.status] ?? task.status}</Badge>
            <div className="progress-cell"><div className={`progress-track ${progressTone(task.status)}`}><span style={{ width: `${task.progress}%` }} /></div><small>{task.progress}%</small></div>
            <span className="task-row-cell">{dateTime(task.created_at)}</span>
          </div>)}
        </div>}
      </Panel>

      {tasks.length > 0 && !error && <>
      {isMobile && active && <button className="detail-scrim" aria-label="关闭任务详情" onClick={() => setSelectedId(undefined)} />}
      <Panel className={`task-detail ${active ? 'open' : ''}`}>
        {active ? <>
          <div className="detail-head">
            <div className="detail-title"><span className="page-kicker">任务详情</span><h2>{active.kind === 'image' ? '图片任务' : active.kind === 'video' ? '视频任务' : '对话任务'}<span>{active.id}</span></h2></div>
            <div className="detail-head-actions"><Badge tone={tone(active.status)}>{taskStatusText[active.status] ?? active.status}</Badge><button className="icon-button detail-drawer-close" aria-label="关闭详情" onClick={() => setSelectedId(undefined)}><X size={18} /></button></div>
          </div>
          <div className="detail-body">
            <section className="detail-section">
              <div className="detail-section-title"><span>执行状态</span><span>{active.progress}%</span></div>
              <div className="progress-cell"><div className={`progress-track ${progressTone(active.status)}`}><span style={{ width: `${active.progress}%` }} /></div></div>
              <div className="detail-grid" style={{ marginTop: 12 }}>
                <Detail label="类型" value={active.kind === 'image' ? '图片' : active.kind === 'video' ? '视频' : '对话'} />
                <Detail label="账号" value={active.account || '未分配'} />
                <Detail label="提交时间" value={fullDate(active.created_at)} />
                <Detail label="开始时间" value={fullDate(active.started_at)} />
                <Detail label="完成时间" value={fullDate(active.completed_at)} />
                <Detail label="耗时" value={duration(active.elapsed)} />
                {active.kind === 'video' && active.result?.duration_ms ? <Detail label="视频时长" value={`${(Number(active.result.duration_ms) / 1000).toFixed(1)} 秒`} /> : null}
                {active.kind === 'video' && active.result?.width ? <Detail label="视频分辨率" value={`${String(active.result.width)}×${String(active.result.height)}`} /> : null}
              </div>
            </section>

            <section className="detail-section">
              <div className="detail-section-title"><span>提示词</span></div>
              <div className="task-prompt-block">{active.prompt || '无提示词'}</div>
            </section>

            {active.error && <section className="detail-section"><div className="detail-section-title"><span>错误</span></div><div className="error-box">{active.error}</div></section>}

            {active.url && <section className="detail-section">
              <div className="detail-section-title"><span>结果</span></div>
              <div className="task-result-preview">{active.kind === 'image' ? <img src={active.url} alt="任务结果" loading="lazy" /> : active.kind === 'video' ? <video src={active.url} controls playsInline preload="metadata" /> : null}</div>
              <div className="task-result-actions"><Button variant="secondary" size="sm" onClick={() => openResult(active)}><Eye size={14} />打开结果</Button></div>
            </section>}

            <section className="detail-section">
              <div className="detail-section-title"><span>操作</span></div>
              <div className="detail-actions">
                {['queued', 'running', 'processing'].includes(active.status) ? (active.kind !== 'chat' ? <Button variant="secondary" size="sm" onClick={() => setCancel(active)}><Ban size={14} />取消任务</Button> : null) : <Button variant="danger" size="sm" onClick={() => setRemove(active)}><Trash2 size={14} />删除记录</Button>}
                <span className="secondary-cell"><Clock3 size={12} />{dateTime(active.updated_at)} 更新</span>
              </div>
            </section>
          </div>
        </> : <div className="task-detail-empty"><ListChecks size={24} /><h3>选择一条任务</h3><p>查看完整提示词、账号、耗时和生成结果。列表会随任务进度自动更新。</p><div className="empty-stats"><div><strong>{statusCount(['queued'])}</strong><span>排队</span></div><div><strong>{statusCount(['running', 'processing'])}</strong><span>执行中</span></div><div><strong>{statusCount(['completed', 'succeeded'])}</strong><span>成功</span></div><div><strong>{statusCount(['failed'])}</strong><span>失败</span></div></div></div>}
      </Panel>
      </>}
    </div>

    <Confirm open={Boolean(cancel)} onOpenChange={() => setCancel(undefined)} title="取消任务？" description="取消本地任务；上游已开始的生成可能继续。" busy={mutation.isPending} onConfirm={() => cancel && mutation.mutate({ task: cancel, action: 'cancel' })} />
    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title="删除任务记录？" description="仅删除任务记录，媒体文件保留。" busy={mutation.isPending} onConfirm={() => remove && mutation.mutate({ task: remove, action: 'delete' })} />
  </div>
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span className="detail-label">{label}</span><div className="detail-value">{value}</div></div> }
