import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useIsAdmin } from '../auth'
import { api, type DirSummary, type EarningsProgress, type LLMUsage, type RankDriver, type RankItem, type RankModel, type TrainingProgress } from '../api'
import { EquityChart } from '../charts'
import { Stat } from '../components'
import { ago, pct, price, spct, tone } from '../format'

export const UNIVERSE_LABELS: Record<string, string> = {
  stocks: 'Stocks',
  etfs: 'ETFs and sectors',
  crypto: 'Crypto',
  meme: 'Meme coins',
  commodities: 'Commodities',
}
export const UNIVERSE_ORDER = ['stocks', 'etfs', 'crypto', 'meme', 'commodities']
export const UNIVERSE_ADJ: Record<string, string> = { stocks: 'stock', etfs: 'fund', crypto: 'crypto', meme: 'meme-coin', commodities: 'commodity' }
const byUniverse = (a: string, b: string) => (UNIVERSE_ORDER.indexOf(a) + 1 || 99) - (UNIVERSE_ORDER.indexOf(b) + 1 || 99)
export const UNIVERSE_ITEMS: Record<string, string> = { stocks: 'stocks', etfs: 'funds', crypto: 'coins', meme: 'meme coins', commodities: 'commodities' }
export const H_SHORT: Record<string, string> = { '1d': 'Day', '1w': 'Week', '1m': 'Month', '1y': 'Year' }
export const H_AHEAD: Record<string, string> = { '1d': 'a day', '1w': 'a week', '1m': 'a month', '1y': 'a year' }
const H_NEXT: Record<string, string> = { '1d': 'next day', '1w': 'next week', '1m': 'next month', '1y': 'next year' }
export const MARK: Record<string, [string, string, string]> = {
  validated: ['✓', 'up', 'Tested edge'],
  weak: ['◐', 'hold', 'Weak evidence'],
  no_edge: ['○', 'faint', 'Not proven'],
}

// Plain words for where an asset sits within its group on each input: [low, high].
const PLAIN: Record<string, [string, string]> = {
  ret_1: ['weak last day', 'strong last day'], ret_5: ['weak last week', 'strong last week'], ret_21: ['weak last month', 'strong last month'],
  ret_63: ['weak last 3 months', 'strong last 3 months'], mom_12_1: ['weak past year', 'strong past year'], mom_vol: ['weak past year', 'strong past year'],
  dist_sma20: ['low vs its 20-day average', 'high vs its 20-day average'], dist_sma50: ['low vs its 50-day average', 'high vs its 50-day average'],
  dist_sma200: ['low vs its 200-day average', 'high vs its 200-day average'], sma50_200: ['weak long-term trend', 'strong long-term trend'],
  adx_dir: ['weak trend', 'strong uptrend'], rsi14: ['oversold', 'overbought'], stoch14: ['oversold', 'overbought'], bb_z20: ['stretched low', 'stretched high'],
  macd_hist: ['losing momentum', 'gaining momentum'], vol_21: ['calmer than most', 'swings more than most'], atr_pct: ['small daily swings', 'big daily swings'],
  vol_ratio: ['calming down', 'getting choppier'], dist_52w_high: ['far below its 52-week high', 'near its 52-week high'],
  dist_52w_low: ['near its 52-week low', 'far above its 52-week low'], volume_z: ['quiet trading', 'busy trading'], obv_flow: ['money flowing out', 'money flowing in'],
  rel_strength_63: ['lagging the market', 'beating the market'], funding_7d: ['cheap to bet on', 'expensive to bet on'], funding_z: ['cheap to bet on', 'expensive to bet on'],
}

/** The two inputs that matter most, in plain words, with whether each lifts or lowers the rank. */
function reasons(ds: RankDriver[] | null) {
  const strong = (ds ?? []).filter((d) => d.percentile <= 0.33 || d.percentile >= 0.67)
  return (strong.length ? strong : ds ?? []).slice(0, 2).map((d) => {
    const hi = d.percentile >= 0.5
    const words = PLAIN[d.key]?.[hi ? 1 : 0] ?? `${d.percentile >= 0.67 ? 'high' : d.percentile <= 0.33 ? 'low' : 'middling'} ${d.label.toLowerCase()}`
    return { key: d.key, words, up: d.contribution >= 0, title: `${d.label}: ${ordinal(d.percentile)} percentile in its group; ${d.contribution >= 0 ? 'lifts' : 'lowers'} the rank` }
  })
}

export function useRankModels() {
  const [models, setModels] = useState<RankModel[] | null>(null)
  useEffect(() => {
    api.rankings().then((m) => setModels(m ?? [])).catch(() => setModels([]))
  }, [])
  return models
}

/** Every universe × horizon that has a ranking model, with how well it tested. */
export function TestedGrid({ models, sel, onPick }: { models: RankModel[]; sel?: RankModel; onPick: (m: RankModel) => void }) {
  const unis = [...new Set(models.map((m) => m.universe))].sort(byUniverse)
  const hs = ['1d', '1w', '1m', '1y'].filter((h) => models.some((m) => m.horizon === h))
  return (
    <div className="tgrid" style={{ gridTemplateColumns: `minmax(90px, 150px) repeat(${hs.length}, minmax(44px, 1fr))` }} role="grid" aria-label="Rankings tested so far">
      <span />
      {hs.map((h) => <span key={h} className="tag faint tgrid-h">{H_SHORT[h]}</span>)}
      {unis.map((u) => (
        <div key={u} className="tgrid-row" role="row">
          <span className="tgrid-u">{UNIVERSE_LABELS[u] ?? u}</span>
          {hs.map((h) => {
            const m = models.find((x) => x.universe === u && x.horizon === h)
            if (!m?.validation) return <span key={h} className="faint tgrid-x">—</span>
            const [mk, cls, label] = MARK[m.validation.status] ?? MARK.no_edge
            const on = sel === m
            return (
              <button key={h} className={`tgrid-c ${cls} ${on ? 'on' : ''}`} aria-pressed={on} title={`${UNIVERSE_LABELS[u] ?? u}, ${H_NEXT[h]}: ${label}`} onClick={() => onPick(m)}>
                {mk}
              </button>
            )
          })}
        </div>
      ))}
    </div>
  )
}

/** One ranking: today's order with plain reasons, and its test record folded away. */
export function RankingCard({ m }: { m: RankModel }) {
  const [all, setAll] = useState(false)
  const [proof, setProof] = useState(false)
  const v = m.validation
  const [mk, cls, label] = MARK[v.status] ?? MARK.no_edge
  const items = UNIVERSE_ITEMS[m.universe] ?? 'assets'
  const live = m.live ?? []
  const shown = all ? live : live.slice(0, 8)
  const ahead = H_AHEAD[m.horizon] ?? m.horizon
  const per = { '1d': 'a day', '1w': 'a week', '1m': 'a month', '1y': 'a year' }[m.horizon] ?? 'a period'
  const summary = v.status === 'validated'
    ? `In testing since ${new Date(v.testFrom).toLocaleDateString(undefined, { month: 'long', year: 'numeric' })}, the top fifth beat the group by ${pct(v.topExcess, 2)} ${per}; a basket of them grew ${pct(v.strategy.cagr, 0)} a year against ${spct(v.strategy.benchCagr, 1)} for all ${items}, after costs.`
    : v.status === 'weak'
      ? `There are signs of skill in testing, but not enough to rely on: the top fifth beat the group by ${pct(v.topExcess, 2)} ${per}. Shown for interest.`
      : `In testing, its top picks did not beat the rest. Today’s order is shown for interest only; don’t pick from it.`
  return (
    <section className="panel" style={{ overflow: 'hidden' }}>
      <div className="panel-head" style={{ alignItems: 'flex-start' }}>
        <div>
          <p className={`tag ${cls}`} style={{ marginBottom: 6 }}>{mk} {label}</p>
          <h2>{UNIVERSE_LABELS[m.universe] ?? m.universe}, {H_NEXT[m.horizon] ?? m.horizon}</h2>
          <p className="sub">Which of {live.length} {items} are most likely to beat the middle one over {ahead}. {summary}</p>
        </div>
        <span className="small faint nowrap">Prices {new Date(m.asOf).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} · trained {ago(m.trainedAt)}</span>
      </div>
      <div className="rk-row rk-head tag faint"><span>#</span><span>{items[0].toUpperCase() + items.slice(1, -1)}</span><span>Chance it beats the middle</span><span className="hide-phone">Main reasons</span></div>
      {shown.map((it) => <RankRow key={it.symbol} it={it} />)}
      <div className="rk-foot">
        {live.length > 8 && <button className="linkish" onClick={() => setAll(!all)}>{all ? 'Show the top 8' : `Show all ${live.length}`}</button>}
        <button className="linkish" onClick={() => setProof(!proof)} aria-expanded={proof}>{proof ? 'Hide the test record' : 'How well has this ranking worked?'}</button>
      </div>
      {proof && <Proof m={m} />}
    </section>
  )
}

function RankRow({ it }: { it: RankItem }) {
  const rs = reasons(it.drivers)
  const p = it.pOutperform
  return (
    <div className="rk-row">
      <span className="mono faint">{it.rank}</span>
      <Link to={`/asset/${encodeURIComponent(it.symbol)}`} className="rk-name">
        <strong>{it.name.replace(/ USD$/, '').length > 26 ? it.symbol : it.name.replace(/ USD$/, '')}</strong> <span className="mono small faint">{it.symbol}</span>
      </Link>
      <span className="rk-chance">
        <span className="wl-bar" role="img" aria-label={`${pct(p)} chance it beats the middle`}><i style={{ width: `${Math.max(0, Math.min(1, (p - 0.4) / 0.2)) * 100}%` }} /><b /></span>
        <span className="mono small">{pct(p)}</span>
      </span>
      <span className="small muted rk-why">
        {rs.map((r, i) => (
          <span key={r.key} title={r.title}>{i > 0 && ' · '}{r.words.charAt(0).toUpperCase() + r.words.slice(1)} <span className={r.up ? 'up' : 'down'}>{r.up ? '↑' : '↓'}</span></span>
        ))}
      </span>
    </div>
  )
}

function Proof({ m }: { m: RankModel }) {
  const v = m.validation
  const hz = m.horizon
  return (
    <div className="panel-body stack" style={{ gap: 18, borderTop: '1px solid var(--border)', paddingTop: 18 }}>
      <p className="small muted">{v.statusNote}</p>
      <div className="grid grid-4">
        <Stat label="Rank correlation (IC)" value={v.meanIc.toFixed(3)} hint={`t-stat ${v.icTStat.toFixed(2)} over ${v.periods} periods · above 0 ${pct(v.icHitRate, 0)} of the time`} tone={tone(v.meanIc)} />
        <Stat label="Top fifth vs group" value={spct(v.topExcess, 2)} hint={`per ${H_NEXT[hz].replace('next ', '')}; bottom fifth ${spct(v.botExcess, 2)}`} tone={tone(v.topExcess)} />
        <Stat label="Top-fifth basket, a year" value={spct(v.strategy.cagr)} hint={`whole group ${spct(v.strategy.benchCagr)} · after ${pct(v.strategy.costPerTrade, 1)} costs`} tone={v.strategy.cagr > v.strategy.benchCagr ? 'up' : 'down'} />
        <Stat label="Beat-the-middle hit rate" value={pct(v.hitRate)} hint={`${v.rows.toLocaleString()} out-of-sample forecasts, ${v.testFrom} → ${v.testTo}`} />
      </div>
      {(v.topAlpha !== undefined || v.components) && (
        <p className="small muted">
          {v.topAlpha !== undefined && <>With market exposure removed, the top fifth’s edge was <span className={`num ${tone(v.topAlpha)}`}>{spct(v.topAlpha, 2)}</span> per period (average beta {v.topBeta?.toFixed(2)}). </>}
          {v.components && <>Rank correlation by model: {Object.entries(v.components).map(([k, c]) => `${k === 'gbm' ? 'boosted trees' : k} ${c.meanIc.toFixed(3)} (t ${c.icTStat.toFixed(1)})`).join(' · ')}.</>}
        </p>
      )}
      <div className="grid grid-2">
        <div>
          <h3 style={{ marginBottom: 6 }}>Does a higher rank mean higher returns?</h3>
          <DecileBars deciles={v.deciles ?? []} />
          <p className="tiny faint">Average return against the group for each tenth of forecasts (1 = least favoured, 10 = most). A working ranking slopes upward.</p>
        </div>
        <div>
          <h3 style={{ marginBottom: 6 }}>Top-fifth basket vs the whole group <span className="faint small">(<span style={{ color: 'var(--accent-ink)' }}>━ basket</span> ━ group)</span></h3>
          {v.strategy.equity && <EquityChart equity={v.strategy.equity} buyHold={v.strategy.bench ?? []} dates={v.strategy.dates ?? []} />}
          <p className="tiny faint">
            Rebalanced every {H_NEXT[hz].replace('next ', '')}, turnover {pct(v.strategy.turnover, 0)} each time, Sharpe {v.strategy.sharpe.toFixed(2)} vs {v.strategy.benchSharpe.toFixed(2)}, worst fall {pct(v.strategy.maxDrawdown, 0)} vs {pct(v.strategy.benchMaxDrawdown, 0)}.
            {v.strategy.breakEvenCost != null && v.strategy.breakEvenCost > 0 && (
              <> Trading costs above <strong>{pct(v.strategy.breakEvenCost, 2)} per trade</strong> would use up the edge (this test assumes {pct(v.strategy.costPerTrade, 1)}).</>
            )}
          </p>
        </div>
      </div>
      <p className="tiny faint">Prices: {(m.live ?? []).slice(0, 3).map((x) => `${x.symbol} ${price(x.price)}`).join(' · ')}…</p>
    </div>
  )
}

function DecileBars({ deciles }: { deciles: { decile: number; avgExcess: number; n: number }[] }) {
  const W = 520, H = 180, P = 30
  const max = Math.max(0.001, ...deciles.map((d) => Math.abs(d.avgExcess)))
  const bw = (W - 2 * P) / Math.max(1, deciles.length)
  const y0 = H / 2
  return (
    <svg viewBox={`0 0 ${W} ${H}`} width="100%" style={{ maxHeight: 220 }} role="img" aria-label="Average return by forecast decile">
      <line x1={P} x2={W - P} y1={y0} y2={y0} style={{ stroke: 'var(--border-strong)' }} />
      {deciles.map((d, i) => {
        const h = (Math.abs(d.avgExcess) / max) * (H / 2 - 22)
        const up = d.avgExcess >= 0
        return (
          <g key={d.decile}>
            <rect x={P + i * bw + 4} width={bw - 8} y={up ? y0 - h : y0} height={h} rx={2} style={{ fill: up ? 'var(--up)' : 'var(--down)' }} opacity={0.8}>
              <title>{`Decile ${d.decile}: ${spct(d.avgExcess, 2)} vs the group (n=${d.n})`}</title>
            </rect>
            <text x={P + i * bw + bw / 2} y={H - 4} textAnchor="middle" style={{ fill: 'var(--faint)', font: '11px var(--mono)' }}>{d.decile}</text>
          </g>
        )
      })}
    </svg>
  )
}

/** Training, cross-asset model and earnings-data status. */
export function ModelStatus() {
  const [dir, setDir] = useState<DirSummary[]>([])
  const [train, setTrain] = useState<TrainingProgress | null>(null)
  const [earn, setEarn] = useState<EarningsProgress | null>(null)
  const [usage, setUsage] = useState<LLMUsage | null>(null)
  const isAdmin = useIsAdmin()
  useEffect(() => {
    const load = () => {
      if (isAdmin) api.llmUsage().then(setUsage).catch(() => {})
      api.models().then((r) => {
        setDir(r.direction ?? [])
        setTrain(r.training)
        setEarn(r.earnings)
      }).catch(() => {})
    }
    load()
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [isAdmin])

  return (
    <div className="stack">
      {isAdmin && (
        <div className="row">
          <button disabled={train?.running} onClick={() => api.trainModels().then(setTrain)}>{train?.running ? <><span className="spinner" /> Training…</> : 'Train models now'}</button>
          <span className="small faint">Cross-asset models retrain daily on the schedule.</span>
        </div>
      )}
      {train?.running && (
        <div className="stack" style={{ gap: 6 }}>
          <div className="progress"><div style={{ width: `${(100 * train.done) / Math.max(1, train.total)}%` }} /></div>
          <span className="small muted">{train.stage} · {train.done}/{train.total} assets loaded</span>
        </div>
      )}
      {train?.lastError && <div className="callout bad small">{train.lastError}</div>}
      {usage && usage.calls30 > 0 && (
        <p className="small muted">
          <strong>Claude spend:</strong> today ${usage.todayUsd.toFixed(2)} · 7 days ${usage.last7Usd.toFixed(2)} · 30 days ${usage.last30Usd.toFixed(2)}
          {' '}({usage.calls30} analyses, avg ${usage.avgPerCallUsd.toFixed(3)} each, {pct(usage.batchShare30, 0)} at the 50% batch rate)
        </p>
      )}
      {earn && (
        <p className="small muted">
          <strong>Earnings history:</strong> {earn.events.toLocaleString()} reports
          {earn.coveredFrom ? `, complete from ${earn.coveredFrom}` : ''}
          {earn.running ? ` · downloading ${earn.daysDone.toLocaleString()} / ${earn.daysTotal.toLocaleString()} days…` : ''}
          {isAdmin && !earn.running && earn.daysTotal > 0 && earn.daysDone < earn.daysTotal && (
            <button className="small ghost" style={{ marginLeft: 8 }} onClick={() => api.backfillEarnings().then(setEarn)}>Resume download</button>
          )}
        </p>
      )}
      {dir.length > 0 && (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Cross-asset direction model</th><th>Horizon</th><th className="r">Assets</th><th className="r">Hit rate</th><th className="r">Naive</th><th className="r">Brier skill</th><th className="r">AUC</th><th className="r">p</th><th>Tested</th></tr></thead>
            <tbody>
              {dir.map((d) => (
                <tr key={d.pool + d.horizon}>
                  <td>{d.pool}</td><td>{d.horizon}</td><td className="num r">{d.assets}</td>
                  <td className="num r">{pct(d.accuracy)}</td><td className="num r muted">{pct(d.baseAccuracy)}</td>
                  <td className={`num r ${tone(d.brierSkill)}`}>{spct(d.brierSkill, 2)}</td><td className="num r">{d.auc.toFixed(3)}</td>
                  <td className="num r">{d.pValue.toFixed(3)}</td><td className="small muted">{d.testFrom} → {d.testTo}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function ordinal(p: number) {
  if (!Number.isFinite(p)) return '—'
  const n = Math.max(1, Math.min(99, Math.round(p * 100)))
  const s = n % 100 >= 11 && n % 100 <= 13 ? 'th' : ['th', 'st', 'nd', 'rd'][n % 10] ?? 'th'
  return `${n}${s}`
}
