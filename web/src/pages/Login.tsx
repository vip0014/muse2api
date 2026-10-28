import { useState } from 'react'
import { ArrowRight, KeyRound, Layers3, ShieldCheck } from 'lucide-react'
import { api } from '../lib/api'
import { Button, InlineError, Input } from '../components/UI'
import { ThemeToggle } from '../components/ThemeToggle'

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

  return <main className="auth-page">
    <section className="auth-visual" aria-label="Muse2API">
      <div className="auth-brand">
        <img src="/logo.png" alt="" width={54} height={32} />
        <div>
          <strong>Muse<em>2</em>API</strong>
          <span>Operator console</span>
        </div>
      </div>

      <div className="auth-hero">
        <span className="auth-kicker">Self-hosted · OpenAI compatible</span>
        <h1>把 <em>Muse</em> 的生成能力，<br />接进你的工作流。</h1>
        <p>对话、图片和视频统一成一套 OpenAI 兼容 API。自托管，多账号轮换。</p>
        <div className="auth-points">
          <span><Layers3 size={12} />OpenAI 兼容</span>
          <span><KeyRound size={12} />多账号轮换</span>
          <span><ShieldCheck size={12} />本地凭据加密</span>
        </div>
      </div>

      <div>
        <div className="auth-flow" aria-hidden="true">
          <div className="auth-flow-node"><strong>Muse</strong><span>session</span></div>
          <div className="auth-flow-line" />
          <div className="auth-flow-node"><strong>Pool</strong><span>rotation</span></div>
          <div className="auth-flow-line" />
          <div className="auth-flow-node accent"><strong>/v1</strong><span>OpenAI API</span></div>
        </div>
        <div className="auth-meta">MUSE2API · OPERATOR CONSOLE</div>
      </div>
    </section>

    <section className="auth-panel" aria-labelledby="login-title">
      <div className="auth-top">
        <ThemeToggle />
      </div>
      <div className="auth-form-wrap">
        <span className="page-kicker">管理员会话</span>
        <h2 id="login-title">管理员登录</h2>
        <p>使用服务端配置的管理员账号进入控制台。</p>
        <form className="auth-form" onSubmit={submit}>
          <label className="field">
            <span className="field-label">用户名</span>
            <Input autoFocus required autoComplete="username" placeholder="admin" value={username} onChange={event => setUsername(event.target.value)} />
          </label>
          <label className="field">
            <span className="field-label">密码</span>
            <Input required type="password" autoComplete="current-password" placeholder="输入密码" value={password} onChange={event => setPassword(event.target.value)} />
          </label>
          {error && <InlineError>{error}</InlineError>}
          <Button className="auth-submit" disabled={busy || !username.trim() || !password}>{busy ? '登录中…' : '登录'}<ArrowRight size={15} /></Button>
        </form>
      </div>
      <div className="auth-side-note">
        <span><ShieldCheck size={12} />本地部署</span>
        <span>Muse2API</span>
      </div>
    </section>
  </main>
}
