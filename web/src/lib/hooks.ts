import { useEffect, useState, type KeyboardEvent } from 'react'

// Roving focus for collection lists: buttons keep their native Enter/Space
// behaviour, while Up/Down/Home/End move between the visible objects.
export function listKeyDown(event: KeyboardEvent<HTMLElement>) {
  const keys = ['ArrowDown', 'ArrowUp', 'Home', 'End']
  if (!keys.includes(event.key)) return
  const rows = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[data-list-row]'))
  if (rows.length === 0) return
  event.preventDefault()
  const current = document.activeElement instanceof HTMLElement ? document.activeElement : null
  const index = current ? rows.indexOf(current) : -1
  let next = 0
  if (event.key === 'End') next = rows.length - 1
  else if (event.key === 'ArrowDown') next = index < 0 ? 0 : Math.min(rows.length - 1, index + 1)
  else if (event.key === 'ArrowUp') next = index < 0 ? rows.length - 1 : Math.max(0, index - 1)
  rows[next]?.focus()
}

export function useMediaQuery(query: string) {
  const read = () => typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia(query).matches : false
  const [matches, setMatches] = useState(read)

  useEffect(() => {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return
    const media = window.matchMedia(query)
    const update = () => setMatches(media.matches)
    update()
    if (typeof media.addEventListener === 'function') {
      media.addEventListener('change', update)
      return () => media.removeEventListener('change', update)
    }
    media.addListener(update)
    return () => media.removeListener(update)
  }, [query])

  return matches
}

export function useOnlineStatus() {
  const [online, setOnline] = useState(() => typeof navigator === 'undefined' ? true : navigator.onLine)
  useEffect(() => {
    const up = () => setOnline(true)
    const down = () => setOnline(false)
    window.addEventListener('online', up)
    window.addEventListener('offline', down)
    return () => { window.removeEventListener('online', up); window.removeEventListener('offline', down) }
  }, [])
  return online
}
