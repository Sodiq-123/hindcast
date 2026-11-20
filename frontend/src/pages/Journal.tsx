import { useCallback, useEffect, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useIsAdmin } from '../auth'
import { api, type Journal as JournalT, type JournalSettings, type PracticeState, type Position, type Status } from '../api'
import { WorthALook } from '../learn'
import { Loading } from '../components'
import { pct, price } from '../format'

const clean = (x: number) => (Math.abs(x) < 0.005 ? 0 : x)
const money = (x: number) => {
  if (!Number.isFinite(x)) return '—'
  const v = clean(x)
  return `${v < 0 ? '−' : ''}$${Math.abs(v).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}
// Trade prices keep their significant digits (a token at 0.06395 must not show as 0.0640).
const tp = (x?: number) => (x == null || !Number.isFinite(x) ? '—' : x >= 1 ? `$${price(x)}` : `$${Number(x.toPrecision(4))}`)
const pctx = (x: number, d = 1) => pct(x, d).replace(/\.0%$/, '%')
const num = (s: string) => (s.trim() === '' ? undefined : Number(s.replace(/,/g, '')))
const isToken = (s: string) => /^0x[0-9a-fA-F]{40}$/.test(s.trim()) || /^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(s.trim())
const isCrypto = (s: string) => /-(USD|USDT|EUR|GBP|BTC|ETH)$/i.test(s.trim())
const feeFor = (s: JournalSettings, symbol: string) => (isToken(symbol) ? s.feeToken : isCrypto(symbol) ? s.feeCrypto : s.feeStock)
const kindOf = (p: Position) => (p.kind === 'token' ? 'Token' : isCrypto(p.symbol) ? 'Crypto' : 'Stock')
const nameOf = (p: Position) => (p.kind === 'token' ? p.name || `${p.symbol.slice(0, 4)}…${p.symbol.slice(-4)}` : p.symbol)
const href = (p: Position) => (p.kind === 'token' ? `/token/${p.symbol}` : `/asset/${encodeURIComponent(p.symbol)}`)
const day = (d?: string) => {
  if (!d) return ''
  const t = new Date(d.slice(0, 10) + 'T12:00:00Z')
  return Number.isNaN(t.getTime()) ? d : t.toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(t.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {}) })
}
const days = (n: number) => `${n} day${n === 1 ? '' : 's'}`
const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`

function Move({ x, pc }: { x: number; pc?: number }) {
  const v = clean(x)
  if (!v) return <span className="chip-sm flat">{money(0)}</span>
  return <span className={`chip-sm ${v > 0 ? 'up' : 'down'}`}>{v > 0 ? '▲' : '▼'} {money(Math.abs(v))}{pc != null && ` · ${Math.abs(pc * 100).toFixed(1)}%`}</span>
}

export default function Journal() {
  const [j, setJ] = useState<JournalT | null>(null)
  const [status, setStatus] = useState<Status | null>(null)
  const [err, setErr] = useState('')
  const [params] = useSearchParams()
  const [form, setForm] = useState(() => params.has('symbol'))
  const isAdmin = useIsAdmin()
  const load = useCallback(() => {
    api.journal().then(setJ).catch((e) => setErr((e as Error).message))
  }, [])
  useEffect(() => {
    load()
    api.status().then(setStatus).catch(() => {})
  }, [load])

  if (err && !j) return <div className="callout bad">{err}</div>
  if (!j) return <Loading label="Valuing positions…" />
  return (
    <div className="stack" style={{ gap: 20 }}>
      <header className="asset-head">
        <div style={{ maxWidth: 760 }}>
          <h1 style={{ fontSize: 36 }}>Journal</h1>
          <p className="answer-more" style={{ marginTop: 8 }}>Practice with pretend money. Every trade starts with a plan: how much, and the price where you’d admit you were wrong.</p>
        </div>
        {!form && <button className="primary" onClick={() => setForm(true)}>+ New paper trade</button>}
      </header>

      {form && <TradeForm j={j} onDone={() => { setForm(false); load() }} onCancel={() => setForm(false)} />}
      <Standing j={j} />
      <Checks j={j} />
      <PracticeCard />

      <div className="why-grid">
        <section className="panel pad-card stack" style={{ gap: 14 }}>
          <h2>Open trades{j.open.length > 0 && ` (${j.open.length})`}</h2>
          {j.open.length === 0 ? (
            <p className="muted" style={{ lineHeight: 1.6 }}>
              Nothing open. Start one with <strong style={{ color: 'var(--text)' }}>New paper trade</strong>, or from “Record a paper trade” on any asset or token page.
            </p>
          ) : (
            j.open.map((p) => <OpenTrade key={p.id} p={p} j={j} onChange={load} />)
          )}
        </section>
        <Lessons j={j} />
      </div>

      {j.closed.length > 0 && (
        <section className="panel" style={{ overflow: 'hidden' }}>
          <div className="panel-head"><div><h2>Closed trades ({j.closed.length})</h2><p className="sub">Your real track record, after fees. Newest first.</p></div></div>
          {[...j.closed].reverse().map((p) => <ClosedTrade key={p.id} p={p} />)}
        </section>
      )}

      <Rules s={j.settings} onDone={load} />
      {isAdmin && <Alerts on={!!status?.alerts} />}
    </div>
  )
}

// ---------------------------------------------------------------- practice rounds

function PracticeCard() {
  const [s, setS] = useState<PracticeState | null>(null)
  useEffect(() => { api.practice().then(setS).catch(() => {}) }, [])
  const cur = s?.current
  return (
    <section className="panel pr-entrycard">
      <div>
        <p style={{ fontSize: 15, lineHeight: 1.55 }}>
          <strong>Practice on past days.</strong>{' '}
          <span className="muted">{cur ? `You’re on round ${cur.n} of ${cur.of}.` : 'Ten quick rounds with real prices; results in seconds, not weeks.'}{s && s.done > 0 && !cur ? ` ${s.done} rounds so far.` : ''}</span>
        </p>
      </div>
      <Link className="btn" to="/journal/practice">{cur ? 'Carry on' : s?.finished ? 'Practice again' : 'Start 10 rounds'}</Link>
    </section>
  )
}

// ---------------------------------------------------------------- where you stand

function Standing({ j }: { j: JournalT }) {
  const s = j.settings
  const total = clean(j.realized + j.openPnl)
  const n = j.closed.length
  const parts = [n > 0 && `${plural(n, 'closed trade')}`, j.open.length > 0 && `${j.open.length} open`].filter(Boolean).join(' and ')
  const say = n === 0 && j.open.length === 0
    ? 'No trades yet.'
    : `${total > 0 ? `Up ${money(total)}` : total < 0 ? `Down ${money(-total)}` : 'Even'} on paper, from ${parts}.`
  const risk = j.open.length === 0
    ? 'Nothing is open, so nothing is at risk right now.'
    : `If every stop were hit you would lose ${money(j.openRisk)}, ${pct(j.openRiskPct)} of the account, ${j.openRiskPct > s.maxOpenRisk ? `above your ${pct(s.maxOpenRisk, 0)} limit` : `within your ${pct(s.maxOpenRisk, 0)} limit`}.`
  const fill = Math.min(1, s.maxOpenRisk > 0 ? j.openRiskPct / s.maxOpenRisk : 0)
  const growth = j.equity / s.account - 1
  return (
    <section className="panel answer">
      <div className="answer-top">
        <div style={{ maxWidth: 820 }}>
          <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>Where you stand</p>
          <h2 className="answer-say">{say}</h2>
          <p className="answer-more">
            {n === 0 && j.open.length === 0 ? `Your pretend account starts at ${money(s.account)}. Record a trade with a stop, and this page tracks it every day.` : <>{risk} Your pretend account is worth {money(j.equity)} against the {money(s.account)} you started with.</>}
          </p>
        </div>
      </div>
      <div className="answer-stats four">
        <div>
          <p className="tag faint">Account value</p>
          <p className="mono big">{money(j.equity)}</p>
          <p className="small muted">{Math.abs(growth) < 0.0005 ? 'where you started' : <><span className={growth > 0 ? 'up' : 'down'}>{growth > 0 ? '▲' : '▼'} {Math.abs(growth * 100).toFixed(1)}%</span> since you started</>}</p>
        </div>
        <div>
          <p className="tag faint">At risk now</p>
          <p className={`mono big ${j.openRiskPct > s.maxOpenRisk ? 'down' : ''}`}>{money(j.openRisk)}</p>
          <div className="risk-track" role="img" aria-label={`${pct(j.openRiskPct)} of the account at risk, limit ${pct(s.maxOpenRisk, 0)}`}><i style={{ width: `${fill * 100}%`, background: j.openRiskPct > s.maxOpenRisk ? 'var(--down)' : 'var(--accent)' }} /></div>
          <p className="small muted">{pct(j.openRiskPct)} of your {pct(s.maxOpenRisk, 0)} limit, if every stop were hit</p>
        </div>
        <div>
          <p className="tag faint">Closed trades</p>
          <p className="mono big">{n === 0 ? 'None yet' : `${j.closed.filter((p) => p.pnl > 0).length} of ${n} won`}</p>
          <p className="small muted">{n === 0 ? 'Your record builds as you close trades' : `${money(j.realized)} after fees`}</p>
        </div>
        <div>
          <p className="tag faint">Fees paid</p>
          <p className="mono big">{money(j.feesPaid)}</p>
          <p className="small muted">buy and sell, all trades</p>
        </div>
      </div>
    </section>
  )
}

function Checks({ j }: { j: JournalT }) {
  if (j.warnings.length === 0) return null
  const calm = j.warnings.every((w) => w.level === 'green')
  return (
    <section className={calm ? 'earn-band' : 'panel pad-card'} style={calm ? undefined : { borderColor: 'color-mix(in srgb, var(--hold) 40%, transparent)' }}>
      {!calm && <p className="tag hold" style={{ marginBottom: 8 }}>Checks</p>}
      <div className="stack" style={{ gap: 10 }}>
        {j.warnings.map((w, i) => (
          <div key={i} className="check-row">
            <span className={`lvl ${w.level}`} aria-label={w.level === 'red' ? 'problem' : w.level === 'amber' ? 'caution' : 'fine'} />
            <span><strong>{w.title}.</strong> <span className="muted">{w.detail}</span></span>
          </div>
        ))}
      </div>
    </section>
  )
}

// ---------------------------------------------------------------- open trades

/** The plan as a line: stop, entry and target, with today's price marked. Labels sit in a row underneath so they
 * never overlap, whatever the distances. */
function PlanBar({ stop, entry, target, now }: { stop?: number; entry: number; target?: number; now?: number }) {
  const pts = [stop, entry, target, now].filter((x): x is number => x != null && x > 0)
  const lo = Math.min(...pts) * 0.985, hi = Math.max(...pts) * 1.01
  const x = (v: number) => ((v - lo) / (hi - lo)) * 100
  const s = stop ? x(stop) : 0, e = x(entry), t = target ? x(target) : 100
  return (
    <div>
      <div className="plan-bar" role="img" aria-label={`Stop ${stop ? tp(stop) : 'none'}, entry ${tp(entry)}, target ${target ? tp(target) : 'none'}${now ? `, now ${tp(now)}` : ''}`}>
        <div className="pb-line" style={{ left: `${s}%`, right: `${100 - t}%`, background: `linear-gradient(90deg, var(--pb-down) ${((e - s) / Math.max(1, t - s)) * 100}%, var(--pb-up) 0)` }} />
        {stop != null && <i className="pb-tick" style={{ left: `${s}%`, background: 'var(--down)' }} />}
        <i className="pb-tick" style={{ left: `${e}%`, background: 'var(--muted)' }} />
        {target != null && <i className="pb-tick" style={{ left: `${t}%`, background: 'var(--up)' }} />}
        {now != null && <><i className="pb-now" style={{ left: `${x(now)}%` }} /><span className="pb-now-lab mono" style={{ left: `${Math.min(88, Math.max(12, x(now)))}%` }}>Now {tp(now)}</span></>}
      </div>
      <div className="pb-labels mono">
        <span className="down">{stop != null ? `Stop ${tp(stop)}` : 'No stop'}</span>
        <span className="muted">In {tp(entry)}</span>
        <span className={target != null ? 'up' : 'faint'}>{target != null ? `Target ${tp(target)}` : 'No target'}</span>
      </div>
    </div>
  )
}

function OpenTrade({ p, j, onChange }: { p: Position; j: JournalT; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const close = () => {
    setBusy(true)
    api.closePosition(p.id).then(onChange).catch((e) => alert((e as Error).message)).finally(() => setBusy(false))
  }
  return (
    <div className="trade-card">
      <div className="row between" style={{ alignItems: 'baseline', flexWrap: 'wrap', gap: 8 }}>
        <span>
          <Link to={href(p)} className="trade-name">{nameOf(p)}</Link>{' '}
          <span className="small muted">{p.kind === 'token' ? '' : ''}{kindOf(p)} · opened {day(p.openedAt)}, {days(p.days)}</span>
        </span>
        <span className="mono" style={{ fontWeight: 500 }}>
          <span className={clean(p.pnl) > 0 ? 'up' : clean(p.pnl) < 0 ? 'down' : 'muted'}>{clean(p.pnl) > 0 ? '▲' : clean(p.pnl) < 0 ? '▼' : ''} {money(Math.abs(p.pnl))}</span>{' '}
          <span className="small muted">{(p.pnlPct * 100).toFixed(1)}% after fees</span>
        </span>
      </div>
      <PlanBar stop={p.stop} entry={p.entry} target={p.target} now={p.price} />
      {p.priceErr && <p className="small hold">No live price: valued at the entry price. {p.priceErr}</p>}
      <div className="trade-facts">
        <div><p className="tag faint">Value</p><p className="mono">{money(p.value)}</p></div>
        <div><p className="tag faint">Of account</p><p className={`mono ${p.weight > j.settings.maxPosition ? 'hold' : ''}`}>{pct(p.weight)}</p></div>
        <div><p className="tag faint">Lost if stop hit</p><p className={`mono ${!p.stop ? 'down' : ''}`}>{p.stop ? `${money(p.riskToStop)} · ${pct(p.riskPct)}` : 'No stop set'}</p></div>
        <div><p className="tag faint">Fees</p><p className="mono">{money(p.fees)}</p></div>
      </div>
      {(p.note || p.signal) && <p className="small faint">{p.note && `“${p.note}”`}{p.note && p.signal && ' · '}{p.signal && `Signal then: ${p.signal}`}</p>}
      <div className="row" style={{ gap: 14 }}>
        <button onClick={close} disabled={busy}>{busy ? 'Closing…' : 'Close at today’s price'}</button>
        <button className="linkish small" style={{ color: 'var(--faint)' }} onClick={() => confirm('Delete this entry? Use Close to keep it in your record.') && api.deletePosition(p.id).then(onChange)}>Delete (a mistake)</button>
      </div>
    </div>
  )
}

function ClosedTrade({ p }: { p: Position }) {
  const gapped = p.stop != null && p.exit != null && p.exit < p.stop
  return (
    <div className="closed-row">
      <div>
        <Link to={href(p)} className="trade-name">{nameOf(p)}</Link>{' '}
        <span className="small muted">{kindOf(p)} · {day(p.openedAt)} → {day(p.closedAt)} · {days(p.days)}</span>
        {gapped && <p className="small down" style={{ marginTop: 4 }}>▼ Closed {Math.round((1 - p.exit! / p.stop!) * 100)}% below its stop</p>}
        {p.note && <p className="small faint" style={{ marginTop: 4 }}>“{p.note}”</p>}
        {p.signal && <p className="small faint" style={{ marginTop: 2 }}>Signal then: {p.signal}</p>}
      </div>
      <div className="mono small muted">{tp(p.entry)} → {tp(p.exit)}<br /><span className="faint">fees {money(p.fees)}</span></div>
      <div className="r"><Move x={p.pnl} pc={p.pnlPct} /></div>
    </div>
  )
}

// ---------------------------------------------------------------- what your record shows

/** Plain observations from closed trades. Facts only; never advice on what to buy. */
function lessonsFor(j: JournalT): [string, string][] {
  const c = j.closed
  const out: [string, string][] = []
  const gaps = c.filter((p) => p.stop != null && p.exit != null && p.exit < p.stop)
  if (gaps.length === 1) {
    const g = gaps[0]
    out.push(['One loss gapped past its stop.', ` ${nameOf(g)}’s stop was ${tp(g.stop)}, but it was closed at ${tp(g.exit)}, ${Math.round((1 - g.exit! / g.stop!) * 100)}% lower. Prices can jump straight past a stop, so a stop limits a loss without guaranteeing it.`])
  } else if (gaps.length > 1) {
    out.push([`${gaps.length} losses gapped past their stops.`, ' Prices can jump straight past a stop, so a stop limits a loss without guaranteeing it. Smaller positions keep a gap affordable.'])
  }
  if (c.length >= 3 && j.realized > 0) {
    const best = c.reduce((a, b) => (b.pnl > a.pnl ? b : a))
    if (best.pnl > j.realized * 0.8) {
      const rest = j.realized - best.pnl
      out.push(['One trade made most of the profit.', ` ${nameOf(best)} made ${money(best.pnl)}. Without it you’d be ${rest < 0 ? 'down' : 'up'} ${money(Math.abs(rest))}. One big winner can hide a weak method, so judge the plan, not just the total.`])
    }
  }
  const wins = c.filter((p) => p.pnl > 0), losses = c.filter((p) => p.pnl < 0)
  if (wins.length >= 2 && losses.length >= 2) {
    const avg = (xs: Position[]) => xs.reduce((s, p) => s + p.days, 0) / xs.length
    if (avg(losses) > 1.5 * avg(wins)) out.push(['You held losers longer than winners.', ` On average ${Math.round(avg(losses))} days for losses against ${Math.round(avg(wins))} for gains. Most people do this; the stop you set at the start is there to stop it.`])
  }
  const all = [...c, ...j.open]
  if (all.length >= 3 && all.every((p) => p.kind === 'token')) {
    out.push(['Every trade so far was a meme token.', ` They can lose most of their value in hours, and at ${pctx(j.settings.feeToken)} each way the fees came to ${money(j.feesPaid)}. Try the same plan on a stock or a large coin to compare.`])
  }
  const gross = wins.reduce((s, p) => s + p.pnl + p.fees, 0)
  if (out.length < 3 && gross > 0 && j.feesPaid > 0.25 * gross) {
    out.push(['Fees ate a big share of the gains.', ` ${money(j.feesPaid)} in fees against ${money(gross)} of gains before fees. Fewer, larger-conviction trades keep more of it.`])
  }
  return out.slice(0, 3)
}

function Lessons({ j }: { j: JournalT }) {
  const ls = lessonsFor(j)
  return (
    <section className="panel pad-card stack" style={{ gap: 14 }}>
      <h2>What your record shows</h2>
      {ls.length === 0 ? (
        <p className="muted" style={{ lineHeight: 1.6 }}>
          {j.closed.length < 3 ? `Close a few trades and patterns will show here: stops that didn’t hold, winners cut short, fees adding up. ${j.closed.length ? `${plural(j.closed.length, 'closed trade')} so far.` : ''}` : 'Nothing stands out yet: no gaps past stops, no single trade carrying the rest.'}
        </p>
      ) : (
        ls.map(([a, b], i) => <div key={a} className="reason"><span className="n">{i + 1}</span><span><strong>{a}</strong>{b}</span></div>)
      )}
    </section>
  )
}

// ---------------------------------------------------------------- new trade

function TradeForm({ j, onDone, onCancel }: { j: JournalT; onDone: () => void; onCancel: () => void }) {
  const [params] = useSearchParams()
  const [symbol, setSymbol] = useState(params.get('symbol') ?? '')
  const [stop, setStop] = useState(params.get('stop') ?? '')
  const [target, setTarget] = useState(params.get('target') ?? '')
  const [entry, setEntry] = useState('')
  const [amount, setAmount] = useState('')
  const [note, setNote] = useState('')
  const [signal] = useState(params.get('signal') ?? '')
  const [quote, setQuote] = useState<{ symbol: string; price: number } | null>(null)
  const [hint, setHint] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const s = j.settings
  const sym = symbol.trim()

  // Today's price, for the plan preview when no entry price is typed.
  useEffect(() => {
    if (sym.length < 1) return
    const id = setTimeout(() => {
      api.quote(sym).then((q) => setQuote({ symbol: sym, price: q.price })).catch(() => setQuote(null))
    }, 500)
    return () => clearTimeout(id)
  }, [sym])

  const e = num(entry) ?? (quote?.symbol === sym ? quote.price : undefined)
  const st = num(stop), tg = num(target), amt = num(amount)
  const fee = feeFor(s, sym)
  let plan = ''
  if (sym && e && amt) {
    const qty = amt / (1 + fee) / e
    plan = `buy about ${money(amt)} of ${sym.length > 14 ? 'this token' : sym.toUpperCase()} at ${tp(e)}.`
    if (st && st < e) {
      const loss = amt - qty * st * (1 - fee)
      plan += ` If it falls to ${tp(st)} you lose about ${money(loss)}, ${pct(loss / j.equity)} of your account, fees included.`
      if (loss / j.equity > s.riskPerTrade * 1.5) plan += ` That’s above your ${pct(s.riskPerTrade, 0)} rule.`
    } else if (!st) {
      plan += ' Without a stop, the whole amount counts as at risk.'
    }
    if (tg && tg > e) plan += ` If it reaches ${tp(tg)} you make about ${money(qty * tg * (1 - fee) - amt)}.`
  }

  const sizeIt = () => {
    setErr('')
    setHint('')
    if (!sym || st == null) return setErr('Enter the symbol (or token address) and your stop price first.')
    if (!e) return setErr('Still fetching today’s price. Try again in a moment, or type an entry price.')
    if (st >= e) return setErr('The stop must be below the entry price.')
    const dist = (e - st) / e
    // Loss if the stop is hit = the fall to the stop + the buy fee + the sell fee at the stop.
    const lossPerDollar = dist + fee + fee * (1 - dist)
    const risk = j.equity * s.riskPerTrade * j.sizeMultiplier
    const byRisk = (risk / lossPerDollar) * (1 + fee)
    const cap = j.equity * s.maxPosition
    setAmount(Math.min(byRisk, cap).toFixed(2))
    setHint(
      `Risking ${pct(s.riskPerTrade * j.sizeMultiplier)} of ${money(j.equity)} = ${money(risk)}. A stop-out loses ${pct(lossPerDollar)} of the position including fees, so spend ${money(byRisk)}` +
        (byRisk > cap ? `, capped at your ${pct(s.maxPosition, 0)} max position (${money(cap)}).` : '.') +
        (j.sizeMultiplier < 1 ? ' Halved by the circuit-breaker.' : ''),
    )
  }

  const submit = async () => {
    setBusy(true)
    setErr('')
    try {
      await api.openPosition({ symbol: sym, amount: amt, entry: num(entry), stop: st, target: tg, note, signal })
      onDone()
    } catch (x) {
      setErr((x as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <section className="panel pad-card stack research-confirm" style={{ gap: 14 }}>
      <div>
        <p className="tag" style={{ color: 'var(--accent-ink)' }}>New paper trade</p>
        <h2 className="display" style={{ fontSize: 20, marginTop: 6 }}>Make the plan before you buy</h2>
      </div>
      <div className="trade-steps">
        <div className="tstep">
          <p><span className="n">1</span><strong>What</strong></p>
          <label>Symbol or token address<input value={symbol} onChange={(x) => setSymbol(x.target.value)} placeholder="AAPL, BTC-USD or an address" /></label>
          {quote?.symbol === sym && <p className="small faint">Now {tp(quote.price)}</p>}
        </div>
        <div className="tstep">
          <p><span className="n">2</span><strong>Where you’d be wrong</strong></p>
          <label>Stop: sell if it falls to<input value={stop} onChange={(x) => setStop(x.target.value)} inputMode="decimal" /></label>
          <label>Target (optional)<input value={target} onChange={(x) => setTarget(x.target.value)} inputMode="decimal" /></label>
        </div>
        <div className="tstep">
          <p><span className="n">3</span><strong>How much</strong></p>
          <label>Amount ($)<input value={amount} onChange={(x) => setAmount(x.target.value)} inputMode="decimal" /></label>
          <button className="linkish small" style={{ alignSelf: 'flex-start' }} onClick={sizeIt}>Size it with the {pct(s.riskPerTrade, 0)} rule</button>
          <label>Entry price (optional)<input value={entry} onChange={(x) => setEntry(x.target.value)} inputMode="decimal" placeholder="today’s price" /></label>
        </div>
        <div className="tstep">
          <p><span className="n">4</span><strong>Why</strong></p>
          <label>Why buy, and what would prove you wrong?<textarea rows={3} value={note} onChange={(x) => setNote(x.target.value)} placeholder="e.g. validated 1-month signal; wrong if it closes below the stop" /></label>
          {signal && <p className="small faint">Signal recorded with it: {signal}</p>}
        </div>
      </div>
      {hint && <p className="small muted">{hint}</p>}
      <WorthALook symbol={sym} amount={amt} entry={e} stop={st} fee={fee} />
      {plan && <p className="plan-say"><strong>The plan:</strong> {plan}</p>}
      <p className="small faint">Long positions only. Fees are taken from the amount: {pct(s.feeStock, 1)} for stocks, {pct(s.feeCrypto, 1)} for crypto, {pct(s.feeToken, 1)} for DEX tokens, on the buy and again on the sell.</p>
      {err && <p className="small down">{err}</p>}
      <div className="row" style={{ gap: 8 }}>
        <button className="primary" onClick={submit} disabled={busy || !sym || !amt}>{busy ? 'Saving…' : 'Record trade'}</button>
        <button onClick={onCancel}>Cancel</button>
      </div>
    </section>
  )
}

// ---------------------------------------------------------------- rules and alerts

function Rules({ s, onDone }: { s: JournalSettings; onDone: () => void }) {
  const [open, setOpen] = useState(false)
  const [v, setV] = useState({
    account: String(s.account), risk: String(100 * s.riskPerTrade), open: String(100 * s.maxOpenRisk), pos: String(100 * s.maxPosition),
    fs: String(100 * s.feeStock), fc: String(100 * s.feeCrypto), ft: String(100 * s.feeToken),
  })
  const [msg, setMsg] = useState('')
  const save = () =>
    api
      .saveJournalSettings({
        account: Number(v.account), riskPerTrade: Number(v.risk) / 100, maxOpenRisk: Number(v.open) / 100, maxPosition: Number(v.pos) / 100,
        feeStock: Number(v.fs) / 100, feeCrypto: Number(v.fc) / 100, feeToken: Number(v.ft) / 100,
      })
      .then(() => {
        setMsg('Saved.')
        onDone()
      })
      .catch((e) => setMsg((e as Error).message))
  const f = (k: keyof typeof v, label: string) => <label>{label}<input value={v[k]} onChange={(e) => setV({ ...v, [k]: e.target.value })} inputMode="decimal" /></label>
  return (
    <section className="panel pad-card">
      <div className="row between" style={{ alignItems: 'flex-start', gap: 12 }}>
        <div>
          <h2>Your rules</h2>
          <p className="small muted" style={{ marginTop: 4 }}>
            Risk {pctx(s.riskPerTrade)} a trade · {pctx(s.maxOpenRisk)} in total · no position above {pctx(s.maxPosition)} · fees each way: {pctx(s.feeStock)} stocks, {pctx(s.feeCrypto)} crypto, {pctx(s.feeToken)} tokens · started with {money(s.account)}
          </p>
        </div>
        <button className="linkish" onClick={() => setOpen(!open)} aria-expanded={open}>{open ? 'Done' : 'Edit'}</button>
      </div>
      {open && (
        <div className="stack" style={{ marginTop: 14 }}>
          <div className="form-grid">
            {f('account', 'Starting account ($)')}
            {f('risk', 'Risk per trade (%)')}
            {f('open', 'Max total risk (%)')}
            {f('pos', 'Max single position (%)')}
            {f('fs', 'Fee, stocks & ETFs (% each way)')}
            {f('fc', 'Fee, crypto (% each way)')}
            {f('ft', 'Fee, DEX tokens (% each way)')}
          </div>
          <p className="small muted">Common starting points: risk 1% a trade, 6% in total, no position above 20%. Fees include the spread; match them to your real broker or exchange. New rates apply to new trades only.</p>
          <div className="row"><button onClick={save}>Save rules</button>{msg && <span className="small muted">{msg}</span>}</div>
        </div>
      )}
    </section>
  )
}

function Alerts({ on }: { on: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <section className="panel pad-card">
      <div className="row between" style={{ alignItems: 'flex-start', gap: 12 }}>
        <div>
          <h2>Telegram alerts</h2>
          <p className="small muted" style={{ marginTop: 4 }}>{on ? 'On' : 'Off'} · messages after each scheduled run about your watchlist signals, journal stops and targets, and the circuit-breaker.</p>
        </div>
        <button className="linkish" onClick={() => setOpen(!open)} aria-expanded={open}>{open ? 'Done' : on ? 'Test' : 'How to turn on'}</button>
      </div>
      {open && (
        <div className="stack" style={{ marginTop: 12 }}>
          {!on && <p className="small muted">Create a bot with @BotFather in Telegram, send it any message, put <code>TELEGRAM_BOT_TOKEN</code> and <code>TELEGRAM_CHAT_ID</code> in your <code>.env</code>, and restart.</p>}
          <div><button disabled={!on} onClick={() => api.testAlert().then(() => alert('Sent. Check Telegram.')).catch((e) => alert((e as Error).message))}>Send a test message</button></div>
          <p className="small faint">Alerts cover your own watchlist and journal only, and go to the Telegram chat set on the server.</p>
        </div>
      )}
    </section>
  )
}
