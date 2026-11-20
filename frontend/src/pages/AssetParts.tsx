import { Fragment, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { api, type Article, type HorizonForecast, type PlainAnswer, type Report } from '../api'
import { PriceChart } from '../charts'
import { BackLink, Panel } from '../components'
import { ago, pct, price, spct } from '../format'
import { BusinessPanel, LevelsPanel } from './AssetExtras'
import { AnalogsTab, BacktestTab, DriversTab, EarningsPanel, EvidencePanel, OptionsPanel, RiskTab, WhyTab } from './Asset'

const LABEL: Record<string, string> = { '1d': 'Day', '1w': 'Week', '1m': 'Month', '1y': 'Year' }
const money = (x: number, cur: string) => (cur === 'USD' || !cur ? `$${price(x)}` : `${price(x)} ${cur}`)
const shortDate = (d: string) => {
  const t = new Date(d + 'T12:00:00Z')
  if (Number.isNaN(t.getTime())) return d
  return t.toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(t.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {}) })
}
const shortName = (name: string, sym: string) => {
  let n = name.trim()
  for (const s of [' USD', ' Corporation', ' Corp.', ' Corp', ' Inc.', ' Inc', ' plc', ' PLC', ' Ltd.', ' Ltd', ' N.V.', ' S.A.', ' Holdings', ' Company']) if (n.endsWith(s)) n = n.slice(0, -s.length)
  n = n.replace(/,$/, '').trim()
  return !n || n.length > 24 ? sym : n
}

function Change({ x }: { x: number }) {
  if (!x) return <span className="muted">0.0%</span>
  return <span className={x > 0 ? 'up' : 'down'}>{x > 0 ? '▲' : '▼'} {Math.abs(x * 100).toFixed(1)}%</span>
}

// ---------- header ----------

export function AssetHeader({ report, running, onRun, history, onPick, hasLLM }: {
  report: Report
  running: boolean
  onRun: (llm: boolean, force: boolean) => void
  history: { id: number; createdAt: string; headline: string }[]
  onPick: (id: number) => void
  hasLLM: boolean
}) {
  const [watching, setWatching] = useState<boolean | null>(null)
  const [menu, setMenu] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    api.watchlist().then((w) => setWatching(w.some((x) => x.symbol === report.symbol))).catch(() => setWatching(false))
  }, [report.symbol])
  useEffect(() => {
    const close = (e: MouseEvent) => box.current && !box.current.contains(e.target as Node) && setMenu(false)
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [])
  return (
    <div className="stack" style={{ gap: 10 }}>
      <BackLink fallback="/" />
      <header className="asset-head">
        <div>
          <div className="row" style={{ alignItems: 'baseline', gap: 12 }}>
            <h1 style={{ fontSize: 36 }}>{shortName(report.name, report.symbol)}</h1>
            <span className="mono muted small">{report.symbol}{report.exchange ? ` · ${report.exchange}` : ''}</span>
          </div>
          <p className="row" style={{ alignItems: 'baseline', gap: 12, marginTop: 6 }}>
            <span className="mono" style={{ fontSize: 26, fontWeight: 500, letterSpacing: '-0.02em' }}>{money(report.price, report.currency)}</span>
            <span className="mono"><Change x={report.change1d} /></span>
            <span className="small faint">
              Close {shortDate(report.asOf)} · analysed {ago(report.generatedAt)}
              {history.length > 1 && (
                <>
                  {' · '}
                  <select className="inline-select" aria-label="Earlier analyses" onChange={(e) => onPick(Number(e.target.value))} value={report.id || ''}>
                    {history.map((x, i) => <option key={x.id} value={x.id}>{i === 0 ? 'Latest analysis' : new Date(x.createdAt).toLocaleString()}</option>)}
                  </select>
                </>
              )}
            </span>
          </p>
        </div>
        <div className="row" style={{ gap: 8 }}>
          <button disabled={watching !== false} onClick={() => api.addWatch(report.symbol).then(() => setWatching(true))}>
            {watching ? '✓ On your watchlist' : '+ Watchlist'}
          </button>
          <Link className="btn" to={`/research/${encodeURIComponent(report.symbol)}`}>Research</Link>
          <div className="dropdown" ref={box}>
            <button onClick={() => setMenu(!menu)} aria-expanded={menu} disabled={running}>
              {running ? <><span className="spinner" /> Analysing…</> : 'Refresh ▾'}
            </button>
            {menu && (
              <div className="menu">
                <button onClick={() => { setMenu(false); onRun(false, true) }}>Quick refresh<span className="sub">Prices and model, about a minute</span></button>
                <button onClick={() => { setMenu(false); onRun(true, true) }}>Full analysis<span className="sub">{hasLLM ? 'Also has the AI analyst read the news' : 'AI analyst not set up on this server'}</span></button>
              </div>
            )}
          </div>
        </div>
      </header>
    </div>
  )
}

// ---------- earnings band ----------

export function EarningsBand({ report }: { report: Report }) {
  const [now] = useState(() => Date.now())
  const e = report.earnings
  if (!e?.covered) return null
  const name = shortName(report.name, report.symbol)
  const timing = (t: string) => (t === 'pre' ? ', before the market opens' : t === 'after' ? ', after the market closes' : '')
  const move = e.reactions >= 4 ? ` Past reports moved it about ${pct(e.avgAbsMove)} the next day, either way, so short-term ranges are wider than usual.` : ''
  const today = new Date(now).toISOString().slice(0, 10)
  if (e.next && e.next.date < today) {
    // The report came out after this analysis was made.
    return (
      <section className="earn-band">
        <span className="tag">Earnings since this analysis</span>
        <span><strong>{name} reported on {shortDate(e.next.date)}, after this analysis was made.</strong> Refresh to include the price reaction.{move}</span>
      </section>
    )
  }
  if (e.next && e.next.barsAway <= 3) {
    const days = Math.round((Date.parse(e.next.date) - Date.parse(today)) / 86400000)
    const when = days <= 0 ? 'today' : days === 1 ? 'tomorrow' : `on ${new Date(e.next.date + 'T12:00:00Z').toLocaleDateString(undefined, { weekday: 'long' })}`
    return (
      <section className="earn-band">
        <span className="tag">Earnings {when}</span>
        <span><strong>{name} reports {when}{timing(e.next.time)}.</strong>{move}</span>
      </section>
    )
  }
  const last = e.history[0]
  if (last && (now - Date.parse(last.date)) / 86400000 <= 4) {
    return (
      <section className="earn-band">
        <span className="tag">Earnings just out</span>
        <span><strong>{name} reported on {shortDate(last.date)}.</strong> The price moved {spct(last.reaction)} in reaction{e.reactions >= 4 ? `, against a typical ${pct(e.avgAbsMove)} either way` : ''}.</span>
      </section>
    )
  }
  return null
}

// ---------- the short answer ----------

const TESTED_MARK: Record<string, string> = { proven: '✓', weak: '◐', not_proven: '○' }

export function ShortAnswer({ report, h, a, sel, setSel }: { report: Report; h?: HorizonForecast; a?: PlainAnswer; sel: string; setSel: (k: string) => void }) {
  return (
    <section className="panel answer">
      <div className="answer-top">
        <div style={{ maxWidth: 760 }}>
          <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
          {h && h.available && a ? (
            <>
              <h2 className="answer-say">{a.say}</h2>
              <p className="answer-more">{a.more}</p>
            </>
          ) : (
            <>
              <h2 className="answer-say">No forecast for this horizon.</h2>
              <p className="answer-more">{h?.reason || 'There isn’t enough price history to test one.'}</p>
            </>
          )}
        </div>
        <HorizonTabs report={report} sel={sel} setSel={setSel} />
      </div>
      {h && h.available && a && (
        <div className="answer-stats">
          <div>
            <p className="tag faint">Chance it's higher</p>
            <p className="mono big">{pct(h.pUp, 0)} <small>usually {pct(h.baseRate, 0)}</small></p>
          </div>
          <div>
            <p className="tag faint">Tested on {shortName(report.name, report.symbol)}</p>
            <p className={`verdict-sm ${a.tested === 'proven' ? 'up' : ''}`}>{TESTED_MARK[a.tested]} {a.testLabel}</p>
            <p className="small muted">Right {pct(a.right)} vs {pct(a.guess)} by always guessing “{a.guessWord}”</p>
          </div>
          <div>
            <p className="tag faint">Likely by {shortDate(h.targetDate)}</p>
            <p className="mono mid">{money(h.priceTargets.p10, report.currency)} – {money(h.priceTargets.p90, report.currency)}</p>
            <p className="small muted">middle {money(h.priceTargets.p50, report.currency)} · 8 in 10 similar cases</p>
          </div>
        </div>
      )}
    </section>
  )
}

function HorizonTabs({ report, sel, setSel }: { report: Report; sel: string; setSel: (k: string) => void }) {
  return (
    <div className="seg" role="tablist" aria-label="How far ahead">
      {report.horizons.map((x) => (
        <button key={x.key} role="tab" aria-selected={x.key === sel} className={x.key === sel ? 'on' : ''} disabled={!x.available} onClick={() => setSel(x.key)}>
          {LABEL[x.key] ?? x.label}
        </button>
      ))}
    </div>
  )
}

// ---------- chart with the forecast cone ----------

const HIST: Record<number, number> = { 1: 45, 5: 70, 21: 126, 252: 504 }

export function ConeChart({ report, h }: { report: Report; h?: HorizonForecast }) {
  const box = useRef<HTMLDivElement>(null)
  const [w, setW] = useState(760)
  const [hover, setHover] = useState<number | null>(null)
  useEffect(() => {
    if (!box.current) return
    const ro = new ResizeObserver(([e]) => setW(Math.max(280, Math.round(e.contentRect.width))))
    ro.observe(box.current)
    return () => ro.disconnect()
  }, [])
  const c = report.chart
  const live = h && h.available ? h : undefined
  const histN = Math.min(c.close.length, live ? (HIST[live.bars] ?? Math.max(60, live.bars * 4)) : 126)
  const closes = c.close.slice(-histN)
  const dates = c.dates.slice(-histN)
  const H = w < 600 ? 220 : 300
  const padL = 8, padR = 74, padT = 14, padB = 26
  const fwd = live ? Math.max(live.bars, Math.round(histN * 0.16)) : 0
  const n = closes.length - 1 + fwd
  const lo = Math.min(...closes, live?.priceTargets.p10 ?? Infinity) * 0.97
  const hi = Math.max(...closes, live?.priceTargets.p90 ?? -Infinity) * 1.02
  const x = (i: number) => padL + (i / Math.max(1, n)) * (w - padL - padR)
  const y = (v: number) => padT + (1 - (v - lo) / (hi - lo)) * (H - padT - padB)
  const line = closes.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('')
  const last = closes.length - 1
  const x0 = x(last), y0 = y(closes[last]), x1 = x(n)
  const ticks = [0.15, 0.5, 0.85].map((f) => lo + (hi - lo) * f)
  const label = live
    ? `${report.name} over the past ${histN} trading days, with a cone showing where 8 in 10 similar past cases ended up by ${live.targetDate}: between ${price(live.priceTargets.p10)} and ${price(live.priceTargets.p90)}, middle ${price(live.priceTargets.p50)}.`
    : `${report.name} over the past ${histN} trading days.`
  const onMove = (e: React.PointerEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect()
    const i = Math.round(((e.clientX - r.left - padL) / (w - padL - padR)) * n)
    setHover(i >= 0 && i <= last ? i : null)
  }
  return (
    <div ref={box} style={{ position: 'relative' }}>
      <svg width={w} height={H} role="img" aria-label={label} onPointerMove={onMove} onPointerLeave={() => setHover(null)} style={{ display: 'block', touchAction: 'pan-y' }}>
        {ticks.map((t) => (
          <g key={t}>
            <line x1={padL} x2={w - padR} y1={y(t)} y2={y(t)} style={{ stroke: 'var(--border)' }} />
            <text x={padL + 2} y={y(t) - 5} style={{ fill: 'var(--faint)', fontSize: 11 }}>{price(t)}</text>
          </g>
        ))}
        {live && <path d={`M${x0},${y0}L${x1},${y(live.priceTargets.p90)}L${x1},${y(live.priceTargets.p10)}Z`} style={{ fill: 'var(--band)' }} />}
        <path d={line} fill="none" style={{ stroke: 'var(--text)' }} strokeWidth={1.6} strokeLinejoin="round" />
        {live && <path d={`M${x0},${y0}L${x1},${y(live.priceTargets.p50)}`} fill="none" style={{ stroke: 'var(--accent-ink)' }} strokeWidth={1.6} strokeDasharray="4 4" />}
        <circle cx={x0} cy={y0} r={4.5} style={{ fill: 'var(--panel)', stroke: 'var(--text)' }} strokeWidth={2} />
        {live && (
          <>
            <text x={x1 + 6} y={y(live.priceTargets.p90) + 4} style={{ fill: 'var(--muted)' }}>{price(live.priceTargets.p90)}</text>
            <text x={x1 + 6} y={y(live.priceTargets.p50) + 4} style={{ fill: 'var(--accent-ink)', fontWeight: 600 }}>{price(live.priceTargets.p50)}</text>
            <text x={x1 + 6} y={y(live.priceTargets.p10) + 4} style={{ fill: 'var(--muted)' }}>{price(live.priceTargets.p10)}</text>
            <text x={x1} y={H - 6} textAnchor="end" style={{ fill: 'var(--faint)' }}>{shortDate(live.targetDate)}</text>
          </>
        )}
        <text x={padL} y={H - 6} style={{ fill: 'var(--faint)' }}>{shortDate(dates[0])}</text>
        {(!live || x1 - x0 > 64) && <text x={x0 - 2} y={H - 6} textAnchor="end" style={{ fill: 'var(--faint)' }}>Today</text>}
        {hover != null && (
          <g pointerEvents="none">
            <line x1={x(hover)} x2={x(hover)} y1={padT} y2={H - padB} style={{ stroke: 'var(--border-strong)' }} />
            <circle cx={x(hover)} cy={y(closes[hover])} r={3.5} style={{ fill: 'var(--text)' }} />
          </g>
        )}
      </svg>
      {hover != null && (
        <div className="chart-tip" style={{ left: Math.min(x(hover), w - 150) }}>
          {shortDate(dates[hover])} · <strong className="mono">{price(closes[hover])}</strong>
        </div>
      )}
    </div>
  )
}

export function HorizonRows({ report, sel, setSel }: { report: Report; sel: string; setSel: (k: string) => void }) {
  return (
    <div className="hrows">
      <p className="tag faint" style={{ margin: '0 0 6px 4px' }}>Every horizon</p>
      {report.horizons.map((x) =>
        x.available ? (
          <button key={x.key} className={`hrow ${x.key === sel ? 'on' : ''}`} aria-pressed={x.key === sel} onClick={() => setSel(x.key)}>
            <span className="hl">{LABEL[x.key] ?? x.label}</span>
            <span className="mono small muted">{money(x.priceTargets.p10, report.currency)} – {money(x.priceTargets.p90, report.currency)}</span>
            <span className="mono">{pct(x.pUp, 0)} <small className="faint">vs {pct(x.baseRate, 0)}</small></span>
          </button>
        ) : (
          <div key={x.key} className="hrow off"><span className="hl">{LABEL[x.key] ?? x.label}</span><span className="small faint">Not enough history</span><span /></div>
        ),
      )}
      <p className="tiny faint" style={{ margin: '10px 4px 0', lineHeight: 1.5 }}>
        Right: chance it's higher vs how often it usually is.{' '}
        {report.horizons.some((x) => x.status === 'validated') ? 'Horizons with a tested edge say so above.' : 'None of these is a tested signal today.'}
      </p>
    </div>
  )
}

// ---------- why ----------

export function WhyCard({ a }: { a?: PlainAnswer }) {
  const [all, setAll] = useState(false)
  if (!a) return null
  const list = all ? a.reasons : a.reasons.slice(0, a.shown)
  return (
    <section className="panel pad-card stack" style={{ gap: 14 }}>
      <h2>Why, in plain words</h2>
      {list.map((r, i) => (
        <div key={r.lead} className="reason"><span className="n">{i + 1}</span><span><strong>{r.lead}</strong>{r.text}</span></div>
      ))}
      {a.reasons.length > a.shown && (
        <button className="linkish" style={{ alignSelf: 'flex-start', minHeight: 36 }} onClick={() => setAll(!all)}>
          {all ? 'Show fewer' : `Show all ${a.reasons.length} reasons`}
        </button>
      )}
    </section>
  )
}

// ---------- AI analyst, with numbered sources ----------

const CITE = /\s*\[([0-9a-f]{8,12})\]/g

/** Numbers the stories the analyst cites, in order of first mention. */
export function useCitations(report: Report) {
  return useMemo(() => {
    const known = new Set((report.news.articles ?? []).map((a) => a.id))
    const num = new Map<string, number>()
    const texts = [...(report.bullCase ?? []), ...(report.bearCase ?? []), ...(report.catalysts ?? []), ...(report.risks ?? []), report.narrative ?? '']
    for (const t of texts) for (const m of t.matchAll(CITE)) if (known.has(m[1]) && !num.has(m[1])) num.set(m[1], num.size + 1)
    const render = (t: string): ReactNode[] => {
      const out: ReactNode[] = []
      let at = 0
      for (const m of t.matchAll(CITE)) {
        out.push(t.slice(at, m.index))
        const n = num.get(m[1])
        if (n) out.push(<a key={`${m.index}`} className="cite" href={`#news-${m[1]}`} title="Source below">{n}</a>)
        at = (m.index ?? 0) + m[0].length
      }
      out.push(t.slice(at))
      return out
    }
    return { num, render }
  }, [report])
}

export function AnalystCard({ report, cites }: { report: Report; cites: ReturnType<typeof useCitations> }) {
  const [full, setFull] = useState(false)
  if (!report.usedLlm || (!report.bullCase?.length && !report.bearCase?.length)) return null
  const r = cites.render
  return (
    <section className="panel pad-card stack" style={{ gap: 16 }}>
      <div>
        <h2>What the AI analyst reads into the news</h2>
        <p className="small muted" style={{ marginTop: 4 }}>Written by {report.llmModel} from today's headlines and the model's numbers. Numbered boxes link to the stories below.</p>
      </div>
      <div className="stack" style={{ gap: 6 }}>
        <p className="tag up">▲ For a rise</p>
        {report.bullCase?.slice(0, full ? undefined : 2).map((b) => <p key={b} className="analyst-line">{r(b)}</p>)}
      </div>
      <div className="stack" style={{ gap: 6 }}>
        <p className="tag down">▼ For a fall</p>
        {report.bearCase?.slice(0, full ? undefined : 2).map((b) => <p key={b} className="analyst-line">{r(b)}</p>)}
      </div>
      {report.catalysts && report.catalysts.length > 0 && (
        <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
          <p className="tag faint" style={{ marginBottom: 6 }}>Watch for</p>
          <p className="analyst-line">{report.catalysts.slice(0, full ? undefined : 3).map((c, i) => <Fragment key={c}>{i > 0 && ' · '}{r(c)}</Fragment>)}</p>
        </div>
      )}
      {full && (
        <>
          {report.risks && report.risks.length > 0 && (
            <div>
              <p className="tag faint" style={{ marginBottom: 6 }}>Key risks</p>
              <ul className="list small">{report.risks.map((b) => <li key={b}>{r(b)}</li>)}</ul>
            </div>
          )}
          <div style={{ borderTop: '1px solid var(--border)', paddingTop: 14 }}>
            <p className="tag faint" style={{ marginBottom: 6 }}>The full write-up</p>
            {report.narrative.split(/\n\n+/).map((para, i) => <p key={i} className="analyst-line" style={{ marginBottom: 10 }}>{r(para)}</p>)}
          </div>
        </>
      )}
      <button className="linkish" style={{ alignSelf: 'flex-start', minHeight: 36 }} onClick={() => setFull(!full)}>{full ? 'Show less' : 'Read the full analysis'}</button>
    </section>
  )
}

// ---------- news ----------

function impactChip(a: Article) {
  const word = a.impact ? a.impact.charAt(0).toUpperCase() + a.impact.slice(1) : ''
  if (a.direction > 0.1) return <span className="chip-sm up">▲ {word}</span>
  if (a.direction < -0.1) return <span className="chip-sm down">▼ {word}</span>
  return <span className="chip-sm flat">— {word}</span>
}

export function NewsList({ report, cites }: { report: Report; cites: ReturnType<typeof useCitations> }) {
  const [all, setAll] = useState(false)
  const arts = useMemo(() => {
    const list = [...(report.news.articles ?? [])]
    return list.sort((a, b) => {
      const ca = cites.num.get(a.id) ?? 999, cb = cites.num.get(b.id) ?? 999
      if (ca !== cb) return ca - cb
      return Math.abs(b.direction) * b.relevance - Math.abs(a.direction) * a.relevance
    })
  }, [report, cites])
  if (arts.length === 0) return null
  const shown = all ? arts : arts.slice(0, 6)
  return (
    <section className="panel pad-card" id="news">
      <h2 style={{ marginBottom: 6 }}>News</h2>
      {report.news.error && <div className="callout warn small" style={{ margin: '6px 0 10px' }}>{report.news.error}</div>}
      {shown.map((a) => {
        const n = cites.num.get(a.id)
        return (
          <div key={a.id} id={`news-${a.id}`} className="news-item">
            {impactChip(a)}
            <div>
              <a className="news-title" href={a.url} target="_blank" rel="noreferrer">{a.title}</a>
              {n && <span className="cite">{n}</span>}
              <p className="tiny faint" style={{ marginTop: 2 }}>{a.source || (a.scope === 'asset' ? 'Company news' : 'Markets')} · {ago(a.published)}</p>
              {a.reasoning && (n || all) && <p className="small muted" style={{ marginTop: 4 }}>{a.reasoning}</p>}
            </div>
          </div>
        )
      })}
      {arts.length > 6 && <button className="linkish" style={{ minHeight: 40 }} onClick={() => setAll(!all)}>{all ? 'Show fewer' : `Show all ${arts.length} headlines`}</button>}
    </section>
  )
}

// ---------- go deeper ----------

export const DEEPER = [
  ['earn', 'Earnings'],
  ['sim', 'Similar days'],
  ['track', 'Track record'],
  ['drivers', 'Drivers'],
  ['plan', 'If you hold it'],
  ['tech', 'Technical'],
  ['reasoning', 'Full reasoning'],
] as const
export type DeeperTab = (typeof DEEPER)[number][0]

export function Deeper({ report, h, a, tab, setTab }: { report: Report; h?: HorizonForecast; a?: PlainAnswer; tab: DeeperTab; setTab: (t: DeeperTab) => void }) {
  const tabs = DEEPER.filter(([k]) => k !== 'earn' || report.earnings?.covered)
  const live = h && h.available ? h : undefined
  return (
    <section id="earnings" className="deeper" style={{ scrollMarginTop: 16 }}>
      <div className="deeper-head">
        <div>
          <h2>Go deeper</h2>
          <p className="small muted" style={{ marginTop: 2 }}>The evidence behind the answer, one topic at a time{live ? ` (${LABEL[live.key].toLowerCase()} view)` : ''}.</p>
        </div>
      </div>
      <div className="tabs deeper-tabs" role="tablist" aria-label="Go deeper">
        {tabs.map(([k, l]) => <button key={k} role="tab" aria-selected={tab === k} className={tab === k ? 'active' : ''} onClick={() => setTab(k)}>{l}</button>)}
      </div>
      <div className="stack" style={{ gap: 18, marginTop: 16 }}>
        {tab === 'earn' && report.earnings?.covered && (
          <>
            <EarningsBars report={report} />
            <EarningsPanel report={report} />
          </>
        )}
        {tab === 'sim' && live && <Panel title="Days like today" sub={`The ${live.analogs.k} days in this asset's history most like today across all its inputs, and what happened over the next ${live.bars === 1 ? 'day' : `${live.bars} trading days`}.`}><AnalogsTab h={live} /></Panel>}
        {tab === 'track' && <TrackTab report={report} h={live} a={a} />}
        {tab === 'drivers' && live && <Panel title="What's moving the model" sub="Each input's effect on the factor model, compared with an ordinary day for this asset. Click a row for the explanation."><DriversTab h={live} /></Panel>}
        {tab === 'plan' && live && <PlanTab report={report} h={live} />}
        {tab === 'tech' && <TechTab report={report} />}
        {tab === 'reasoning' && live && <Panel title="Full reasoning" sub="Every step the engine took, in its own words."><WhyTab h={live} /></Panel>}
        {!live && ['sim', 'drivers', 'plan', 'reasoning'].includes(tab) && <div className="callout info">Pick a horizon with a forecast to see this.</div>}
      </div>
    </section>
  )
}

function EarningsBars({ report }: { report: Report }) {
  const e = report.earnings!
  const recent = e.history.slice(0, 8).reverse()
  if (recent.length < 3) return null
  const max = Math.max(0.02, ...recent.map((x) => Math.abs(x.reaction)))
  const name = shortName(report.name, report.symbol)
  const falls = recent.filter((x) => x.reaction < 0).length
  return (
    <Panel title="How it moved after recent reports" sub={`Next-day moves after ${name}'s last ${recent.length} reports. Over ${e.reactions} reports the typical move was ${pct(e.avgAbsMove)} either way, against ${pct(e.normalMove)} on an ordinary day.${e.beatRate ? ` It beat forecasts ${pct(e.beatRate, 0)} of the time, and the price still fell after ${falls} of these ${recent.length}.` : ''}`}>
      <div className="earn-bars" role="img" aria-label={recent.map((x) => `${x.date}: ${spct(x.reaction)}`).join(', ')}>
        {recent.map((x) => {
          const up = x.reaction >= 0
          return (
            <div key={x.date} className="eb">
              <span className={`mono tiny ${up ? 'up' : 'down'}`}>{up ? '▲' : '▼'} {Math.abs(x.reaction * 100).toFixed(1)}%</span>
              <div className="eb-track">
                <div className="eb-axis" />
                <div className={`eb-bar ${up ? 'pos' : 'neg'}`} style={{ height: `${(Math.abs(x.reaction) / max) * 50}%` }} />
              </div>
              <span className="mono tiny faint">{new Date(x.date + 'T12:00:00Z').toLocaleDateString(undefined, { month: 'short', year: '2-digit' })}</span>
            </div>
          )
        })}
      </div>
    </Panel>
  )
}

function TrackTab({ report, h, a }: { report: Report; h?: HorizonForecast; a?: PlainAnswer }) {
  const rows = report.plain ?? []
  return (
    <>
      <Panel title="Track record" sub="Each test is a forecast made using only data available that day, then checked against what happened. “Guessing” means always predicting whichever way this asset usually goes.">
        <div className="table-wrap">
          <table>
            <thead><tr><th>Horizon</th><th className="r hide-phone">Tests</th><th className="r">Right</th><th className="r">Guessing</th><th>Verdict</th></tr></thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key} style={r.key === a?.key ? { background: 'var(--tint)' } : undefined}>
                  <td>{LABEL[r.key] ?? r.key}</td>
                  <td className="num r hide-phone">{r.tests.toLocaleString()}</td>
                  <td className="num r">{pct(r.right)}</td>
                  <td className="num r">{pct(r.guess)}</td>
                  <td className={r.tested === 'proven' ? 'up' : 'muted'}>{TESTED_MARK[r.tested]} {r.testLabel}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {report.ranks && report.ranks.length > 0 && (
          <p className="small muted" style={{ marginTop: 12 }}>
            Cross-asset rankings:{' '}
            {report.ranks.filter((r) => r.horizon === '1m' || r.horizon === '1w').map((r) => `#${r.item.rank} of ${r.item.of} in ${r.universe} (${r.horizon}${r.status === 'validated' ? ', tested' : ', not proven'})`).join(' · ')}
          </p>
        )}
      </Panel>
      {h && <Panel title={`The full test, ${LABEL[h.key].toLowerCase()} view`}><BacktestTab h={h} /></Panel>}
    </>
  )
}

function PlanTab({ report, h }: { report: Report; h: HorizonForecast }) {
  const tested = h.status === 'validated'
  return (
    <Panel title="If you hold it" sub="Deciding in advance where you'd sell is easier than deciding in the middle of a move.">
      <div className="stack" style={{ gap: 14 }}>
        <p style={{ maxWidth: 760, lineHeight: 1.6 }}>
          {h.risk.suggestedSize > 0
            ? <>The model suggests a position of up to <strong>{pct(h.risk.suggestedSize)}</strong> of trading capital for this horizon.</>
            : <><strong>No buy suggested{tested ? '' : ': there’s no tested signal'}.</strong> </>}{' '}
          If you already hold it, the lower edge of the range, about <strong className="mono">{money(h.priceTargets.p10, report.currency)}</strong> ({spct(h.returnRange.p10)}), is a reasonable place to decide in advance where you'd sell. Only 1 in 10 similar cases ended below it.
        </p>
        <RiskTab h={h} report={report} />
      </div>
    </Panel>
  )
}

function TechTab({ report }: { report: Report }) {
  return (
    <>
      <Panel title="Price detail" sub={`Daily candles for the last ${report.chart.dates.length} sessions, with 50- and 200-day averages.`}>
        <PriceChart report={report} />
      </Panel>
      <LevelsPanel report={report} />
      <Panel title="Indicator readings" sub="Textbook interpretation of each indicator. The model learns from this asset's own history whether these readings actually matter, so its view can differ.">
        <div className="table-wrap">
          <table>
            <thead><tr><th>Indicator</th><th>Reading</th><th>Textbook signal</th><th>What it means</th></tr></thead>
            <tbody>
              {report.indicators?.map((r) => (
                <tr key={r.name}>
                  <td>{r.name}<div className="tiny faint">{r.group}</div></td>
                  <td className="num small">{r.display}</td>
                  <td><span className={`badge ${r.signal === 'bullish' ? 'up' : r.signal === 'bearish' ? 'down' : 'none'}`}>{r.signal === 'bullish' ? '▲ ' : r.signal === 'bearish' ? '▼ ' : ''}{r.signal}</span></td>
                  <td className="small muted">{r.note}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>
      {report.options && report.options.length > 0 && <OptionsPanel report={report} />}
      <EvidencePanel report={report} />
      {report.class === 'equity' && <BusinessPanel symbol={report.symbol} />}
      <Panel title="Model inputs" sub={`${report.features.used?.length ?? 0} inputs used · benchmark ${report.benchmark} · data from ${report.provider}`}>
        <div>{report.features.used?.map((f) => <span key={f} className="chip">{f}</span>)}</div>
        {report.features.dropped && report.features.dropped.length > 0 && (
          <p className="small faint" style={{ marginTop: 8 }}>Skipped (insufficient history): {report.features.dropped.join(', ')}</p>
        )}
      </Panel>
    </>
  )
}

