export type AccountStatus = 'active' | 'disabled' | 'expired' | 'cooldown' | 'error' | 'unknown'
export type TaskStatus = 'queued' | 'running' | 'processing' | 'succeeded' | 'completed' | 'failed' | 'cancelled' | 'unknown'

export interface Account {
  id: string
  label: string
  cookie_count: number
  essential_ok: boolean
  status: AccountStatus
  enabled: boolean
  created_at: number
  last_used?: number
  checked_at?: number
  use_count: number
  expires_at?: number
  expires_estimated: boolean
  last_error?: string
  note?: string
  max_concurrency: number
  quota?: { plan?: string; weekly_used_pct?: number; extra_left?: string; checked_at?: number; error?: string }
}

export interface Task {
  id: string
  kind: 'chat' | 'image' | 'video'
  prompt: string
  status: TaskStatus
  progress: number
  account?: string
  created_at: number
  started_at?: number
  completed_at?: number
  updated_at: number
  elapsed?: number
  error?: string
  url?: string
  result?: Record<string, unknown>
}

export interface MediaItem { name: string; url: string; bytes: number; mtime: number; kind: 'image' | 'video'; task_id?: string; mime: string }
export interface SystemEvent { id: string; time: string; level: string; type: string; message: string; request_id?: string; task_id?: string; account_id?: string; upstream_status?: number; duration_ms?: number }
export interface Capability { id: string; alias: string; upstream_model: string; features: string[]; status: string; last_verified?: number; reason?: string }
export interface Overview {
  status: string
  version: string
  account_stats: Record<string, number>
  task_stats: Record<string, number>
  tasks: Task[]
  media_count: number
  events: SystemEvent[]
  base_url: string
  browser_running?: boolean
  browser_enabled?: boolean
  protocol?: string
}
export interface ServiceConfig { host: string; port: number; public_base: string; site: string; concurrency: number; queue_size: number; image_timeout: number; video_timeout: number; chat_timeout: number; media_dir: string; media_retention: string; log_level: string; credential_encryption: boolean; public_media: boolean; browser_runtime: boolean; admin_username?: string; session_ttl?: number; trust_proxy?: boolean }
