import { describe, expect, it } from 'vitest'
import { taskRefetchInterval } from './tasks'
import type { Task } from './types'

const task = (status: Task['status']): Task => ({
  id: 'task_1', kind: 'image', prompt: 'p', status, progress: 0, created_at: 1, updated_at: 1,
})

describe('taskRefetchInterval', () => {
  it('polls quickly only while work is active', () => {
    expect(taskRefetchInterval([task('queued')])).toBe(3_000)
    expect(taskRefetchInterval([task('running'), task('succeeded')])).toBe(3_000)
    expect(taskRefetchInterval([task('processing')])).toBe(3_000)
  })

  it('backs off when all tasks are terminal or the list is empty', () => {
    expect(taskRefetchInterval([])).toBe(30_000)
    expect(taskRefetchInterval(undefined)).toBe(30_000)
    expect(taskRefetchInterval([task('succeeded'), task('failed'), task('cancelled')])).toBe(30_000)
  })
})
