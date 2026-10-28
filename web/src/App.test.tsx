import { beforeEach, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import App from './App'

beforeEach(() => { vi.restoreAllMocks(); sessionStorage.clear() })

it('shows the single-admin login screen when there is no session', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('{"authenticated":false}', { status: 200 }))
  render(<App />)
  expect(await screen.findByRole('heading', { name: '管理员登录' })).toBeInTheDocument()
  expect(screen.getByLabelText('用户名')).toHaveValue('admin')
  expect(screen.getByLabelText('密码')).toHaveAttribute('type', 'password')
})

it('loads the lazy dashboard after an authenticated session', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async input => {
    if (String(input).includes('/admin/session')) return new Response('{"authenticated":true}', { status: 200 })
    if (String(input).includes('/admin/overview')) {
      return new Response(JSON.stringify({
        status: 'ok', version: '2.1.0', account_stats: {}, task_stats: {},
        tasks: [], media_count: 0, events: [], base_url: '/v1',
        browser_enabled: true, browser_running: false,
      }), { status: 200, headers: { 'Content-Type': 'application/json' } })
    }
    return new Response('{}', { status: 200 })
  })
  render(<App />)
  expect(await screen.findByRole('heading', { name: '服务总览' })).toBeInTheDocument()
})

it('shows an authentication error and keeps no browser-stored key', async () => {
  vi.spyOn(globalThis, 'fetch').mockImplementation(async input => {
    if (String(input).includes('/admin/session')) return new Response('{"authenticated":false}', { status: 200 })
    if (String(input).includes('/admin/login')) return new Response('{"detail":"用户名或密码错误"}', { status: 401 })
    return new Response('{}', { status: 200 })
  })
  render(<App />)
  await screen.findByRole('heading', { name: '管理员登录' })
  await userEvent.type(screen.getByLabelText('密码'), 'wrong-password')
  await userEvent.click(screen.getByRole('button', { name: '登录' }))
  expect(await screen.findByText('用户名或密码错误')).toBeInTheDocument()
  expect(sessionStorage.getItem('muse2api.session-key')).toBeNull()
})
