import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ArrowLeft, Check, Copy, Download, Eye, Image, Play, RefreshCw, Trash2, Video } from 'lucide-react'
import { Link } from 'react-router-dom'
import { api } from '../lib/api'
import type { MediaItem } from '../lib/types'
import { bytes, dateTime, fullDate } from '../lib/format'
import { listKeyDown, useMediaQuery } from '../lib/hooks'
import { Button, Empty, ErrorState, Loading, PageHeader, RefreshError, SearchField, Select } from '../components/UI'
import { Confirm } from '../components/Dialog'

const kindText = (kind: MediaItem['kind']) => kind === 'image' ? '图片' : '视频'

export default function MediaLibrary() {
  const [search, setSearch] = useState('')
  const [kind, setKind] = useState('')
  const [selectedName, setSelectedName] = useState<string>()
  const [remove, setRemove] = useState<MediaItem>()
  const [copiedName, setCopiedName] = useState('')
  const isMobile = useMediaQuery('(max-width: 960px)')
  const isWide = useMediaQuery('(min-width: 961px)')
  const client = useQueryClient()
  const { data, isLoading, isFetching, error, refetch } = useQuery({ queryKey: ['media'], queryFn: ({ signal }) => api<{ media: MediaItem[] }>('/admin/media', { signal }) })
  const deleteMutation = useMutation({
    mutationFn: (item: MediaItem) => api(`/admin/media/${encodeURIComponent(item.name)}`, { method: 'DELETE' }),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['media'] })
      client.invalidateQueries({ queryKey: ['overview'] })
      setRemove(undefined)
      setSelectedName(undefined)
    },
  })
  const items = useMemo(() => (data?.media ?? []).filter(item => (!search || item.name.toLowerCase().includes(search.toLowerCase())) && (!kind || item.kind === kind)), [data, search, kind])
  const active = items.find(item => item.name === selectedName) ?? (isWide ? items[0] : undefined)
  const loadError = Boolean(error && !data)
  const refreshError = Boolean(error && data)
  const single = Boolean(loadError || isLoading || items.length === 0)

  useEffect(() => {
    if (!isMobile) return
    const handler = (event: KeyboardEvent) => { if (event.key === 'Escape') setSelectedName(undefined) }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [isMobile])

  const copy = async (item: MediaItem) => {
    try {
      await navigator.clipboard.writeText(`${location.origin}${item.url}`)
      setCopiedName(item.name)
      window.setTimeout(() => setCopiedName(''), 1800)
    } catch { /* Clipboard may be unavailable outside a secure context. */ }
  }

  return <div className="page-stack">
    <PageHeader title="媒体库" description="已生成的图片与视频；选中后可直接预览、复制链接或清理。" kicker="产物" actions={<Button variant="secondary" onClick={() => refetch()} disabled={isFetching}><RefreshCw size={15} className={isFetching ? 'spin' : ''} />刷新</Button>} />
    {refreshError && <RefreshError message={error instanceof Error ? error.message : '无法刷新媒体列表'} retry={() => refetch()} />}

    <section className={`workbench media-workbench ${single ? 'single' : ''}`} aria-label="媒体工作台" aria-busy={isLoading}>
      <div className="collection-pane">
        <div className="media-toolbar">
          <SearchField value={search} onChange={setSearch} placeholder="搜索文件名" label="搜索媒体" />
          <Select value={kind} onChange={event => setKind(event.target.value)} aria-label="媒体类型"><option value="">全部类型</option><option value="image">图片</option><option value="video">视频</option></Select>
          <span className="toolbar-count">{isLoading ? '正在读取…' : loadError ? '读取失败' : `${items.length} 个文件`}</span>
        </div>
        {isLoading ? <Loading /> : loadError ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : items.length === 0 ? <Empty title="媒体库为空" description="生成并下载成功的媒体会出现在这里。" action={<Link className="button button-secondary" to="/playground">去调试台生成</Link>} /> : <div className="media-scroll">
          <div className="media-grid" aria-label="媒体列表" onKeyDown={listKeyDown}>
            {items.map(item => <article className={`media-tile ${active?.name === item.name ? 'selected' : ''}`} key={item.name}>
              <button className="media-preview" data-list-row onClick={() => setSelectedName(item.name)} aria-label={`打开预览 ${item.name}`} aria-current={active?.name === item.name ? 'true' : undefined}>
                <MediaThumb item={item} />
                <span className="media-kind">{item.kind === 'image' ? <Image size={10} /> : <Video size={10} />}{kindText(item.kind)}</span>
                {item.kind === 'video' && <span className="media-play" aria-hidden="true"><Play size={14} fill="currentColor" /></span>}
                <span className="media-hover"><Eye size={20} /></span>
              </button>
              <div className="media-caption">
                <strong title={item.name}>{item.name}</strong>
                <span>{bytes(item.bytes)} · {dateTime(item.mtime)}</span>
                <div className="media-actions">
                  <button onClick={() => void copy(item)} aria-label={`复制 ${item.name} 链接`} title="复制链接">{copiedName === item.name ? <Check size={13} /> : <Copy size={13} />}</button>
                  <button className="danger" onClick={() => setRemove(item)} aria-label={`删除 ${item.name}`} title="删除"><Trash2 size={13} /></button>
                </div>
              </div>
            </article>)}
          </div>
        </div>}
        {!isLoading && !loadError && items.length > 0 && <div className="collection-foot"><span>↑↓ 浏览 · Enter 预览</span><span>{search || kind ? '筛选结果' : '全部文件'}</span></div>}
      </div>

      {items.length > 0 && !loadError && <>
        {isMobile && active && <button className="detail-scrim" aria-label="关闭媒体详情" onClick={() => setSelectedName(undefined)} />}
        <section className={`inspector-pane media-detail ${active ? 'open' : ''}`} aria-label="媒体详情">
          {active ? <>
            <div className="inspector-head">
              <div className="inspector-title-block">
                <span className="page-kicker">媒体详情</span>
                <div className="inspector-title"><h2 className="media-name" title={active.name}>{active.name}</h2></div>
                <div className="media-meta-line"><span>{kindText(active.kind)}</span><span>·</span><span className="mono">{active.mime}</span></div>
              </div>
              <div className="inspector-head-actions">
                {isMobile && <button className="button button-secondary button-sm detail-drawer-close" onClick={() => setSelectedName(undefined)}><ArrowLeft size={14} />返回</button>}
                <Button variant="secondary" size="sm" onClick={() => void copy(active)}>{copiedName === active.name ? <Check size={13} /> : <Copy size={13} />}{copiedName === active.name ? '已复制' : '复制链接'}</Button>
              </div>
            </div>
            <div className="inspector-body">
              <section className="detail-section">
                <div className="detail-section-title"><span>预览</span><span className="title-hint">{kindText(active.kind)}</span></div>
                <div className="media-detail-preview">{active.kind === 'image' ? <img src={active.url} alt={active.name} /> : <video src={active.url} controls playsInline preload="metadata" />}</div>
              </section>
              <section className="detail-section">
                <div className="detail-section-title"><span>文件信息</span></div>
                <div className="detail-grid two">
                  <Detail label="大小" value={bytes(active.bytes)} />
                  <Detail label="修改时间" value={fullDate(active.mtime)} />
                  <Detail label="类型" value={active.mime} />
                  <div><span className="detail-label">关联任务</span><div className="detail-value">{active.task_id ? <Link className="text-link mono" to={`/tasks?focus=${encodeURIComponent(active.task_id)}`}>{active.task_id}</Link> : '—'}</div></div>
                </div>
              </section>
            </div>
            <div className="inspector-foot">
              <a className="button button-secondary button-sm" href={active.url} download={active.name}><Download size={13} />下载</a>
              <Button variant="secondary" size="sm" onClick={() => void copy(active)}><Copy size={13} />复制链接</Button>
              <span className="spacer" />
              <Button variant="danger" size="sm" onClick={() => setRemove(active)}><Trash2 size={13} />删除文件</Button>
            </div>
          </> : <div className="media-detail-empty"><span className="state-icon"><Image size={20} /></span><h3>选择一个媒体文件</h3><p>查看完整预览、文件信息和任务关联。图片与视频会直接从这里流式加载。</p></div>}
        </section>
      </>}
    </section>

    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title="删除媒体文件？" description={`${remove?.name ?? ''} 将被永久删除，任务记录保留。`} busy={deleteMutation.isPending} onConfirm={() => remove && deleteMutation.mutate(remove)} />
  </div>
}

function MediaThumb({ item }: { item: MediaItem }) {
  if (item.kind === 'video') return <video src={item.url} muted playsInline preload="metadata" />
  return <img src={item.url} alt="" loading="lazy" />
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span className="detail-label">{label}</span><div className="detail-value">{value}</div></div> }
