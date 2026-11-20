import { Fragment, useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router-dom'
import { useMe } from '../auth'
import { api, ApiError, track, type HorizonForecast, type Report, type Status } from '../api'
import { CalibrationChart, EquityChart } from '../charts'
import { AnalystCard, AssetHeader, ConeChart, DEEPER, Deeper, EarningsBand, HorizonRows, NewsList, ShortAnswer, WhyCard, useCitations, type DeeperTab } from './AssetParts'
import { DriverList, Loading, Panel, ProbBar, SignalBadge, Stat, StatusBadge } from '../components'
import { ago, pct, pp, price, spct, tone } from '../format'

const OLD_TABS: Record<string, DeeperTab> = { Why: 'reasoning', Drivers: 'drivers', Analogs: 'sim', Backtest: 'track', 'Risk plan': 'plan' }
const EMPTY = { news: { articles: [] }, bullCase: [], bearCase: [], catalysts: [], risks: [], narrative: '' } as unknown as Report

export default function Asset({ status }: { status: Status | null }) {
  const { symbol = '' } = useParams()
  const sym = symbol.toUpperCase()
  const [report, setReport] = useState<Report | null>(null)
  const [running, setRunning] = useState(false)
  const [err, setErr] = useState('')
  const [params, setParams] = useSearchParams()
  const { settings } = useMe()
  // Go deeper: people who chose full detail open on the technical tab.
  const tabParam = OLD_TABS[params.get('tab') ?? ''] ?? params.get('tab')
  const tab = (DEEPER.some(([k]) => k === tabParam) ? tabParam : window.location.hash === '#earnings' ? 'earn' : settings.detail === 'full' ? 'tech' : 'earn') as DeeperTab
  const setTab = (t: DeeperTab) => {
    if (t === 'tech' || t === 'drivers' || t === 'reasoning') track('detail', sym)
    setParams((p) => { p.set('tab', t); return p }, { replace: true })
  }
  // Open on the person's own horizon when this asset has a forecast for it.
  const sel = params.get('h') ?? (report?.horizons.find((x) => x.key === settings.horizon)?.available ? settings.horizon : '1d')
  const setSel = (h: string) => setParams((p) => { p.set('h', h); return p }, { replace: true })
  const [history, setHistory] = useState<{ id: number; createdAt: string; headline: string }[]>([])
  // Links like /asset/NVDA#earnings jump to that panel once the report is in.
  useEffect(() => {
    if (report && window.location.hash.length > 1) document.getElementById(window.location.hash.slice(1))?.scrollIntoView({ behavior: 'smooth' })
  }, [report])

  const run = useCallback(
    async (llm: boolean, force: boolean) => {
      setRunning(true)
      setErr('')
      try {
        const r = await api.runReport(sym, llm, force)
        setReport(r)
        api.reportHistory(sym).then((h) => setHistory(h ?? []))
      } catch (e) {
        setErr((e as Error).message)
      } finally {
        setRunning(false)
      }
    },
    [sym],
  )

  useEffect(() => track('visit', sym), [sym])

  useEffect(() => {
    setReport(null)
    setErr('')
    api
      .latestReport(sym)
      .then(setReport)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 404) run(true, false)
        else setErr((e as Error).message)
      })
    api.reportHistory(sym).then((h) => setHistory(h ?? [])).catch(() => {})
  }, [sym, run])

  const h = useMemo(() => report?.horizons.find((x) => x.key === sel), [report, sel])
  const plainFor = useMemo(() => report?.plain?.find((x) => x.key === sel), [report, sel])
  const cites = useCitations(report ?? EMPTY)

  if (!report) {
    return running || !err ? (
      <Loading label={`Analysing ${sym}: downloading full price history, benchmark and macro series, running walk-forward validation on 4 horizons and reading the latest news${status?.llm ? ' with the AI analyst' : ''}. This can take up to a couple of minutes.`} />
    ) : (
      <div className="callout bad">
        Could not analyse {sym}: {err}
        <div style={{ marginTop: 8 }}>
          <button onClick={() => run(true, true)}>Try again</button>
        </div>
      </div>
    )
  }

  return (
    <>
      <AssetHeader report={report} running={running} onRun={run} history={history} onPick={(id) => api.reportById(id).then(setReport)} hasLLM={!!status?.llm} />
      {err && <div className="callout bad">{err}</div>}
      {report.warnings && report.warnings.length > 0 && (
        <div className="callout warn">
          <strong>Data notes</strong>
          <ul>{report.warnings.map((w) => <li key={w}>{w}</li>)}</ul>
        </div>
      )}
      <EarningsBand report={report} />
      <ShortAnswer report={report} h={h} a={plainFor} sel={sel} setSel={setSel} />
      <section className="panel chart-card">
        <div style={{ minWidth: 0 }}>
          <h2>Price{h?.available ? `, and where it could be by ${new Date(h.targetDate + 'T12:00:00Z').toLocaleDateString(undefined, { day: 'numeric', month: 'short' })}` : ''}</h2>
          <p className="small muted" style={{ margin: '4px 0 14px' }}>{h?.available ? 'The shaded cone is where 8 in 10 similar past cases ended up. The dashed line is the middle one.' : 'No forecast for this horizon.'}</p>
          <ConeChart report={report} h={h} />
        </div>
        <HorizonRows report={report} sel={sel} setSel={setSel} />
      </section>
      <div className={`why-grid ${report.usedLlm && (report.bullCase?.length || report.bearCase?.length) ? '' : 'one'}`}>
        <WhyCard key={sel} a={plainFor} />
        <AnalystCard report={report} cites={cites} />
      </div>
      <Deeper report={report} h={h} a={plainFor} tab={tab} setTab={setTab} />
      <NewsList report={report} cites={cites} />
    </>
  )
}

export function HorizonCard({ h, selected, onClick, currency }: { h: HorizonForecast; selected: boolean; onClick: () => void; currency: string }) {
  if (!h.available) {
    return (
      <div className="hcard disabled">
        <div className="row between"><h3>{h.label}</h3><span className="badge none">N/A</span></div>
        <p className="small muted">{h.reason}</p>
      </div>
    )
  }
  return (
    <div className={`hcard ${selected ? 'selected' : ''}`} onClick={onClick}>
      <div className="row between">
        <h3>{h.label}{h.earningsInside && <span className="badge hold" style={{ marginLeft: 8 }} title={h.earningsNote}>earnings</span>}</h3>
        <SignalBadge signal={h.signal} />
      </div>
      <div className="row between" style={{ alignItems: 'flex-end' }}>
        <div>
          <div className={`prob ${h.pUp >= 0.5 ? 'up' : 'down'}`}>{pct(h.pUp)}</div>
          <div className="tiny muted">chance price is higher by {h.targetDate}</div>
        </div>
        <StatusBadge status={h.status} />
      </div>
      <ProbBar p={h.pUp} base={h.baseRate} />
      <dl className="kv">
        <dt>Conviction</dt><dd>{h.conviction}/100</dd>
        <dt>Expected move</dt><dd className={tone(h.expectedReturn)}>{spct(h.expectedReturn)}</dd>
        <dt>80% range</dt><dd>{spct(h.returnRange.p10)} … {spct(h.returnRange.p90)}</dd>
        <dt>Median target</dt><dd>{price(h.priceTargets.p50)} {currency}</dd>
        <dt>Base rate</dt><dd>{pct(h.baseRate)}</dd>
        {h.options && (
          <>
            <dt title={`At-the-money straddle for the ${h.options.expiry} expiry, scaled to the target date`}>Options-implied move</dt>
            <dd>±{pct(h.options.impliedMove)}</dd>
          </>
        )}
      </dl>
    </div>
  )
}

const TABS = ['Why', 'Drivers', 'Analogs', 'Backtest', 'Risk plan'] as const

export function HorizonDetail({ h, report }: { h: HorizonForecast; report: Report }) {
  const [params, setParams] = useSearchParams()
  const tab = (TABS as readonly string[]).includes(params.get('tab') ?? '') ? (params.get('tab') as (typeof TABS)[number]) : 'Why'
  const setTab = (t: string) => setParams((p) => { p.set('tab', t); return p }, { replace: true })
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>{h.label}: why the model says {h.signal}</h2>
          <div className="sub">{h.statusNote}</div>
        </div>
        <SignalBadge signal={h.signal} large />
      </div>
      <div className="tabs">
        {TABS.map((t) => (
          <button key={t} className={tab === t ? 'active' : ''} onClick={() => setTab(t)}>{t}</button>
        ))}
      </div>
      <div className="panel-body">
        {tab === 'Why' && <WhyTab h={h} report={report} />}
        {tab === 'Drivers' && <DriversTab h={h} report={report} />}
        {tab === 'Analogs' && <AnalogsTab h={h} report={report} />}
        {tab === 'Backtest' && <BacktestTab h={h} report={report} />}
        {tab === 'Risk plan' && <RiskTab h={h} report={report} />}
      </div>
    </section>
  )
}

export function WhyTab({ h }: { h: HorizonForecast; report?: Report }) {
  return (
    <div className="grid" style={{ gridTemplateColumns: 'minmax(0,2fr) minmax(0,1fr)' }}>
      <ol className="why">{h.why?.map((w) => <li key={w}>{w}</li>)}</ol>
      <div className="stack">
        <h3>How the {pct(h.pUp)} is built</h3>
        <dl className="kv">
          <dt>Base rate (any {h.bars}-bar window)</dt><dd>{pct(h.baseRate)}</dd>
          <dt>Linear factor model</dt><dd>{pct(h.pLogistic)}</dd>
          <dt>Historical-analog model</dt><dd>{pct(h.pAnalogs)}</dd>
          {h.pCross != null && <><dt>Cross-asset model</dt><dd>{pct(h.pCross)}</dd></>}
          <dt>Equal-weight blend</dt><dd>{pct(h.pRaw)}</dd>
          <dt>After calibration</dt><dd>{pct(h.pModel)}</dd>
          <dt>News overlay</dt><dd className={tone(h.newsShift)}>{pp(h.newsShift)}</dd>
          <dt><strong>Final</strong></dt><dd><strong>{pct(h.pUp)}</strong></dd>
        </dl>
        <h3 style={{ marginTop: 10 }}>Conviction {h.conviction}/100</h3>
        <dl className="kv">
          {Object.entries(h.convictionParts).map(([k, val]) => (
            <Fragment key={k}><dt>{k}</dt><dd>{(val * 100).toFixed(0)}%</dd></Fragment>
          ))}
        </dl>
        <p className="tiny faint">Conviction = 35% strength of edge + 35% past accuracy at this confidence + 20% statistical significance + 10% model agreement. It is halved for weak evidence and zero when there is no proven edge.</p>
      </div>
    </div>
  )
}

export function DriversTab({ h }: { h: HorizonForecast; report?: Report }) {
  return (
    <div className="grid" style={{ gridTemplateColumns: 'minmax(0,2fr) minmax(0,1fr)' }}>
      <div>
        <p className="small muted" style={{ marginBottom: 10 }}>Each input's effect on the linear model's probability, in percentage points, relative to an average day for this asset. Click a row for the explanation.</p>
        <DriverList drivers={h.drivers ?? []} />
        {h.crossAsset && <CrossAssetDrivers ca={h.crossAsset} />}
      </div>
      <div>
        <h3 style={{ marginBottom: 8 }}>By category</h3>
        {h.groupImpact?.map((g) => (
          <div key={g.group} className="row between small" style={{ padding: '6px 0', borderBottom: '1px solid var(--border)' }}>
            <span>{g.group}</span>
            <span className={`num ${tone(g.probImpact)}`}>{pp(g.probImpact)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

export function AnalogsTab({ h }: { h: HorizonForecast; report?: Report }) {
  return (
    <div className="stack">
      <div className="grid grid-4">
        <Stat label="Similar past setups" value={h.analogs.k} hint="nearest neighbours across all inputs" />
        <Stat label="Went up afterwards" value={pct(h.analogs.upRate)} tone={h.analogs.upRate >= 0.5 ? 'up' : 'down'} hint={`vs ${pct(h.baseRate)} on a random day`} />
        <Stat label="Median outcome" value={spct(h.analogs.medianReturn)} tone={tone(h.analogs.medianReturn)} hint="scaled to today's volatility" />
        <Stat label="Mean outcome" value={spct(h.analogs.meanReturn)} tone={tone(h.analogs.meanReturn)} />
      </div>
      <table>
        <thead><tr><th>Date</th><th className="r">Similarity</th><th className="r">What happened next ({h.bars} bars)</th></tr></thead>
        <tbody>
          {h.analogs.examples?.map((a) => (
            <tr key={a.date}>
              <td className="mono">{a.date}</td>
              <td className="num r">{pct(a.similarity, 0)}</td>
              <td className={`num r ${tone(a.return)}`}>{spct(a.return)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

export function BacktestTab({ h }: { h: HorizonForecast; report?: Report }) {
  const v = h.validation
  return (
    <div className="stack" style={{ gap: 18 }}>
      <p className="small muted">
        Walk-forward test from {v.testFrom} to {v.testTo}: the model was re-trained {'>'}40 times, each time only on data available before the forecast date (with a purge gap so no future information leaked), and scored on what actually happened.
      </p>
      <div className="grid grid-4">
        <Stat label="Hit rate" value={pct(v.ensemble.accuracy)} hint={`naive guess ${pct(v.ensemble.baseAccuracy)}`} tone={v.ensemble.accuracy > v.ensemble.baseAccuracy ? 'up' : 'down'} />
        <Stat label="Brier skill" value={spct(v.ensemble.brierSkill, 2)} hint=">0 means better than the base rate" tone={tone(v.ensemble.brierSkill)} />
        <Stat label="AUC" value={v.ensemble.auc.toFixed(3)} hint="0.5 = coin flip" />
        <Stat label="Chance it's luck" value={pct(v.ensemble.pValue)} hint={`${v.ensemble.nEffective.toLocaleString()} independent periods`} />
      </div>
      <div className="grid grid-2">
        <div>
          <h3 style={{ marginBottom: 6 }}>Calibration: when it said X%, how often did it go up?</h3>
          <CalibrationChart buckets={v.calibration} />
          <p className="tiny faint">Points on the dashed diagonal mean the stated probabilities can be taken at face value. Bubble size = number of forecasts.</p>
        </div>
        <div>
          <h3 style={{ marginBottom: 6 }}>Accuracy by confidence level</h3>
          <table>
            <thead><tr><th>Confidence ≥</th><th className="r">Forecasts</th><th className="r">Share of days</th><th className="r">Hit rate</th></tr></thead>
            <tbody>
              {v.confidence.map((r) => (
                <tr key={r.minEdge} style={h.trackAtConfidence?.minEdge === r.minEdge ? { background: 'var(--accent-bg)' } : undefined}>
                  <td className="num">{pct(0.5 + r.minEdge, 1)} / {pct(0.5 - r.minEdge, 1)}</td>
                  <td className="num r">{r.n.toLocaleString()}</td>
                  <td className="num r">{pct(r.coverage, 0)}</td>
                  <td className={`num r ${r.accuracy > v.ensemble.baseAccuracy ? 'up' : ''}`}>{r.n ? pct(r.accuracy) : '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="tiny faint" style={{ marginTop: 6 }}>Highlighted row = today's confidence level.</p>
        </div>
      </div>
      <div>
        <h3 style={{ marginBottom: 6 }}>Would trading these signals have made money?</h3>
        <p className="small muted" style={{ marginBottom: 8 }}>
          Long when P(up) {'>'} {pct(0.5 + v.strategy.threshold)}, otherwise in cash; non-overlapping {h.bars}-bar holds; {pct(v.strategy.costPerTrade, 2)} cost per trade.
          <span style={{ color: 'var(--accent-ink)' }}> ━ strategy</span> <span className="faint">━ buy & hold</span>
        </p>
        {v.strategy.equity && <EquityChart equity={v.strategy.equity} buyHold={v.strategy.buyHold ?? []} dates={v.strategy.dates ?? []} />}
        <table style={{ marginTop: 10 }}>
          <thead><tr><th /><th className="r">Total return</th><th className="r">CAGR</th><th className="r">Sharpe</th><th className="r">Max drawdown</th><th className="r">Time invested</th><th className="r">Win rate</th></tr></thead>
          <tbody>
            <tr>
              <td>Model signals</td>
              <td className="num r">{spct(v.strategy.totalReturn, 0)}</td>
              <td className="num r">{spct(v.strategy.cagr)}</td>
              <td className="num r">{v.strategy.sharpe.toFixed(2)}</td>
              <td className="num r down">{pct(v.strategy.maxDrawdown)}</td>
              <td className="num r">{pct(v.strategy.exposure, 0)}</td>
              <td className="num r">{pct(v.strategy.winRate)}</td>
            </tr>
            <tr>
              <td className="muted">Buy & hold</td>
              <td className="num r muted">{spct(v.strategy.buyHoldReturn, 0)}</td>
              <td className="num r muted">{spct(v.strategy.buyHoldCagr)}</td>
              <td className="num r muted">{v.strategy.buyHoldSharpe.toFixed(2)}</td>
              <td className="num r muted">{pct(v.strategy.buyHoldMaxDrawdown)}</td>
              <td className="num r muted">100%</td>
              <td className="num r muted">—</td>
            </tr>
          </tbody>
        </table>
      </div>
      <details>
        <summary>Component models</summary>
        <table style={{ marginTop: 8 }}>
          <thead><tr><th>Model</th><th className="r">Hit rate</th><th className="r">Brier skill</th><th className="r">AUC</th><th className="r">Log loss</th></tr></thead>
          <tbody>
            {([['Ensemble', v.ensemble], ['Linear factor (logistic)', v.logistic], ['Historical analogs (k-NN)', v.analogs], ...(v.external ? [['Cross-asset model (on this asset)', v.external] as const] : [])] as const).map(([n, m]) => (
              <tr key={n}><td>{n}</td><td className="num r">{pct(m.accuracy)}</td><td className="num r">{spct(m.brierSkill, 2)}</td><td className="num r">{m.auc.toFixed(3)}</td><td className="num r">{m.logLoss.toFixed(4)}</td></tr>
            ))}
          </tbody>
        </table>
        <p className="tiny faint" style={{ marginTop: 6 }}>Training rows {v.trainRows.toLocaleString()} · ridge λ {v.lambda.toFixed(1)}</p>
      </details>
    </div>
  )
}

export function RiskTab({ h, report }: { h: HorizonForecast; report: Report }) {
  return (
    <div className="stack">
      <div className="grid grid-4">
        <Stat label="Entry (last price)" value={price(report.price)} hint={report.currency} />
        <Stat label="Stop loss" value={price(h.risk.stopLoss)} tone="down" hint={`${spct(h.risk.stopLoss / report.price - 1)} (10th percentile outcome)`} />
        <Stat label="Take profit" value={price(h.risk.takeProfit)} tone="up" hint={`${spct(h.risk.takeProfit / report.price - 1)} (75th percentile outcome)`} />
        <Stat label="Reward : risk" value={h.risk.rewardRisk.toFixed(2)} />
      </div>
      <div className="grid grid-3">
        <Stat label="Size that risks 1% of your account" value={pct(Math.min(h.risk.onePctSize, 0.2))} hint={h.risk.onePctSize > 0.2 ? `${pct(h.risk.onePctSize)} by the stop alone, capped at a 20% max position` : 'if the stop is hit you lose 1% of the account'} />
        <Stat label="Suggested position size" value={pct(h.risk.suggestedSize)} hint="of trading capital (quarter-Kelly, capped at 20%)" />
        <Stat label="Full Kelly (reference only)" value={pct(h.risk.kellyFraction)} hint="never trade full Kelly: estimates are noisy" />
      </div>
      <p className="small muted">{h.risk.note} The 1% rule works for any signal: position = 1% of the account ÷ the distance to the stop. A wide stop means a small position.</p>
      {h.vol && (
        <div className="grid grid-4">
          <Stat label="Forecast daily move" value={`±${pct(h.vol.daily, 2)}`} hint={`63-day history ±${pct(h.vol.naiveDaily, 2)}; long-run ±${pct(h.vol.longRunDaily, 2)}`} />
          <Stat label={`Forecast move over ${h.label.toLowerCase()}`} value={`±${pct(Math.exp(h.vol.over) - 1, 1)}`} hint="one standard deviation" />
          <Stat label="Volatility model" value={h.vol.applied ? 'In use' : 'Not used'} tone={h.vol.applied ? 'up' : ''} hint={`forecast loss ${h.vol.eval.qlike.toFixed(3)} vs ${h.vol.eval.naiveQlike.toFixed(3)} (lower is better)`} />
          <Stat label="Range held the outcome" value={pct(h.vol.eval.coverage, 0)} hint={`target 80%; old scaling ${pct(h.vol.eval.naiveCoverage, 0)} · ${h.vol.eval.independent} independent periods`} />
        </div>
      )}
      {h.vol && <p className="small muted">{h.vol.note}</p>}
      <div className="row"><Link className="btn" to={`/journal?symbol=${encodeURIComponent(report.symbol)}&stop=${h.risk.stopLoss.toPrecision(6)}&target=${h.risk.takeProfit.toPrecision(6)}&signal=${encodeURIComponent(`${h.label}: ${h.signal}`)}`}>Record a paper trade with this plan →</Link></div>
      <table>
        <thead><tr><th>Outcome percentile</th>{['p05', 'p10', 'p25', 'p50', 'p75', 'p90', 'p95'].map((k) => <th key={k} className="r">{k.replace('p', '')}th</th>)}</tr></thead>
        <tbody>
          <tr><td className="muted">Return</td>{['p05', 'p10', 'p25', 'p50', 'p75', 'p90', 'p95'].map((k) => <td key={k} className={`num r ${tone(h.returnRange[k])}`}>{spct(h.returnRange[k])}</td>)}</tr>
          <tr><td className="muted">Price</td>{['p05', 'p10', 'p25', 'p50', 'p75', 'p90', 'p95'].map((k) => <td key={k} className="num r">{price(h.priceTargets[k])}</td>)}</tr>
        </tbody>
      </table>
    </div>
  )
}

export function NewsPanel({ report, sel }: { report: Report; sel: string }) {
  const [scope, setScope] = useState<'all' | 'asset' | 'market'>('all')
  const arts = (report.news.articles ?? []).filter((a) => scope === 'all' || a.scope === scope)
  const sorted = [...arts].sort((a, b) => Math.abs(b.direction) * b.relevance - Math.abs(a.direction) * a.relevance)
  const agg = report.news.aggregates[sel]
  return (
    <Panel
      title="News & why it matters"
      sub={`${report.news.articles?.length ?? 0} headlines · scored by ${report.news.scoredBy === 'llm' ? report.llmModel : report.news.scoredBy === 'lexicon' ? 'finance lexicon' : 'n/a'}${agg ? ` · net ${sel} effect ${pp(agg.probShift)}` : ''}`}
      actions={
        <select value={scope} onChange={(e) => setScope(e.target.value as typeof scope)}>
          <option value="all">All</option>
          <option value="asset">This asset</option>
          <option value="market">Market & macro</option>
        </select>
      }
    >
      {report.news.error && <div className="callout warn small" style={{ marginBottom: 10 }}>{report.news.error}</div>}
      <div style={{ maxHeight: 640, overflowY: 'auto' }}>
        {sorted.length === 0 && <div className="empty">No recent headlines found.</div>}
        {sorted.map((a) => (
          <div key={a.id} className="article">
            <div className={`dir ${a.direction > 0.1 ? 'up' : a.direction < -0.1 ? 'down' : 'muted'}`} style={{ background: a.direction > 0.1 ? 'var(--up-bg)' : a.direction < -0.1 ? 'var(--down-bg)' : 'var(--none-bg)' }} title="Expected price impact (-1 to +1)">
              {a.direction >= 0 ? '+' : ''}{a.direction.toFixed(2)}
            </div>
            <div>
              <a className="title" href={a.url} target="_blank" rel="noreferrer">{a.title}</a>
              <div className="meta">
                {a.source || 'unknown source'} · {ago(a.published)} · {a.scope === 'asset' ? 'asset-specific' : 'market-wide'} · relevance {pct(a.relevance, 0)} · {a.impact} impact{a.horizon ? ` · ${a.horizon}` : ''}
              </div>
              {a.reasoning ? (
                <div className="reason">{a.reasoning}</div>
              ) : a.scoredBy === 'llm' ? (
                <div className="reason faint">Judged not relevant enough to this asset to explain.</div>
              ) : a.lexHits && a.lexHits.length > 0 ? (
                <div className="reason">Lexicon cues: {a.lexHits.join(', ')}</div>
              ) : null}
            </div>
          </div>
        ))}
      </div>
    </Panel>
  )
}

export function EvidencePanel({ report }: { report: Report }) {
  const keys = report.horizons.filter((h) => h.available).map((h) => h.key)
  if (!report.evidence || report.evidence.length === 0) return null
  return (
    <Panel title="Historical evidence: when this asset was in today's situation before" sub="Each row is a condition that is true today. Compare what happened next with an ordinary day. These are raw counts from this asset's own history, not model output.">
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Condition true today</th>
              {keys.map((k) => <th key={k} className="r">{k}: up-rate (vs normal) · avg move</th>)}
            </tr>
          </thead>
          <tbody>
            {report.evidence.map((e) => (
              <tr key={e.condition}>
                <td>{e.condition}{e.note && <div className="tiny faint">{e.note}</div>}</td>
                {keys.map((k) => {
                  const o = e.byHorizon[k]
                  if (!o) return <td key={k} className="r faint">—</td>
                  const edge = o.upRate - o.allUpRate
                  return (
                    <td key={k} className="num r small">
                      <span className={tone(edge)}>{pct(o.upRate, 0)}</span> <span className="faint">({pct(o.allUpRate, 0)})</span> · <span className={tone(o.avgReturn)}>{spct(o.avgReturn)}</span>
                      <div className="tiny faint">n={o.n.toLocaleString()}</div>
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  )
}

export function CrossAssetDrivers({ ca }: { ca: NonNullable<HorizonForecast['crossAsset']> }) {
  const max = Math.max(0.005, ...(ca.drivers ?? []).map((d) => Math.abs(d.probImpact)))
  return (
    <div style={{ marginTop: 20 }}>
      <h3>
        Cross-asset model: {pct(ca.p)}{' '}
        {ca.used ? <span className="badge up">in the blend</span> : <span className="badge none" title="It joins the forecast only after beating the base rate out-of-sample (p < 0.10)">not used: no proven skill yet</span>}
      </h3>
      <p className="small muted" style={{ margin: '4px 0 10px' }}>
        Trained on {ca.assets} {ca.pool} assets{ca.inSample ? ' (including this one)' : ''}. Inputs are measured relative to each asset's own
        last two years, so the model learns patterns that hold across many assets. Out-of-sample {ca.testFrom} → {ca.testTo}: hit rate{' '}
        {pct(ca.overall.accuracy)} vs {pct(ca.overall.baseAccuracy)} naive, Brier skill {spct(ca.overall.brierSkill, 2)}
        {ca.onAsset ? `; on this asset ${pct(ca.onAsset.accuracy)} hit rate over ${ca.onAsset.n.toLocaleString()} forecasts` : ''}.
      </p>
      {(ca.drivers ?? []).slice(0, 10).map((d) => {
        const w = (Math.abs(d.probImpact) / max) * 50
        const pos = d.probImpact >= 0
        return (
          <div key={d.key} className="driver" style={{ cursor: 'default' }}>
            <div className="lbl">{d.label}<span className="grp">{d.z >= 0 ? '+' : ''}{d.z.toFixed(1)}σ vs its own history</span></div>
            <div className="track"><div className="axis" /><div className="bar" style={{ left: pos ? '50%' : `${50 - w}%`, width: `${w}%`, background: pos ? 'var(--up)' : 'var(--down)' }} /></div>
            <div className={`num small ${pos ? 'up' : 'down'}`} style={{ textAlign: 'right' }}>{pp(d.probImpact, Math.abs(d.probImpact) < 0.001 ? 2 : 1)}</div>
          </div>
        )
      })}
    </div>
  )
}

export function EarningsPanel({ report }: { report: Report }) {
  const e = report.earnings!
  if (!e.covered) return null
  const timing = (t: string) => (t === 'pre' ? 'before open' : t === 'after' ? 'after close' : 'time n/a')
  return (
    <Panel title="Earnings" sub={e.note || (e.coveredFrom ? `Reports since ${e.coveredFrom} from the Nasdaq earnings calendar` : '')}>
      <div className="stack">
        <div className="grid grid-4">
          <Stat label="Next report" value={e.next ? e.next.date : '—'} hint={e.next ? `${timing(e.next.time)} · ${e.next.barsAway} trading day${e.next.barsAway === 1 ? '' : 's'} away${e.next.epsForecast != null ? ` · EPS est. $${e.next.epsForecast.toFixed(2)}` : ''}` : 'none scheduled in the next ~6 weeks'} />
          <Stat label="Typical earnings move" value={e.reactions ? `±${pct(e.avgAbsMove)}` : '—'} hint={e.reactions ? `vs ±${pct(e.normalMove, 2)} on a normal day · ${e.reactions} reports` : ''} />
          <Stat label="Beat rate" value={e.reactions ? pct(e.beatRate, 0) : '—'} hint="share of reports above consensus EPS" />
          <Stat label="Drift after beat / miss" value={e.driftBeat != null || e.driftMiss != null ? `${spct(e.driftBeat)} / ${spct(e.driftMiss)}` : '—'} hint="average 20-day move after the reaction" />
        </div>
        {e.history.length > 0 && (
          <table>
            <thead><tr><th>Date</th><th>Timing</th><th className="r">EPS</th><th className="r">Consensus</th><th className="r">Surprise</th><th className="r">Price reaction</th><th className="r">Next 20 days</th></tr></thead>
            <tbody>
              {e.history.map((x) => (
                <tr key={x.date}>
                  <td className="mono">{x.date}</td>
                  <td className="small muted">{timing(x.time)}</td>
                  <td className="num r">{x.eps != null ? x.eps.toFixed(2) : '—'}</td>
                  <td className="num r muted">{x.forecast != null ? x.forecast.toFixed(2) : '—'}</td>
                  <td className={`num r ${tone(x.surprisePct ?? 0)}`}>{x.surprisePct != null ? `${x.surprisePct >= 0 ? '+' : ''}${x.surprisePct.toFixed(1)}%` : '—'}</td>
                  <td className={`num r ${tone(x.reaction)}`}>{spct(x.reaction)}</td>
                  <td className={`num r ${tone(x.drift20 ?? 0)}`}>{spct(x.drift20)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </Panel>
  )
}

export function OptionsPanel({ report }: { report: Report }) {
  const exps = report.options ?? []
  return (
    <Panel title="Options market" sub="What option prices say about the size of the move ahead (direction-neutral). From today's at-the-money straddles: call + put at the strike nearest the price. A live snapshot only, so it is not a model input.">
      <div className="table-wrap" style={{ maxHeight: 340, overflowY: 'auto' }}>
        <table>
          <thead><tr><th>Expiry</th><th className="r">Strike</th><th className="r">Straddle</th><th className="r">Expected move (±)</th><th className="r">Implied volatility</th></tr></thead>
          <tbody>
            {exps.map((e) => (
              <tr key={e.date}>
                <td className="mono">{e.date}</td>
                <td className="num r">{price(e.strike)}</td>
                <td className="num r">{price(e.straddle)}</td>
                <td className="num r">{pct(e.impliedMove)}</td>
                <td className="num r">{pct(e.impliedVol, 0)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="tiny faint" style={{ marginTop: 6 }}>A jump in expected move around a date usually marks an event the market is pricing, most often earnings.</p>
    </Panel>
  )
}
