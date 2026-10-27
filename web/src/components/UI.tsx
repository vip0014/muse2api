import type { ButtonHTMLAttributes, HTMLAttributes, ReactNode } from 'react'
import { AlertCircle, CheckCircle2, Inbox, LoaderCircle } from 'lucide-react'

export function Button({ className = '', variant = 'primary', size = 'md', ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'secondary' | 'ghost' | 'danger'; size?: 'sm' | 'md' }) {
  return <button className={`button button-${variant} button-${size} ${className}`} {...props} />
}
export function Badge({ children, tone = 'neutral' }: { children: ReactNode; tone?: 'neutral' | 'success' | 'warning' | 'danger' | 'info' }) { return <span className={`badge badge-${tone}`}><span className="badge-dot" />{children}</span> }
export function Field({ label, hint, error, children }: { label: string; hint?: string; error?: string; children: ReactNode }) { return <label className="field"><span className="field-label">{label}</span>{children}{error ? <span className="field-error">{error}</span> : hint ? <span className="field-hint">{hint}</span> : null}</label> }
export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) { return <input className={`input ${props.className ?? ''}`} {...props} /> }
export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) { return <select className={`input select ${props.className ?? ''}`} {...props} /> }
export function PageHeader({ title, actions }: { title: string; actions?: ReactNode }) { return <div className="page-header"><h1>{title}</h1>{actions && <div className="page-actions">{actions}</div>}</div> }
export function Panel({ children, className = '', ...props }: HTMLAttributes<HTMLDivElement>) { return <section className={`panel ${className}`} {...props}>{children}</section> }
export function Empty({ title, description, action }: { title: string; description?: string; action?: ReactNode }) { return <div className="state"><Inbox size={26} /><h3>{title}</h3>{description && <p>{description}</p>}{action}</div> }
export function Loading({ label = '加载中…' }: { label?: string }) { return <div className="state" role="status"><LoaderCircle className="spin" size={24} /><p>{label}</p></div> }
export function ErrorState({ message, retry }: { message: string; retry?: () => void }) { return <div className="state state-error"><AlertCircle size={26} /><h3>加载失败</h3><p>{message}</p>{retry && <Button variant="secondary" onClick={retry}>重试</Button>}</div> }
export function SuccessMessage({ children }: { children: ReactNode }) { return <div className="inline-message success"><CheckCircle2 size={16} />{children}</div> }
export function InlineError({ children }: { children: ReactNode }) { return <div className="inline-message error"><AlertCircle size={16} />{children}</div> }
export function SkeletonRows() { return <div className="skeleton-list">{[1, 2, 3, 4].map(i => <div className="skeleton-row" key={i} />)}</div> }
