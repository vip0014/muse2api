import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { useForm } from 'react-hook-form'
import { z } from 'zod'
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, ArrowLeft, Clock3, KeyRound, Pencil, Plus, RefreshCw, ShieldCheck, Trash2, UserCheck, UserX, Users } from 'lucide-react'
import { api, json } from '../lib/api'
import type { Account, AccountStatus } from '../lib/types'
import { accountStatusText, fullDate } from '../lib/format'
import { listKeyDown, useMediaQuery } from '../lib/hooks'
import { Badge, Button, CopyButton, Empty, ErrorState, Field, InlineError, Input, Loading, PageHeader, RefreshError, SearchField, Select, SuccessMessage } from '../components/UI'
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
function initialOf(label: string) { return label.trim().slice(0, 1).toUpperCase() || '?' }

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

  const { data, isLoading, isFetching, error, refetch } = useQuery({ queryKey: ['accounts'], queryFn: ({ signal }) => api<{ accounts: Account[] }>('/admin/accounts', { signal }), refetchInterval: autoChecking ? 3000 : false })
  useEffect(() => {
    if (!autoChecking) return
    const timer = window.setTimeout(() => setAutoChecking(false), 180_000)
    return () => window.clearTimeout(timer)
  }, [autoChecking])
  useEffect(() => {
    if (autoChecking && data?.accounts.length && data.accounts.every(account => account.checked_at && (account.quota?.checked_at || account.last_error))) setAutoChecking(false)
  }, [autoChecking, data])
  useEffect(() => { if (initialFocus) setSelectedId(initialFocus) }, [initialFocus])
  useEffect(() => {
    if (!isMobile) return
    const handler = (event: KeyboardEvent) => { if (event.key === 'Escape') setSelectedId(undefined) }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [isMobile])

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
  const loadError = Boolean(error && !data)
  const refreshError = Boolean(error && data)
  const single = Boolean(loadError || isLoading || accounts.length === 0)

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
    {refreshError && <RefreshError message={error instanceof Error ? error.message : '无法刷新账号列表'} retry={() => refetch()} />}

    <section className={`workbench accounts-workbench ${single ? 'single' : ''}`} aria-label="账号工作台" aria-busy={isLoading}>
      <div className="collection-pane">
        <div className="collection-toolbar">
          <div className="collection-toolbar-row">
            <SearchField value={search} onChange={setSearch} placeholder="搜索标签或 ID" label="搜索账号" />
            <Select value={status} onChange={event => setStatus(event.target.value)} aria-label="账号状态"><option value="">全部状态</option>{Object.entries(accountStatusText).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</Select>
          </div>
        </div>

        <div className="collection-meta">
          {selectedIds.size > 0 ? <>
            <span className="bulk-note">已选 {selectedIds.size} 项</span>
            <div className="meta-actions">
              <Button size="sm" variant="secondary" onClick={() => bulk('enable')} disabled={action.isPending}><UserCheck size={12} />启用</Button>
              <Button size="sm" variant="secondary" onClick={() => bulk('disable')} disabled={action.isPending}><UserX size={12} />禁用</Button>
              <Button size="sm" variant="ghost" onClick={() => setSelectedIds(new Set())}>取消</Button>
            </div>
          </> : <>
            <label className="select-all"><input type="checkbox" aria-label="选择全部账号" checked={allSelected} onChange={toggleAll} disabled={accounts.length === 0} />全选</label>
            <div className="meta-actions">
              {autoChecking && <span className="meta-live"><span className="live-dot" />检测中</span>}
              <span>{isLoading ? '正在读取…' : loadError ? '读取失败' : `${accounts.length} 个账号`}</span>
              <button className="icon-button" onClick={() => refetch()} aria-label="刷新账号列表" title="刷新"><RefreshCw size={13} className={isFetching ? 'spin' : ''} /></button>
            </div>
          </>}
        </div>

        {isLoading ? <Loading /> : loadError ? <ErrorState message={error instanceof Error ? error.message : '加载失败'} retry={() => refetch()} /> : accounts.length === 0 ? <Empty title={search || status ? '没有匹配的账号' : '暂无账号'} description={search || status ? '调整筛选条件后再试。' : '导入已登录的 Muse 会话后开始调度。'} action={search || status ? undefined : <Button onClick={() => setAddOpen(true)}><Plus size={15} />导入账号</Button>} /> : <div className="collection-list" aria-label="账号列表" onKeyDown={listKeyDown}>
          {accounts.map(account => <div key={account.id} className={`account-row ${active?.id === account.id ? 'selected' : ''}`}>
            <input type="checkbox" aria-label={`选择 ${account.label}`} checked={selectedIds.has(account.id)} onChange={() => toggleOne(account.id)} />
            <button type="button" className="account-row-button" data-list-row onClick={() => setSelectedId(account.id)} aria-label={`查看 ${account.label}`} aria-current={active?.id === account.id ? 'true' : undefined}>
              <span className="account-avatar" aria-hidden="true">{initialOf(account.label)}</span>
              <span className="account-row-main">
                <span className="account-row-title"><strong>{account.label}</strong><Badge tone={statusTone(account.status)}>{accountStatusText[account.status]}</Badge></span>
                <span className="account-row-sub"><span className="mono">{account.id}</span><span>·</span><span>{account.quota?.plan || '未同步额度'}</span></span>
              </span>
              <span className="account-row-quota"><QuotaMini quota={account.quota} /></span>
            </button>
          </div>)}
        </div>}

        {!isLoading && !loadError && accounts.length > 0 && <div className="collection-foot"><span>↑↓ 切换 · Enter 查看</span><span>{search || status ? '筛选结果' : '全部账号'}</span></div>}
      </div>

      {accounts.length > 0 && !loadError && <>
        {isMobile && active && <button className="detail-scrim" aria-label="关闭账号详情" onClick={() => setSelectedId(undefined)} />}
        <section className={`inspector-pane account-detail ${active ? 'open' : ''}`} aria-label="账号详情">
          {active ? <AccountInspector account={active} busy={action.isPending} onTest={testAccount} onQuota={refreshQuota} onReauth={account => { setMessage(''); setErrorMessage(''); setReauth(account) }} onEdit={setEdit} onToggle={toggleEnabled} onRemove={setRemove} onClose={() => setSelectedId(undefined)} /> : <div className="inline-empty"><Users size={17} />选择一个账号查看额度、会话和操作。</div>}
        </section>
      </>}
    </section>

    <AccountDialog open={addOpen} onOpenChange={setAddOpen} onSubmit={async value => { await action.mutateAsync({ path: '/admin/accounts', body: value }); setAddOpen(false); setMessage('账号已导入，后台正在检测并刷新额度'); setAutoChecking(true) }} />
    <AccountDialog open={Boolean(reauth)} onOpenChange={() => setReauth(undefined)} account={reauth} onSubmit={async value => { if (!reauth) return; await action.mutateAsync({ path: `/admin/accounts/${reauth.id}/cookies`, body: value }); setReauth(undefined); setMessage('凭据已更新，后台正在检测并刷新额度'); setAutoChecking(true) }} />
    <Modal open={Boolean(edit)} onOpenChange={() => setEdit(undefined)} title="编辑账号" description={edit?.id}>{edit && <form className="form-stack" onSubmit={event => { event.preventDefault(); const form = new FormData(event.currentTarget); action.mutate({ path: `/admin/accounts/${edit.id}`, method: 'PATCH', body: { label: form.get('label'), max_concurrency: Number(form.get('max_concurrency')) } }, { onSuccess: () => setEdit(undefined) }) }}><Field label="账号标签"><Input name="label" required maxLength={80} defaultValue={edit.label} /></Field><Field label="并发上限"><Input name="max_concurrency" type="number" min={1} max={32} defaultValue={edit.max_concurrency} /></Field><div className="dialog-actions"><Button type="button" variant="secondary" onClick={() => setEdit(undefined)}>取消</Button><Button disabled={action.isPending}>保存</Button></div></form>}</Modal>
    <Confirm open={Boolean(remove)} onOpenChange={() => setRemove(undefined)} title={`删除账号「${remove?.label}」？`} description="永久删除账号及加密凭据，任务与媒体保留。" busy={action.isPending} onConfirm={() => remove && deleteAccount(remove)} />
  </div>
}

function AccountInspector({ account, busy, onTest, onQuota, onReauth, onEdit, onToggle, onRemove, onClose }: { account: Account; busy: boolean; onTest: (account: Account) => void; onQuota: (account: Account) => void; onReauth: (account: Account) => void; onEdit: (account: Account) => void; onToggle: (account: Account) => void; onRemove: (account: Account) => void; onClose: () => void }) {
  const weeklyUsed = typeof account.quota?.weekly_used_pct === 'number' ? clampPct(account.quota.weekly_used_pct) : undefined
  const weeklyRemaining = weeklyUsed === undefined ? undefined : 100 - weeklyUsed
  const extraUsed = typeof account.quota?.extra_used_pct === 'number' ? clampPct(account.quota.extra_used_pct) : undefined
  const extraRemaining = extraUsed === undefined ? undefined : 100 - extraUsed
  const ringRemaining = weeklyRemaining ?? extraRemaining
  const hasQuota = account.quota?.found !== false && ringRemaining !== undefined

  return <>
    <div className="inspector-head">
      <div className="account-hero">
        <span className="account-avatar" aria-hidden="true">{initialOf(account.label)}</span>
        <div className="account-hero-copy">
          <span className="page-kicker">账号详情</span>
          <div className="inspector-title"><h2 className="truncate" title={account.label}>{account.label}</h2><Badge tone={statusTone(account.status)}>{accountStatusText[account.status]}</Badge></div>
          <div className="inspector-sub">
            <span className="mono">{account.id}</span>
            <CopyButton value={account.id} label="复制 ID" />
            <span>·</span>
            <span>{account.quota?.plan || '未同步额度'}</span>
          </div>
        </div>
      </div>
      <div className="inspector-head-actions">
        <button className="button button-secondary button-sm detail-drawer-close" onClick={onClose}><ArrowLeft size={14} />返回</button>
        <Button variant="secondary" size="sm" onClick={() => onTest(account)} disabled={busy}><RefreshCw size={13} />检测</Button>
        <Button variant="secondary" size="sm" onClick={() => onQuota(account)} disabled={busy}><Clock3 size={13} />刷新额度</Button>
      </div>
    </div>
    <div className="inspector-body">
      <div className="detail-section-title"><span>官方额度</span><span className="title-hint">{account.quota?.checked_at ? `同步于 ${fullDate(account.quota.checked_at)}` : '尚未同步'}</span></div>
      <div className="quota-overview">
        <QuotaRing remaining={hasQuota ? ringRemaining : undefined} />
        <div className="quota-lines">
          {hasQuota && weeklyRemaining !== undefined && <div className="quota-line">
            <div className="quota-line-head"><strong>每周额度</strong><span className={quotaTone(weeklyRemaining)}>剩余 {formatPct(weeklyRemaining)}</span></div>
            <div className={`meter ${quotaTone(weeklyRemaining)}`} role="progressbar" aria-label="每周额度剩余" aria-valuemin={0} aria-valuemax={100} aria-valuenow={weeklyRemaining}><span style={{ width: `${weeklyRemaining}%` }} /></div>
            <div className="meter-caption"><span>{account.quota?.weekly_reset ? `每周额度 · ${account.quota.weekly_reset} 重置` : '每周额度'}</span>{weeklyUsed !== undefined && <span>已用 {formatPct(weeklyUsed)}</span>}</div>
          </div>}
          {hasQuota && extraRemaining !== undefined && <div className="quota-line">
            <div className="quota-line-head"><strong>额外额度</strong><span className={quotaTone(extraRemaining)}>剩余 {formatPct(extraRemaining)}</span></div>
            <div className={`meter ${quotaTone(extraRemaining)}`} role="progressbar" aria-label="额外额度剩余" aria-valuemin={0} aria-valuemax={100} aria-valuenow={extraRemaining}><span style={{ width: `${extraRemaining}%` }} /></div>
            {(account.quota?.extra_left || account.quota?.extra_expires) && <div className="meter-caption"><span>{[account.quota.extra_left, account.quota.extra_expires === 'never' ? '永不过期' : account.quota.extra_expires].filter(Boolean).join(' · ')}</span>{extraUsed !== undefined && <span>已用 {formatPct(extraUsed)}</span>}</div>}
          </div>}
          {!hasQuota && <div>
            <div className="quota-line-head"><strong>{account.quota?.plan || '官方额度'}</strong><span>未同步</span></div>
            <p className="detail-note">账号检测会自动尝试同步；也可以使用上方“刷新额度”。</p>
          </div>}
          {account.quota?.error && <div className="meter-caption text-danger">{account.quota.error}</div>}
        </div>
      </div>

      <section className="detail-section">
        <div className="detail-section-title"><span>会话与调度</span><ShieldCheck size={13} /></div>
        <div className="detail-grid">
          <Detail label="调度状态" value={account.enabled ? '已启用' : '已禁用'} />
          <Detail label="并发上限" value={`${account.max_concurrency} 个任务`} />
          <Detail label="累计调用" value={`${account.use_count} 次`} />
          <Detail label="到期时间" value={`${fullDate(account.expires_at)}${account.expires_estimated ? '（估算）' : ''}`} />
          <Detail label="最后使用" value={fullDate(account.last_used)} />
          <Detail label="最后检测" value={fullDate(account.checked_at)} />
          <Detail label="创建时间" value={fullDate(account.created_at)} />
          <Detail label="Cookie" value={`${account.cookie_count} 条 Cookie`} />
          <Detail label="必要凭据" value={account.essential_ok ? '完整' : '缺失'} />
        </div>
      </section>

      {(account.last_error || account.note) && <section className="detail-section">
        <div className="detail-section-title"><span>最近状态</span></div>
        {account.last_error
          ? <div className="account-state-callout error"><AlertTriangle size={14} /><span>{account.last_error}</span></div>
          : <div className="account-state-callout"><ShieldCheck size={14} /><span>{account.note}</span></div>}
      </section>}
    </div>
    <div className="inspector-foot">
      <Button variant="secondary" size="sm" onClick={() => onReauth(account)} disabled={busy}><KeyRound size={13} />更新凭据</Button>
      <Button variant="secondary" size="sm" onClick={() => onEdit(account)} disabled={busy}><Pencil size={13} />编辑</Button>
      <Button variant="ghost" size="sm" onClick={() => onToggle(account)} disabled={busy}>{account.enabled ? <UserX size={13} /> : <UserCheck size={13} />}{account.enabled ? '禁用' : '启用'}</Button>
      <span className="spacer" />
      <Button variant="danger" size="sm" onClick={() => onRemove(account)} disabled={busy}><Trash2 size={13} />删除</Button>
    </div>
  </>
}

function QuotaRing({ remaining }: { remaining?: number }) {
  if (remaining === undefined) return <div className="quota-ring missing" role="img" aria-label="额度未同步"><div className="quota-ring-copy"><strong>—</strong><span>未同步</span></div></div>
  const tone = quotaTone(remaining)
  return <div className={`quota-ring ${tone}`} style={{ '--p': remaining } as CSSProperties} role="img" aria-label={`额度剩余 ${formatPct(remaining)}`}><div className="quota-ring-copy"><strong>{formatPct(remaining)}</strong><span>剩余</span></div></div>
}

function QuotaMini({ quota }: { quota?: Account['quota'] }) {
  const remaining = remainingOf(quota)
  if (remaining === undefined) return <span className="secondary-cell">未同步</span>
  return <div className="mini-quota"><small>剩余 {formatPct(remaining)}</small><div className={`mini-quota-track ${quotaTone(remaining)}`}><span style={{ width: `${remaining}%` }} /></div></div>
}

function AccountDialog({ open, onOpenChange, onSubmit, account }: { open: boolean; onOpenChange: (open: boolean) => void; onSubmit: (value: AccountForm) => Promise<void>; account?: Account }) {
  const [serverError, setServerError] = useState('')
  const { register, handleSubmit, reset, formState: { errors, isSubmitting } } = useForm<AccountForm>({ resolver: zodResolver(accountSchema), defaultValues: { label: '', cookie_header: '' } })
  const submit = handleSubmit(async value => { setServerError(''); try { await onSubmit(value); reset() } catch (err) { setServerError(err instanceof Error ? err.message : '提交失败') } })
  return <Modal open={open} onOpenChange={onOpenChange} title={account ? '更新凭据' : '添加账号'} description={account ? '粘贴新的 Cookie Header 或 JSON，后台会自动检测。' : '从已登录的 Muse 浏览器会话导入 Cookie。'}><form className="form-stack" onSubmit={submit}><Field label="账号标签"><Input placeholder="例如：主账号" {...register('label')} /></Field><Field label="Cookie Header 或 JSON" error={errors.cookie_header?.message}><textarea className="input textarea credential-input" rows={6} placeholder="hatch_sess=…; hatch_vml=…" autoComplete="off" spellCheck={false} {...register('cookie_header')} /></Field>{serverError && <InlineError>{serverError}</InlineError>}<div className="dialog-actions"><Button type="button" variant="secondary" onClick={() => onOpenChange(false)}>取消</Button><Button disabled={isSubmitting}>{isSubmitting ? '提交中…' : account ? '更新' : '导入'}</Button></div></form></Modal>
}

function Detail({ label, value }: { label: string; value: string }) { return <div><span className="detail-label">{label}</span><div className="detail-value">{value}</div></div> }
