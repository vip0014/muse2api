import { lazy, useEffect, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { ArrowRight } from 'lucide-react'
import { api } from './lib/api'
import { Layout } from './components/Layout'
import { Button, InlineError, Input, Loading } from './components/UI'

// Route-level splitting keeps the login and shell bundle small; each page is
// fetched only when visited.
const Dashboard = lazy(() => import('./pages/Dashboard'))
const Accounts = lazy(() => import('./pages/Accounts'))
const Tasks = lazy(() => import('./pages/Tasks'))
const MediaLibrary = lazy(() => import('./pages/MediaLibrary'))
const Playground = lazy(() => import('./pages/Playground'))
const Models = lazy(() => import('./pages/Models'))
const Settings = lazy(() => import('./pages/Settings'))
const Logs = lazy(() => import('./pages/Logs'))

const queryClient = new QueryClient({ defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 10_000 }, mutations: { retry: 0 } } })

function Login({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setBusy(true); setError('')
    try {
      await api('/admin/login', { method: 'POST', body: JSON.stringify({ username: username.trim(), password }) })
      onLogin()
    } catch (err) { setError(err instanceof Error ? err.message : '登录失败') } finally { setBusy(false) }
  }
  return <main className="login-page-centered"><section className="login-card">
    <div className="login-brand"><img className="brand-mark large" src="/logo.png" alt="" width={70} height={42} /><strong>Muse2API</strong></div>
    <div className="login-title"><h1>管理员登录</h1></div>
    <form className="login-form" onSubmit={submit}>
      <label className="field"><span className="field-label">用户名</span><Input autoFocus required autoComplete="username" placeholder="admin" value={username} onChange={e => setUsername(e.target.value)} /></label>
      <label className="field"><span className="field-label">密码</span><Input required type="password" autoComplete="current-password" placeholder="输入密码" value={password} onChange={e => setPassword(e.target.value)} /></label>
      {error && <InlineError>{error}</InlineError>}
      <Button className="login-submit" disabled={busy || !username.trim() || !password}>{busy ? '登录中…' : '登录'}<ArrowRight size={15} /></Button>
    </form>
  </section></main>
}

function AppRoutes() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null)
  const client = useQueryClient()
  useEffect(() => {
    let active = true
    api<{ authenticated: boolean }>('/admin/session')
      .then(result => {
        if (!active) return
        if (!result.authenticated) client.clear()
        setAuthenticated(result.authenticated)
      })
      .catch(() => { if (active) setAuthenticated(false) })
    const handler = () => { client.clear(); setAuthenticated(false) }
    window.addEventListener('muse2api:unauthorized', handler)
    return () => { active = false; window.removeEventListener('muse2api:unauthorized', handler) }
  }, [client])
  if (authenticated === null) return <div className="app-loading"><Loading /></div>
  if (!authenticated) return <Login onLogin={() => { client.clear(); setAuthenticated(true) }} />
  const logout = () => { void api('/admin/logout', { method: 'POST' }).finally(() => { client.clear(); setAuthenticated(false) }) }
  return <Routes><Route element={<Layout onLogout={logout} />}><Route index element={<Dashboard />} /><Route path="accounts" element={<Accounts />} /><Route path="tasks" element={<Tasks />} /><Route path="media" element={<MediaLibrary />} /><Route path="playground" element={<Playground />} /><Route path="models" element={<Models />} /><Route path="settings" element={<Settings />} /><Route path="logs" element={<Logs />} /><Route path="*" element={<Navigate to="/" replace />} /></Route></Routes>
}

export default function App() { return <QueryClientProvider client={queryClient}><BrowserRouter><AppRoutes /></BrowserRouter></QueryClientProvider> }
