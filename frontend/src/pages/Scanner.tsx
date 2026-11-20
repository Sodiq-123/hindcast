import { useEffect, useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { api, type RankModel, type ScanItem, type ScanProgress, type ScanResult, type StrategyResults } from '../api'
import { Loading } from '../components'
import { pct, spct, tone } from '../format'
import { useIsAdmin } from '../auth'
import { H_AHEAD, ModelStatus, RankingCard, TestedGrid, UNIVERSE_ADJ, UNIVERSE_LABELS, UNIVERSE_ORDER, useRankModels } from './Rankings'
import Strategies from './Strategies'
import { verdict } from './Dashboard'

const SCAN_UNIVERSES: Record<string, string> = {
  us_large_caps: 'US large caps',
  global: 'ETFs and sectors',
  international: 'International leaders',
  crypto: 'Crypto',
  commodities: 'Commodities',
}
const ORDER = ['1w', '1d', '1m', '1y'] // which tested horizon to open first
const joinOr = (xs: string[]) => (xs.length <= 1 ? xs.join('') : `${xs.slice(0, -1).join(', ')} or ${xs[xs.length - 1]}`)
const joinAnd = (xs: string[]) => (xs.length <= 1 ? xs.join('') : `${xs.slice(0, -1).join(', ')} and ${xs[xs.length - 1]}`)

export default function Scanner() {
  const models = useRankModels()
  const [params] = useSearchParams()
  const [pick, setPick] = useState<RankModel | null>(null)
  const [scan, setScan] = useState<ScanResult | null | undefined>(undefined)
  const [strat, setStrat] = useState<StrategyResults | null>(null)
  useEffect(() => {
    api.latestScan().then(setScan).catch(() => setScan(null))
    api.strategies().then(setStrat).catch(() => {})
  }, [])

  // The ranking to show: a deep link, else the first tested one, else stocks a month ahead.
  const sel = useMemo(() => {
    if (!models || models.length === 0) return undefined
    if (pick) return pick
    const want = models.find((m) => m.universe === params.get('universe') && m.horizon === (params.get('h') ?? '1m'))
    if (want) return want
    for (const h of ORDER) {
      const m = models.find((x) => x.validation?.status === 'validated' && x.horizon === h)
      if (m) return m
    }
    return models.find((m) => m.universe === 'stocks' && m.horizon === '1m') ?? models[0]
  }, [models, pick, params])

  return (
    <div className="stack" style={{ gap: 20 }}>
      <header style={{ maxWidth: 760 }}>
        <h1 style={{ fontSize: 36 }}>Opportunities</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>Where the models see an edge today, and where they don’t. Only a tested edge is worth acting on.</p>
      </header>

      {!models ? (
        <Loading label="Loading rankings…" />
      ) : (
        <section className="panel answer">
          <div className="opp-top">
            <ShortAnswer models={models} scan={scan} strat={strat} />
            {models.length > 0 && (
              <div>
                <p className="tag faint" style={{ marginBottom: 10 }}>Rankings tested so far · pick one</p>
                <TestedGrid models={models} sel={sel} onPick={setPick} />
                <p className="tiny faint" style={{ marginTop: 10 }}>✓ tested edge · ◐ weak evidence · ○ not proven</p>
              </div>
            )}
          </div>
        </section>
      )}

      {models && models.length === 0 && (
        <div className="callout info">No ranking models yet. They train automatically on first start and then daily. The first training downloads about 110 instruments and takes a few minutes.</div>
      )}
      {sel && <RankingCard key={`${sel.universe}-${sel.horizon}`} m={sel} />}

      <div className="grid-2-even">
        <ScanCard scan={scan} onScan={setScan} />
        <Strategies data={strat} />
      </div>

      <details className="panel pad-card">
        <summary className="small muted" style={{ cursor: 'pointer' }}>Models and data: training, earnings history, cross-asset direction models</summary>
        <div style={{ marginTop: 14 }}><ModelStatus /></div>
      </details>
    </div>
  )
}

function ShortAnswer({ models, scan, strat }: { models: RankModel[]; scan: ScanResult | null | undefined; strat: StrategyResults | null }) {
  const tested = new Map<string, string[]>()
  for (const m of models) {
    if (m.validation?.status !== 'validated') continue
    tested.set(m.universe, [...(tested.get(m.universe) ?? []), m.horizon])
  }
  const buys = scan?.buys?.length ?? 0, avoid = scan?.avoid?.length ?? 0
  const valueOK = strat?.results?.value?.status === 'validated'
  const fibOK = strat?.results?.levels?.status === 'special'
  const NATURAL = ['1d', '1w', '1m', '1y']
  const edges = [...tested.entries()].map(([u, hs]) => `the ${UNIVERSE_ADJ[u] ?? (UNIVERSE_LABELS[u] ?? u).toLowerCase()} ranking, ${joinOr(hs.sort((a, b) => NATURAL.indexOf(a) - NATURAL.indexOf(b)).map((h) => H_AHEAD[h]))} ahead`)
  if (buys + avoid > 0) edges.push(`${buys + avoid} asset${buys + avoid > 1 ? 's' : ''} in the direction scan`)
  if (valueOK) edges.push('the “stronger business, lower price” pattern')
  if (fibOK) edges.push('the 0.618 Fibonacci level')

  const notProven: string[] = []
  const weakUnis = [...new Set(models.map((m) => m.universe))].filter((u) => !tested.has(u)).sort((a, b) => (UNIVERSE_ORDER.indexOf(a) + 1 || 99) - (UNIVERSE_ORDER.indexOf(b) + 1 || 99)).map((u) => UNIVERSE_ADJ[u] ?? (UNIVERSE_LABELS[u] ?? u).toLowerCase())
  if (weakUnis.length) notProven.push(`the ${joinAnd(weakUnis)} rankings`)
  if (scan && buys + avoid === 0) notProven.push('the direction scan')
  if (strat?.results && !valueOK && !fibOK) notProven.push('both strategy tests')

  const say = edges.length === 0 ? 'No tested edge today.' : edges.length === 1 ? `One tested edge today: ${edges[0]}.` : `Tested edges today: ${joinAnd(edges)}.`
  const more = edges.length === 0
    ? 'None of the rankings, the scan or the strategy tests has beaten chance out of sample. Sitting out is a valid position.'
    : `${tested.size > 0 ? 'Out of sample and after costs, it has picked which assets beat the others better than chance. ' : ''}${notProven.length ? `${joinAnd(notProven).replace(/^./, (c) => c.toUpperCase())} ${notProven.length > 1 || notProven[0].endsWith('s') ? 'are' : 'is'} not proven, so ${notProven.length > 1 ? 'they are' : 'it is'} shown for interest only.` : ''}`
  return (
    <div style={{ maxWidth: 640 }}>
      <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
      <h2 className="answer-say">{say}</h2>
      <p className="answer-more">{more}</p>
    </div>
  )
}

function ScanCard({ scan, onScan }: { scan: ScanResult | null | undefined; onScan: (s: ScanResult | null) => void }) {
  const isAdmin = useIsAdmin()
  const [all, setAll] = useState(false)
  const [form, setForm] = useState(false)
  if (scan === undefined) return <section className="panel pad-card"><Loading label="Loading last scan…" /></section>
  const buys = scan?.buys ?? [], avoid = scan?.avoid ?? [], rest = scan?.noEdge ?? []
  const ok = buys.length + avoid.length > 0
  const ahead = scan ? H_AHEAD[scan.horizon] ?? scan.horizon : ''
  return (
    <section className="panel pad-card stack" style={{ gap: 10 }}>
      <div className="row between" style={{ alignItems: 'flex-start', gap: 12 }}>
        <div>
          <p className={`tag ${ok ? 'up' : 'faint'}`} style={{ marginBottom: 6 }}>{ok ? '✓ Tested edge' : '○ Not proven'}</p>
          <h2>Direction scan</h2>
        </div>
        {scan && <span className="small faint nowrap">{new Date(scan.finishedAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })} · {scan.scanned} assets · {ahead} ahead</span>}
      </div>
      {!scan ? (
        <p className="muted">No scan yet. Scans run on the schedule{isAdmin ? ', or start one below' : ''}.</p>
      ) : (
        <>
          <p style={{ fontSize: 15, lineHeight: 1.6 }}>
            {ok ? (
              <>{buys.length > 0 && <>{buys.length} {buys.length === 1 ? 'asset has' : 'assets have'} a tested edge pointing up for {ahead}. </>}{avoid.length > 0 && <>{avoid.length} {avoid.length === 1 ? 'has' : 'have'} one pointing down. </>}</>
            ) : (
              <>None of the {scan.scanned} assets has a tested edge pointing up or down for {ahead}. </>
            )}
            <span className="muted">Each asset’s own model is checked, with a correction for testing so many at once.{!ok && ' Sitting out is a valid position.'}</span>
          </p>
          {[...buys, ...avoid].map((it, i) => <ScanRow key={it.symbol} it={it} n={i + 1} />)}
          {rest.length > 0 && <button className="linkish" style={{ alignSelf: 'flex-start' }} onClick={() => setAll(!all)}>{all ? 'Hide the rest' : `Show ${ok ? `the other ${rest.length}` : `all ${rest.length}`}`}</button>}
          {all && rest.map((it, i) => <ScanRow key={it.symbol} it={it} n={buys.length + avoid.length + i + 1} />)}
          {scan.failed && scan.failed.length > 0 && <p className="tiny faint">{scan.failed.length} failed: {scan.failed.map((f) => f.symbol).join(', ')}</p>}
        </>
      )}
      {isAdmin && (
        <>
          <button className="linkish" style={{ alignSelf: 'flex-start', fontSize: 13 }} onClick={() => setForm(!form)} aria-expanded={form}>{form ? 'Close' : 'Run a new scan'}</button>
          {form && <NewScan onDone={onScan} />}
        </>
      )}
    </section>
  )
}

function ScanRow({ it, n }: { it: ScanItem; n: number }) {
  const v = verdict(it.signal, it.status)
  return (
    <div className="scan-row">
      <span className="mono faint">{n}</span>
      <Link to={`/asset/${encodeURIComponent(it.symbol)}`}><strong>{it.symbol}</strong> <span className="small muted">{it.name}</span></Link>
      <span className={`small ${v.cls}`}>{v.mark} {v.text}</span>
      <span className="mono small r">{pct(it.pUp, 0)} <span className={tone(it.expectedReturn)}>{spct(it.expectedReturn)}</span></span>
    </div>
  )
}

function NewScan({ onDone }: { onDone: (s: ScanResult | null) => void }) {
  const [universes, setUniverses] = useState<Record<string, string[]>>({})
  const [chosen, setChosen] = useState<string[]>(['us_large_caps', 'crypto'])
  const [horizon, setHorizon] = useState('1w')
  const [extra, setExtra] = useState('')
  const [prog, setProg] = useState<ScanProgress | null>(null)
  const [err, setErr] = useState('')
  useEffect(() => {
    api.universes().then(setUniverses).catch(() => {})
    api.scanProgress().then(setProg).catch(() => {})
  }, [])
  useEffect(() => {
    if (!prog?.running) return
    const id = setInterval(async () => {
      const p = await api.scanProgress()
      setProg(p)
      if (!p.running) api.latestScan().then(onDone).catch(() => {})
    }, 2000)
    return () => clearInterval(id)
  }, [prog?.running, onDone])
  const start = async () => {
    setErr('')
    try {
      const syms = extra.split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)
      setProg(await api.startScan(chosen, syms, horizon))
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  const total = chosen.reduce((n, u) => n + (universes[u]?.length ?? 0), 0)
  return (
    <div className="stack" style={{ gap: 10, borderTop: '1px solid var(--border)', paddingTop: 12 }}>
      <div className="row" style={{ flexWrap: 'wrap' }}>
        {Object.keys(universes).map((u) => (
          <label key={u} className="check">
            <input type="checkbox" checked={chosen.includes(u)} onChange={(e) => setChosen(e.target.checked ? [...chosen, u] : chosen.filter((x) => x !== u))} />
            {SCAN_UNIVERSES[u] ?? u} <span className="faint">({universes[u].length})</span>
          </label>
        ))}
      </div>
      <input placeholder="Extra symbols (comma separated)" value={extra} onChange={(e) => setExtra(e.target.value)} />
      <div className="row" style={{ flexWrap: 'wrap' }}>
        <select value={horizon} onChange={(e) => setHorizon(e.target.value)}>
          <option value="1d">Rank by next-day forecast</option>
          <option value="1w">Rank by next-week forecast</option>
          <option value="1m">Rank by next-month forecast</option>
          <option value="1y">Rank by next-year forecast</option>
        </select>
        <button className="primary" disabled={prog?.running || (chosen.length === 0 && !extra.trim())} onClick={start}>
          {prog?.running ? 'Scanning…' : `Scan ${total + extra.split(',').filter((s) => s.trim()).length} instruments`}
        </button>
      </div>
      {prog?.running && (
        <div className="stack" style={{ gap: 6 }}>
          <div className="progress"><div style={{ width: `${(100 * prog.done) / Math.max(1, prog.total)}%` }} /></div>
          <span className="small muted">{prog.done} / {prog.total} · analysing {prog.current}</span>
        </div>
      )}
      {err && <div className="callout bad">{err}</div>}
    </div>
  )
}

