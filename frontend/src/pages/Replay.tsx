import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, track, type HorizonForecast, type Report, type ReplaySeries } from '../api'
import { Loading } from '../components'
import { pct, price, spct, tone } from '../format'
import { HorizonCard, HorizonDetail } from './Asset'
import { verdict } from './Dashboard'

const iso = (d: Date) => d.toISOString().slice(0, 10)
const daysAgo = (n: number) => iso(new Date(Date.now() - n * 86400000))

const PRESETS: [string, string][] = [
  ['1 month ago', daysAgo(31)],
  ['3 months ago', daysAgo(92)],
  ['6 months ago', daysAgo(183)],
  ['1 year ago', daysAgo(366)],
  ['COVID crash low (16 Mar 2020)', '2020-03-16'],
  ['2022 bear-market low (12 Oct 2022)', '2022-10-12'],
  ['Start of 2024', '2024-01-02'],
]

export default function Replay() {
  useEffect(() => track('page', 'replay'), [])
  // ?symbol=SPY&date=2025-04-08 opens a replay directly (shareable links).
  const [params, setParams] = useSearchParams()
  const [symbol, setSymbol] = useState(params.get('symbol') ?? 'AAPL')
  const [date, setDate] = useState(params.get('date') ?? daysAgo(92))
  const [rep, setRep] = useState<Report | null>(null)
  const [sel, setSel] = useState('1m')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const [from, setFrom] = useState(daysAgo(730))
  const [to, setTo] = useState(daysAgo(35))
  const [every, setEvery] = useState(30)
  const [series, setSeries] = useState<ReplaySeries | null>(null)
  const [seriesBusy, setSeriesBusy] = useState(false)

  const run = async (d = date) => {
    setBusy(true)
    setErr('')
    setDate(d)
    setParams((p) => {
      p.set('symbol', symbol.trim().toUpperCase())
      p.set('date', d)
      return p
    }, { replace: true })
    try {
      setRep(await api.replay(symbol.trim().toUpperCase(), d))
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const runSeries = async () => {
    setSeriesBusy(true)
    setErr('')
    try {
      setSeries(await api.replaySeries(symbol.trim().toUpperCase(), from, to, every))
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setSeriesBusy(false)
    }
  }

  useEffect(() => {
    if (params.get('symbol') && params.get('date')) run(params.get('date')!)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const h = rep?.horizons.find((x) => x.key === sel && x.available) ?? rep?.horizons.find((x) => x.available)
  return (
    <div className="stack" style={{ gap: 20 }}>
      <header style={{ maxWidth: 760 }}>
        <h1 style={{ fontSize: 36 }}>Replay</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>
          Go back to any day and see what the app would have said then, and what actually happened. Every model is rebuilt using only what was known on that day. Replays never count towards the track record.
        </p>
      </header>

      <section className="panel pad-card stack" style={{ gap: 14 }}>
        <h2>Pick a day</h2>
        <div className="row" style={{ flexWrap: 'wrap', gap: 10 }}>
          <label className="field" style={{ width: 220 }}>Symbol<input value={symbol} onChange={(e) => setSymbol(e.target.value)} placeholder="NVDA, SPY, BTC-USD" /></label>
          <label className="field" style={{ width: 180 }}>Day<input type="date" value={date} max={daysAgo(1)} onChange={(e) => setDate(e.target.value)} /></label>
          <button className="primary" style={{ alignSelf: 'flex-end' }} disabled={busy || !symbol.trim()} onClick={() => run()}>{busy ? <><span className="spinner" /> Replaying…</> : 'Replay'}</button>
        </div>
        <div className="row" style={{ flexWrap: 'wrap', gap: 6 }}>
          <span className="small faint" style={{ marginRight: 4 }}>Or try</span>
          {PRESETS.map(([label, d]) => (
            <button key={label} className="pill-btn" disabled={busy} onClick={() => run(d)}>{label}</button>
          ))}
        </div>
      </section>
      {err && <div className="callout bad">{err}</div>}
      {busy && !rep && <Loading label="Rebuilding the analysis as of that day…" />}

      {rep && rep.replay && h && <ReplayResult rep={rep} h={h} setSel={setSel} />}

      <section className="panel pad-card stack" style={{ gap: 14 }}>
        <div>
          <h2>Replay many days</h2>
          <p className="small muted" style={{ marginTop: 4 }}>Runs the replay on a regular schedule (up to 40 days) and tallies how the calls turned out: the most direct test of whether the signals work on {symbol.trim().toUpperCase() || 'this asset'}.</p>
        </div>
        <div className="row" style={{ flexWrap: 'wrap', gap: 10 }}>
          <label className="field" style={{ width: 170 }}>From<input type="date" value={from} onChange={(e) => setFrom(e.target.value)} /></label>
          <label className="field" style={{ width: 170 }}>To<input type="date" value={to} onChange={(e) => setTo(e.target.value)} /></label>
          <label className="field" style={{ width: 120 }}>Every (days)<input type="number" min={1} value={every} onChange={(e) => setEvery(Number(e.target.value))} /></label>
          <button className="primary" style={{ alignSelf: 'flex-end' }} disabled={seriesBusy || !symbol.trim()} onClick={runSeries}>{seriesBusy ? <><span className="spinner" /> Running…</> : `Replay ${symbol.toUpperCase() || ''}`}</button>
        </div>
        {seriesBusy && <p className="small muted">Each day rebuilds every model from scratch, so this takes a few seconds per day.</p>}
        {series && <SeriesResults s={series} />}
      </section>
    </div>
  )
}

const LABEL: Record<string, string> = { '1d': 'Day', '1w': 'Week', '1m': 'Month', '1y': 'Year' }
const longDay = (d: string) => new Date(d + 'T12:00:00Z').toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })

/** What it said on the day, what happened, and the full analysis folded away. */
function ReplayResult({ rep, h, setSel }: { rep: Report; h: HorizonForecast; setSel: (k: string) => void }) {
  const [more, setMore] = useState(false)
  const a = rep.plain?.find((x) => x.key === h.key)
  const o = rep.replay?.outcomes[h.key]
  const happened = o
    ? `${o.complete ? 'By' : 'So far, to'} ${longDay(o.exitDate)} the price ${Math.abs(o.return) < 0.0005 ? 'was flat' : `${o.return > 0 ? 'rose' : 'fell'} ${Math.abs(o.return * 100).toFixed(1)}%`}, ${o.inRange ? 'inside' : 'outside'} the range it gave${o.correct === undefined ? '' : o.correct ? ', so its call was right' : ', so its call was wrong'}.`
    : ''
  return (
    <>
      <section className="panel answer">
        <div className="answer-top">
          <div style={{ maxWidth: 780 }}>
            <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>{rep.name.length > 24 ? rep.symbol : rep.name.replace(/ (Inc\.?|Corporation|Corp\.?|USD)$/, '')} on {longDay(rep.asOf)} · price then {price(rep.price)} {rep.currency}</p>
            <h2 className="answer-say">{a ? `It said: ${a.say.charAt(0).toLowerCase()}${a.say.slice(1)}` : `${h.label}: ${pct(h.pUp)} chance it would be higher.`}</h2>
            <p className="answer-more"><strong style={{ color: 'var(--text)' }}>What happened:</strong> {happened}</p>
          </div>
          <div className="seg" role="tablist" aria-label="How far ahead">
            {rep.horizons.map((x) => (
              <button key={x.key} role="tab" aria-selected={x.key === h.key} className={x.key === h.key ? 'on' : ''} disabled={!x.available} onClick={() => setSel(x.key)}>{LABEL[x.key] ?? x.label}</button>
            ))}
          </div>
        </div>
        <div className="replay-rows">
          {rep.horizons.map((x) => {
            const r = rep.replay?.outcomes[x.key]
            const v = verdict(x.signal, x.status)
            if (!x.available || !r) return <div key={x.key} className="replay-row off"><span className="hl">{LABEL[x.key]}</span><span className="small faint">{x.reason ?? 'Not available'}</span></div>
            return (
              <button key={x.key} className={`replay-row ${x.key === h.key ? 'on' : ''}`} onClick={() => setSel(x.key)}>
                <span className="hl">{LABEL[x.key]}</span>
                <span className="small"><span className={v.cls}>{v.mark} {v.text}</span> <span className="muted">· {pct(x.pUp, 0)} higher</span></span>
                <span className={`mono ${tone(r.return)}`}>{r.return > 0 ? '▲' : r.return < 0 ? '▼' : ''} {Math.abs(r.return * 100).toFixed(1)}%{!r.complete && <span className="tiny faint"> so far</span>}</span>
                <span className="small muted">{r.inRange ? 'inside its range' : 'outside its range'}</span>
                <span>{r.correct === undefined ? <span className="chip-sm flat">no call</span> : <span className={`chip-sm ${r.correct ? 'up' : 'down'}`}>{r.correct ? '✓ right' : '✗ wrong'}</span>}</span>
              </button>
            )
          })}
        </div>
      </section>

      <section className="panel pad-card">
        <h2>What happened next</h2>
        <p className="small muted" style={{ margin: '4px 0 12px' }}>Closing prices after {longDay(rep.asOf)}. The bars are each horizon’s 80% range from that day, with the middle as a dot. {rep.replay?.note}</p>
        <ReplayChart rep={rep} />
      </section>

      <section className="panel pad-card">
        <div className="row between" style={{ gap: 12 }}>
          <div><h2>The analysis as it looked then</h2><p className="small muted" style={{ marginTop: 4 }}>{rep.historyBars.toLocaleString()} days of history were available at the time.</p></div>
          <button className="linkish" onClick={() => setMore(!more)} aria-expanded={more}>{more ? 'Hide' : 'Show'}</button>
        </div>
        {more && (
          <div className="stack" style={{ marginTop: 14 }}>
            <div className="grid grid-4">
              {rep.horizons.map((x) => <HorizonCard key={x.key} h={x} selected={x.key === h.key} onClick={() => x.available && setSel(x.key)} currency={rep.currency} />)}
            </div>
            <HorizonDetail h={h} report={rep} />
          </div>
        )}
      </section>
    </>
  )
}

function ReplayChart({ rep }: { rep: Report }) {
  const path = rep.replay?.path ?? []
  if (path.length < 2) return <div className="empty">No prices after this date yet.</div>
  const W = 1100, H = 300, P = 56
  const hs = rep.horizons.filter((h) => h.available && h.bars < path.length + 1)
  const vals = path.map((p) => p.close)
  for (const h of hs) vals.push(h.priceTargets.p10, h.priceTargets.p90)
  const lo = Math.min(...vals), hi = Math.max(...vals)
  const x = (i: number) => P + (i / (path.length - 1)) * (W - P - 20)
  const y = (v: number) => H - 30 - ((v - lo) / (hi - lo || 1)) * (H - 50)
  const d = path.map((p, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(p.close).toFixed(1)}`).join('')
  const ticks = [lo, (lo + hi) / 2, hi]
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" style={{ maxHeight: 340 }}>
      {ticks.map((t) => (
        <g key={t}>
          <line x1={P} x2={W - 20} y1={y(t)} y2={y(t)} style={{ stroke: 'var(--border)' }} />
          <text x={P - 6} y={y(t) + 4} textAnchor="end">{price(t)}</text>
        </g>
      ))}
      <line x1={x(0)} x2={W - 20} y1={y(rep.price)} y2={y(rep.price)} style={{ stroke: 'var(--faint)' }} strokeDasharray="3 4" />
      <path d={d} fill="none" style={{ stroke: 'var(--text)' }} strokeWidth={1.6} />
      {hs.map((h) => {
        const i = Math.min(h.bars, path.length - 1)
        const xi = x(i)
        return (
          <g key={h.key}>
            <line x1={xi} x2={xi} y1={y(h.priceTargets.p10)} y2={y(h.priceTargets.p90)} style={{ stroke: 'var(--accent-ink)' }} strokeWidth={6} opacity={0.35} />
            <circle cx={xi} cy={y(h.priceTargets.p50)} r={4} style={{ fill: 'var(--accent-ink)' }} />
            <text x={xi} y={H - 8} textAnchor="middle">{h.key}</text>
          </g>
        )
      })}
      <text x={x(0)} y={16}>{path[0].date}</text>
      <text x={W - 20} y={16} textAnchor="end">{path[path.length - 1].date}</text>
    </svg>
  )
}

function SeriesResults({ s }: { s: ReplaySeries }) {
  return (
    <div className="stack" style={{ gap: 14 }}>
      <div className="table-wrap"><table>
        <thead>
          <tr>
            <th>Horizon</th><th className="r">Dates</th><th className="r">Probability side right</th><th className="r">Went up (bar to beat)</th>
            <th className="r">BUY/SELL calls</th><th className="r">Calls right</th><th className="r">Avg return per call</th><th className="r">Inside 80% range</th>
          </tr>
        </thead>
        <tbody>
          {(s.summary ?? []).map((r) => (
            <tr key={r.horizon}>
              <td>{LABEL[r.horizon] ?? r.horizon}</td>
              <td className="num r">{r.dates}</td>
              <td className={`num r ${r.directionHit > Math.max(r.upRate, 1 - r.upRate) ? 'up' : ''}`}>{pct(r.directionHit, 0)}</td>
              <td className="num r muted">{pct(r.upRate, 0)}</td>
              <td className="num r">{r.calls}</td>
              <td className="num r">{r.calls ? pct(r.callHit, 0) : '—'}</td>
              <td className={`num r ${tone(r.avgCallReturn)}`}>{r.calls ? spct(r.avgCallReturn) : '—'}</td>
              <td className="num r">{pct(r.inRange, 0)}</td>
            </tr>
          ))}
        </tbody>
      </table></div>
      <p className="tiny faint">
        Read it like this: "probability side right" must beat the "went up" column (always guessing up) to show skill. The
        80%-range column should sit near 80% if the ranges are honest. A few dozen dates is a small sample, so the walk-forward
        backtest on the asset page (thousands of dates) is the stronger evidence.
      </p>
      <details>
        <summary>Every replayed date ({(s.rows ?? []).length} rows)</summary>
        <div className="table-wrap" style={{ maxHeight: 420, overflowY: 'auto', marginTop: 8 }}>
          <table>
            <thead><tr><th>Date</th><th>Horizon</th><th>Signal</th><th className="r">P(up)</th><th className="r">Actual</th><th>Result</th></tr></thead>
            <tbody>
              {(s.rows ?? []).map((r) => (
                <tr key={r.date + r.horizon}>
                  <td className="mono small">{r.date}</td>
                  <td>{r.horizon}</td>
                  <td className="small">{(() => { const v = verdict(r.signal, r.status); return <span className={v.cls}>{v.mark} {v.text}</span> })()}</td>
                  <td className="num r">{pct(r.pUp)}</td>
                  <td className={`num r ${tone(r.return)}`}>{spct(r.return)}{!r.complete && <span className="faint"> (so far)</span>}</td>
                  <td>{r.correct === undefined ? <span className="faint small">no call</span> : <span className={`chip-sm ${r.correct ? 'up' : 'down'}`}>{r.correct ? '✓ right' : '✗ wrong'}</span>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
      {s.failed && s.failed.length > 0 && <p className="small faint">Could not replay: {s.failed.join(', ')}</p>}
    </div>
  )
}
