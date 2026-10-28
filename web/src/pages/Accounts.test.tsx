import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
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
})
