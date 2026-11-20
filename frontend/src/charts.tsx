import { useEffect, useRef } from 'react'
import {
  CandlestickSeries,
  ColorType,
  createChart,
  HistogramSeries,
  LineSeries,
  LineStyle,
  type IChartApi,
  type UTCTimestamp,
} from 'lightweight-charts'
import type { Bucket, Report } from './api'
import { pct } from './format'
import { cssColor, useLookVersion, withAlpha } from './theme'

/** Colours for canvas charts, read from the current look. */
function palette() {
  return {
    text: cssColor('--muted'), grid: cssColor('--border'), axis: cssColor('--border-strong'),
    up: cssColor('--up'), down: cssColor('--down'), accent: cssColor('--accent-ink'), hold: cssColor('--hold'), ink: cssColor('--text'),
  }
}
const alpha = withAlpha
function baseOptions(p: ReturnType<typeof palette>) {
  return {
    autoSize: true,
    layout: { background: { type: ColorType.Solid, color: 'transparent' }, textColor: p.text, fontSize: 11, fontFamily: "'JetBrains Mono Variable', ui-monospace, monospace" },
    grid: { vertLines: { color: alpha(p.grid, 0.6) }, horzLines: { color: alpha(p.grid, 0.6) } },
    rightPriceScale: { borderColor: p.axis },
    timeScale: { borderColor: p.axis },
  }
}

const ts = (d: string) => (Date.parse(d + 'T00:00:00Z') / 1000) as UTCTimestamp

/** Candlestick chart with 50/200-day averages and the selected horizon's forecast range. */
export function PriceChart({ report, targets }: { report: Report; targets?: Record<string, number> }) {
  const el = useRef<HTMLDivElement>(null)
  const chartRef = useRef<IChartApi | null>(null)

  const look = useLookVersion()
  useEffect(() => {
    const p = palette()
    if (!el.current) return
    const c = report.chart
    const chart = createChart(el.current, {
      ...baseOptions(p),
      crosshair: { mode: 0 },
    })
    chartRef.current = chart
    const candles = chart.addSeries(CandlestickSeries, {
      upColor: p.up, downColor: p.down, borderVisible: false, wickUpColor: p.up, wickDownColor: p.down,
    })
    candles.setData(c.dates.map((d, i) => ({ time: ts(d), open: c.open[i], high: c.high[i], low: c.low[i], close: c.close[i] })))
    const line = (vals: (number | null)[], color: string, title: string) => {
      const s = chart.addSeries(LineSeries, { color, lineWidth: 1, priceLineVisible: false, lastValueVisible: false, title })
      s.setData(c.dates.flatMap((d, i) => (vals[i] == null ? [] : [{ time: ts(d), value: vals[i] as number }])))
    }
    line(c.sma50, p.accent, 'SMA 50')
    line(c.sma200, p.hold, 'SMA 200')
    if (c.volume.some((v) => v > 0)) {
      const vol = chart.addSeries(HistogramSeries, { priceFormat: { type: 'volume' }, priceScaleId: 'vol', lastValueVisible: false, priceLineVisible: false })
      chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
      vol.setData(c.dates.map((d, i) => ({ time: ts(d), value: c.volume[i], color: c.close[i] >= c.open[i] ? alpha(p.up, 0.3) : alpha(p.down, 0.3) })))
    }
    if (targets) {
      const lines: [string, string, string][] = [
        ['p90', p.up, '90th pct'],
        ['p50', p.ink, 'median'],
        ['p10', p.down, '10th pct'],
      ]
      for (const [k, color, title] of lines) {
        if (targets[k]) candles.createPriceLine({ price: targets[k], color, lineWidth: 1, lineStyle: LineStyle.Dashed, axisLabelVisible: true, title })
      }
    }
    const n = c.dates.length
    if (n > 260) chart.timeScale().setVisibleRange({ from: ts(c.dates[n - 260]), to: ts(c.dates[n - 1]) })
    return () => {
      chart.remove()
      chartRef.current = null
    }
  }, [report, targets, look])

  return <div ref={el} className="chart-box" />
}

/** Reliability diagram: predicted probability (x) vs realised up-rate (y). */
export function CalibrationChart({ buckets }: { buckets: Bucket[] }) {
  const W = 360, H = 260, P = 36
  const x = (v: number) => P + ((v - 0.2) / 0.6) * (W - 2 * P)
  const y = (v: number) => H - P - v * (H - 2 * P)
  const used = buckets.filter((b) => b.n > 0)
  const maxN = Math.max(1, ...used.map((b) => b.n))
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" style={{ maxWidth: 480 }}>
      {[0, 0.25, 0.5, 0.75, 1].map((v) => (
        <g key={v}>
          <line x1={P} x2={W - P} y1={y(v)} y2={y(v)} style={{ stroke: 'var(--border)' }} />
          <text x={P - 6} y={y(v) + 4} textAnchor="end">{pct(v, 0)}</text>
        </g>
      ))}
      {[0.3, 0.4, 0.5, 0.6, 0.7].map((v) => (
        <text key={v} x={x(v)} y={H - P + 16} textAnchor="middle">{pct(v, 0)}</text>
      ))}
      <line x1={x(0.2)} y1={y(0.2)} x2={x(0.8)} y2={y(0.8)} style={{ stroke: 'var(--faint)' }} strokeDasharray="4 4" />
      {used.map((b) => {
        const cx = x(Math.min(0.8, Math.max(0.2, b.meanPred)))
        const r = 3 + 9 * Math.sqrt(b.n / maxN)
        return (
          <g key={b.lo}>
            <circle cx={cx} cy={y(b.upRate)} r={r} style={{ fill: 'var(--band)', stroke: 'var(--accent-ink)' }} />
            <title>{`Predicted ${pct(b.meanPred)} → actually up ${pct(b.upRate)} (n=${b.n})`}</title>
          </g>
        )
      })}
      <text x={W / 2} y={H - 4} textAnchor="middle">predicted P(up)</text>
      <text x={10} y={H / 2} textAnchor="middle" transform={`rotate(-90 10 ${H / 2})`}>actual up-rate</text>
    </svg>
  )
}

/** Strategy equity vs buy-and-hold on a log scale. */
export function EquityChart({ equity, buyHold, dates }: { equity: number[]; buyHold: number[]; dates: string[] }) {
  const W = 1100, H = 240, P = 52
  if (equity.length < 2) return <div className="empty">Not enough periods</div>
  const all = [...equity, ...buyHold].filter((v) => v > 0)
  const lo = Math.log(Math.min(...all)), hi = Math.log(Math.max(...all))
  const span = hi - lo || 1
  const x = (i: number) => P + (i / (equity.length - 1)) * (W - P - 10)
  const y = (v: number) => H - 24 - ((Math.log(Math.max(v, 1e-9)) - lo) / span) * (H - 40)
  const path = (vals: number[]) => vals.map((v, i) => `${i ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join('')
  const ticks = [Math.exp(lo), Math.exp((lo + hi) / 2), Math.exp(hi)]
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" style={{ maxHeight: 280 }}>
      {ticks.map((t) => (
        <g key={t}>
          <line x1={P} x2={W - 10} y1={y(t)} y2={y(t)} style={{ stroke: 'var(--border)' }} />
          <text x={P - 6} y={y(t) + 4} textAnchor="end">{t >= 10 ? t.toFixed(0) : t.toFixed(2)}×</text>
        </g>
      ))}
      <path d={path(buyHold)} fill="none" style={{ stroke: 'var(--faint)' }} strokeWidth={1.5} />
      <path d={path(equity)} fill="none" style={{ stroke: 'var(--accent-ink)' }} strokeWidth={2} />
      <text x={P} y={H - 6}>{dates[0]}</text>
      <text x={W - 10} y={H - 6} textAnchor="end">{dates[dates.length - 1]}</text>
    </svg>
  )
}

/** Candlestick chart for a DEX token's price history (unix-second candles). */
export function TokenChart({ candles }: { candles: { time: number; open: number; high: number; low: number; close: number; volume: number }[] }) {
  const el = useRef<HTMLDivElement>(null)
  const look = useLookVersion()
  useEffect(() => {
    const p = palette()
    if (!el.current || candles.length === 0) return
    const chart = createChart(el.current, {
      ...baseOptions(p),
      timeScale: { borderColor: p.axis, timeVisible: true },
      localization: { priceFormatter: (p: number) => (p >= 1 ? p.toFixed(2) : p <= 0 ? '0' : p.toFixed(Math.min(20, 2 - Math.floor(Math.log10(p))))) },
    })
    const s = chart.addSeries(CandlestickSeries, { upColor: p.up, downColor: p.down, borderVisible: false, wickUpColor: p.up, wickDownColor: p.down, priceFormat: { type: 'price', precision: 10, minMove: 1e-10 } })
    s.setData(candles.map((c) => ({ time: c.time as UTCTimestamp, open: c.open, high: c.high, low: c.low, close: c.close })))
    const vol = chart.addSeries(HistogramSeries, { priceFormat: { type: 'volume' }, priceScaleId: 'vol', lastValueVisible: false, priceLineVisible: false })
    chart.priceScale('vol').applyOptions({ scaleMargins: { top: 0.82, bottom: 0 } })
    vol.setData(candles.map((c) => ({ time: c.time as UTCTimestamp, value: c.volume, color: c.close >= c.open ? alpha(p.up, 0.3) : alpha(p.down, 0.3) })))
    chart.timeScale().fitContent()
    return () => chart.remove()
  }, [candles, look])
  return <div ref={el} className="chart-box" style={{ height: 340 }} />
}

/** Price with horizontal levels (Fibonacci, entry zone, stop). */
export function LevelsChart({ report, lines }: { report: Report; lines: { price: number; color: string; title: string; dashed?: boolean }[] }) {
  const el = useRef<HTMLDivElement>(null)
  const look = useLookVersion()
  useEffect(() => {
    const p = palette()
    if (!el.current) return
    const c = report.chart
    const chart = createChart(el.current, {
      ...baseOptions(p),
    })
    const candles = chart.addSeries(CandlestickSeries, { upColor: p.up, downColor: p.down, borderVisible: false, wickUpColor: p.up, wickDownColor: p.down })
    candles.setData(c.dates.map((d, i) => ({ time: ts(d), open: c.open[i], high: c.high[i], low: c.low[i], close: c.close[i] })))
    for (const l of lines) {
      candles.createPriceLine({ price: l.price, color: l.color.startsWith('--') ? cssColor(l.color) : l.color, lineWidth: 1, lineStyle: l.dashed ? LineStyle.Dashed : LineStyle.Solid, axisLabelVisible: true, title: l.title })
    }
    chart.timeScale().fitContent()
    return () => chart.remove()
  }, [report, lines, look])
  return <div ref={el} className="chart-box" style={{ height: 380 }} />
}

/** Twelve-month revenue, operating profit and free cash flow (bars, left) against the share price (line, right). */
export function BusinessChart({ quarters }: { quarters: { end: string; revenue: number | null; opIncome: number | null; fcf: number | null; price: number | null; known: string }[] }) {
  const el = useRef<HTMLDivElement>(null)
  const look = useLookVersion()
  useEffect(() => {
    const p = palette()
    if (!el.current || quarters.length === 0) return
    const chart = createChart(el.current, {
      ...baseOptions(p),
      leftPriceScale: { visible: true, borderColor: p.axis },
      localization: { priceFormatter: (p: number) => (Math.abs(p) >= 1e9 ? `${(p / 1e9).toFixed(1)}B` : Math.abs(p) >= 1e6 ? `${(p / 1e6).toFixed(0)}M` : p.toFixed(2)) },
    })
    const add = (key: 'revenue' | 'opIncome' | 'fcf', color: string, title: string) => {
      const s = chart.addSeries(LineSeries, { color, lineWidth: 2, priceScaleId: 'left', title, lastValueVisible: false, priceLineVisible: false })
      s.setData(quarters.flatMap((q) => (q[key] == null ? [] : [{ time: ts(q.end), value: q[key] as number }])))
    }
    add('revenue', p.accent, 'Revenue (12m)')
    add('opIncome', p.up, 'Operating profit (12m)')
    add('fcf', p.hold, 'Free cash flow (12m)')
    const price = chart.addSeries(LineSeries, { color: p.ink, lineWidth: 1, lineStyle: LineStyle.Dashed, priceScaleId: 'right', title: 'Share price' })
    // The price is plotted on the day the figures became public.
    // Filings can become public out of order (amendments), so sort by date.
    const seen = new Set<string>()
    const pts = quarters.flatMap((q) => (q.price == null || !q.known || seen.has(q.known) ? [] : (seen.add(q.known), [{ time: ts(q.known), value: q.price }])))
    price.setData(pts.sort((a, b) => a.time - b.time))
    chart.timeScale().fitContent()
    return () => chart.remove()
  }, [quarters, look])
  return <div ref={el} className="chart-box" style={{ height: 320 }} />
}
