import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, track, type Status, type TokenBrief, type WatchItem } from '../api'
import { useMe } from '../auth'
import { Loading, SearchBox, type SearchPick } from '../components'
import { ago, price } from '../format'

const usd = (x: number) => (!x ? '—' : x >= 1e9 ? `$${(x / 1e9).toFixed(2)}B` : x >= 1e6 ? `$${(x / 1e6).toFixed(1)}M` : x >= 1e3 ? `$${(x / 1e3).toFixed(0)}K` : `$${x.toFixed(0)}`)
const tokenPrice = (x: number) => (!x ? '—' : x >= 1 ? `$${x.toFixed(4)}` : `$${Number(x.toPrecision(4))}`)
const IDENTITY: Record<TokenBrief['identity'], [string, string]> = {
  original: ['up', '✓ The listed coin'],
  different_project: ['hold', 'Same ticker as a bigger coin'],
  lookalike: ['down', '⚠ Not the listed one'],
  unlisted: ['hold', 'Not listed on CoinGecko'],
  unknown: ['faint', ''],
}
const HORIZONS: [string, string][] = [['1d', 'Day'], ['1w', 'Week'], ['1m', 'Month'], ['1y', 'Year']]
const LONG: Record<string, string> = { '1d': 'a day', '1w': 'a week', '1m': 'a month', '1y': 'a year' }
const shortName = (name: string | undefined, sym: string) => {
  if (!name) return sym
  let n = name.trim()
  for (const s of [' USD', ' Corporation', ' Corp.', ' Corp', ' Inc.', ' Inc', ' plc', ' PLC', ' Ltd.', ' Ltd', ' N.V.', ' S.A.', ' Holdings', ' Company']) if (n.endsWith(s)) n = n.slice(0, -s.length)
  n = n.replace(/,$/, '').trim()
  return !n || n.length > 24 ? sym : n
}

/** The plain reading of a watchlist signal, with how well it is tested. */
export function verdict(signal: string, status: string): { mark: string; text: string; cls: string } {
  const up = signal.includes('BUY'), down = signal.includes('SELL')
  const cls = up ? 'up' : down ? 'down' : ''
  if (status === 'validated') return { mark: '✓', text: up ? 'Likely higher' : down ? 'Likely lower' : 'No clear lean', cls }
  if (status === 'weak') return { mark: '◐', text: up ? 'Leaning higher' : down ? 'Leaning lower' : 'Too close to call', cls }
  return { mark: '○', text: 'No proven edge', cls: '' }
}

function Change({ x }: { x?: number }) {
  if (x == null || !Number.isFinite(x)) return null
  if (Math.abs(x) < 0.0005) return <span className="muted">0.0%</span>
  return <span className={x > 0 ? 'up' : 'down'}>{x > 0 ? '▲' : '▼'} {Math.abs(x * 100).toFixed(1)}%</span>
}

export default function Dashboard({ status }: { status: Status | null }) {
  const [items, setItems] = useState<WatchItem[] | null>(null)
  const [err, setErr] = useState('')
  const [tokens, setTokens] = useState<{ address: string; chain: string; brief?: TokenBrief }[] | null>(null)
  const [busy, setBusy] = useState<Record<string, boolean>>({})
  const [now] = useState(() => Date.now())
  const me = useMe()
  const { settings } = me
  const isAdmin = me.user.role === 'admin'
  const [hz, setHz] = useState<string>(settings.detail === 'full' ? 'all' : settings.horizon || '1m')

  const load = useCallback(() => {
    api.watchlist().then(setItems).catch((e) => setErr(String(e.message ?? e)))
    api.watchTokens().then(setTokens).catch(() => setTokens([]))
  }, [])
  useEffect(() => {
    load()
    const id = setInterval(load, 30000)
    return () => clearInterval(id)
  }, [load])

  const refresh = async (sym: string) => {
    setBusy((b) => ({ ...b, [sym]: true }))
    try {
      await api.runReport(sym, true, true)
      load()
    } catch (e) {
      setErr(`${sym}: ${(e as Error).message}`)
    } finally {
      setBusy((b) => ({ ...b, [sym]: false }))
    }
  }

  const add = async (p: SearchPick) => {
    setErr('')
    try {
      if (p.kind === 'asset') {
        await api.addWatch(p.symbol)
        load()
        refresh(p.symbol)
        return
      }
      if (p.kind === 'coin') return // coins on other blockchains open in Research, not the watchlist
      const res = await api.addWatch(p.address)
      const addr = res.address ?? p.address
      setBusy((b) => ({ ...b, [addr]: true }))
      load()
      await api.token(addr).catch(() => {}) // first safety check
      setBusy((b) => ({ ...b, [addr]: false }))
      load()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  const recheck = async (addr: string) => {
    setBusy((b) => ({ ...b, [addr]: true }))
    await api.token(addr, true).catch((e) => setErr((e as Error).message))
    setBusy((b) => ({ ...b, [addr]: false }))
    load()
  }

  const sched = status?.scheduler
  const newest = (items ?? []).reduce((t, it) => Math.max(t, it.generatedAt ? Date.parse(it.generatedAt) : 0), 0)
  const staleHours = newest ? (now - newest) / 3600000 : 0
  const nextRun = sched?.nextRun ? new Date(sched.nextRun) : null
  const when = (d: Date) => `${d.toDateString() === new Date(now).toDateString() ? 'today' : d.toLocaleDateString(undefined, { weekday: 'long' })} at ${d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })}`

  return (
    <div className="stack" style={{ gap: 20 }}>
      <header className="asset-head">
        <div style={{ maxWidth: 680 }}>
          <h1 style={{ fontSize: 36 }}>Watchlist</h1>
          <p className="answer-more" style={{ marginTop: 8 }}>
            Everything here is re-analysed at {sched?.times.join(' and ') ?? 'the scheduled times'}. Open any row for the short answer and the reasons.
          </p>
        </div>
        <div className="wl-add">
          <SearchBox onPick={add} placeholder={settings.showTokens ? 'Add a stock, crypto or meme coin…' : 'Add a stock, fund or crypto…'} />
        </div>
      </header>

      {err && <div className="callout bad">{err}</div>}
      {staleHours > 16 && (
        <section className="callout warn" style={{ fontSize: 15 }}>
          <strong>These analyses are {staleHours >= 36 ? `${Math.round(staleHours / 24)} days` : `${Math.round(staleHours)} hours`} old ({new Date(newest).toLocaleString(undefined, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' })}).</strong>{' '}
          <span className="muted">{nextRun ? `The next scheduled run is ${when(nextRun)}.` : ''}</span>{' '}
          {isAdmin && (
            <button className="linkish" disabled={sched?.running} onClick={() => api.runAll(false).then(() => setErr('')).catch((e) => setErr((e as Error).message))}>
              {sched?.running ? 'Running…' : 'Run all now'}
            </button>
          )}
        </section>
      )}
      {isAdmin && sched?.lastErr && <div className="callout warn">Last scheduled run reported: {sched.lastErr}</div>}
      {settings.tips?.length > 0 && (
        <div className="callout info">
          <div className="row between" style={{ marginBottom: 4 }}>
            <strong>For you</strong>
            <Link to="/about-you" className="small">Based on your answers · change</Link>
          </div>
          <ul>{settings.tips.map((t) => <li key={t}>{t}</li>)}</ul>
        </div>
      )}

      <section className="panel" style={{ overflow: 'hidden' }}>
        <div className="panel-head">
          <div>
            <h2>Forecasts</h2>
            <p className="sub">{hz === 'all' ? 'Every horizon at once.' : `For ${LONG[hz]} ahead.`} Your horizon is {LONG[settings.horizon] ?? 'a month'}.</p>
          </div>
          <div className="seg" role="tablist" aria-label="How far ahead">
            {[...HORIZONS, ['all', 'All'] as [string, string]].map(([k, l]) => (
              <button key={k} role="tab" aria-selected={hz === k} className={hz === k ? 'on' : ''} onClick={() => { setHz(k); track('view', k) }}>{l}</button>
            ))}
          </div>
        </div>
        {!items ? (
          <Loading label="Loading watchlist…" />
        ) : items.length === 0 ? (
          <div className="empty">Your watchlist is empty. Add a stock, fund or crypto with the search box above.</div>
        ) : (
          items.map((it) => <Row key={it.symbol} it={it} hz={hz} busy={!!busy[it.symbol]} onRefresh={() => refresh(it.symbol)} onRemove={() => api.removeWatch(it.symbol).then(load)} />)
        )}
        <p className="small muted wl-legend">
          ✓ tested edge · ◐ weak evidence · ○ not proven. The bar is the chance the price is higher; the line marks 50%. Only a tested edge is worth acting on. <Link to="/methodology">How it works</Link>
        </p>
      </section>

      {tokens && tokens.length > 0 && (
        <section className="panel" style={{ overflow: 'hidden' }}>
          <div className="panel-head"><div><h2>Meme coins and DEX tokens</h2><p className="sub">No forecast for these: not enough history to test one. Each is safety-checked at every run, with an alert if things get worse.</p></div></div>
          {tokens.map(({ address, chain, brief: b }) => {
            const [cls, idText] = IDENTITY[b?.identity ?? 'unknown']
            const risk = !b ? '' : b.riskScore >= 60 ? 'down' : b.riskScore >= 15 ? 'hold' : 'up'
            return (
              <div key={address} className="tok-row">
                <Link to={`/token/${address}`} className="wl-main">
                  <span className="wl-name">{b?.name ?? `${address.slice(0, 6)}…`}</span>{' '}
                  <span className="mono small faint">{b ? `${b.symbol} · ${b.chainLabel}` : chain}</span>
                  {idText && <span className={`small ${cls}`} style={{ display: 'block', marginTop: 3 }}>{idText}</span>}
                </Link>
                <div className="mono small">{b ? tokenPrice(b.priceUsd) : '—'}<div><Change x={b?.change24h} /> {b && <span className="faint">24h</span>}</div></div>
                <div className="tok-facts">
                  <div><p className="tag faint">Market cap</p><p className="mono small">{b ? usd(b.marketCap) : '—'}</p></div>
                  <div><p className="tag faint">Liquidity</p><p className="mono small">{b ? usd(b.liquidity) : '—'}</p></div>
                  <div><p className="tag faint">Top 10 hold</p><p className="mono small">{b?.top10 ? `${Math.round(b.top10 * 100)}%` : '—'}</p></div>
                </div>
                <div>
                  {b ? <><span className={`chip-sm ${risk === 'hold' ? 'hold' : risk}`}>{b.verdict.split(' (')[0]}</span><div className="tiny faint" style={{ marginTop: 4 }}>{b.reds} red · {b.ambers} amber flags</div><div className="tiny faint">checked {ago(b.checkedAt)}</div></> : <span className="faint small">Not checked yet</span>}
                </div>
                <div className="wl-act">
                  {busy[address] ? <span className="spinner" /> : <button className="linkish small" onClick={() => recheck(address)}>Re-check</button>}
                  <button className="linkish small" style={{ color: 'var(--faint)' }} aria-label={`Remove ${b?.symbol ?? 'token'}`} onClick={() => api.removeWatch(address).then(load)}>Remove</button>
                </div>
              </div>
            )
          })}
        </section>
      )}
    </div>
  )
}

function Row({ it, hz, busy, onRefresh, onRemove }: { it: WatchItem; hz: string; busy: boolean; onRefresh: () => void; onRemove: () => void }) {
  const h = it.horizons.find((x) => x.key === hz)
  return (
    <div className="wl-row">
      <Link to={`/asset/${encodeURIComponent(it.symbol)}`} className="wl-main">
        <span className="wl-name">{shortName(it.name, it.symbol)}</span> <span className="mono small faint">{it.symbol}</span>
      </Link>
      <div className="mono">{it.price != null ? `${it.currency && it.currency !== 'USD' ? '' : '$'}${price(it.price)}${it.currency && it.currency !== 'USD' ? ` ${it.currency}` : ''}` : '—'}<div className="small"><Change x={it.change1d} /></div></div>
      <div>
        {!it.generatedAt ? (
          <span className="small muted">{busy ? 'Analysing…' : 'Not analysed yet'}</span>
        ) : hz === 'all' ? (
          <div className="wl-all">
            {HORIZONS.map(([k, l]) => {
              const x = it.horizons.find((y) => y.key === k)
              const v = x?.available ? verdict(x.signal, x.status) : null
              return <div key={k}><p className="tag faint">{l}</p><p className={`mono small ${v?.cls ?? ''}`} title={v?.text}>{v ? `${v.mark} ${Math.round(x!.pUp * 100)}%` : '—'}</p></div>
            })}
          </div>
        ) : h?.available ? (
          <Reading signal={h.signal} status={h.status} p={h.pUp} />
        ) : (
          <span className="small faint">No forecast for this horizon</span>
        )}
      </div>
      <div className="small muted">
        {busy ? <span className="spinner" /> : it.generatedAt ? ago(it.generatedAt) : ''}
        {it.warnings > 0 && <div className="hold">⚠ {it.warnings} warning{it.warnings > 1 ? 's' : ''}</div>}
      </div>
      <div className="wl-act">
        <button className="linkish small" disabled={busy} onClick={onRefresh}>Refresh</button>
        <button className="linkish small" style={{ color: 'var(--faint)' }} aria-label={`Remove ${it.symbol}`} onClick={onRemove}>Remove</button>
      </div>
    </div>
  )
}

function Reading({ signal, status, p }: { signal: string; status: string; p: number }) {
  const v = verdict(signal, status)
  return (
    <div>
      <p className={`wl-verdict ${v.cls}`}>{v.mark} {v.text}</p>
      <div className="wl-chance">
        <div className="wl-bar" role="img" aria-label={`${Math.round(p * 100)}% chance it is higher`}><i style={{ width: `${p * 100}%` }} /><b /></div>
        <span className="mono small muted">{Math.round(p * 100)}% higher</span>
      </div>
    </div>
  )
}
