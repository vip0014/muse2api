import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Check, Copy, Download, Eye, Image, RefreshCw, Search, Trash2, X } from 'lucide-react'
import { api } from '../lib/api'
import type { MediaItem } from '../lib/types'
import { bytes, fullDate } from '../lib/format'
import { useMediaQuery } from '../lib/hooks'
import { Button, Empty, ErrorState, Input, Loading, PageHeader, Panel, Select, SuccessMessage } from '../components/UI'
import { Confirm } from '../components/Dialog'

export default function MediaLibrary() {
  const [search, setSearch] = useState('')
  const [kind, setKind] = useState('')
  const [selectedName, setSelectedName] = useState<string>()
  const [remove, setRemove] = useState<MediaItem>()
  const [copied, setCopied] = useState(false)
  const [autoOpened, setAutoOpened] = useState(false)
  const isMobile = useMediaQuery('(max-width: 960px)')
  const isWide = useMediaQuery('(min-width: 1100px)')
  const client = useQueryClient()
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['media'], queryFn: ({ signal }) => api<{ media: MediaItem[] }>('/admin/media', { signal }) })
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
  useEffect(() => {
    if (!isWide || autoOpened || !data?.media.length) return
    setSelectedName(data.media[0].name)
    setAutoOpened(true)
  }, [autoOpened, data, isWide])
  const copy = async (item: MediaItem) => {
    await navigator.clipboard.writeText(`${location.origin}${item.url}`)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 1800)
  }

  return <div className="page-stack">
    <PageHeader title="媒体库" description="已生成的图片与视频；选中后可直接预览、复制链接或清理。" kicker="产物" actions={<Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button>} />
    {copied && <SuccessMessage>链接已复制</SuccessMessage>}
    <div className="media-toolbar">
      <div className="search-box"><Search size={16} /><Input placeholder="搜索文件名" value={search} onChange={event => setSearch(event.target.value)} aria-label="搜索媒体" /></div>
      <Select value={kind} onChange={event => setKind(event.target.value)} aria-label="媒体类型"><option value="">全部类型</option><option value="image">图片</option><option value="video">视频</option></Select>
      <span className="toolbar-count">{error ? '读取失败' : `${items.length} 个文件`}</span>
    </div>

    <div className={`media-workbench ${error || (items.length === 0 && !isLoading) ? 'single' : ''}`}>
      <div>
        {isLoading ? <Panel><Loading /></Panel> : error ? <Panel><ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /></Panel> : items.length === 0 ? <Panel><Empty title="媒体库为空" description="生成并下载成功的媒体会出现在这里。" /></Panel> : <div className="media-grid">
          {items.map(item => <article className={`media-card ${active?.name === item.name ? 'selected' : ''}`} key={item.name}>
            <button className="media-preview" onClick={() => setSelectedName(item.name)} aria-label={`打开预览 ${item.name}`}>
              <MediaThumb item={item} />
              <span className="media-type">{item.kind === 'image' ? '图片' : '视频'}</span>
              <span className="media-hover"><Eye size={21} /></span>
            </button>
            <div className="media-info">
              <strong title={item.name}>{item.name}</strong>
              <p>{bytes(item.bytes)} · {fullDate(item.mtime)}</p>
              <div className="media-actions">
                <button onClick={() => setSelectedName(item.name)} aria-label={`预览 ${item.name}`}><Eye size={13} /><span>预览</span></button>
                <button onClick={() => void copy(item)} aria-label={`复制 ${item.name} 链接`}><Copy size={13} /><span>链接</span></button>
                <button className="danger" onClick={() => setRemove(item)} aria-label={`删除 ${item.name}`}><Trash2 size={13} /></button>
              </div>
            </div>
          </article>)}
        </div>}
      </div>

      {items.length > 0 && !error && <>
      {isMobile && active && <button className="detail-scrim" aria-label="关闭媒体详情" onClick={() => setSelectedName(undefined)} />}
      <Panel className={`media-detail ${active ? 'open' : ''}`}>
        {active ? <>
          <div className="detail-head">
            <div className="detail-title"><span className="page-kicker">媒体详情</span><h2>{active.name}<span>{active.mime}</span></h2></div>
            <div className="detail-head-actions"><Button variant="secondary" size="sm" onClick={() => void copy(active)}>{copied ? <Check size={14} /> : <Copy size={14} />}复制链接</Button><button className="icon-button detail-drawer-close" aria-label="关闭详情" onClick={() => setSelectedName(undefined)}><X size={18} /></button></div>
          </div>
          <div className="detail-body">
            <section className="detail-section">
              <div className="detail-section-title"><span>预览</span><span>{active.kind === 'image' ? '图片' : '视频'}</span></div>
              <div className="media-detail-preview">{active.kind === 'image' ? <img src={active.url} alt={active.name} /> : <video src={active.url} controls playsInline preload="metadata" />}</div>
            </section>
            <section className="detail-section">
              <div className="detail-section-title"><span>文件信息</span></div>
              <div className="detail-grid">
                <Detail label="大小" value={bytes(active.bytes)} />
                <Detail label="修改时间" value={fullDate(active.mtime)} />
                <Detail label="类型" value={active.mime} />
                <Detail label="任务" value={active.task_id || '—'} />
              </div>
            </section>
            <section className="detail-section">
              <div className="detail-section-title"><span>操作</span></div>
              <div className="detail-actions">
                <a className="button button-secondary button-sm" href={active.url} download={active.name}><Download size={14} />下载</a>
                <Button variant="secondary" size="sm" onClick={() => void copy(active)}><Copy size={14} />复制链接</Button>
                <Button variant="danger" size="sm" onClick={() => setRemove(active)}><Trash2 size={14} />删除文件</Button>
              </div>
            </section>
          </div>
        </> : <div className="media-detail-empty"><Image size={24} /><h3>选择一个媒体文件</h3><p>查看完整预览、文件信息和任务关联。图片与视频会直接从这里流式加载。</p></div>}
      </Panel>
      </>}
    </div>

    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title="删除媒体文件？" description={`${remove?.name ?? ''} 将被永久删除，任务记录保留。`} busy={deleteMutation.isPending} onConfirm={() => remove && deleteMutation.mutate(remove)} />
  </div>
}

function MediaThumb({ item }: { item: MediaItem }) {
  if (item.kind === 'video') return <video src={item.url} muted playsInline preload="metadata" />
  return <img src={item.url} alt="" loading="lazy" />
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span className="detail-label">{label}</span><div className="detail-value">{value}</div></div> }
