import type { LucideIcon } from 'lucide-react'
import { Braces, CircleGauge, Database, FileClock, Image, ListChecks, Settings, Users } from 'lucide-react'

export interface NavItem {
  to: string
  label: string
  description: string
  icon: LucideIcon
}

export const primaryNav: NavItem[] = [
  { to: '/', label: '总览', description: '服务状态、待处理事项与最近活动', icon: CircleGauge },
  { to: '/accounts', label: '账号池', description: 'Muse 会话、额度与调度状态', icon: Users },
  { to: '/tasks', label: '任务中心', description: '生成任务、进度与结果', icon: ListChecks },
  { to: '/media', label: '媒体库', description: '已生成的图片与视频', icon: Image },
  { to: '/playground', label: 'API 调试台', description: '直接发起 OpenAI 兼容请求', icon: Braces },
]

export const secondaryNav: NavItem[] = [
  { to: '/models', label: '模型与能力', description: '模型映射与能力边界', icon: Database },
  { to: '/settings', label: '系统设置', description: '服务、凭据与运行时配置', icon: Settings },
  { to: '/logs', label: '日志与审计', description: '请求、安全事件与导出', icon: FileClock },
]

export const allNav = [...primaryNav, ...secondaryNav]

export function navLabel(pathname: string) {
  if (pathname === '/') return '总览'
  return allNav.find(item => item.to !== '/' && pathname.startsWith(item.to))?.label ?? '控制台'
}
