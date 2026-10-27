import type { Task } from './types'

const ACTIVE_STATUSES = new Set(['queued', 'running', 'processing'])

// Active work needs fast feedback; a terminal-only list does not. Keeping the
// idle interval long avoids a five-second full task-list request forever after
// the last job finishes.
export function taskRefetchInterval(tasks: Task[] | undefined): number {
  if (!tasks || tasks.length === 0) return 30_000
  return tasks.some(task => ACTIVE_STATUSES.has(task.status)) ? 3_000 : 30_000
}
