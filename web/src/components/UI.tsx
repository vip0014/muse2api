import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from 'react'
import { AlertCircle, CheckCircle2, Inbox, LoaderCircle, WifiOff } from 'lucide-react'

export function Button({ className = '', variant = 'primary', size = 'md', ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'ghost' | 'danger'; size?: 'sm' | 'md' }) {
  return <button className={`button button-${variant} button-${size} ${className}`} {...props} />
}
export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' }) { return <span className={`badge badge-${tone}`}><span className="badge-dot" />{children}</span> }
export function Field({ label, hint, error, children }: { label: string; hint?: string; error?: string; children: ReactNode }) { return <label className="field"><span className="field-label">{label}</span>{children}{error ? <span className="field-error">{error}</span> : hint ? <span className="field-hint">{hint}</span> : null}</label> }
export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) { return <input className={`input ${props.className ?? ''}`} {...props} /> }
export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) { return <select className={`input select ${props.className ?? ''}`} {...props} /> }

export function PageHeader({ title, description, actions, kicker }: { title: string; description?: string; actions?: ReactNode; kicker?: string }) {
  return <div className="page-header">
    <div className="page-heading">
      {kicker && <span className="page-kicker">{kicker}</span>}
      <h1>{title}</h1>
      {description && <p>{description}</p>}
    </div>
    {actions && <div className="page-actions">{actions}</div>}
  </div>
}

export function SectionHeader({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return <div className="section-head"><div><h2>{title}</h2>{hint && <p>{hint}</p>}</div>{action}</div>
}

export function Panel({ children, className = '', ...props }: HTMLAttributes<HTMLDivElement>) { return <section className={`panel ${className}`} {...props}>{children}</section> }
export function Empty({ title, description, action }: { title: string; description?: string; action?: ReactNode }) { return <div className="state"><Inbox size={24} /><h3>{title}</h3>{description && <p>{description}</p>}{action}</div> }
export function Loading({ label = '加载中…' }: { label?: string }) { return <div className="state" role="status"><LoaderCircle className="spin" size={22} /><p>{label}</p></div> }
export function ErrorState({ message, retry }: { message: string; retry?: () => void }) { return <div className="state state-error"><AlertCircle size={24} /><h3>加载失败</h3><p>{message}</p>{retry && <Button variant="secondary" onClick={retry}>重试</Button>}</div> }
export function OfflineState() { return <div className="state state-offline"><WifiOff size={24} /><h3>网络已断开</h3><p>恢复连接后会自动继续；当前显示的数据可能不是最新。</p></div> }
export function SuccessMessage({ children }: { children: ReactNode }) { return <div className="inline-message success" role="status"><CheckCircle2 size={15} />{children}</div> }
export function InlineError({ children }: { children: ReactNode }) { return <div className="inline-message error" role="alert"><AlertCircle size={15} />{children}</div> }
export function SkeletonRows({ rows = 4 }: { rows?: number }) { return <div className="skeleton-list" aria-label="加载中">{[...Array(rows)].map((_, i) => <div className="skeleton-row" key={i}><span /><span /><span /></div>)}</div> }
export function PageSkeleton() { return <div className="page-stack page-skeleton"><div className="skeleton-block title" /><div className="skeleton-block band" /><div className="skeleton-columns"><div className="skeleton-block tall" /><div className="skeleton-block tall" /></div></div> }
