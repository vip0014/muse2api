export function dateTime(timestamp?: number) { if (!timestamp) return '—'; return new Intl.DateTimeFormat('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }).format(new Date(timestamp * 1000)) }
export function fullDate(timestamp?: number) { if (!timestamp) return '—'; return new Date(timestamp * 1000).toLocaleString('zh-CN') }
export function bytes(value: number) { if (!value) return '0 B'; const units = ['B', 'KB', 'MB', 'GB']; const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1); return `${(value / 1024 ** index).toFixed(index ? 1 : 0)} ${units[index]}` }
export function percent(success: number, total: number) { return total ? `${Math.round(success / total * 100)}%` : '—' }
export function duration(seconds?: number) { if (!seconds) return '—'; return seconds < 60 ? `${seconds.toFixed(1)} 秒` : `${Math.floor(seconds / 60)} 分 ${Math.round(seconds % 60)} 秒` }
export const taskStatusText: Record<string, string> = { queued: '排队中', running: '执行中', processing: '执行中', succeeded: '已完成', completed: '已完成', failed: '失败', cancelled: '已取消', unknown: '结果未知' }
export const accountStatusText: Record<string, string> = { active: '正常', disabled: '已禁用', expired: '已失效', cooldown: '冷却中', error: '异常', unknown: '未检测' }
