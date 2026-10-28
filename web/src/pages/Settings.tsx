import { useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Archive, CheckCircle2, Copy, Eye, KeyRound, RotateCcw, Server, ShieldCheck, SlidersHorizontal, Timer, Wrench } from 'lucide-react'
import { api } from '../lib/api'
import type { ServiceConfig } from '../lib/types'
import { Button, ErrorState, Field, InlineError, Input, Loading, PageHeader, SuccessMessage } from '../components/UI'
import { Confirm, Modal } from '../components/Dialog'

type APIKeyInfo = { configured: boolean; api_key_masked: string; base_url: string }
type SectionId = 'service' | 'credentials' | 'runtime' | 'media' | 'security' | 'maintenance'

const sections: { id: SectionId; label: string; icon: typeof Server; title: string; description: string }[] = [
  { id: 'service', label: 'API 服务', icon: Server, title: 'API 服务', description: '监听地址、公开入口与本版本信息。' },
  { id: 'credentials', label: 'API 凭据', icon: KeyRound, title: 'API 凭据', description: '下游客户端使用的 Bearer Key 与 Base URL。' },
  { id: 'runtime', label: '任务与超时', icon: Timer, title: '任务与超时', description: '队列容量与各能力的上游超时。' },
  { id: 'media', label: '媒体与日志', icon: SlidersHorizontal, title: '媒体与日志', description: '媒体目录、访问策略和日志级别。' },
  { id: 'security', label: '安全状态', icon: ShieldCheck, title: '安全状态', description: '凭据加密、会话保护与请求防护。' },
  { id: 'maintenance', label: '维护', icon: Wrench, title: '维护', description: '备份元数据与轮换 API Key。' },
]

export default function Settings() {
  const [backupConfirm, setBackupConfirm] = useState(false)
  const [rotateConfirm, setRotateConfirm] = useState(false)
  const [revealOpen, setRevealOpen] = useState(false)
  const [revealedKey, setRevealedKey] = useState('')
  const [revealError, setRevealError] = useState('')
  const [copyState, setCopyState] = useState('')
  const [message, setMessage] = useState('')
  const [notice, setNotice] = useState('')
  const [active, setActive] = useState<SectionId>('service')
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['settings'], queryFn: ({ signal }) => api<{ config: ServiceConfig; version: string; restart_required_for: string }>('/admin/settings', { signal }) })
  const keyInfo = useQuery({ queryKey: ['apikey'], queryFn: ({ signal }) => api<APIKeyInfo>('/admin/apikey', { signal }) })
  const backup = useMutation({ mutationFn: () => api<{ message: string }>('/admin/settings/backup', { method: 'POST' }), onSuccess: result => { setNotice(result.message); setMessage(''); setBackupConfirm(false) }, onError: (err: Error) => setMessage(err.message) })
  const rotate = useMutation({ mutationFn: () => api<{ api_key: string }>('/admin/apikey/rotate', { method: 'POST' }), onSuccess: result => { setRevealedKey(result.api_key); setCopyState(''); setNotice('API Key 已轮换，旧 Key 已失效；请立即更新下游客户端。'); setMessage(''); setRotateConfirm(false); void keyInfo.refetch() }, onError: (err: Error) => setMessage(err.message) })
  const reveal = useMutation({ mutationFn: (password: string) => api<{ api_key: string }>('/admin/apikey/reveal', { method: 'POST', body: JSON.stringify({ password }) }), onSuccess: result => { setRevealedKey(result.api_key); setCopyState(''); setRevealError(''); setRevealOpen(false) }, onError: (err: Error) => setRevealError(err.message) })

  useEffect(() => {
    if (!revealedKey) return
    const timer = window.setTimeout(() => setRevealedKey(''), 30_000)
    return () => window.clearTimeout(timer)
  }, [revealedKey])

  useEffect(() => {
    if (!data || typeof IntersectionObserver === 'undefined') return
    const elements = Array.from(document.querySelectorAll<HTMLElement>('.settings-section'))
    const observer = new IntersectionObserver(entries => {
      const atBottom = window.innerHeight + window.scrollY >= document.documentElement.scrollHeight - 8
      if (atBottom) { setActive('maintenance'); return }
      const visible = entries.filter(entry => entry.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0]
      if (!visible) return
      const id = visible.target.id.replace('settings-', '') as SectionId
      if (sections.some(section => section.id === id)) setActive(id)
    }, { rootMargin: '-84px 0px -62% 0px', threshold: 0 })
    elements.forEach(element => observer.observe(element))
    return () => observer.disconnect()
  }, [data])

  const go = (id: SectionId) => {
    setActive(id)
    const element = document.getElementById(`settings-${id}`)
    const reduce = typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches
    element?.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' })
  }

  const copyKey = async () => {
    if (!revealedKey) return
    try { await navigator.clipboard.writeText(revealedKey); setCopyState('已复制') } catch { setCopyState('复制失败') }
    window.setTimeout(() => setCopyState(''), 1500)
  }

  if (isLoading) return <div className="page-stack"><PageHeader title="系统设置" /><Loading /></div>
  if (error || !data) return <div className="page-stack"><PageHeader title="系统设置" /><ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /></div>
  const c = data.config
  const maskedKey = keyInfo.isLoading ? '加载中…' : keyInfo.error ? '读取失败' : keyInfo.data?.configured ? keyInfo.data.api_key_masked : '未配置'

  return <div className="page-stack">
    <PageHeader title="系统设置" description="服务端配置与安全边界；改动通过环境变量或部署流程生效。" kicker="系统" />
    {notice && <SuccessMessage>{notice}</SuccessMessage>}
    {message && <InlineError>{message}</InlineError>}
    <div className="settings-layout">
      <nav className="settings-nav" aria-label="设置分区">
        {sections.map(({ id, label, icon: Icon }) => <button key={id} className={active === id ? 'active' : ''} onClick={() => go(id)}><Icon size={14} />{label}</button>)}
      </nav>
      <div className="settings-content">
        <section className="settings-section" id="settings-service">
          <SectionHead icon={Server} title="API 服务" description="监听地址、公开入口与本版本信息。" />
          <dl className="settings-list">
            <Setting label="监听地址" value={`${c.host}:${c.port}`} mono />
            <Setting label="公开 Base URL" value={c.public_base || '由请求地址决定'} />
            <Setting label="上游站点" value={c.site} />
            <Setting label="管理员" value={c.admin_username || 'admin'} />
            <Setting label="会话有效期" value={c.session_ttl ? `${Math.round(c.session_ttl / 3600)} 小时` : '—'} />
            <Setting label="版本" value={data.version} />
            <Setting label="生效方式" value={data.restart_required_for} />
          </dl>
        </section>

        <section className="settings-section" id="settings-credentials">
          <SectionHead icon={KeyRound} title="API 凭据" description="下游客户端使用的 Bearer Key 与 Base URL。" />
          <dl className="settings-list">
            <Setting label="Bearer Key" value={maskedKey} mono />
            <Setting label="Base URL" value={keyInfo.data?.base_url || '—'} mono />
          </dl>
          <div className="maintenance-row">
            <div className="maintenance-icon"><Eye size={18} /></div>
            <div><strong>查看完整 API Key</strong><p>需要再次输入管理员密码；查看与拒绝都会写入审计日志，30 秒后自动隐藏。</p></div>
            <Button variant="secondary" disabled={!keyInfo.data?.configured || reveal.isPending} onClick={() => { setRevealError(''); setRevealOpen(true) }}><Eye size={15} />显示</Button>
          </div>
        </section>

        <section className="settings-section" id="settings-runtime">
          <SectionHead icon={Timer} title="任务与超时" description="队列容量与各能力的上游超时。" />
          <dl className="settings-list">
            <Setting label="全局并发" value={`${c.concurrency} 个任务`} />
            <Setting label="队列容量" value={`${c.queue_size} 个任务`} />
            <Setting label="对话超时" value={`${c.chat_timeout} 秒`} />
            <Setting label="图片超时" value={`${c.image_timeout} 秒`} />
            <Setting label="视频超时" value={`${c.video_timeout} 秒`} />
          </dl>
        </section>

        <section className="settings-section" id="settings-media">
          <SectionHead icon={SlidersHorizontal} title="媒体与日志" description="媒体目录、访问策略和日志级别。" />
          <dl className="settings-list">
            <Setting label="媒体目录" value={c.media_dir} mono />
            <Setting label="保留策略" value="手动管理" />
            <Setting label="媒体访问" value={c.public_media ? '匿名可读' : '需要 Bearer Key'} />
            <Setting label="日志级别" value={c.log_level} />
          </dl>
        </section>

        <section className="settings-section" id="settings-security">
          <SectionHead icon={ShieldCheck} title="安全状态" description="凭据加密、会话保护与请求防护。" />
          <div className="security-checks">
            <span className={c.credential_encryption ? '' : 'is-warn'}><CheckCircle2 size={15} />凭据加密 {c.credential_encryption ? '已启用' : '未启用'}</span>
            <span><CheckCircle2 size={15} />恒定时间校验</span>
            <span><CheckCircle2 size={15} />CSRF / CORS 防护</span>
            <span><CheckCircle2 size={15} />原生 CDP 运行时</span>
            <span className={c.ref_image_local_files ? 'is-warn' : ''}><CheckCircle2 size={15} />参考图本地文件 {c.ref_image_local_files ? '已允许' : '已禁止'}</span>
            <span className={c.ref_image_private_fetch ? 'is-warn' : ''}><CheckCircle2 size={15} />参考图私网抓取 {c.ref_image_private_fetch ? '已允许' : '已禁止'}</span>
            <span><CheckCircle2 size={15} />请求体上限 {c.max_upload_bytes ? `${Math.round(c.max_upload_bytes / 1024 / 1024)} MiB` : '—'}</span>
          </div>
        </section>

        <section className="settings-section" id="settings-maintenance">
          <SectionHead icon={Wrench} title="维护" description="备份元数据与轮换 API Key。" />
          <div className="maintenance-row">
            <div className="maintenance-icon"><Archive size={18} /></div>
            <div><strong>备份元数据</strong><p>账号密文、API Key、任务与审计事件；保存到 data/backups，不覆盖旧备份。</p></div>
            <Button variant="secondary" onClick={() => setBackupConfirm(true)}>创建备份</Button>
          </div>
          <div className="maintenance-row">
            <div className="maintenance-icon"><KeyRound size={18} /></div>
            <div><strong>轮换 API Key</strong><p>旧 Key 立即失效，新 Key 会在弹窗中显示并可复制。</p></div>
            <Button variant="danger" onClick={() => setRotateConfirm(true)}><RotateCcw size={15} />轮换</Button>
          </div>
        </section>
      </div>
    </div>

    <Confirm open={backupConfirm} onOpenChange={setBackupConfirm} title="创建元数据备份？" description="保存到 data/backups，不会覆盖旧备份。" busy={backup.isPending} onConfirm={() => backup.mutate()} />
    <Confirm open={rotateConfirm} onOpenChange={setRotateConfirm} title="轮换 API Key？" description="旧 Key 将立即失效，管理员会话不受影响。" busy={rotate.isPending} onConfirm={() => rotate.mutate()} />
    <Modal open={revealOpen} onOpenChange={open => { setRevealOpen(open); if (!open) setRevealError('') }} title="显示 API Key" description="输入管理员密码确认身份">
      <form className="form-stack" onSubmit={event => { event.preventDefault(); const password = String(new FormData(event.currentTarget).get('password') || ''); reveal.mutate(password) }}>
        <Field label="管理员密码"><Input name="password" type="password" required autoFocus autoComplete="current-password" /></Field>
        {revealError && <InlineError>{revealError}</InlineError>}
        <div className="dialog-actions"><Button type="button" variant="secondary" onClick={() => setRevealOpen(false)}>取消</Button><Button disabled={reveal.isPending}>{reveal.isPending ? '验证中…' : '显示'}</Button></div>
      </form>
    </Modal>
    <Modal open={Boolean(revealedKey)} onOpenChange={open => { if (!open) { setRevealedKey(''); setCopyState('') } }} title="API Key" description="30 秒后自动隐藏；请勿在录屏或共享屏幕时展示">
      <div className="credential-reveal">{revealedKey}</div>
      <div className="dialog-actions"><Button variant="secondary" onClick={() => void copyKey()}><Copy size={15} />{copyState || '复制'}</Button><Button onClick={() => { setRevealedKey(''); setCopyState('') }}>关闭</Button></div>
    </Modal>
  </div>
}

function SectionHead({ icon: Icon, title, description }: { icon: typeof Server; title: string; description: string }) {
  return <div className="settings-section-head"><span className="section-icon"><Icon size={15} /></span><div><h2>{title}</h2><p>{description}</p></div></div>
}

function Setting({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><dt>{label}</dt><dd className={mono ? 'mono' : ''}>{value}</dd></div> }
