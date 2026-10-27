import { useState } from 'react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { Archive, CheckCircle2, KeyRound, RotateCcw, Server, ShieldCheck } from 'lucide-react'
import { api } from '../lib/api'
import type { ServiceConfig } from '../lib/types'
import { Button, ErrorState, InlineError, Loading, PageHeader, Panel, SuccessMessage } from '../components/UI'
import { Confirm } from '../components/Dialog'

export default function Settings() {
  const [backupConfirm, setBackupConfirm] = useState(false); const [rotateConfirm, setRotateConfirm] = useState(false); const [newKey, setNewKey] = useState(''); const [message, setMessage] = useState(''); const [notice, setNotice] = useState('')
  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['settings'], queryFn: ({ signal }) => api<{ config: ServiceConfig; version: string; restart_required_for: string }>('/admin/settings', { signal }) })
  const backup = useMutation({ mutationFn: () => api<{ message: string }>('/admin/settings/backup', { method: 'POST' }), onSuccess: result => { setNotice(result.message); setMessage(''); setBackupConfirm(false) }, onError: (err: Error) => setMessage(err.message) })
  const rotate = useMutation({ mutationFn: () => api<{ api_key: string }>('/admin/apikey/rotate', { method: 'POST' }), onSuccess: result => { setNewKey(result.api_key); setNotice(''); setMessage(''); setRotateConfirm(false) }, onError: (err: Error) => setMessage(err.message) })
  if (isLoading) return <Loading />; if (error || !data) return <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} />
  const c = data.config
  return <div className="page-stack"><PageHeader title="系统设置" />
    {notice && <SuccessMessage>{notice}</SuccessMessage>}{message && <InlineError>{message}</InlineError>}{newKey && <SuccessMessage>新 API Key 仅显示一次：<code>{newKey}</code>，请立即更新下游客户端。</SuccessMessage>}
    <div className="settings-grid"><Panel><div className="section-head"><h2>API 服务</h2><Server size={17} /></div><dl className="settings-list"><Setting label="监听地址" value={`${c.host}:${c.port}`} mono /><Setting label="公开 Base URL" value={c.public_base || '由请求地址决定'} /><Setting label="上游站点" value={c.site} /><Setting label="管理员" value={c.admin_username || 'admin'} /><Setting label="会话有效期" value={c.session_ttl ? `${Math.round(c.session_ttl / 3600)} 小时` : '—'} /><Setting label="版本" value={data.version} /><Setting label="生效方式" value={data.restart_required_for} /></dl></Panel>
      <Panel><div className="section-head"><h2>任务与超时</h2></div><dl className="settings-list"><Setting label="全局并发" value={`${c.concurrency} 个任务`} /><Setting label="队列容量" value={`${c.queue_size} 个任务`} /><Setting label="对话超时" value={`${c.chat_timeout} 秒`} /><Setting label="图片超时" value={`${c.image_timeout} 秒`} /><Setting label="视频超时" value={`${c.video_timeout} 秒`} /></dl></Panel>
      <Panel><div className="section-head"><h2>媒体与日志</h2></div><dl className="settings-list"><Setting label="媒体目录" value={c.media_dir} mono /><Setting label="保留策略" value="手动管理" /><Setting label="媒体访问" value={c.public_media ? '匿名可读' : '需要 Bearer Key'} /><Setting label="日志级别" value={c.log_level} /></dl></Panel>
      <Panel><div className="section-head"><h2>安全状态</h2><ShieldCheck size={17} /></div><div className="security-checks"><span className={c.credential_encryption ? '' : 'is-warn'}><CheckCircle2 size={15} />凭据加密 {c.credential_encryption ? '已启用' : '未启用'}</span><span><CheckCircle2 size={15} />恒定时间校验</span><span><CheckCircle2 size={15} />CSRF / CORS 防护</span><span><CheckCircle2 size={15} />原生 CDP 运行时</span></div></Panel></div>
    <Panel><div className="section-head"><h2>维护</h2></div><div className="maintenance-row"><div className="maintenance-icon"><Archive size={18} /></div><div><strong>备份元数据</strong><p>账号密文、API Key、任务与审计事件。</p></div><Button variant="secondary" onClick={() => setBackupConfirm(true)}>创建备份</Button></div><div className="maintenance-row"><div className="maintenance-icon"><KeyRound size={18} /></div><div><strong>轮换 API Key</strong><p>旧 Key 立即失效，新 Key 仅显示一次。</p></div><Button variant="danger" onClick={() => setRotateConfirm(true)}><RotateCcw size={15} />轮换</Button></div></Panel>
    <Confirm open={backupConfirm} onOpenChange={setBackupConfirm} title="创建元数据备份？" description="保存到 data/backups，不会覆盖旧备份。" busy={backup.isPending} onConfirm={() => backup.mutate()} />
    <Confirm open={rotateConfirm} onOpenChange={setRotateConfirm} title="轮换 API Key？" description="旧 Key 将立即失效，管理员会话不受影响。" busy={rotate.isPending} onConfirm={() => rotate.mutate()} />
  </div>
}
function Setting({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) { return <div><dt>{label}</dt><dd className={mono ? 'mono' : ''}>{value}</dd></div> }
