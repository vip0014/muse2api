import { useEffect, useMemo, useRef, useState, type ComponentType } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { ArrowRight, LogOut, Moon, Search, Sun, type LucideIcon } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { allNav } from '../lib/navigation'
import { useTheme } from '../lib/theme'

interface Command {
  id: string
  label: string
  description: string
  group: string
  icon: LucideIcon
  run: () => void
  danger?: boolean
}

export function CommandPalette({ open, onOpenChange, onLogout }: { open: boolean; onOpenChange: (open: boolean) => void; onLogout: () => void }) {
  const [query, setQuery] = useState('')
  const [focusIndex, setFocusIndex] = useState(0)
  const inputRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()
  const { theme, toggle } = useTheme()

  useEffect(() => {
    if (!open) return
    setQuery('')
    setFocusIndex(0)
    const timer = window.setTimeout(() => inputRef.current?.focus(), 30)
    return () => window.clearTimeout(timer)
  }, [open])

  const commands = useMemo<Command[]>(() => {
    const close = () => onOpenChange(false)
    const pages: Command[] = allNav.map(item => ({
      id: `page:${item.to}`,
      label: item.label,
      description: item.description,
      group: '前往',
      icon: item.icon,
      run: () => { close(); navigate(item.to) },
    }))
    const actions: Command[] = [
      {
        id: 'action:theme',
        label: theme === 'dark' ? '切换到浅色模式' : '切换到深色模式',
        description: '只影响当前浏览器',
        group: '操作',
        icon: theme === 'dark' ? Sun : Moon,
        run: () => { toggle(); close() },
      },
      {
        id: 'action:logout',
        label: '退出登录',
        description: '结束当前管理员会话',
        group: '操作',
        icon: LogOut,
        danger: true,
        run: () => { close(); onLogout() },
      },
    ]
    const typed = query.trim()
    const searches: Command[] = typed ? [
      { id: 'search:accounts', label: `在账号池中搜索“${typed}”`, description: '按标签或 ID 定位账号', group: '搜索', icon: Search, run: () => { close(); navigate(`/accounts?q=${encodeURIComponent(typed)}`) } },
      { id: 'search:tasks', label: `在任务中心搜索“${typed}”`, description: '按提示词或任务 ID 定位', group: '搜索', icon: Search, run: () => { close(); navigate(`/tasks?q=${encodeURIComponent(typed)}`) } },
    ] : []
    const term = typed.toLowerCase()
    const filteredPages = pages.filter(item => !term || `${item.label} ${item.description}`.toLowerCase().includes(term))
    return [...searches, ...filteredPages, ...actions.filter(item => !term || item.label.toLowerCase().includes(term))]
  }, [navigate, onLogout, onOpenChange, query, theme, toggle])

  useEffect(() => { setFocusIndex(0) }, [query])
  useEffect(() => { setFocusIndex(index => Math.min(index, Math.max(0, commands.length - 1))) }, [commands.length])

  const groups = useMemo(() => {
    const order: string[] = []
    const map = new Map<string, Command[]>()
    for (const command of commands) {
      if (!map.has(command.group)) { map.set(command.group, []); order.push(command.group) }
      map.get(command.group)!.push(command)
    }
    return order.map(group => [group, map.get(group)!] as const)
  }, [commands])

  const run = (command: Command | undefined) => { if (command) command.run() }
  let flatIndex = -1
  const activeId = commands[focusIndex]?.id

  return <Dialog.Root open={open} onOpenChange={onOpenChange}>
    <Dialog.Portal>
      <Dialog.Overlay className="command-overlay" />
      <Dialog.Content className="command-palette" aria-describedby={undefined}>
        <Dialog.Title className="sr-only">搜索或跳转</Dialog.Title>
        <div className="command-input-row">
          <Search size={18} aria-hidden="true" />
          <input
            ref={inputRef}
            className="command-input"
            value={query}
            onChange={event => setQuery(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'ArrowDown') { event.preventDefault(); setFocusIndex(index => commands.length ? (index + 1) % commands.length : 0) }
              if (event.key === 'ArrowUp') { event.preventDefault(); setFocusIndex(index => commands.length ? (index - 1 + commands.length) % commands.length : 0) }
              if (event.key === 'Enter') { event.preventDefault(); run(commands[focusIndex]) }
            }}
            placeholder="搜索页面、账号或任务…"
            aria-label="搜索或跳转"
            role="combobox"
            aria-expanded="true"
            aria-autocomplete="list"
            aria-controls="command-results"
            aria-activedescendant={activeId ? `command-option-${activeId}` : undefined}
            autoComplete="off"
            spellCheck={false}
          />
          <kbd>ESC</kbd>
        </div>
        <div id="command-results" className="command-results" role="listbox" aria-label="命令">
          {groups.length === 0 && <div className="command-empty">没有匹配的结果。换个关键词，或直接打开账号池 / 任务中心。</div>}
          {groups.map(([group, items]) => <div className="command-group" key={group}>
            <div className="command-group-label">{group}</div>
            {items.map(item => {
              flatIndex += 1
              const index = flatIndex
              const Icon = item.icon as ComponentType<{ size?: number; strokeWidth?: number }>
              return <button
                key={item.id}
                id={`command-option-${item.id}`}
                type="button"
                role="option"
                aria-selected={activeId === item.id}
                className={`command-item ${activeId === item.id ? 'active' : ''} ${item.danger ? 'danger' : ''}`}
                onMouseEnter={() => setFocusIndex(index)}
                onClick={() => item.run()}
              >
                <span className="command-icon"><Icon size={16} strokeWidth={1.8} /></span>
                <span className="command-copy"><strong>{item.label}</strong><small>{item.description}</small></span>
                <ArrowRight size={14} className="command-arrow" />
              </button>
            })}
          </div>)}
        </div>
        <div className="command-footer">
          <span><kbd>↑</kbd><kbd>↓</kbd> 选择</span>
          <span><kbd>↵</kbd> 打开</span>
          <span>不需要离开键盘</span>
        </div>
      </Dialog.Content>
    </Dialog.Portal>
  </Dialog.Root>
}
