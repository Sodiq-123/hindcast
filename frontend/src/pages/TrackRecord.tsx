import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { useIsAdmin } from '../auth'
import { api, track, type Prediction, type TrackRecord as TR, type TrackStats } from '../api'
import { CalibrationChart } from '../charts'
import { Loading } from '../components'
import { pct, spct, tone } from '../format'
import { verdict } from './Dashboard'

const H_LABEL: Record<string, string> = { '1d': 'Next day', '1w': 'Next week', '1m': 'Next month', '1y': 'Next year' }
const ENOUGH = 30 // fewer scored forecasts than this tells you little
const naive = (s: TrackStats) => Math.max(s.baseRate, 1 - s.baseRate)
const guessWord = (s: TrackStats) => (s.baseRate >= 0.5 ? 'up' : 'down')
const day = (d: string) => new Date(d.length === 10 ? d + 'T12:00:00Z' : d).toLocaleDateString(undefined, { day: 'numeric', month: 'short' })

export default function TrackRecord() {
  const [rec, setRec] = useState<TR | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const isAdmin = useIsAdmin()
  const load = useCallback(() => api.trackRecord().then(setRec).catch((e) => setErr(e.message)), [])
  useEffect(() => {
    load()
  }, [load])
  useEffect(() => track('page', 'track-record'), [])

  const resolve = async () => {
    setBusy(true)
    try {
      await api.resolve()
      await load()
    } finally {
      setBusy(false)
    }
  }

  if (err) return <div className="callout bad">{err}</div>
  if (!rec) return <Loading label="Loading track record…" />
  return (
    <div className="stack" style={{ gap: 20 }}>
      <header className="asset-head">
        <div style={{ maxWidth: 760 }}>
          <h1 style={{ fontSize: 36 }}>Track record</h1>
          <p className="answer-more" style={{ marginTop: 8 }}>Every forecast is saved unchanged and scored when its date arrives. This is the real test: unlike a backtest, it can’t be tuned after the fact.</p>
        </div>
        {isAdmin && <button onClick={resolve} disabled={busy}>{busy ? 'Scoring…' : 'Score due forecasts now'}</button>}
      </header>

      {rec.total === 0 ? (
        <section className="panel answer">
          <div className="answer-top">
            <div style={{ maxWidth: 820 }}>
              <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
              <h2 className="answer-say">Nothing scored yet.</h2>
              <p className="answer-more">{rec.open} forecasts are waiting for their dates. Next-day forecasts are scored after one trading day, weekly ones after a week, and so on. Give it a few weeks, and a few hundred scored forecasts, before drawing conclusions.</p>
            </div>
          </div>
        </section>
      ) : (
        <>
          <Answer rec={rec} />
          <div className="grid-2-even">
            <section className="panel pad-card stack" style={{ gap: 12 }}>
              <div>
                <h2>By how well the model was tested</h2>
                <p className="small muted" style={{ marginTop: 4 }}>The bar is how often it was right; the line is how often always guessing the more common direction was right.</p>
              </div>
              {(['validated', 'weak', 'no_edge'] as const).filter((k) => rec.byStatus[k]).map((k) => (
                <HitRow key={k} label={k === 'validated' ? '✓ Tested edge' : k === 'weak' ? '◐ Weak evidence' : '○ Not proven'} cls={k === 'validated' ? 'up' : k === 'weak' ? 'hold' : 'faint'} s={rec.byStatus[k]} />
              ))}
            </section>
            <section className="panel pad-card stack" style={{ gap: 12 }}>
              <div>
                <h2>By horizon</h2>
                <p className="small muted" style={{ marginTop: 4 }}>All forecasts, whatever their test result.</p>
              </div>
              {['1d', '1w', '1m', '1y'].filter((k) => rec.byHorizon[k]).map((k) => <HitRow key={k} label={H_LABEL[k]} s={rec.byHorizon[k]} />)}
            </section>
          </div>

          <div className="grid-2-even">
            <section className="panel pad-card">
              <h2>When it said 60%, did it happen 60% of the time?</h2>
              <p className="small muted" style={{ margin: '4px 0 10px' }}>Each circle is a group of forecasts: how likely they said a rise was, against how often it rose. On the dashed line is perfect; bigger circles hold more forecasts.</p>
              <CalibrationChart buckets={rec.calibration} />
            </section>
            <div className="stack" style={{ gap: 20 }}>
              <section className="panel pad-card">
                <h2>Does the news help?</h2>
                <p style={{ marginTop: 8, fontSize: 15, lineHeight: 1.6 }}>
                  {rec.withNews.brier < rec.modelOnly.brier - 0.0005 ? <><strong>Yes, a little, so far.</strong> </> : <><strong>Not yet.</strong> </>}
                  <span className="muted">The same forecasts scored with and without the news adjustment: {rec.withNews.brier.toFixed(4)} with news against {rec.modelOnly.brier.toFixed(4)} without (lower is better); right {pct(rec.withNews.hitRate)} against {pct(rec.modelOnly.hitRate)}.</span>
                </p>
              </section>
              <section className="panel pad-card">
                <h2>Live drift check</h2>
                {rec.drift && rec.drift.length > 0 ? (
                  <>
                    <p className="small muted" style={{ margin: '4px 0 10px' }}>These were switched to “no proven edge” because their recent live forecasts scored worse than simply predicting the usual rate. The live record outranks the backtest.</p>
                    {rec.drift.map((d) => (
                      <p key={d.symbol + d.horizon} className="small" style={{ padding: '6px 0', borderTop: '1px solid var(--border)' }}>
                        <Link to={`/asset/${encodeURIComponent(d.symbol)}`}><strong>{d.symbol}</strong></Link> {H_LABEL[d.horizon]?.toLowerCase()} · {d.n} live forecasts, right {pct(d.hitRate)} · score {d.brier.toFixed(3)} against {d.baseBrier.toFixed(3)} · {d.firstAsOf} → {d.lastAsOf}
                      </p>
                    ))}
                  </>
                ) : (
                  <p style={{ marginTop: 8, fontSize: 15, lineHeight: 1.6 }}>
                    <strong>Nothing switched off.</strong> <span className="muted">A tested signal is demoted when its last 30–100 live forecasts score worse than simply predicting the usual rate. That needs about 2 weeks of daily forecasts for a day ahead and 10 weeks for a week ahead.</span>
                  </p>
                )}
              </section>
            </div>
          </div>

          <Preds title="Recently scored" preds={rec.recent ?? []} resolved />
        </>
      )}
      <Preds title="Waiting to be scored" preds={rec.pending ?? []} folded />
    </div>
  )
}

function Answer({ rec }: { rec: TR }) {
  const o = rec.overall
  const v = rec.byStatus.validated
  const ne = rec.byStatus.no_edge
  const vn = v?.n ?? 0
  let say: string
  let more: string
  if (vn < ENOUGH) {
    say = vn === 0 ? 'No forecast from a tested model has been scored yet.' : 'Too early to judge the forecasts that matter.'
    more = `${vn ? `Only ${vn} forecast${vn === 1 ? '' : 's'} from a model that passed testing ${vn === 1 ? 'has' : 'have'} been scored: too few to tell anything. ` : ''}Across all ${rec.total.toLocaleString()} scored forecasts, ${pct(o.hitRate)} were right${o.hitRate < naive(o) ? `, less than always guessing “${guessWord(o)}” (${pct(naive(o))})` : ''}.${ne ? ` Most of them (${ne.n.toLocaleString()}) came with “no proven edge”, which the app already says not to act on.` : ''}`
  } else {
    const better = v.hitRate > naive(v) && v.brier < 0.25
    say = better ? 'Tested forecasts are beating guessing, live.' : 'Tested forecasts are not beating guessing, live.'
    more = `${vn.toLocaleString()} forecasts from models that passed testing were right ${pct(v.hitRate)} of the time, against ${pct(naive(v))} for always guessing “${guessWord(v)}”. ${better ? 'Keep watching: a few hundred more will make this firmer.' : 'If this lasts, the drift check below switches those signals off.'}`
  }
  const brierWord = o.brier < 0.245 ? 'better than a coin flip' : o.brier <= 0.255 ? 'about a coin flip' : 'worse than a coin flip'
  return (
    <section className="panel answer">
      <div className="answer-top">
        <div style={{ maxWidth: 840 }}>
          <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
          <h2 className="answer-say">{say}</h2>
          <p className="answer-more">{more}</p>
        </div>
      </div>
      <div className="answer-stats four">
        <div>
          <p className="tag faint">Scored</p>
          <p className="mono big">{rec.total.toLocaleString()}</p>
          <p className="small muted">{rec.open.toLocaleString()} still waiting</p>
        </div>
        <div>
          <p className="tag faint">From tested models</p>
          <p className="mono big">{vn}</p>
          <p className="small muted">{vn ? `right ${pct(v.hitRate)} · need ${ENOUGH}+ to judge` : `need ${ENOUGH}+ to judge`}</p>
        </div>
        <div>
          <p className="tag faint">All forecasts right</p>
          <p className="mono big">{pct(o.hitRate)}</p>
          <p className="small muted">guessing “{guessWord(o)}”: {pct(naive(o))}</p>
        </div>
        <div>
          <p className="tag faint">Accuracy score</p>
          <p className="mono big">{o.brier.toFixed(3)}</p>
          <p className="small muted">{brierWord} (0.250) · lower is better</p>
        </div>
      </div>
    </section>
  )
}

function HitRow({ label, s, cls }: { label: string; s: TrackStats; cls?: string }) {
  const n = naive(s)
  const few = s.n < ENOUGH
  return (
    <div className="hit-row">
      <span className={cls ?? ''} style={{ fontWeight: 500 }}>{label}</span>
      <span className="hit-bar" role="img" aria-label={`Right ${pct(s.hitRate)}; guessing ${pct(n)}`}>
        <i style={{ width: `${s.hitRate * 100}%`, opacity: few ? 0.45 : 1 }} />
        <b style={{ left: `${n * 100}%` }} title={`Always guessing “${guessWord(s)}”: ${pct(n)}`} />
      </span>
      <span className="mono small">{pct(s.hitRate)} <span className="faint">vs {pct(n)}</span></span>
      <span className="small faint">{s.n.toLocaleString()}{few ? ' · too few' : ''}</span>
    </div>
  )
}

function Preds({ title, preds, resolved, folded }: { title: string; preds: Prediction[]; resolved?: boolean; folded?: boolean }) {
  const [open, setOpen] = useState(!folded)
  const [all, setAll] = useState(false)
  if (preds.length === 0) return null
  const shown = all ? preds : preds.slice(0, 12)
  const next = folded ? preds.reduce((a, p) => (p.targetDate < a ? p.targetDate : a), preds[0].targetDate) : ''
  const today = new Date().toISOString().slice(0, 10)
  const due = folded ? preds.filter((p) => p.targetDate < today).length : 0
  return (
    <section className="panel" style={{ overflow: 'hidden' }}>
      <div className="panel-head">
        <div>
          <h2>{title} ({preds.length.toLocaleString()})</h2>
          {folded && <p className="sub">{due > 0 ? `${due} are already due and will be scored at the next run.` : `The next ones are due ${day(next)}.`}</p>}
        </div>
        {folded && <button className="linkish" onClick={() => setOpen(!open)} aria-expanded={open}>{open ? 'Hide' : 'Show'}</button>}
      </div>
      {open && (
        <>
          {shown.map((p) => {
            const r = p.realizedReturn ?? 0
            const right = p.signal.includes('SELL') ? r < 0 : p.signal.includes('BUY') ? r > 0 : (p.pUp > 0.5) === (r > 0)
            const v = verdict(p.signal, p.status)
            return (
              <div key={p.id} className="pred-row">
                <span className="small muted">{day(p.createdAt)}</span>
                <Link to={`/asset/${encodeURIComponent(p.symbol)}`} className="mono"><strong>{p.symbol}</strong></Link>
                <span className="small">{H_LABEL[p.horizon] ?? p.horizon} · <span className={v.cls}>{v.mark} {v.text}</span></span>
                <span className="mono small">{pct(p.pUp, 0)} higher</span>
                {resolved ? (
                  <span className="r">
                    <span className={`mono small ${tone(r)}`}>{spct(p.realizedReturn)}</span>{' '}
                    <span className={`chip-sm ${right ? 'up' : 'down'}`}>{right ? '✓ right' : '✗ wrong'}</span>
                  </span>
                ) : (
                  <span className="small faint r">due {day(p.targetDate)}</span>
                )}
              </div>
            )
          })}
          {preds.length > 12 && <div className="rk-foot"><button className="linkish" onClick={() => setAll(!all)}>{all ? 'Show fewer' : `Show all ${preds.length.toLocaleString()}`}</button></div>}
        </>
      )}
    </section>
  )
}
