import { useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type BusinessVsPrice, type LevelsView, type Report } from '../api'
import { BusinessChart, LevelsChart } from '../charts'
import { Loading, Panel, Stat } from '../components'
import { pct, price, spct, tone } from '../format'

const big = (x: number | null | undefined) =>
  x == null ? '—' : Math.abs(x) >= 1e9 ? `$${(x / 1e9).toFixed(1)}B` : Math.abs(x) >= 1e6 ? `$${(x / 1e6).toFixed(0)}M` : `$${x.toFixed(0)}`
const STATUS: Record<string, [string, string]> = {
  validated: ['up', 'Validated in testing'],
  weak: ['hold', 'Weak evidence'],
  no_edge: ['none', 'No proven edge'],
}

/** "Business vs price": is the company stronger now than when the stock was higher? */
export function BusinessPanel({ symbol }: { symbol: string }) {
  const [d, setD] = useState<BusinessVsPrice | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    setD(null)
    setErr('')
    api.businessVsPrice(symbol).then(setD).catch((e) => setErr((e as Error).message))
  }, [symbol])
  if (err) return null // non-US stocks, funds, crypto: no SEC filings
  if (!d) return <Panel title="Business vs price"><Loading label="Reading SEC filings…" /></Panel>
  const c = d.comparison
  const qs = d.quarters
  const bt = d.backtest
  return (
    <Panel
      title="Business vs price"
      sub="Is the company stronger now than when the stock was at its recent high? Figures are trailing twelve months from SEC filings, counted from the day they were filed."
    >
      <div className="stack">
        {c && (
          <>
            <div className={`callout ${c.signal ? 'good' : 'info'}`}>
              <strong>{c.signal ? '✓ Stronger business, lower price.' : 'Pattern not present.'}</strong>{' '}
              The stock is {spct(c.fromPeak)} from its {new Date(c.peakDate).toLocaleDateString(undefined, { month: 'short', year: 'numeric' })} high of {price(c.peakPrice)}.
              Since then, 12-month revenue is {spct(c.revenueChange)}, operating profit {spct(c.opIncomeChange)}, net profit {spct(c.netIncomeChange)} and free cash flow {spct(c.fcfChange)}.
              {c.peThen != null && c.peNow != null && <> Price-to-earnings went from about {c.peThen.toFixed(0)} to {c.peNow.toFixed(0)}.</>}
            </div>
            <div className="grid grid-4">
              <Stat label="Below the 3-year high" value={spct(c.fromPeak)} tone={tone(c.fromPeak)} hint={`${c.peakDate} at ${price(c.peakPrice)}`} />
              <Stat label="Revenue since then" value={spct(c.revenueChange)} tone={tone(c.revenueChange ?? 0)} hint={`TTM to ${c.thenQuarter} vs ${c.nowQuarter}`} />
              <Stat label="Operating profit since then" value={spct(c.opIncomeChange)} tone={tone(c.opIncomeChange ?? 0)} hint={`net profit ${spct(c.netIncomeChange)}`} />
              <Stat label="P/E then → now" value={c.peThen != null && c.peNow != null ? `${c.peThen.toFixed(0)} → ${c.peNow.toFixed(0)}` : '—'} hint="using today's share count for both" />
            </div>
            <table>
              <tbody>
                {c.checks.map((ch) => (
                  <tr key={ch.name}>
                    <td style={{ width: 24 }}>{ch.pass ? <span className="up">✓</span> : <span className="down">✗</span>}</td>
                    <td>{ch.name}</td>
                    <td className="small muted">{ch.detail}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
        <BusinessChart quarters={qs} />
        <details>
          <summary className="small muted">Quarterly figures (trailing twelve months)</summary>
          <div className="table-wrap">
            <table>
              <thead>
                <tr><th>Quarter</th><th>Filed</th><th className="r">Revenue</th><th className="r">Op. profit</th><th className="r">Net profit</th><th className="r">Free cash flow</th><th className="r">Debt</th><th className="r">Cash</th><th className="r">Price then</th><th className="r">P/E</th></tr>
              </thead>
              <tbody>
                {[...qs].reverse().map((q) => (
                  <tr key={q.end}>
                    <td className="small">{q.end}</td>
                    <td className="small faint">{q.known}</td>
                    <td className="num r">{big(q.revenue)}</td>
                    <td className="num r">{big(q.opIncome)}</td>
                    <td className={`num r ${tone(q.netIncome ?? 0)}`}>{big(q.netIncome)}</td>
                    <td className="num r">{big(q.fcf)}</td>
                    <td className="num r faint">{big(q.debt)}</td>
                    <td className="num r faint">{big(q.cash)}</td>
                    <td className="num r">{price(q.price)}</td>
                    <td className="num r">{q.pe != null ? q.pe.toFixed(1) : '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </details>
        <p className="small muted">
          <strong>Does this pattern work?</strong>{' '}
          {bt ? (
            <>
              <span className={`badge ${STATUS[bt.status]?.[0] ?? 'none'}`}>{STATUS[bt.status]?.[1] ?? bt.status}</span> Across the S&P 500 from {bt.from.slice(0, 4)} to {bt.to.slice(0, 4)}, stocks
              matching it returned {spct(bt.groups.signal?.excess12m)} over the next 12 months versus the average stock ({bt.groups.signal?.n.toLocaleString()} cases), and{' '}
              {pct(bt.groups.signal?.trap12m, 0)} of them fell a further 20% or more. Stocks that simply fell 20% (without the business improving) returned {spct(bt.groups.dip_only?.excess12m)}.{' '}
              <Link to="/scanner#strategies">Full test results →</Link>
            </>
          ) : (
            <>Not tested yet. Run the strategy tests under <Link to="/scanner#strategies">Opportunities → Strategy tests</Link>.</>
          )}
        </p>
      </div>
    </Panel>
  )
}

/** Weekly Fibonacci levels, support zones, the entry/stop plan, and whether these levels have worked. */
export function LevelsPanel({ report }: { report: Report }) {
  const [d, setD] = useState<LevelsView | null>(null)
  useEffect(() => {
    setD(null)
    api.chartLevels(report.symbol).then(setD).catch(() => setD(null))
  }, [report.symbol])
  const v = d?.view
  const lines = useMemo(() => {
    if (!v) return []
    const out: { price: number; color: string; title: string; dashed?: boolean }[] = (v.levels ?? []).map((l) => ({
      price: l.price, color: l.ratio === 0.618 ? '--hold' : '--faint', title: `${l.ratio}`, dashed: l.ratio !== 0.618,
    }))
    if (v.plan) out.push({ price: v.plan.stop, color: '--down', title: 'stop' })
    return out
  }, [v])
  if (!d || !v) return null
  const t = d.test
  const f618 = t?.levels.find((l) => l.ratio === 0.618)
  const hist = v.history ?? []
  return (
    <Panel title="Chart levels (weekly)" sub="Fibonacci retracements of the last major up-leg (+30% or more within three years), support zones from weekly lows, and the buy-zone / stop plan, with a test of whether these levels have worked.">
      <div className="stack">
        {!v.swing ? (
          <p className="small muted">{v.note}</p>
        ) : (
          <>
            <div className="grid grid-4">
              <Stat label="Up-leg" value={`${price(v.swing.low)} → ${price(v.swing.high)}`} hint={`${v.swing.lowDate} → ${v.swing.highDate}`} />
              <Stat label="Retraced so far" value={pct(v.retracement, 0)} hint="0% = at the high, 100% = back at the low" />
              {v.plan && <Stat label="0.618 buy zone" value={`${price(v.plan.zoneLow)}–${price(v.plan.zoneHigh)}`} hint={v.plan.state === 'above' ? 'price is above the zone' : v.plan.state === 'in_zone' ? 'price is IN the zone' : 'price is below the zone'} tone={v.plan.state === 'in_zone' ? 'up' : ''} />}
              {v.plan && <Stat label="Stop (next support below)" value={price(v.plan.stop)} tone="down" hint={`${spct(v.plan.riskFromMid)} from the middle of the zone`} />}
            </div>
            {v.plan && <p className="small faint">Stop: {v.plan.stopReason}.</p>}
            <LevelsChart report={report} lines={lines} />
          </>
        )}
        <div className="callout info">
          <strong>Do these levels work?</strong>{' '}
          {t && f618 ? (
            <>
              Across {t.stocks} stocks ({t.from.slice(0, 4)}–{t.to.slice(0, 4)}), price bounced from the 0.618 level {pct(f618.bounceRate, 0)} of the time ({f618.touches.toLocaleString()} touches)
              versus {pct(t.neighbours.bounceRate, 0)} at the nearby non-Fibonacci levels 0.55 and 0.68 (break-even {pct(t.breakEven, 0)}). {t.note}
            </>
          ) : (
            <>Not tested yet: run the strategy tests under <Link to="/scanner#strategies">Opportunities → Strategy tests</Link>.</>
          )}{' '}
          For this stock: {v.summary.touches} past 0.618 touch{v.summary.touches === 1 ? '' : 'es'}, {v.summary.bounces} bounced, {v.summary.stopped} broke down.
        </div>
        {hist.length > 0 && (
          <details>
            <summary className="small muted">This stock's past 0.618 touches</summary>
            <table>
              <thead><tr><th>Week</th><th className="r">Entry</th><th className="r">Target (0.382)</th><th className="r">Stop (0.786)</th><th>What happened</th><th className="r">13 weeks later</th></tr></thead>
              <tbody>
                {hist.map((e) => (
                  <tr key={e.date}>
                    <td className="small">{e.date}</td>
                    <td className="num r">{price(e.entry)}</td>
                    <td className="num r">{price(e.target)}</td>
                    <td className="num r">{price(e.stop)}</td>
                    <td className={`small ${e.outcome === 'bounce' ? 'up' : e.outcome === 'stopped' ? 'down' : 'muted'}`}>
                      {e.outcome === 'bounce' ? `bounced in ${e.weeks} weeks` : e.outcome === 'stopped' ? `broke down in ${e.weeks} weeks` : 'neither within a year'}
                    </td>
                    <td className={`num r ${tone(e.ret13w ?? 0)}`}>{spct(e.ret13w)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
        )}
      </div>
    </Panel>
  )
}
