import { lazy, useEffect, useState } from 'react'
import { BrowserRouter, Navigate, Route, Routes, useSearchParams } from 'react-router-dom'
import { QueryClient, QueryClientProvider, useQueryClient } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { api } from './lib/api'
import { ThemeProvider } from './lib/theme'
import { Layout } from './components/Layout'
import { Login } from './pages/Login'

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

function AccountsRoute() {
  const [params] = useSearchParams()
  return <Accounts key={params.toString()} initialSearch={params.get('q') ?? ''} initialStatus={params.get('status') ?? ''} initialFocus={params.get('focus') ?? undefined} />
}

function TasksRoute() {
  const [params] = useSearchParams()
  return <Tasks key={params.toString()} initialSearch={params.get('q') ?? ''} initialStatus={params.get('status') ?? ''} initialKind={params.get('kind') ?? ''} initialFocus={params.get('focus') ?? undefined} />
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
  if (authenticated === null) return <div className="app-loading" role="status" aria-label="正在连接控制台"><img src="/logo.png" alt="Muse2API" width={58} height={34} /><LoaderCircle className="spin" size={18} /><span>正在连接控制台</span></div>
  if (!authenticated) return <Login onLogin={() => { client.clear(); setAuthenticated(true) }} />
  const logout = () => { void api('/admin/logout', { method: 'POST' }).finally(() => { client.clear(); setAuthenticated(false) }) }
  return <Routes><Route element={<Layout onLogout={logout} />}><Route index element={<Dashboard />} /><Route path="accounts" element={<AccountsRoute />} /><Route path="tasks" element={<TasksRoute />} /><Route path="media" element={<MediaLibrary />} /><Route path="playground" element={<Playground />} /><Route path="models" element={<Models />} /><Route path="settings" element={<Settings />} /><Route path="logs" element={<Logs />} /><Route path="*" element={<Navigate to="/" replace />} /></Route></Routes>
}

export default function App() {
  return <ThemeProvider><QueryClientProvider client={queryClient}><BrowserRouter><AppRoutes /></BrowserRouter></QueryClientProvider></ThemeProvider>
}
