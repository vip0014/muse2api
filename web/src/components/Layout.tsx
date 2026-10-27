import { Suspense, useState } from 'react'
import { NavLink, Outlet } from 'react-router-dom'
import { Braces, CircleGauge, Database, FileClock, Image, ListChecks, LogOut, Menu, Settings, Users, X } from 'lucide-react'
import { Loading } from './UI'

const primaryNav = [
  { to: '/', label: '总览', icon: CircleGauge },
  { to: '/accounts', label: '账号池', icon: Users },
  { to: '/tasks', label: '任务中心', icon: ListChecks },
  { to: '/media', label: '媒体库', icon: Image },
  { to: '/playground', label: 'API 调试台', icon: Braces },
]
const secondaryNav = [
  { to: '/models', label: '模型与能力', icon: Database },
  { to: '/settings', label: '系统设置', icon: Settings },
  { to: '/logs', label: '日志与审计', icon: FileClock },
]

export function Layout({ onLogout }: { onLogout: () => void }) {
  const [mobileOpen, setMobileOpen] = useState(false)
  const close = () => setMobileOpen(false)
  return <div className="app-shell">
    {mobileOpen && <button className="mobile-scrim" aria-label="关闭导航" onClick={close} />}
    <aside className={`sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
      <div className="brand">
        <img className="brand-mark" src="/logo.png" alt="" width={54} height={32} />
        <div className="brand-copy"><strong>Muse2API</strong></div>
        <button className="mobile-close icon-button" onClick={close} aria-label="关闭"><X size={18} /></button>
      </div>
      <nav className="navigation" aria-label="主导航">
        <div className="nav-group">{primaryNav.map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} end={to === '/'} onClick={close} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}><Icon size={17} /><span>{label}</span></NavLink>)}</div>
        <div className="nav-divider" />
        <div className="nav-group">{secondaryNav.map(({ to, label, icon: Icon }) => <NavLink key={to} to={to} onClick={close} className={({ isActive }) => isActive ? 'nav-link active' : 'nav-link'}><Icon size={17} /><span>{label}</span></NavLink>)}</div>
      </nav>
      <div className="sidebar-bottom"><div className="user-row"><span className="user-avatar">管</span><div className="user-meta"><strong>管理员</strong></div><button className="icon-button" title="退出登录" aria-label="退出登录" onClick={onLogout}><LogOut size={16} /></button></div></div>
    </aside>
    <div className="main-shell">
      <header className="mobile-header"><button className="icon-button" onClick={() => setMobileOpen(true)} aria-label="打开导航"><Menu size={20} /></button><img className="mobile-brand-mark" src="/logo.png" alt="" width={37} height={22} /><span className="mobile-brand">Muse2API</span></header>
      <main className="main-content"><Suspense fallback={<div className="panel"><Loading /></div>}><Outlet /></Suspense></main>
    </div>
  </div>
}
