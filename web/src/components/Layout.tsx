import { Suspense, useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { ChevronRight, LogOut, Menu, Search, WifiOff, X } from 'lucide-react'
import { useOnlineStatus } from '../lib/hooks'
import { navLabel, primaryNav, secondaryNav } from '../lib/navigation'
import { CommandPalette } from './CommandPalette'
import { ThemeToggle } from './ThemeToggle'
import { PageSkeleton } from './UI'

export function Layout({ onLogout }: { onLogout: () => void }) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const [commandOpen, setCommandOpen] = useState(false)
  const location = useLocation()
  const online = useOnlineStatus()
  const label = navLabel(location.pathname)

  useEffect(() => { setMobileOpen(false) }, [location.pathname])
  useEffect(() => {
    if (!mobileOpen) return
    const previous = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = previous }
  }, [mobileOpen])
  useEffect(() => {
    const handler = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setCommandOpen(open => !open)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [])

  return <div className="app-shell">
    <a className="skip-link" href="#main">跳到主要内容</a>
    {mobileOpen && <button className="mobile-scrim" aria-label="关闭导航" onClick={() => setMobileOpen(false)} />}
    <aside className={`sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
      <div className="brand">
        <img className="brand-mark" src="/logo.png" alt="Muse2API" width={54} height={32} />
        <div className="brand-copy"><strong>Muse2API</strong><span>Operator console</span></div>
        <button className="mobile-close icon-button" onClick={() => setMobileOpen(false)} aria-label="关闭导航"><X size={18} /></button>
      </div>
      <nav className="navigation" aria-label="主导航">
        <div className="nav-caption">工作区</div>
        <div className="nav-group">{primaryNav.map(({ to, label: itemLabel, icon: Icon }) => <NavLink key={to} to={to} end={to === '/'} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}><Icon size={16} strokeWidth={1.8} /><span>{itemLabel}</span></NavLink>)}</div>
        <div className="nav-divider" />
        <div className="nav-caption">系统</div>
        <div className="nav-group">{secondaryNav.map(({ to, label: itemLabel, icon: Icon }) => <NavLink key={to} to={to} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}><Icon size={16} strokeWidth={1.8} /><span>{itemLabel}</span></NavLink>)}</div>
      </nav>
      <div className="sidebar-bottom">
        <div className={`rail-status ${online ? 'online' : 'offline'}`}><span className="status-dot" /><span>{online ? '控制台在线' : '网络已断开'}</span></div>
        <div className="user-row">
          <span className="user-avatar">管</span>
          <div className="user-meta"><strong>管理员</strong><span>单管理员会话</span></div>
          <button className="icon-button rail-logout" title="退出登录" aria-label="退出登录" onClick={onLogout}><LogOut size={16} /></button>
        </div>
      </div>
    </aside>
    <div className="main-shell">
      <header className="topbar">
        <button className="icon-button mobile-menu" onClick={() => setMobileOpen(true)} aria-label="打开导航"><Menu size={19} /></button>
        <div className="topbar-crumb"><span>控制台</span><ChevronRight size={13} /><strong>{label}</strong></div>
        <button className="command-trigger" onClick={() => setCommandOpen(true)} aria-label="搜索或跳转" aria-keyshortcuts="Control+K Meta+K">
          <Search size={15} />
          <span>搜索或跳转</span>
          <kbd>⌘ K</kbd>
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
