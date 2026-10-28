import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Clock3, KeyRound, Plus, RefreshCw, Search, ShieldCheck, Trash2, UserCheck, UserX, X } from 'lucide-react'
import { api, json } from '../lib/api'
import type { Account, AccountStatus } from '../lib/types'
import { accountStatusText, fullDate } from '../lib/format'
import { useMediaQuery } from '../lib/hooks'
import { Badge, Button, Empty, ErrorState, Field, InlineError, Input, Loading, PageHeader, Panel, Select, SuccessMessage } from '../components/UI'
import { Confirm, Modal } from '../components/Dialog'

const accountSchema = z.object({ label: z.string().trim().max(80, '标签最多 80 个字符'), cookie_header: z.string().trim().min(1, '请粘贴 Cookie Header 或 JSON') })
type AccountForm = z.infer<typeof accountSchema>
const statusTone = (status: AccountStatus) => status === 'active' ? 'success' : ['expired', 'error'].includes(status) ? 'danger' : status === 'unknown' ? 'neutral' : 'warning'

function clampPct(value: number) { return Math.max(0, Math.min(100, value)) }
function formatPct(value: number) { return `${Number.isInteger(value) ? value : value.toFixed(1)}%` }
function quotaTone(remaining: number) { return remaining <= 15 ? 'danger' : remaining <= 40 ? 'warn' : '' }
function remainingOf(quota?: Account['quota']) {
  if (!quota) return undefined
  const used = typeof quota.weekly_used_pct === 'number' ? quota.weekly_used_pct : quota.extra_used_pct
  return typeof used === 'number' ? 100 - clampPct(used) : undefined
}

export default function Accounts({ initialSearch = '', initialStatus = '', initialFocus }: { initialSearch?: string; initialStatus?: string; initialFocus?: string } = {}) {
  const [search, setSearch] = useState(initialSearch)
  const [status, setStatus] = useState(initialStatus)
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set())
  const [selectedId, setSelectedId] = useState<string | undefined>(initialFocus)
  const [addOpen, setAddOpen] = useState(false)
  const [edit, setEdit] = useState<Account>()
  const [reauth, setReauth] = useState<Account>()
  const [remove, setRemove] = useState<Account>()
  const [message, setMessage] = useState('')
  const [errorMessage, setErrorMessage] = useState('')
  const [autoChecking, setAutoChecking] = useState(false)
  const isMobile = useMediaQuery('(max-width: 960px)')
  const client = useQueryClient()

  const { data, isLoading, error, refetch } = useQuery({ queryKey: ['accounts'], queryFn: ({ signal }) => api<{ accounts: Account[] }>('/admin/accounts', { signal }), refetchInterval: autoChecking ? 3000 : false })
  useEffect(() => {
    if (!autoChecking) return
    const timer = window.setTimeout(() => setAutoChecking(false), 180_000)
    return () => window.clearTimeout(timer)
  }, [autoChecking])
  useEffect(() => {
    if (autoChecking && data?.accounts.length && data.accounts.every(account => account.checked_at && (account.quota?.checked_at || account.last_error))) setAutoChecking(false)
  }, [autoChecking, data])
  useEffect(() => { if (initialFocus) setSelectedId(initialFocus) }, [initialFocus])

  const invalidate = () => { client.invalidateQueries({ queryKey: ['accounts'] }); client.invalidateQueries({ queryKey: ['overview'] }) }
  const action = useMutation({
    mutationFn: ({ path, method = 'POST', body }: { path: string; method?: string; body?: unknown }) => api(path, { method, body: body === undefined ? undefined : json(body) }),
    onSuccess: () => { invalidate() },
    onError: (err: Error) => setErrorMessage(err.message),
  })

  const accounts = useMemo(() => (data?.accounts ?? []).filter(account => (!search || `${account.label} ${account.id}`.toLowerCase().includes(search.toLowerCase())) && (!status || account.status === status)), [data, search, status])
  const active = accounts.find(account => account.id === selectedId) ?? (isMobile ? undefined : accounts[0])
  const allSelected = accounts.length > 0 && accounts.every(account => selectedIds.has(account.id))
  const toggleAll = () => setSelectedIds(allSelected ? new Set() : new Set(accounts.map(account => account.id)))
  const toggleOne = (id: string) => setSelectedIds(current => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next })

  const testAccount = (account: Account) => {
    setMessage(''); setErrorMessage('')
    action.mutate({ path: `/admin/accounts/${account.id}/test` }, {
      onSuccess: result => {
        const value = result as { ok: boolean; message: string }
        if (value.ok) { setMessage(value.message); setErrorMessage('') } else { setMessage(''); setErrorMessage(value.message) }
      },
    })
  }
  const refreshQuota = (account: Account) => {
    setMessage(''); setErrorMessage('')
    action.mutate({ path: `/admin/accounts/${account.id}/quota` }, { onSuccess: () => setMessage('额度已同步') })
  }
  const bulk = (operation: string) => action.mutate({ path: '/admin/accounts/bulk', body: { ids: [...selectedIds], action: operation } }, { onSuccess: () => setSelectedIds(new Set()) })
  const toggleEnabled = (account: Account) => action.mutate({ path: `/admin/accounts/${account.id}`, method: 'PATCH', body: { enabled: !account.enabled } })
  const deleteAccount = (account: Account) => action.mutate({ path: `/admin/accounts/${account.id}`, method: 'DELETE' }, {
    onSuccess: () => {
      setRemove(undefined)
      if (selectedId === account.id) setSelectedId(undefined)
      client.invalidateQueries({ queryKey: ['accounts'] })
    },
  })

  return <div className="page-stack">
    <PageHeader title="账号池" description="Muse 会话、官方额度与调度状态；选中账号查看详情并处理。" kicker="资源" actions={<Button onClick={() => setAddOpen(true)}><Plus size={15} />添加账号</Button>} />
    {message && <SuccessMessage>{message}</SuccessMessage>}
    {errorMessage && <InlineError>{errorMessage}</InlineError>}
    {selectedIds.size > 0 && <div className="bulk-bar"><span>已选择 <strong>{selectedIds.size}</strong> 个账号</span><div><Button size="sm" variant="secondary" onClick={() => bulk('enable')}><UserCheck size={14} />启用</Button><Button size="sm" variant="secondary" onClick={() => bulk('disable')}><UserX size={14} />禁用</Button><Button size="sm" variant="ghost" onClick={() => setSelectedIds(new Set())}>取消</Button></div></div>}

    <div className={`accounts-workbench ${error || (accounts.length === 0 && !isLoading) ? 'single' : ''}`}>
      <Panel className="account-list-panel">
        <div className="account-toolbar">
          <div className="account-toolbar-row">
            <div className="search-box"><Search size={15} /><Input placeholder="搜索标签或 ID" value={search} onChange={event => setSearch(event.target.value)} aria-label="搜索账号" /></div>
            <Select value={status} onChange={event => setStatus(event.target.value)} aria-label="账号状态"><option value="">全部状态</option>{Object.entries(accountStatusText).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</Select>
          </div>
          <div className="account-toolbar-row">
            <Button size="sm" variant="ghost" onClick={() => refetch()}><RefreshCw size={14} />刷新</Button>
            <span className="toolbar-count">{error ? '读取失败' : `${accounts.length} 个账号${autoChecking ? ' · 后台检测中…' : ''}`}</span>
          </div>
        </div>
        <div className="account-list-head"><label><input type="checkbox" aria-label="选择全部账号" checked={allSelected} onChange={toggleAll} />全选</label><span>{isMobile ? '轻点查看详情' : '选择账号查看详情'}</span></div>
        {isLoading ? <Loading /> : error ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : accounts.length === 0 ? <Empty title={search || status ? '没有匹配的账号' : '暂无账号'} description={search || status ? '调整筛选条件后再试。' : '导入已登录的 Muse 会话后开始调度。'} action={search || status ? undefined : <Button onClick={() => setAddOpen(true)}><Plus size={15} />导入账号</Button>} /> : <div className="account-list" aria-label="账号列表">
          {accounts.map(account => <div key={account.id} className={`account-row ${active?.id === account.id ? 'selected' : ''}`}>
            <input type="checkbox" aria-label={`选择 ${account.label}`} checked={selectedIds.has(account.id)} onChange={() => toggleOne(account.id)} />
            <button type="button" className="account-row-button" onClick={() => setSelectedId(account.id)} aria-label={`查看 ${account.label}`}>
              <div className="account-row-main">
                <div className="account-row-title"><strong>{account.label}</strong><Badge tone={statusTone(account.status)}>{accountStatusText[account.status]}</Badge></div>
                <div className="account-row-sub"><span className="mono">{account.id}</span><span>·</span><span>{account.quota?.plan || '未同步额度'}</span></div>
              </div>
              <div className="account-row-quota"><QuotaMini quota={account.quota} /></div>
            </button>
          </div>)}
        </div>}
      </Panel>

      {accounts.length > 0 && !error && <>
      {isMobile && active && <button className="detail-scrim" aria-label="关闭账号详情" onClick={() => setSelectedId(undefined)} />}
      <Panel className={`account-detail ${active ? 'open' : ''}`}>
        {active ? <>
          <div className="detail-head">
            <div className="detail-title"><span className="page-kicker">账号详情</span><h2 className="account-detail-name" aria-label={`账号 ${active.label}`}><span aria-hidden="true" className="detail-display-name" data-name={active.label} /><span className="detail-id">{active.id}</span></h2></div>
            <div className="detail-head-actions">
              <Badge tone={statusTone(active.status)}>{accountStatusText[active.status]}</Badge>
              <button className="icon-button detail-drawer-close" aria-label="关闭详情" onClick={() => setSelectedId(undefined)}><X size={18} /></button>
            </div>
          </div>
          <div className="detail-body">
            <section className="detail-section">
              <div className="detail-section-title"><span>官方额度</span>{active.quota?.checked_at && <span>{fullDate(active.quota.checked_at)}</span>}</div>
              {active.quota && active.quota.found !== false ? <QuotaMeter quota={active.quota} large /> : <div className="quota-card"><div className="quota-card-head"><strong>尚未同步</strong><span className="secondary-cell">额度读取后显示剩余比例</span></div><div className="secondary-cell">账号检测会自动尝试同步；也可以使用下方“刷新额度”。</div></div>}
            </section>

            <section className="detail-section">
              <div className="detail-section-title"><span>会话与调度</span><ShieldCheck size={14} /></div>
              <div className="detail-grid">
                <Detail label="并发上限" value={`${active.max_concurrency} 个任务`} />
                <Detail label="累计调用" value={`${active.use_count} 次`} />
                <Detail label="到期时间" value={`${fullDate(active.expires_at)}${active.expires_estimated ? '（估算）' : ''}`} />
                <Detail label="最后使用" value={fullDate(active.last_used)} />
                <Detail label="最后检测" value={fullDate(active.checked_at)} />
                <Detail label="创建时间" value={fullDate(active.created_at)} />
                <Detail label="Cookie" value={`${active.cookie_count} 条 Cookie`} />
                <Detail label="必要凭据" value={active.essential_ok ? '完整' : '缺失'} />
              </div>
            </section>

            {(active.last_error || active.note) && <section className="detail-section">
              <div className="detail-section-title"><span>最近状态</span></div>
              {active.last_error ? <div className="error-box">{active.last_error}</div> : <div className="detail-note">{active.note}</div>}
            </section>}

            <section className="detail-section">
              <div className="detail-section-title"><span>直接操作</span></div>
              <div className="detail-actions">
                <Button variant="secondary" size="sm" onClick={() => testAccount(active)} disabled={action.isPending}><RefreshCw size={14} />检测</Button>
                <Button variant="secondary" size="sm" onClick={() => refreshQuota(active)} disabled={action.isPending}><Clock3 size={14} />刷新额度</Button>
                <Button variant="secondary" size="sm" onClick={() => { setMessage(''); setErrorMessage(''); setReauth(active) }}><KeyRound size={14} />更新凭据</Button>
                <Button variant="secondary" size="sm" onClick={() => setEdit(active)}><UserCheck size={14} />编辑</Button>
                <Button variant="ghost" size="sm" onClick={() => toggleEnabled(active)} disabled={action.isPending}>{active.enabled ? <UserX size={14} /> : <UserCheck size={14} />}{active.enabled ? '禁用' : '启用'}</Button>
                <Button variant="danger" size="sm" onClick={() => setRemove(active)}><Trash2 size={14} />删除</Button>
              </div>
            </section>
          </div>
        </> : <div className="inline-empty"><CheckCircle2 size={17} />选择一个账号查看额度、会话和操作。</div>}
      </Panel>
      </>}
    </div>

    <AccountDialog open={addOpen} onOpenChange={setAddOpen} onSubmit={async value => { await action.mutateAsync({ path: '/admin/accounts', body: value }); setAddOpen(false); setMessage('账号已导入，后台正在检测并刷新额度'); setAutoChecking(true) }} />
    <AccountDialog open={Boolean(reauth)} onOpenChange={() => setReauth(undefined)} account={reauth} onSubmit={async value => { if (!reauth) return; await action.mutateAsync({ path: `/admin/accounts/${reauth.id}/cookies`, body: value }); setReauth(undefined); setMessage('凭据已更新，后台正在检测并刷新额度'); setAutoChecking(true) }} />
    <Modal open={Boolean(edit)} onOpenChange={() => setEdit(undefined)} title="编辑账号" description={edit?.id}>{edit && <form className="form-stack" onSubmit={event => { event.preventDefault(); const form = new FormData(event.currentTarget); action.mutate({ path: `/admin/accounts/${edit.id}`, method: 'PATCH', body: { label: form.get('label'), max_concurrency: Number(form.get('max_concurrency')) } }, { onSuccess: () => setEdit(undefined) }) }}><Field label="账号标签"><Input name="label" required maxLength={80} defaultValue={edit.label} /></Field><Field label="并发上限"><Input name="max_concurrency" type="number" min={1} max={32} defaultValue={edit.max_concurrency} /></Field><div className="dialog-actions"><Button type="button" variant="secondary" onClick={() => setEdit(undefined)}>取消</Button><Button disabled={action.isPending}>保存</Button></div></form>}</Modal>
    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title={`删除账号「${remove?.label}」？`} description="永久删除账号及加密凭据，任务与媒体保留。" busy={action.isPending} onConfirm={() => remove && deleteAccount(remove)} />
  </div>
}

function QuotaMini({ quota }: { quota?: Account['quota'] }) {
  const remaining = remainingOf(quota)
  if (remaining === undefined) return <span className="secondary-cell">未同步</span>
  return <div className="mini-quota"><small>剩余 {formatPct(remaining)}</small><div className={`mini-quota-track ${quotaTone(remaining)}`}><span style={{ width: `${remaining}%` }} /></div></div>
}

function QuotaMeter({ quota, large = false }: { quota?: Account['quota']; large?: boolean }) {
  if (!quota || quota.found === false) return <div className="secondary-cell">额度未同步</div>
  const weeklyUsed = typeof quota.weekly_used_pct === 'number' ? clampPct(quota.weekly_used_pct) : undefined
  const weeklyRemaining = weeklyUsed === undefined ? undefined : 100 - weeklyUsed
  const extraUsed = typeof quota.extra_used_pct === 'number' ? clampPct(quota.extra_used_pct) : undefined
  const extraRemaining = extraUsed === undefined ? undefined : 100 - extraUsed
  const tooltip = [quota.checked_at ? `额度更新：${fullDate(quota.checked_at)}` : '', weeklyUsed !== undefined ? `每周已用 ${formatPct(weeklyUsed)}` : '', extraUsed !== undefined ? `额外已用 ${formatPct(extraUsed)}` : ''].filter(Boolean).join(' · ')
  return <div className={`quota-meter ${large ? 'large' : ''}`} title={tooltip || undefined}>
    <div className="quota-head"><strong>{quota.plan || '官方额度'}</strong>{weeklyRemaining !== undefined && <span className={`quota-pct ${quotaTone(weeklyRemaining)}`}>剩余 {formatPct(weeklyRemaining)}</span>}</div>
    {weeklyRemaining !== undefined && <div className={`quota-track ${quotaTone(weeklyRemaining)}`} role="progressbar" aria-label="每周额度剩余" aria-valuemin={0} aria-valuemax={100} aria-valuenow={weeklyRemaining}><span style={{ width: `${weeklyRemaining}%` }} /></div>}
    {weeklyRemaining !== undefined && <div className="quota-meta">{quota.weekly_reset ? `每周额度 · ${quota.weekly_reset} 重置` : '每周额度'}</div>}
    {extraRemaining !== undefined && <><div className="quota-head extra"><span>额外额度</span><span className={`quota-pct ${quotaTone(extraRemaining)}`}>剩余 {formatPct(extraRemaining)}</span></div><div className={`quota-track ${quotaTone(extraRemaining)}`} role="progressbar" aria-label="额外额度剩余" aria-valuemin={0} aria-valuemax={100} aria-valuenow={extraRemaining}><span style={{ width: `${extraRemaining}%` }} /></div>{(quota.extra_left || quota.extra_expires) && <div className="quota-meta">{[quota.extra_left, quota.extra_expires === 'never' ? '永不过期' : quota.extra_expires].filter(Boolean).join(' · ')}</div>}</>}
    {quota.error && <div className="quota-meta text-danger">{quota.error}</div>}
  </div>
}

function AccountDialog({ open, onOpenChange, onSubmit, account }: { open: boolean; onOpenChange: (open: boolean) => void; onSubmit: (value: AccountForm) => Promise<void>; account?: Account }) {
  const [serverError, setServerError] = useState('')
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = useForm<AccountForm>({ resolver: zodResolver(accountSchema), defaultValues: { label: '', cookie_header: '' } })
  const submit = handleSubmit(async value => { setServerError(''); try { await onSubmit(value); reset() } catch (err) { setServerError(err instanceof Error ? err.message : '提交失败') } })
  return <Modal open={open} onOpenChange={onOpenChange} title={account ? '更新凭据' : '添加账号'} description={account ? '粘贴新的 Cookie Header 或 JSON，后台会自动检测。' : '从已登录的 Muse 浏览器会话导入 Cookie。'}><form className="form-stack" onSubmit={submit}><Field label="账号标签"><Input placeholder="例如：主账号" {...register('label')} /></Field><Field label="Cookie Header 或 JSON" error={errors.cookie_header?.message}><textarea className="input textarea credential-input" rows={6} placeholder="hatch_sess=…; hatch_vml=…" autoComplete="off" spellCheck={false} {...register('cookie_header')} /></Field>{serverError && <InlineError>{serverError}</InlineError>}<div className="dialog-actions"><Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>取消</Button><Button disabled={isSubmitting}>{isSubmitting ? '提交中…' : account ? '更新' : '导入'}</Button></div></form></Modal>
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span className="detail-label">{label}</span><div className="detail-value">{value}</div></div> }
