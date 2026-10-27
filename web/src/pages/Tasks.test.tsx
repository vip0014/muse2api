import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Tasks from './Tasks'
import type { Task } from '../lib/types'

const task: Task = {
  id: 'task_demo',
  kind: 'image',
  prompt: '海边灯塔',
  status: 'completed',
  progress: 100,
  account: 'acc_1',
  created_at: 1_700_000_000,
  updated_at: 1_700_000_010,
  elapsed: 3.2,
  url: '/v1/media/demo.png',
}

function json(value: unknown) {
  return new Response(JSON.stringify(value), { status: 200, headers: { 'Content-Type': 'application/json' } })
}

describe('Tasks page', () => {
  beforeEach(() => { vi.restoreAllMocks() })

  it('renders task state from the API without fabricating progress', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ tasks: [task], count: 1 }))
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(<QueryClientProvider client={client}><Tasks /></QueryClientProvider>)

    expect(await screen.findByText('海边灯塔')).toBeInTheDocument()
    expect(screen.getByText('已完成')).toBeInTheDocument()
    expect(screen.getByText('100%')).toBeInTheDocument()
    expect(screen.getByText('task_demo')).toBeInTheDocument()
  })
})
