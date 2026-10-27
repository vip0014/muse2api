import type { ReactNode } from 'react'
import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { Button } from './UI'

export function Modal({ open, onOpenChange, title, description, children, wide = false }: { open: boolean; onOpenChange: (open: boolean) => void; title: string; description?: string; children: ReactNode; wide?: boolean }) {
  return <Dialog.Root open={open} onOpenChange={onOpenChange}><Dialog.Portal><Dialog.Overlay className="dialog-overlay" /><Dialog.Content className={`dialog ${wide ? 'dialog-wide' : ''}`}><div className="dialog-head"><div><Dialog.Title>{title}</Dialog.Title>{description && <Dialog.Description>{description}</Dialog.Description>}</div><Dialog.Close className="icon-button" aria-label="关闭"><X size={18} /></Dialog.Close></div>{children}</Dialog.Content></Dialog.Portal></Dialog.Root>
}

export function Confirm({ open, onOpenChange, title, description, onConfirm, busy }: { open: boolean; onOpenChange: (open: boolean) => void; title: string; description: string; onConfirm: () => void; busy?: boolean }) {
  return <Modal open={open} onOpenChange={onOpenChange} title={title} description={description}><div className="dialog-actions"><Button variant="secondary" onClick={() => onOpenChange(false)}>取消</Button><Button variant="danger" disabled={busy} onClick={onConfirm}>{busy ? '处理中…' : '确认'}</Button></div></Modal>
}
