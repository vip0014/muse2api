import { useEffect, useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Archive, CheckCircle2, Copy, Eye, KeyRound, RotateCcw, Server, ShieldCheck } from 'lucide-react'
import { api } from '../lib/api'
import type { ServiceConfig } from '../lib/types'
import { Button, ErrorState, Field, InlineError, Input, Loading, PageHeader, Panel, SuccessMessage } from '../components/UI'
import { Confirm, Modal } from '../components/Dialog'

type APIKeyInfo = { configured: boolean; api_key_masked: string; base_url: string }

export default function Settings() {
  const [backupConfirm, setBackupConfirm] = useState(false); const [rotateConfirm, setRotateConfirm] = useState(false); const [revealOpen, setRevealOpen] = useState(false); const [revealedKey, setRevealedKey] = useState(''); const [revealError, setRevealError] = useState(''); const [copyState, setCopyState] = useState(''); const [message, setMessage] = useState(''); const [notice, setNotice] = useState('')
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
  const copyKey = async () => {
    if (!revealedKey) return
    try { await navigator.clipboard.writeText(revealedKey); setCopyState('已复制') } catch { setCopyState('复制失败') }
    window.setTimeout(() => setCopyState(''), 1500)
  }
  if (isLoading) return <Loading />; if (error || !data) return <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} />
  const c = data.config
  const maskedKey = keyInfo.isLoading ? '加载中…' : keyInfo.error ? '读取失败' : keyInfo.data?.configured ? keyInfo.data.api_key_masked : '未配置'
  return <div className="page-stack"><PageHeader title="系统设置" />
    {notice && <SuccessMessage>{notice}</SuccessMessage>}{message && <InlineError>{message}</InlineError>}
    <div className="settings-grid"><Panel><div className="section-head"><h2>API 服务</h2><Server size={17} /></div><dl className="settings-list"><Setting label="监听地址" value={`${c.host}:${c.port}`} mono /><Setting label="公开 Base URL" value={c.public_base || '由请求地址决定'} /><Setting label="上游站点" value={c.site} /><Setting label="管理员" value={c.admin_username || 'admin'} /><Setting label="会话有效期" value={c.session_ttl ? `${Math.round(c.session_ttl / 3600)} 小时` : '—'} /><Setting label="版本" value={data.version} /><Setting label="生效方式" value={data.restart_required_for} /></dl></Panel>
      <Panel><div className="section-head"><h2>API 凭据</h2><KeyRound size={17} /></div><dl className="settings-list"><Setting label="Bearer Key" value={maskedKey} mono /><Setting label="Base URL" value={keyInfo.data?.base_url || '—'} mono /></dl><div className="maintenance-row"><div className="maintenance-icon"><Eye size={18} /></div><div><strong>查看完整 API Key</strong><p>需要再次输入管理员密码；查看与拒绝都会写入审计日志。</p></div><Button variant="secondary" disabled={!keyInfo.data?.configured || reveal.isPending} onClick={() => { setRevealError(''); setRevealOpen(true) }}><Eye size={15} />显示</Button></div></Panel>
      <Panel><div className="section-head"><h2>任务与超时</h2></div><dl className="settings-list"><Setting label="全局并发" value={`${c.concurrency} 个任务`} /><Setting label="队列容量" value={`${c.queue_size} 个任务`} /><Setting label="对话超时" value={`${c.chat_timeout} 秒`} /><Setting label="图片超时" value={`${c.image_timeout} 秒`} /><Setting label="视频超时" value={`${c.video_timeout} 秒`} /></dl></Panel>
      <Panel><div className="section-head"><h2>媒体与日志</h2></div><dl className="settings-list"><Setting label="媒体目录" value={c.media_dir} mono /><Setting label="保留策略" value="手动管理" /><Setting label="媒体访问" value={c.public_media ? '匿名可读' : '需要 Bearer Key'} /><Setting label="日志级别" value={c.log_level} /></dl></Panel>
      <Panel><div className="section-head"><h2>安全状态</h2><ShieldCheck size={17} /></div><div className="security-checks"><span className={c.credential_encryption ? '' : 'is-warn'}><CheckCircle2 size={15} />凭据加密 {c.credential_encryption ? '已启用' : '未启用'}</span><span><CheckCircle2 size={15} />恒定时间校验</span><span><CheckCircle2 size={15} />CSRF / CORS 防护</span><span><CheckCircle2 size={15} />原生 CDP 运行时</span></div></Panel></div>
    <Panel><div className="section-head"><h2>维护</h2></div><div className="maintenance-row"><div className="maintenance-icon"><Archive size={18} /></div><div><strong>备份元数据</strong><p>账号密文、API Key、任务与审计事件。</p></div><Button variant="secondary" onClick={() => setBackupConfirm(true)}>创建备份</Button></div><div className="maintenance-row"><div className="maintenance-icon"><KeyRound size={18} /></div><div><strong>轮换 API Key</strong><p>旧 Key 立即失效，新 Key 会在弹窗中显示并可复制。</p></div><Button variant="danger" onClick={() => setRotateConfirm(true)}><RotateCcw size={15} />轮换</Button></div></Panel>
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
function Setting({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><dt>{label}</dt><dd className={mono ? 'mono' : ''}>{value}</dd></div> }
