import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Accounts from './Accounts'
import type { Account } from '../lib/types'

const account: Account = {
  id: 'acc_1',
  label: '主账号',
  cookie_count: 3,
  essential_ok: true,
  status: 'active',
  enabled: true,
  created_at: 1_700_000_000,
  use_count: 2,
  expires_estimated: false,
  max_concurrency: 1,
}

function json(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
}

describe('Accounts page', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('renders real account metadata and runs a session test', async () => {
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url.endsWith('/admin/accounts/acc_1/test')) {
        expect(init?.method).toBe('POST')
        return json({ ok: true, message: '会话有效' })
      }
      if (url.endsWith('/admin/accounts')) {
        return json({ accounts: [account], stats: { total: 1, active: 1 } })
      }
      return json({})
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: 0 } } })
    render(<QueryClientProvider client={client}><Accounts /></QueryClientProvider>)

    expect(await screen.findByRole('heading', { name: '主账号' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '查看 主账号' })).toBeInTheDocument()
    expect(screen.getByText(/3 条 Cookie/)).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '检测' }))
    expect(await screen.findByText('会话有效')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some(([input, init]) => String(input).endsWith('/admin/accounts/acc_1/test') && init?.method === 'POST')).toBe(true)
  })

  it('stops the background poll when the imported account settles', async () => {
    const neverChecked: Account = { ...account, id: 'acc_old', label: '旧账号', status: 'unknown', checked_at: undefined }
    const imported: Account = {
      ...account,
      id: 'acc_new',
      label: '新账号',
      status: 'active',
      checked_at: 1_700_000_100,
      quota: { found: true, plan: 'Muse 免费版', weekly_used_pct: 10, checked_at: 1_700_000_100 },
    }
    let added = false
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = String(input)
      if (url.endsWith('/admin/accounts') && init?.method === 'POST') {
        added = true
        return json({ added: [imported], count: 1, auto_check: true }, 201)
      }
      if (url.endsWith('/admin/accounts')) {
        return json({ accounts: added ? [neverChecked, imported] : [neverChecked] })
      }
      return json({})
    })
    const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: 0 } } })
    render(<QueryClientProvider client={client}><Accounts /></QueryClientProvider>)

    await screen.findByRole('heading', { name: '旧账号' })
    await userEvent.click(screen.getByRole('button', { name: '添加账号' }))
    await userEvent.type(await screen.findByLabelText('账号标签'), '新账号')
    await userEvent.type(screen.getByLabelText('Cookie Header 或 JSON'), 'hatch_sess=1; hatch_vml=2')
    await userEvent.click(screen.getByRole('button', { name: '导入' }))

    expect(await screen.findByText('账号已导入，后台正在检测并刷新额度')).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: '查看 新账号' })).toBeInTheDocument()
    // The old account is never checked; the poll must still stop once the
    // account this page imported has settled.
    await waitFor(() => expect(screen.queryByText('检测中')).not.toBeInTheDocument())
  })
})
