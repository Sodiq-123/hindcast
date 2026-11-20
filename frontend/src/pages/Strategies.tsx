import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { useIsAdmin } from '../auth'
import { api, type StrategyResults } from '../api'
import { EquityChart } from '../charts'
import { Stat } from '../components'
import { ago, pct, spct, tone } from '../format'

// [mark, class, label] for each test outcome.
const STATUS: Record<string, [string, string, string]> = {
  validated: ['✓', 'up', 'Tested edge'],
  weak: ['◐', 'hold', 'Weak evidence'],
  no_edge: ['○', 'faint', 'Not proven'],
  special: ['✓', 'up', '0.618 is special'],
  not_special: ['○', 'faint', 'Not special'],
  worse: ['▼', 'down', '0.618 is worse'],
}
export const strategyStatus = (s?: string) => STATUS[s ?? ''] ?? STATUS.no_edge

/** Two popular approaches, tested across the S&P 500: a summary line each, the evidence on demand. */
export default function Strategies({ data }: { data?: StrategyResults | null }) {
  const [own, setOwn] = useState<StrategyResults | null>(null)
  const d = data ?? own
  const ref = useRef<HTMLElement>(null)
  const isAdmin = useIsAdmin()
  const { hash } = useLocation()
  const [open, setOpen] = useState<'' | 'value' | 'levels'>('')
  useEffect(() => {
    if (d && hash === '#strategies') ref.current?.scrollIntoView({ block: 'start' })
  }, [d, hash])
  const load = useCallback(() => api.strategies().then(setOwn).catch(() => {}), [])
  useEffect(() => {
    if (data === undefined) load()
  }, [load, data])
  const running = d?.progress.running
  useEffect(() => {
    if (!running) return
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [running, load])
  const r = d?.results
  const v = r?.value
  const l = r?.levels
  const vs = strategyStatus(v?.status)
  const ls = strategyStatus(l?.status)
  const fib = l?.levels.find((x) => x.ratio === 0.618)
  return (
    <section id="strategies" ref={ref} className="panel" style={{ overflow: 'hidden', scrollMarginTop: 70 }}>
      <div className="panel-head" style={{ alignItems: 'flex-start' }}>
        <div>
          <h2>Strategy tests</h2>
          <p className="sub">Two popular ideas, tested on every stock that was in the S&amp;P 500 since 2012, using only what was known at the time.{r && <> Last run {ago(r.at)}.</>}</p>
        </div>
        {isAdmin && (
          <button className="small" onClick={() => api.runStrategies().then(load).catch((e) => alert((e as Error).message))} disabled={running}>
            {running ? <><span className="spinner" /> {d?.progress.stage} ({d?.progress.done}/{d?.progress.total})</> : r ? 'Re-run' : 'Run tests (10–20 min)'}
          </button>
        )}
      </div>
      {d?.progress.lastError && <div className="panel-body"><div className="callout bad">Last run failed: {d.progress.lastError}</div></div>}
      {!r && !running && <div className="empty">Not run yet. The first run downloads about 500 companies’ SEC filings.</div>}

      {v && (
        <div className="st-row">
          <div>
            <p><span className={vs[1]} title={vs[2]} style={{ marginRight: 8 }}>{vs[0]}</span><strong className="display">Stronger business, lower price</strong></p>
            <p className="small muted st-sum">
              {v.statusNote.replace(/\.$/, '')} ({v.groups.signal?.n.toLocaleString() ?? 0} cases; t {v.portfolio.tStat.toFixed(2)}).{r?.screen && ` ${r.screen.length} stocks match today.`}
            </p>
          </div>
          <button className="linkish" style={{ fontSize: 13 }} onClick={() => setOpen(open === 'value' ? '' : 'value')} aria-expanded={open === 'value'}>{open === 'value' ? 'Hide' : 'Details'}</button>
        </div>
      )}
      {v && open === 'value' && (
        <div className="panel-body stack st-detail">
          <p className="small muted">Buy when the stock is 20%+ below its 3-year high while 12-month revenue (+5%) and operating profit (+10%) are higher than they were at that high, the company is profitable and debt hasn’t ballooned. {v.from} to {v.to}, {v.stocks} stocks.</p>
          <div className="table-wrap">
            <table>
              <thead><tr><th>Group</th><th className="r">Cases</th><th className="r">vs average stock, 6 months</th><th className="r">vs average stock, 12 months</th><th className="r">Beat the average (12m)</th><th className="r">Fell another 20%+ (12m)</th></tr></thead>
              <tbody>
                {([['signal', 'Pattern (business stronger, price 20%+ lower)'], ['dip_only', 'Price 20%+ lower, business not stronger'], ['all', 'All stocks']] as const).map(([k, label]) => {
                  const g = v.groups[k]
                  return g ? (
                    <tr key={k}>
                      <td>{label}</td>
                      <td className="num r">{g.n.toLocaleString()}</td>
                      <td className={`num r ${tone(g.excess6m)}`}>{spct(g.excess6m)}</td>
                      <td className={`num r ${tone(g.excess12m)}`}>{spct(g.excess12m)}</td>
                      <td className="num r">{pct(g.hit12m, 0)}</td>
                      <td className="num r">{pct(g.trap12m, 0)}</td>
                    </tr>
                  ) : null
                })}
              </tbody>
            </table>
          </div>
          <div className="grid grid-4">
            <Stat label="Pattern portfolio, a year" value={spct(v.portfolio.cagr)} tone={v.portfolio.cagr > v.portfolio.benchCagr ? 'up' : 'down'} hint={`average stock ${spct(v.portfolio.benchCagr)} · rebalanced monthly`} />
            <Stat label="Monthly edge" value={spct(v.portfolio.meanExcess, 2)} hint={`t-stat ${v.portfolio.tStat.toFixed(2)} over ${v.portfolio.months} months (2+ = significant)`} />
            <Stat label="Worst fall" value={pct(v.portfolio.maxDrawdown, 0)} hint={`average stock ${pct(v.portfolio.benchMaxDrawdown, 0)}`} />
            <Stat label="Just buying 20% dips, a year" value={spct(v.dipOnly.cagr)} hint={`t-stat ${v.dipOnly.tStat.toFixed(2)}`} />
          </div>
          {v.portfolio.equity && v.portfolio.bench && v.portfolio.dates && <EquityChart equity={v.portfolio.equity} buyHold={v.portfolio.bench} dates={v.portfolio.dates} />}
          {v.notes?.map((n) => <p key={n} className="tiny faint">{n}</p>)}
          {r?.screen && r.screen.length > 0 && (
            <>
              <h3>Matching the pattern today ({r.screen.length} of {r.evaluated} S&amp;P 500 stocks)</h3>
              <p className="small muted">Candidates only: the test above says how much to trust the pattern. Research each one before acting.</p>
              <div className="table-wrap">
                <table>
                  <thead><tr><th>Stock</th><th className="r">From 3-year high</th><th className="r">Revenue since</th><th className="r">Op. profit since</th><th className="r">Free cash flow since</th><th className="r">P/E then → now</th></tr></thead>
                  <tbody>
                    {r.screen.map((s) => (
                      <tr key={s.symbol}>
                        <td><Link to={`/asset/${encodeURIComponent(s.symbol)}`}><strong>{s.symbol}</strong></Link> <span className="small muted">{s.name}</span></td>
                        <td className="num r down">{spct(s.cmp.fromPeak)}</td>
                        <td className="num r up">{spct(s.cmp.revenueChange)}</td>
                        <td className="num r up">{spct(s.cmp.opIncomeChange)}</td>
                        <td className={`num r ${tone(s.cmp.fcfChange ?? 0)}`}>{spct(s.cmp.fcfChange)}</td>
                        <td className="num r">{s.cmp.peThen != null && s.cmp.peNow != null ? `${s.cmp.peThen.toFixed(0)} → ${s.cmp.peNow.toFixed(0)}` : '—'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </>
          )}
        </div>
      )}

      {l && (
        <div className="st-row">
          <div>
            <p><span className={ls[1]} title={ls[2]} style={{ marginRight: 8 }}>{ls[0]}</span><strong className="display">The 0.618 Fibonacci level</strong></p>
            <p className="small muted st-sum">
              {fib ? `Bounced ${pct(fib.bounceRate, 1)} of the time over ${fib.touches.toLocaleString()} touches. ` : ''}{l.note}
            </p>
          </div>
          <button className="linkish" style={{ fontSize: 13 }} onClick={() => setOpen(open === 'levels' ? '' : 'levels')} aria-expanded={open === 'levels'}>{open === 'levels' ? 'Hide' : 'Details'}</button>
        </div>
      )}
      {l && open === 'levels' && (
        <div className="panel-body stack st-detail">
          <p className="small muted">Every first weekly close into each retracement level of a major up-leg, {l.stocks} stocks, {l.from.slice(0, 4)}–{l.to.slice(0, 4)}. “Bounce” = back up 0.236 of the leg before falling 0.168 further. Break-even bounce rate {pct(l.breakEven, 0)}. z = {l.z.toFixed(2)} (|z| ≥ 2 is significant).</p>
          <div className="table-wrap">
            <table>
              <thead><tr><th>Level</th><th className="r">Touches</th><th className="r">Bounced</th><th className="r">Average result</th><th className="r">13 weeks later</th><th className="r">26 weeks later</th></tr></thead>
              <tbody>
                {l.levels.map((s) => (
                  <tr key={s.ratio} style={s.ratio === 0.618 ? { background: 'var(--panel-2)' } : undefined}>
                    <td>{s.ratio.toFixed(3)} {s.fib ? <span className="chip-sm flat">Fibonacci</span> : <span className="tiny faint">not Fibonacci</span>}</td>
                    <td className="num r">{s.touches.toLocaleString()}</td>
                    <td className={`num r ${s.bounceRate >= l.breakEven ? 'up' : 'down'}`}>{pct(s.bounceRate, 1)}</td>
                    <td className={`num r ${tone(s.expectancy)}`}>{s.expectancy >= 0 ? '+' : ''}{s.expectancy.toFixed(2)}R</td>
                    <td className={`num r ${tone(s.avg13w)}`}>{spct(s.avg13w)}</td>
                    <td className={`num r ${tone(s.avg26w)}`}>{spct(s.avg26w)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="tiny faint">“R” is the result in units of the risk to the stop: +0.10R means you’d make 10% of what you risked per trade on average, before costs.</p>
        </div>
      )}
    </section>
  )
}
