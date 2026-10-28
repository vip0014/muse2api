import { Moon, Sun } from 'lucide-react'
import { useTheme } from '../lib/theme'

export function ThemeToggle({ className = '' }: { className?: string }) {
  const { theme, toggle } = useTheme()
  const next = theme === 'dark' ? '浅色' : '深色'
  return <button type="button" className={`icon-button theme-toggle ${className}`} onClick={toggle} title={`切换到${next}模式`} aria-label={`切换到${next}模式`}>
    {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
  </button>
}
