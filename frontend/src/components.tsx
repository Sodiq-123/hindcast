import { Fragment, useEffect, useRef, useState, type InputHTMLAttributes, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, type CoinMatch, type Driver, type SearchResult, type TokenSearchResult } from './api'
import { pct, pp, signalClass, statusLabel } from './format'

/** Colour is never the only cue: up and down signals also carry ▲ and ▼. */
export function SignalBadge({ signal, large }: { signal: string; large?: boolean }) {
  const cls = signalClass(signal)
  const shape = cls === 'up' ? '▲ ' : cls === 'down' ? '▼ ' : ''
  return <span className={`badge ${cls} ${large ? 'lg' : ''}`}>{shape}{signal || '—'}</span>
}

export function Brand() {
  return (
    <Link to="/" className="brand">
      <span className="mark"><svg viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round"><path d="M3 16l5-5 4 3 7-8" /></svg></span>
      Hindcast
    </Link>
  )
}


export function StatusBadge({ status }: { status: string }) {
  const cls = status === 'validated' ? 'up' : status === 'weak' ? 'hold' : 'none'
  return (
    <span className={`badge ${cls}`} title="Out-of-sample validation status for this asset and horizon">
      <span className="dot" />
      {statusLabel[status] ?? status}
    </span>
  )
}

/** Horizontal probability bar: fill from 50% toward p, with the base rate marked. */
export function ProbBar({ p, base }: { p: number; base?: number }) {
  const left = Math.min(p, 0.5) * 100
  const width = Math.abs(p - 0.5) * 100
  return (
    <div className="probbar" title={`P(up) ${pct(p)}${base != null ? ` · base rate ${pct(base)}` : ''}`}>
      <div className="fill" style={{ left: `${left}%`, width: `${width}%`, background: p >= 0.5 ? 'var(--up)' : 'var(--down)' }} />
      <div className="mid" />
      {base != null && <div className="base" style={{ left: `calc(${base * 100}% - 1px)` }} />}
    </div>
  )
}

export function Panel({ title, sub, actions, children, pad = true }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode; children: ReactNode; pad?: boolean }) {
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>{title}</h2>
          {sub && <div className="sub">{sub}</div>}
        </div>
        {actions && <div className="row">{actions}</div>}
      </div>
      {pad ? <div className="panel-body">{children}</div> : children}
    </section>
  )
}

export function Stat({ label, value, hint, tone }: { label: string; value: ReactNode; hint?: ReactNode; tone?: string }) {
  return (
    <div className="stat">
      <span className="label">{label}</span>
      <span className={`value ${tone ?? ''}`}>{value}</span>
      {hint && <span className="hint">{hint}</span>}
    </div>
  )
}

export function Loading({ label }: { label: string }) {
  return (
    <div className="loading-block">
      <span className="spinner" />
      <span>{label}</span>
    </div>
  )
}

const evmAddr = /^0x[0-9a-fA-F]{40}$/
const solAddr = /^[1-9A-HJ-NP-Za-km-z]{32,44}$/
export const isTokenAddress = (q: string) => evmAddr.test(q.trim()) || solAddr.test(q.trim())

type SearchItem =
  | { kind: 'asset'; key: string; symbol: string; name: string; meta: string }
  | { kind: 'token'; key: string; address: string; name: string; meta: string; warning?: string; badge?: string }
  | { kind: 'coin'; key: string; id: string; name: string; meta: string }

const usdShort = (x: number) => (x >= 1e9 ? `$${(x / 1e9).toFixed(1)}B` : x >= 1e6 ? `$${(x / 1e6).toFixed(1)}M` : x >= 1e3 ? `$${(x / 1e3).toFixed(0)}K` : `$${x.toFixed(0)}`)

export type SearchPick = { kind: 'asset'; symbol: string } | { kind: 'token'; address: string } | { kind: 'coin'; id: string }

/** Search stocks/ETFs/crypto tickers, DEX tokens by name, or paste a token contract address.
 * By default a choice opens its page; with onPick (e.g. the watchlist) it is handed to the caller. */
export function SearchBox({
  placeholder = 'Search AAPL, BTC-USD, a meme coin name, or paste a token address',
  onPick,
  width,
  coins: showCoins,
}: {
  placeholder?: string
  onPick?: (p: SearchPick) => void
  width?: number
  coins?: boolean // also list CoinGecko coins on any blockchain (opens their research)
}) {
  const withCoins = showCoins ?? !onPick
  const [q, setQ] = useState('')
  const [assets, setAssets] = useState<SearchResult[]>([])
  const [tokens, setTokens] = useState<TokenSearchResult[]>([])
  const [coinList, setCoinList] = useState<CoinMatch[]>([])
  const [open, setOpen] = useState(false)
  const [hl, setHl] = useState(-1)
  const nav = useNavigate()
  const box = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const t = q.trim()
    if (t.length < 1 || isTokenAddress(t)) {
      setAssets([])
      setTokens([])
      setCoinList([])
      return
    }
    const id = setTimeout(() => {
      api.search(t).then((r) => setAssets(r ?? [])).catch(() => setAssets([]))
      if (t.length >= 2) api.tokenSearch(t).then((r) => setTokens(r ?? [])).catch(() => setTokens([]))
      if (t.length >= 2 && withCoins) api.coinSearch(t).then((r) => setCoinList((r ?? []).filter((c) => c.rank))).catch(() => setCoinList([]))
    }, 300)
    return () => clearTimeout(id)
  }, [q, withCoins])

  useEffect(() => {
    const close = (e: MouseEvent) => {
      if (box.current && !box.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [])

  const t = q.trim()
  const items: SearchItem[] = isTokenAddress(t)
    ? [{ kind: 'token', key: t, address: t, name: onPick ? 'Add this token address' : 'Open the token safety report', meta: 'Solana · Ethereum · Base' }]
    : [
        ...assets.map((r): SearchItem => ({ kind: 'asset', key: 'a' + r.symbol, symbol: r.symbol, name: r.name, meta: `${r.type} · ${r.exchange}` })),
        ...coinList.slice(0, 3).map((c): SearchItem => ({
          kind: 'coin', key: 'c' + c.id, id: c.id, name: `${c.symbol} · ${c.name}`, meta: `#${c.rank} on CoinGecko · research the coin itself`,
        })),
        ...tokens.slice(0, 6).map((r): SearchItem => ({
          kind: 'token', key: 't' + r.chain + r.address, address: r.address, name: `${r.symbol} · ${r.name}`,
          meta: `${r.chain} · ${r.address.slice(0, 6)}…${r.address.slice(-4)} · liquidity ${usdShort(r.liquidity)} · ${r.ageDays < 1 ? 'new today' : `${Math.round(r.ageDays)}d old`}`,
          warning: r.warning,
          badge: r.original
            ? r.listing
              ? `✓ Original: CoinGecko lists this address as ${r.listing.name}${r.listing.rank ? ` (#${r.listing.rank})` : ''}`
              : '✓ Likely original: most liquid token over a month old (not listed on CoinGecko, so verify the address)'
            : undefined,
        })),
      ]

  const choose = (p: SearchPick) => {
    if (onPick) onPick(p)
    else if (p.kind === 'coin') nav(`/research/cg:${p.id}`)
    else nav(p.kind === 'asset' ? `/asset/${encodeURIComponent(p.symbol.toUpperCase())}` : `/token/${p.address}`)
  }
  const go = (it?: SearchItem) => {
    setOpen(false)
    setQ('')
    if (!it) {
      if (isTokenAddress(t)) choose({ kind: 'token', address: t })
      else if (t) choose({ kind: 'asset', symbol: t.toUpperCase() })
      return
    }
    choose(it.kind === 'asset' ? { kind: 'asset', symbol: it.symbol.toUpperCase() } : it.kind === 'coin' ? { kind: 'coin', id: it.id } : { kind: 'token', address: it.address })
  }

  const firstToken = items.findIndex((i) => i.kind === 'token')
  const firstCoin = items.findIndex((i) => i.kind === 'coin')
  return (
    <div className="search" ref={box} style={width ? { width } : undefined}>
      <input
        value={q}
        placeholder={placeholder}
        onChange={(e) => {
          setQ(e.target.value)
          setOpen(true)
          setHl(-1)
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown') setHl((h) => Math.min(h + 1, items.length - 1))
          if (e.key === 'ArrowUp') setHl((h) => Math.max(h - 1, -1))
          if (e.key === 'Enter' && t) go(hl >= 0 ? items[hl] : isTokenAddress(t) ? items[0] : undefined)
          if (e.key === 'Escape') setOpen(false)
        }}
      />
      {open && items.length > 0 && (
        <div className="results">
          {items.map((it, i) => (
            <Fragment key={it.key}>
              {i === firstCoin && <div className="search-section">Coins on any blockchain (CoinGecko) · opens research</div>}
              {i === firstToken && !isTokenAddress(t) && <div className="search-section">DEX tokens (Solana · Ethereum · Base)</div>}
              {i === 0 && it.kind === 'asset' && <div className="search-section">Stocks, ETFs, crypto</div>}
              <div className={i === hl ? 'hl' : ''} onMouseDown={() => go(it)}>
                <span>
                  <strong className="mono">{it.kind === 'asset' ? it.symbol : it.name.split(' · ')[0]}</strong>{' '}
                  <span className="muted">{it.kind === 'asset' ? it.name : it.name.split(' · ').slice(1).join(' · ') || it.name}</span>
                  {it.kind === 'token' && it.badge && <span className="tiny" style={{ display: 'block', color: 'var(--up)' }}>{it.badge}</span>}
                  {it.kind === 'token' && it.warning && (
                    <span className="tiny" style={{ display: 'block', color: /copycat|fake/i.test(it.warning) ? 'var(--down)' : 'var(--hold)' }}>⚠ {it.warning}</span>
                  )}
                </span>
                <span className="faint small">{it.meta}</span>
              </div>
            </Fragment>
          ))}
        </div>
      )}
    </div>
  )
}

export function DriverList({ drivers, limit = 12 }: { drivers: Driver[]; limit?: number }) {
  const [open, setOpen] = useState<string | null>(null)
  const [showAll, setShowAll] = useState(false)
  const shown = showAll ? drivers : drivers.slice(0, limit)
  const maxAbs = Math.max(0.005, ...drivers.map((d) => Math.abs(d.probImpact)))
  return (
    <div>
      {shown.map((d) => {
        const w = (Math.abs(d.probImpact) / maxAbs) * 50
        const pos = d.probImpact >= 0
        return (
          <div key={d.key} className="driver" onClick={() => setOpen(open === d.key ? null : d.key)}>
            <div className="lbl">
              {d.label}
              <span className="grp">
                {d.group} · {d.z >= 0 ? '+' : ''}
                {d.z.toFixed(1)}σ
              </span>
            </div>
            <div className="track">
              <div className="axis" />
              <div
                className="bar"
                style={{ left: pos ? '50%' : `${50 - w}%`, width: `${w}%`, background: pos ? 'var(--up)' : 'var(--down)' }}
              />
            </div>
            <div className={`num r small ${pos ? 'up' : 'down'}`} style={{ textAlign: 'right' }}>
              {pp(d.probImpact, Math.abs(d.probImpact) < 0.001 ? 2 : 1)}
            </div>
            {open === d.key && <div className="text">{d.text}</div>}
          </div>
        )
      })}
      {drivers.length > limit && (
        <button className="ghost small" style={{ marginTop: 10 }} onClick={() => setShowAll(!showAll)}>
          {showAll ? 'Show fewer' : `Show all ${drivers.length} inputs`}
        </button>
      )}
    </div>
  )
}

/** "← Back": returns to the previous page inside the app, or to a sensible
 * parent page when this one was opened directly (bookmark, new tab, link). */
export function BackLink({ fallback, label = 'Back' }: { fallback: string; label?: string }) {
  const nav = useNavigate()
  const inApp = ((window.history.state as { idx?: number } | null)?.idx ?? 0) > 0
  return (
    <button className="small ghost" style={{ alignSelf: 'flex-start', padding: '2px 8px', marginLeft: -8 }} onClick={() => (inApp ? nav(-1) : nav(fallback))}>
      ← {label}
    </button>
  )
}

/** A password box with a button to show what's been typed. */
export function PasswordInput(props: Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>) {
  const [shown, setShown] = useState(false)
  return (
    <span className="pw">
      <input {...props} type={shown ? 'text' : 'password'} autoCapitalize="off" autoCorrect="off" spellCheck={false} />
      <button type="button" className="pw-toggle" onClick={() => setShown(!shown)} aria-label={shown ? 'Hide password' : 'Show password'} aria-pressed={shown} title={shown ? 'Hide password' : 'Show password'}>
        <Eye open={shown} />
      </button>
    </span>
  )
}

/** An eye that opens (password visible) and closes (hidden), animated by CSS. */
function Eye({ open }: { open: boolean }) {
  return (
    <svg className={`eye ${open ? 'open' : ''}`} viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
      <g className="eye-ball">
        <path d="M2.5 12S6 6 12 6s9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6z" />
        <circle cx="12" cy="12" r="3" className="pupil" />
      </g>
      <g className="eye-lid">
        <path d="M2.5 11c2.4 3.3 5.6 5 9.5 5s7.1-1.7 9.5-5" />
        <path d="M6 14.5l-1.4 2M12 16v2.6M18 14.5l1.4 2" />
      </g>
    </svg>
  )
}
