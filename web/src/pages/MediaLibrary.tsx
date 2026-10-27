import { useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Copy, Download, Eye, RefreshCw, Search, Trash2 } from 'lucide-react'
import { api } from '../lib/api'
import type { MediaItem } from '../lib/types'
import { bytes, fullDate } from '../lib/format'
import { Button, Empty, ErrorState, Input, Loading, PageHeader, Panel, Select, SuccessMessage } from '../components/UI'
import { Confirm, Modal } from '../components/Dialog'

export default function MediaLibrary() {
  const [search, setSearch] = useState(''); const [kind, setKind] = useState(''); const [preview, setPreview] = useState<MediaItem>(); const [remove, setRemove] = useState<MediaItem>(); const [copied, setCopied] = useState(false)
  const client = useQueryClient()
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['media'], queryFn: ({ signal }) => api<{ media: MediaItem[] }>('/admin/media', { signal }) })
  const deleteMutation = useMutation({ mutationFn: (item: MediaItem) => api(`/admin/media/${encodeURIComponent(item.name)}`, { method: 'DELETE' }), onSuccess: () => { client.invalidateQueries({ queryKey: ['media'] }); client.invalidateQueries({ queryKey: ['overview'] }); setRemove(undefined) } })
  const items = useMemo(() => (data?.media ?? []).filter(item => (!search || item.name.toLowerCase().includes(search.toLowerCase())) && (!kind || item.kind === kind)), [data, search, kind])
  const copy = async (item: MediaItem) => { await navigator.clipboard.writeText(`${location.origin}${item.url}`); setCopied(true); setTimeout(() => setCopied(false), 1800) }
  return <div className="page-stack"><PageHeader title="媒体库" actions={<Button variant="secondary" onClick={() => refetch()}><RefreshCw size={15} />刷新</Button>} />{copied && <SuccessMessage>链接已复制</SuccessMessage>}
    <Panel className="toolbar"><div className="search-box"><Search size={16} /><Input placeholder="搜索文件" value={search} onChange={e => setSearch(e.target.value)} /></div><Select value={kind} onChange={e => setKind(e.target.value)} aria-label="媒体类型"><option value="">全部类型</option><option value="image">图片</option><option value="video">视频</option></Select><span className="toolbar-count">{items.length} 个文件</span></Panel>
    {isLoading ? <Panel><Loading /></Panel> : error ? <Panel><ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /></Panel> : items.length === 0 ? <Panel><Empty title="媒体库为空" description="生成并下载成功的媒体会出现在这里。" /></Panel> : <div className="media-grid">{items.map(item => <article className="media-card" key={item.name}><button className="media-preview" onClick={() => setPreview(item)} aria-label={`预览 ${item.name}`}><MediaThumb item={item} /><span className="media-type">{item.kind === 'image' ? '图片' : '视频'}</span><span className="media-hover"><Eye size={22} /></span></button><div className="media-info"><strong title={item.name}>{item.name}</strong><p>{bytes(item.bytes)} · {fullDate(item.mtime)}</p><div className="media-actions"><button onClick={() => setPreview(item)}><Eye size={14} />预览</button><button className="danger" onClick={() => setRemove(item)} aria-label={`删除 ${item.name}`}><Trash2 size={14} /></button></div></div></article>)}</div>}
    <Modal open={Boolean(preview)} onOpenChange={() => setPreview(undefined)} title={preview?.name ?? '媒体预览'} description={preview ? `${preview.kind === 'image' ? '图片' : '视频'} · ${bytes(preview.bytes)}` : undefined} wide>{preview && <div className="preview-dialog">{preview.kind === 'image' ? <img src={preview.url} alt={preview.name} /> : <video src={preview.url} controls playsInline preload="metadata" />}<div className="dialog-actions"><a className="button button-secondary" href={preview.url} download={preview.name}><Download size={15} />下载</a><Button onClick={() => copy(preview)}><Copy size={15} />复制链接</Button></div></div>}</Modal>
    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title="删除媒体文件？" description={`${remove?.name ?? ''} 将被永久删除，任务记录保留。`} busy={deleteMutation.isPending} onConfirm={() => remove && deleteMutation.mutate(remove)} />
  </div>
}
function MediaThumb({ item }: { item: MediaItem }) { if (item.kind === 'video') return <div className="media-placeholder video">▶</div>; return <img src={item.url} alt="" loading="lazy" /> }
