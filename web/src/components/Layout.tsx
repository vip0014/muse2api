import { Suspense, useEffect, useRef, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { LogOut, Menu, Search, WifiOff, X } from 'lucide-react'
import { useOnlineStatus } from '../lib/hooks'
import { navLabel, primaryNav, secondaryNav } from '../lib/navigation'
import { CommandPalette } from './CommandPalette'
import { ThemeToggle } from './ThemeToggle'
import { PageSkeleton } from './UI'

export function Layout({ onLogout }: { onLogout: () => void }) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)
  const menuButtonRef = useRef<HTMLButtonElement>(null)
  const closeButtonRef = useRef<HTMLButtonElement>(null)
  const sidebarRef = useRef<HTMLElement>(null)
  const location = useLocation()
  const online = useOnlineStatus()
  const label = navLabel(location.pathname)
  const shortcut = typeof navigator !== 'undefined' && /Mac|iPhone|iPad|iPod/.test(navigator.userAgent) ? '⌘ K' : 'Ctrl K'

  useEffect(() => { setMobileOpen(false) }, [location.pathname])
  useEffect(() => {
    if (!mobileOpen) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const focusTimer = window.setTimeout(() => closeButtonRef.current?.focus(), 40)
    return () => { document.body.style.overflow = previous; window.clearTimeout(focusTimer) }
  }, [mobileOpen])
  useEffect(() => {
    if (!mobileOpen) return
    const trap = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return
      const root = sidebarRef.current
      if (!root) return
      const focusables = Array.from(root.querySelectorAll<HTMLElement>('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])')).filter(element => element.offsetParent !== null)
      if (focusables.length === 0) return
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      const active = document.activeElement
      if (event.shiftKey && (active === first || !root.contains(active))) {
        event.preventDefault(); last.focus()
      } else if (!event.shiftKey && (active === last || !root.contains(active))) {
        event.preventDefault(); first.focus()
      }
    }
    window.addEventListener('keydown', trap)
    return () => window.removeEventListener('keydown', trap)
  }, [mobileOpen])
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setCommandOpen(open => !open)
      }
      if (event.key === 'Escape' && mobileOpen) {
        setMobileOpen(false)
        window.setTimeout(() => menuButtonRef.current?.focus(), 30)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [mobileOpen])

  const closeNav = () => {
    setMobileOpen(false)
    window.setTimeout(() => menuButtonRef.current?.focus(), 30)
  }

  return <div className="app-shell">
    <a className="skip-link" href="#main">跳到主要内容</a>
    {mobileOpen && <button className="mobile-scrim" aria-label="关闭导航" onClick={closeNav} />}
    <aside ref={sidebarRef} id="app-navigation" className={`sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
      <div className="brand">
        <img className="brand-mark" src="/logo.png" alt="Muse2API" width={38} height={23} />
        <div className="brand-copy">
          <span className="brand-word">Muse<em>2</em>API</span>
          <span>Operator console</span>
        </div>
        <button ref={closeButtonRef} className="mobile-close icon-button" onClick={closeNav} aria-label="关闭导航"><X size={18} /></button>
      </div>
      <nav className="navigation" aria-label="主导航">
        <div className="nav-caption">工作区</div>
        <div className="nav-group">{primaryNav.map(({ to, label: itemLabel, icon: Icon }) => <NavLink key={to} to={to} end={to === '/'} onClick={() => { if (mobileOpen) closeNav() }} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}><Icon size={16} strokeWidth={1.75} /><span>{itemLabel}</span></NavLink>)}</div>
        <div className="nav-divider" />
        <div className="nav-caption">系统</div>
        <div className="nav-group">{secondaryNav.map(({ to, label: itemLabel, icon: Icon }) => <NavLink key={to} to={to} onClick={() => { if (mobileOpen) closeNav() }} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}><Icon size={16} strokeWidth={1.75} /><span>{itemLabel}</span></NavLink>)}</div>
      </nav>
      <div className="sidebar-bottom">
        <div className={`rail-status ${online ? 'online' : 'offline'}`}><span className="status-dot" /><span>{online ? '控制台在线' : '网络已断开'}</span></div>
        <div className="user-row">
          <span className="user-avatar">管</span>
          <div className="user-meta"><strong>管理员</strong><span>单管理员会话</span></div>
          <button className="icon-button rail-logout" title="退出登录" aria-label="退出登录" onClick={onLogout}><LogOut size={15} /></button>
        </div>
      </div>
    </aside>
    <div className="main-shell">
      <header className="topbar">
        <button ref={menuButtonRef} className="icon-button mobile-menu" onClick={() => setMobileOpen(true)} aria-label="打开导航" aria-expanded={mobileOpen} aria-controls="app-navigation"><Menu size={19} /></button>
        <div className="topbar-crumb"><span className="crumb-prefix">控制台</span><span className="crumb-sep">/</span><strong>{label}</strong></div>
        <button className="command-trigger" onClick={() => setCommandOpen(true)} aria-label="搜索或跳转" aria-keyshortcuts="Control+K Meta+K">
          <Search size={14} />
          <span>搜索或跳转</span>
          <kbd>{shortcut}</kbd>
        </button>
        <div className="topbar-actions">
          <span className={`connection ${online ? 'online' : 'offline'}`} title={online ? '网络连接正常' : '网络已断开'}>
            {online ? <span className="status-dot" /> : <WifiOff size={13} />}
            <span>{online ? '在线' : '离线'}</span>
          </span>
          <ThemeToggle />
        </div>
      </header>
      <main id="main" className="main-content">
        {!online && <div className="offline-banner" role="status"><WifiOff size={15} /><span>网络已断开，当前显示最后一次同步的数据。</span></div>}
        <Suspense fallback={<PageSkeleton />}><Outlet /></Suspense>
      </main>
    </div>
    <CommandPalette open={commandOpen} onOpenChange={setCommandOpen} onLogout={onLogout} />
  </div>
}
