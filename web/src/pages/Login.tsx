import { useState } from 'react'
import { ArrowRight } from 'lucide-react'
import { api } from '../lib/api'
import { Button, InlineError, Input } from '../components/UI'

export function Login({ onLogin }: { onLogin: () => void }) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/admin/login', { method: 'POST', body: JSON.stringify({ username: username.trim(), password }) })
      onLogin()
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setBusy(false)
    }
  }

  return <main className="login-page">
    <section className="login-card" aria-labelledby="login-title">
      <div className="login-brand">
        <img className="brand-mark" src="/logo.png" alt="" width={56} height={33} />
        <strong>Muse2API</strong>
      </div>
      <div className="login-title">
        <h1 id="login-title">管理员登录</h1>
      </div>
      <form className="login-form" onSubmit={submit}>
        <label className="field">
          <span className="field-label">用户名</span>
          <Input autoFocus required autoComplete="username" placeholder="admin" value={username} onChange={event => setUsername(event.target.value)} />
        </label>
        <label className="field">
          <span className="field-label">密码</span>
          <Input required type="password" autoComplete="current-password" placeholder="输入密码" value={password} onChange={event => setPassword(event.target.value)} />
        </label>
        {error && <InlineError>{error}</InlineError>}
        <Button className="login-submit" disabled={busy || !username.trim() || !password}>{busy ? '登录中…' : '登录'}<ArrowRight size={15} /></Button>
      </form>
    </section>
  </main>
}
