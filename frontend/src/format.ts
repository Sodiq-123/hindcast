export const pct = (x: number | undefined | null, digits = 1) =>
  x == null || !Number.isFinite(x) ? '—' : `${(x * 100).toFixed(digits)}%`

export const spct = (x: number | undefined | null, digits = 1) =>
  x == null || !Number.isFinite(x) ? '—' : `${x >= 0 ? '+' : ''}${(x * 100).toFixed(digits)}%`

export const pp = (x: number | undefined | null, digits = 1) =>
  x == null || !Number.isFinite(x) ? '—' : `${x >= 0 ? '+' : ''}${(x * 100).toFixed(digits)} pp`

export function price(x: number | undefined | null, currency?: string) {
  if (x == null || !Number.isFinite(x)) return '—'
  const digits = x >= 1 ? 2 : x >= 0.01 ? 4 : x > 0 ? Math.min(12, Math.max(6, 3 - Math.floor(Math.log10(x)))) : 6
  const s = x.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
  return currency ? `${s} ${currency}` : s
}

export function ago(iso?: string) {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return '—'
  const s = (Date.now() - t) / 1000
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  return `${Math.round(s / 86400)}d ago`
}

export function signalClass(signal: string) {
  if (signal.includes('BUY')) return 'up'
  if (signal.includes('SELL')) return 'down'
  if (signal === 'HOLD' || signal.startsWith('LEAN')) return 'hold'
  return 'none'
}

export const statusLabel: Record<string, string> = {
  validated: 'Validated edge',
  weak: 'Weak evidence',
  no_edge: 'No proven edge',
}

export const tone = (x: number) => (x > 0 ? 'up' : x < 0 ? 'down' : '')
