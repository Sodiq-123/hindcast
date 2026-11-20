// Practice rounds: decide on a real past day with the name and date hidden,
// then see what happened straight away. Backend: internal/learn/practice.go.
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type PracticeRound, type PracticeState } from '../api'
import { Loading } from '../components'
import { price } from '../format'

const pc = (x: number) => `${Math.round(x * 100)}%`
const usdPrice = (x: number) => '$' + price(x)
const usd = (x: number) => `${x < 0 ? '−' : '+'}$${Math.abs(x).toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
const longDate = (d?: string) => (d ? new Date(`${d}T12:00:00Z`).toLocaleDateString(undefined, { day: 'numeric', month: 'long', year: 'numeric' }) : '')

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null)
  const [w, setW] = useState(0)
  useEffect(() => {
    const el = ref.current
    if (!el) return
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    setW(el.clientWidth)
    return () => ro.disconnect()
  }, [])
  return [ref, w] as const
}

// RoundChart draws the six months before the day (prices ÷ the day's close,
// so the price level gives nothing away), the plan's stop and target, and,
// once decided, the month after.
function RoundChart({ r, stop, target }: { r: PracticeRound; stop: number; target: number }) {
  const [ref, w] = useWidth<HTMLDivElement>()
  const h = w < 520 ? 200 : 260
  const after = r.decided ? r.after ?? [] : []
  const all = [...r.before, ...after, 1 - stop, 1 + target]
  const lo = Math.min(...all) * 0.97
  const hi = Math.max(...all) * 1.03
  const n = r.before.length + r.hold
  const X = (i: number) => 8 + (i / (n - 1)) * (w - 16)
  const Y = (v: number) => 8 + ((hi - v) / (hi - lo)) * (h - 16)
  const i0 = r.before.length - 1
  const line = (vs: number[], from: number) => vs.map((v, k) => `${X(from + k).toFixed(1)},${Y(v).toFixed(1)}`).join(' ')
  const exit = r.decided && r.exitBar ? r.exitBar : 0
  const exitY = r.result === 'target' ? 1 + target : r.result === 'stop' ? 1 - stop : after[exit - 1]
  const label = r.decided
    ? `Six months of prices up to the day, then the month after. ${r.result === 'target' ? 'The target was reached' : r.result === 'stop' ? 'The stop was reached' : 'Neither the stop nor the target was reached'}${exit ? ` on day ${exit}` : ''}.`
    : 'Six months of daily prices up to the day. What came after is hidden.'
  return (
    <div ref={ref} className="pr-chart">
      {w > 0 && (
        <svg viewBox={`0 0 ${w} ${h}`} width="100%" height={h} role="img" aria-label={label}>
          <rect x={X(i0)} y={0} width={w - X(i0)} height={h} className="pr-hidden" />
          {[[1 + target, 'up', `Target +${pc(target)}`], [1 - stop, 'down', `Stop −${pc(stop)}`]].map(([v, c, t]) => (
            <g key={c as string}>
              <line x1={X(i0)} x2={w - 4} y1={Y(v as number)} y2={Y(v as number)} className={`pr-level ${c}`} />
              <text x={w - 8} y={Y(v as number) - 6} textAnchor="end" className={`pr-label ${c}`}>{t}</text>
            </g>
          ))}
          <polyline points={line(r.before, 0)} className="pr-line" />
          {after.length > 0 && (
            <>
              <polyline points={line([1, ...after.slice(0, exit || after.length)], i0)} className="pr-after" />
              {exit > 0 && exit < after.length && <polyline points={line(after.slice(exit - 1), i0 + exit)} className="pr-after-rest" />}
              {exit > 0 && <circle cx={X(i0 + exit)} cy={Y(exitY)} r={5} className={`pr-dot ${r.result === 'target' ? 'up' : r.result === 'stop' ? 'down' : 'flat'}`} />}
            </>
          )}
          <circle cx={X(i0)} cy={Y(1)} r={5} className="pr-entry" />
          <text x={X(i0) - 8} y={h - 8} textAnchor="end" className="pr-axis">THE DAY</text>
          <text x={w - 6} y={h - 8} textAnchor="end" className="pr-axis">{r.decided ? 'AFTER' : 'HIDDEN'}</text>
        </svg>
      )}
    </div>
  )
}

function Choice<T extends string | number>({ label, options, value, onPick, show }: { label: string; options: T[]; value: T; onPick: (v: T) => void; show: (v: T) => string }) {
  return (
    <div>
      <p className="tag faint pr-choice-label" id={`pr-${label}`}>{label}</p>
      <div className="row pr-choices" role="radiogroup" aria-labelledby={`pr-${label}`}>
        {options.map((o) => (
          <button key={String(o)} role="radio" aria-checked={o === value} className={o === value ? 'pr-chip on' : 'pr-chip'} onClick={() => onPick(o)}>{show(o)}</button>
        ))}
      </div>
    </div>
  )
}

function AppBox({ r }: { r: PracticeRound }) {
  return (
    <div className="pr-note">
      {r.app ? (
        <span><strong>What the app said that day:</strong> {r.app.text}</span>
      ) : (
        <span className="muted"><span className="pr-spin" aria-hidden /> Working out what the app said that day. It reruns the whole analysis on only what was known then, which takes a few seconds.</span>
      )}
    </div>
  )
}

function Decide({ r, s, onDone }: { r: PracticeRound; s: PracticeState; onDone: (r: PracticeRound) => void }) {
  const [call, setCall] = useState<'buy' | 'skip'>('buy')
  const [stop, setStop] = useState(0.08)
  const [target, setTarget] = useState(0.15)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const lock = async () => {
    setBusy(true)
    setErr('')
    try {
      onDone(await api.decideRound(r.id, call, stop, target))
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }
  return (
    <section className="panel pr-card">
      <div className="pr-top"><span className="tag pr-step">Round {r.n} of {r.of}</span><span className="small muted">{r.hint} · a real day in the past</span></div>
      <h2 className="pr-h">Would you buy at this day’s close, and on what plan?</h2>
      <p className="small muted pr-lede">The name and date are hidden, so you decide on what you’d have known that day. You hold for up to a month.</p>
      <RoundChart r={r} stop={stop} target={target} />
      <AppBox r={r} />
      <div className="pr-plan">
        <Choice label="Your call" options={['buy', 'skip'] as ('buy' | 'skip')[]} value={call} onPick={setCall} show={(v) => (v === 'buy' ? 'Buy' : 'Skip')} />
        <Choice label="Stop: sell if it falls" options={s.stops} value={stop} onPick={setStop} show={pc} />
        <Choice label="Target: sell if it rises" options={s.targets} value={target} onPick={setTarget} show={pc} />
      </div>
      {call === 'skip' && <p className="small muted" style={{ margin: '12px 0 0' }}>With Skip, the plan shows what would have happened had you bought.</p>}
      {err && <div className="callout bad" style={{ marginTop: 12 }}>{err}</div>}
      <div className="row pr-actions">
        <button className="primary" disabled={busy} onClick={lock}>{busy ? 'Locking in…' : 'Lock it in'}</button>
        <span className="small faint">Then see what happened.</span>
      </div>
    </section>
  )
}

function Answer({ r, onNext, last }: { r: PracticeRound; onNext: () => void; last: boolean }) {
  const e = r.entry ?? 0
  const up = (r.pnl ?? 0) > 0
  return (
    <section className="panel pr-card">
      <div className="pr-top">
        <span className="tag pr-step">Round {r.n} of {r.of} · the answer</span>
        <span className="small muted">It was <strong style={{ color: 'var(--text)' }}>{r.name}{r.name !== r.symbol ? ` (${r.symbol})` : ''}</strong>, {longDate(r.day)}</span>
      </div>
      <h2 className="pr-h"><span className={up ? 'up' : 'down'} aria-hidden>{up ? '▲' : '▼'}</span> {r.headline}</h2>
      <RoundChart r={r} stop={r.stopPct ?? 0.08} target={r.targetPct ?? 0.15} />
      <div className="pr-two">
        <div className="pr-note"><strong>{r.call === 'buy' ? 'Your plan' : 'The plan'}:</strong> buy at {usdPrice(e)}, stop {usdPrice(e * (1 - (r.stopPct ?? 0)))}, target {usdPrice(e * (1 + (r.targetPct ?? 0)))}.{r.result === 'stop' && r.exitPrice && r.exitPrice < e * (1 - (r.stopPct ?? 0)) - 1e-9 ? ` It opened below the stop, so it sold at ${usdPrice(r.exitPrice)}.` : ''}</div>
        <div className="pr-note">{r.appLine ? <><strong>The app that day:</strong> {r.appLine}</> : <AppBox r={r} />}</div>
      </div>
      <div className="row pr-actions">
        <button className="primary" onClick={onNext} autoFocus>{last ? 'See the summary' : 'Next round'}</button>
      </div>
    </section>
  )
}

function Summary({ s, onMore, busy }: { s: PracticeState; onMore: () => void; busy: boolean }) {
  const m = s.summary!
  const rows: [string, string][] = [
    ['Bought', `${m.bought} of ${m.rounds} days`],
    ['Hit the target', String(m.target)],
    ['Hit the stop', String(m.stop)],
    ['Ran out of time', String(m.time)],
    ['Total, after fees', m.bought ? `${usd(m.pnl)} on paper` : '—'],
  ]
  return (
    <section className="panel pr-card">
      <span className="tag pr-step">{m.rounds} rounds done</span>
      <h2 className="pr-h">{m.headline}</h2>
      <div className="pr-sum">
        <div>
          {rows.map(([a, b]) => <div key={a} className="pr-row"><span>{a}</span><span className="mono">{b}</span></div>)}
          {m.lines.map((l) => <p key={l} className="small muted" style={{ margin: '10px 0 0', lineHeight: 1.55 }}>{l}</p>)}
        </div>
        {m.worth ? (
          <div className="worth" style={{ alignSelf: 'start' }}>
            <p className="tag worth-tag">Worth a look</p>
            <p style={{ margin: '6px 0 0', fontSize: 14.5, lineHeight: 1.55 }}><strong>{m.worth.title}</strong> {m.worth.detail}</p>
            <span className="worth-count">{m.worth.count}</span>
          </div>
        ) : (
          <p className="small muted" style={{ margin: 0, lineHeight: 1.55 }}>Nothing stood out in these rounds. Patterns are checked again as rounds add up.</p>
        )}
      </div>
      <p className="small muted" style={{ margin: '16px 0 0', lineHeight: 1.55 }}>
        Practice rounds are kept apart from your paper trades. Your buys count towards your <Link to="/about-you#learned">personal model</Link> as practice, and it always says how many came from practice.
      </p>
      <div className="row pr-actions">
        <button className="primary" disabled={busy} onClick={onMore}>{busy ? 'Picking 10 days…' : '10 more rounds'}</button>
        <Link className="btn" to="/journal">Back to the journal</Link>
      </div>
    </section>
  )
}

export default function Practice() {
  const [s, setS] = useState<PracticeState | null>(null)
  const [shown, setShown] = useState<PracticeRound | null>(null) // the answer just revealed
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const load = useCallback(() => api.practice().then(setS).catch((e) => setErr((e as Error).message)), [])
  useEffect(() => { load() }, [load])

  // While the app's view of the day is still being worked out, check back.
  const waiting = (!!s?.current && s.current.working) || (!!shown && !shown.appLine)
  useEffect(() => {
    if (!waiting) return
    const t = window.setInterval(async () => {
      try {
        const next = await api.practice()
        setS(next)
        if (shown) {
          const r = next.rounds.find((x) => x.id === shown.id)
          if (r?.appLine) setShown(r)
        }
      } catch { /* try again next time */ }
    }, 5000)
    return () => window.clearInterval(t)
  }, [waiting, shown])

  const start = async () => {
    setBusy(true)
    setErr('')
    try {
      setS(await api.startPractice())
      setShown(null)
      window.scrollTo({ top: 0 })
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }
  const next = async () => {
    setShown(null)
    await load()
    window.scrollTo({ top: 0 })
  }

  if (err && !s) return <div className="callout bad">{err}</div>
  if (!s) return <Loading label="Loading practice rounds…" />
  return (
    <div className="stack" style={{ gap: 20, maxWidth: 980 }}>
      <header>
        <p className="small" style={{ margin: '0 0 6px' }}><Link to="/journal">← Journal</Link></p>
        <h1 style={{ fontSize: 36 }}>Practice rounds</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>Hindcast yourself: decide on real past days, then see what happened. Ten rounds take about ten minutes.</p>
      </header>
      {err && <div className="callout bad">{err}</div>}
      {shown ? (
        <Answer r={shown} onNext={next} last={shown.n >= shown.of} />
      ) : s.current ? (
        <Decide key={s.current.id} r={s.current} s={s} onDone={(r) => { setShown(r); load(); window.scrollTo({ top: 0 }) }} />
      ) : s.finished ? (
        <Summary s={s} onMore={start} busy={busy} />
      ) : (
        <section className="panel pr-card">
          <h2 className="pr-h" style={{ marginTop: 0 }}>How it works</h2>
          <ol className="pr-how">
            <li>You see six months of real prices up to a day in the past. The name and date are hidden.</li>
            <li>You decide: buy at that day’s close, or skip. You pick a stop and a target.</li>
            <li>You see the month after straight away, what your plan would have made after fees, and what the app said that day.</li>
          </ol>
          <p className="small muted" style={{ margin: '12px 0 0', lineHeight: 1.55 }}>Days come from well-known stocks, funds and coins, and your watchlist. Your buys count towards your personal model as practice, so it can learn your habits sooner than paper trades alone.</p>
          <div className="row pr-actions">
            <button className="primary" disabled={busy} onClick={start}>{busy ? 'Picking 10 days…' : 'Start 10 rounds'}</button>
          </div>
        </section>
      )}
      {s.done > 0 && <p className="small faint">{s.done} {s.done === 1 ? 'round' : 'rounds'} so far, {s.bought} of them buys.</p>}
    </div>
  )
}
