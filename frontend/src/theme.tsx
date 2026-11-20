import { useEffect, useSyncExternalStore } from 'react'
import type { AccentName, Prefs } from './api'

/** The accent colours people can pick. Values are tuned to keep white text
 * readable on buttons in both light and dark. */
export const ACCENTS: Record<AccentName, { label: string; hex: string }> = {
  teal: { label: 'Teal', hex: '#0E7C86' },
  indigo: { label: 'Indigo', hex: '#3B4FD9' },
  plum: { label: 'Plum', hex: '#8B3FA8' },
  amber: { label: 'Amber', hex: '#A35F00' },
  ocean: { label: 'Ocean', hex: '#0369A1' },
  graphite: { label: 'Graphite', hex: '#4B5563' },
}

export const DEFAULT_PREFS: Prefs = { theme: 'auto', accent: 'teal' }
const KEY = 'look' // localStorage: { theme, hex }, read by index.html before first paint
const dark = () => window.matchMedia('(prefers-color-scheme: dark)')

let current: Prefs = DEFAULT_PREFS
let version = 0
const listeners = new Set<() => void>()

/** Applies a look to the page and remembers it on this device. */
export function applyLook(p: Prefs) {
  current = p
  const resolved = p.theme === 'auto' ? (dark().matches ? 'dark' : 'light') : p.theme
  const hex = (ACCENTS[p.accent] ?? ACCENTS.teal).hex
  const root = document.documentElement
  root.dataset.theme = resolved
  root.style.setProperty('--accent', hex)
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', resolved === 'dark' ? '#090B0F' : '#ECEEF2')
  try {
    localStorage.setItem(KEY, JSON.stringify({ theme: p.theme, hex }))
  } catch {
    /* private mode: the look still applies for this visit */
  }
  version++
  listeners.forEach((l) => l())
}

/** Keeps the page in step with the person's saved look, and with the
 * system setting while they're on "auto". */
export function useLookSync(prefs: Prefs | undefined) {
  const p = prefs ?? DEFAULT_PREFS
  useEffect(() => {
    applyLook(p)
    if (p.theme !== 'auto') return
    const mq = dark()
    const on = () => applyLook(p)
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [p.theme, p.accent]) // eslint-disable-line react-hooks/exhaustive-deps
}

/** Changes whenever the look changes, for things drawn with fixed colours (charts). */
export function useLookVersion() {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l)
      return () => listeners.delete(l)
    },
    () => version,
  )
}

export const currentLook = () => current

let probe: HTMLSpanElement | null = null
let ctx: CanvasRenderingContext2D | null = null

/** Resolves a colour token (which may use color-mix) to plain #rrggbb, for
 * canvas charts that can't read CSS variables. */
export function cssColor(name: string): string {
  if (!probe) {
    probe = document.createElement('span')
    probe.style.display = 'none'
    document.body.appendChild(probe)
  }
  probe.style.color = `var(${name})`
  const computed = getComputedStyle(probe).color
  // Paint it on one pixel and read the result back: works for any colour
  // syntax the browser understands (color(), color-mix(), oklch…), which
  // the chart library can't parse itself.
  if (!ctx) {
    const c = document.createElement('canvas')
    c.width = c.height = 1
    ctx = c.getContext('2d', { willReadFrequently: true })
  }
  if (!ctx) return computed
  ctx.clearRect(0, 0, 1, 1)
  ctx.fillStyle = computed
  ctx.fillRect(0, 0, 1, 1)
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data
  return '#' + [r, g, b].map((v) => v.toString(16).padStart(2, '0')).join('')
}

/** #rrggbb plus an opacity as rgba(). */
export function withAlpha(hex: string, a: number): string {
  const m = /^#([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex)
  if (!m) return hex
  return `rgba(${parseInt(m[1], 16)}, ${parseInt(m[2], 16)}, ${parseInt(m[3], 16)}, ${a})`
}
