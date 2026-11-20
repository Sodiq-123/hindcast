import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type Today as TodayData, type TodayRange, type TodayStep } from '../api'
import { useMe } from '../auth'
import { Loading, SearchBox } from '../components'
import { ago, price } from '../format'
import { SuggestionCard } from '../learn'

type H = TodayRange['key']
const HORIZONS: H[] = ['1d', '1w', '1m', '1y']
const LABEL: Record<H, string> = { '1d': 'Day', '1w': 'Week', '1m': 'Month', '1y': 'Year' }
const WHEN: Record<H, string> = { '1d': 'tomorrow', '1w': 'in a week', '1m': 'in a month', '1y': 'in a year' }

/** Signed percentage with a real minus sign: 0.043 -> "+4.3%". */
const sp = (x: number) => {
  const v = Math.abs(x * 100)
  return `${x > 0 ? '+' : x < 0 ? '−' : ''}${v.toFixed(v >= 10 || v === 0 ? 0 : 1)}%`
}
const money = (x: number, cur: string) => (cur === 'USD' || !cur ? `$${price(x)}` : `${price(x)} ${cur}`)
const tokenMoney = (x: number) => (!x ? '—' : x >= 1 ? `$${x.toFixed(2)}` : `$${x.toFixed(Math.min(12, 3 - Math.floor(Math.log10(x))))}`)

function Change({ x }: { x: number }) {
  if (!x) return <span className="muted">0.0%</span>
  return <span className={x > 0 ? 'up' : 'down'}>{x > 0 ? '▲' : '▼'} {sp(x).replace(/^[+−]/, '')}</span>
}

function greeting(d: Date) {
  const h = d.getHours()
  return h < 12 ? 'Good morning' : h < 18 ? 'Good afternoon' : 'Good evening'
}

export default function Today() {
  const me = useMe()
  const [data, setData] = useState<TodayData | null>(null)
  const [err, setErr] = useState('')
  const [h, setH] = useState<H>(me.settings.horizon)

  const load = useCallback(() => api.today().then(setData).catch((e) => setErr((e as Error).message)), [])
  useEffect(() => {
    load()
    const id = setInterval(load, 60_000)
    return () => clearInterval(id)
  }, [load])

  const [now] = useState(() => new Date())
  const first = (me.user.name || '').split(' ')[0]
  return (
    <>
      <div className="today-head">
        <div>
          <p className="date">{now.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' })}</p>
          <h1>{greeting(now)}{first ? `, ${first}` : ''}</h1>
        </div>
        <div className="hide-narrow"><SearchBox placeholder="Search a stock, coin or address" /></div>
      </div>
      {err && <div className="callout bad">{err}</div>}
      <SuggestionCard />
      {!data ? <Loading label="Putting your day together…" /> : (
        <>
          <Briefing data={data} />
          <OneThing step={data.step} onChange={load} />
          {data.rows.length > 0 && <Ranges data={data} h={h} setH={setH} simple={me.settings.level === 'beginner'} />}
          {data.tokens.length > 0 && <Tokens data={data} />}
        </>
      )}
    </>
  )
}

function Briefing({ data }: { data: TodayData }) {
  if (data.lines.length === 0) return null
  const sub = [
    data.lastSeen ? `You were last here ${ago(data.lastSeen)}` : '',
    data.asOf ? `newest analysis ${ago(data.asOf)}` : '',
  ].filter(Boolean).join(' · ')
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>{data.title}</h2>
          {sub && <div className="sub">{sub.charAt(0).toUpperCase() + sub.slice(1)}</div>}
        </div>
      </div>
      <div className="panel-body">
        <div className={`brief ${data.lines.length <= 2 ? 'one' : ''}`}>
          {data.lines.map((l, i) => (
            <Line key={i} l={l} />
          ))}
        </div>
      </div>
    </section>
  )
}

function Line({ l }: { l: TodayData['lines'][number] }) {
  return (
    <>
      <span className={`tag t-${l.tone}`}>{l.tag}</span>
      <span>
        {l.strong && <strong>{l.strong}</strong>} {l.text}{' '}
        {l.link && <Link className="more" to={l.link}>Details</Link>}
      </span>
    </>
  )
}

function OneThing({ step, onChange }: { step: TodayStep | null; onChange: () => void }) {
  const [busy, setBusy] = useState(false)
  const act = async (action: 'skip' | 'undo') => {
    if (!step) return
    setBusy(true)
    await api.todayStep(step.id, action).catch(() => {})
    await onChange()
    setBusy(false)
  }
  if (!step) {
    return (
      <section className="onething calm">
        <div className="top">
          <span className="n" aria-hidden="true">✓</span>
          <p className="tag label show-phone">One thing to do today</p>
        </div>
        <div className="body">
          <p className="tag label hide-phone">One thing to do today</p>
          <h2>Nothing needs you today.</h2>
          <p>Your list is re-analysed every morning, and this space will tell you when something's worth your time. Checking less often is a good habit too.</p>
        </div>
      </section>
    )
  }
  if (step.skipped) {
    return (
      <div className="skipped-note" role="status">
        <span>Skipped for today: “{step.title.replace(/\.$/, '')}”. It won't come back today.</span>
        <button className="linkish" disabled={busy} onClick={() => act('undo')} style={{ minHeight: 40, padding: '0 10px' }}>Undo</button>
      </div>
    )
  }
  return (
    <section className="onething" aria-labelledby="onething-title">
      <div className="top">
        <span className="n" aria-hidden="true">1</span>
        <p className="tag label show-phone">One thing to do today</p>
      </div>
      <div className="body">
        <p className="tag label hide-phone">One thing to do today</p>
        <h2 id="onething-title">{step.title}</h2>
        <p>{step.body}</p>
      </div>
      <div className="acts">
        <Link to={step.link} className="btn primary" onClick={() => { api.todayStep(step.id, 'done').catch(() => {}) }}>{step.action}</Link>
        <button disabled={busy} onClick={() => act('skip')}>Not today</button>
      </div>
    </section>
  )
}

function Ranges({ data, h, setH, simple }: { data: TodayData; h: H; setH: (h: H) => void; simple: boolean }) {
  const rows = data.rows.map((r) => ({ r, g: r.ranges.find((x) => x.key === h) }))
  const scale = useMemo(() => {
    const live = rows.flatMap(({ g }) => (g && g.available ? [g] : []))
    let lo = Math.min(0, ...live.map((g) => g.p10))
    let hi = Math.max(0, ...live.map((g) => g.p90))
    if (hi - lo < 1e-6) { lo = -0.01; hi = 0.01 }
    const pad = (hi - lo) * 0.04
    return { lo: lo - pad, hi: hi + pad }
  }, [rows])
  const pos = (x: number) => ((x - scale.lo) / (scale.hi - scale.lo)) * 100
  return (
    <section className="panel">
      <div className="panel-head ranges-head">
        <div>
          <h2>Where each could be {WHEN[h]}</h2>
          <div className="sub">
            {simple
              ? 'The shaded span is where 8 in 10 similar past cases ended up. A wide span means less certainty.'
              : 'Shaded: 10th–90th percentile of the conditional return distribution. Dot: median. Right: P(higher) vs base rate.'}
          </div>
        </div>
        <div className="seg" role="tablist" aria-label="How far ahead">
          {HORIZONS.map((k) => (
            <button key={k} role="tab" aria-selected={k === h} className={k === h ? 'on' : ''} onClick={() => setH(k)}>{LABEL[k]}</button>
          ))}
        </div>
      </div>
      <div className="panel-body">
        <div className="rrow axis-row" aria-hidden="true">
          <span className="gutter" />
          <div style={{ position: 'relative', height: 16 }}>
            <span style={{ position: 'absolute', left: 0 }}>{sp(scale.lo)}</span>
            <span style={{ position: 'absolute', left: `${pos(0)}%`, transform: 'translateX(-50%)' }}>0%</span>
            <span style={{ position: 'absolute', right: 0 }}>{sp(scale.hi)}</span>
          </div>
          <span className="gutter">Chance it's higher</span>
        </div>
        <div className="ranges">
          {rows.map(({ r, g }) => {
            const tone = g?.status === 'validated' ? (g.signal.includes('BUY') ? 'up' : g.signal.includes('SELL') ? 'down' : '') : ''
            return (
              <Link key={r.symbol} to={`/asset/${encodeURIComponent(r.symbol)}`} className="rrow">
                <div style={{ minWidth: 0 }}>
                  <span className="nm">{r.name}</span>
                  <span className="px">{r.analysed ? <>{money(r.price, r.currency)} <Change x={r.change1d} /></> : 'Being analysed…'}</span>
                </div>
                {g && g.available ? (
                  <div className="track" role="img" aria-label={`${r.name}: likely between ${sp(g.p10)} and ${sp(g.p90)} ${WHEN[h]}, middle ${sp(g.p50)}`}>
                    <div className="line" />
                    <div className="zero" style={{ left: `${pos(0)}%` }} />
                    <div className="span" style={{ left: `${pos(g.p10)}%`, width: `${pos(g.p90) - pos(g.p10)}%` }} />
                    <div className="mid" style={{ left: `${pos(g.p50)}%` }} />
                  </div>
                ) : (
                  <div className="track na">{r.analysed ? (g?.reason || 'Not enough history for this horizon') : 'Appears after its first analysis'}</div>
                )}
                <div className="right">
                  {g && g.available ? (
                    <>
                      <div className="pp">{Math.round(g.pUp * 100)}% <small>usually {Math.round(g.base * 100)}%</small></div>
                      <div className={`vd ${tone}`}>{tone === 'up' ? '▲ ' : tone === 'down' ? '▼ ' : ''}{g.verdict}</div>
                    </>
                  ) : <span className="faint">—</span>}
                </div>
              </Link>
            )
          })}
        </div>
        <div className="legend">
          <span><i className="sw-band" />8 in 10 outcomes</span>
          <span><i className="sw-mid" />Middle outcome</span>
          <span><i className="sw-zero" />No change</span>
          <span style={{ flex: 1 }} />
          <Link to="/watchlist">Manage watchlist</Link>
        </div>
      </div>
    </section>
  )
}

function Tokens({ data }: { data: TodayData }) {
  return (
    <section className="panel">
      <div className="panel-head">
        <div>
          <h2>Meme coins &amp; DEX tokens</h2>
          <div className="sub">No forecast for these: they're too new to test one. What matters is the safety check.</div>
        </div>
      </div>
      <div className="panel-body stack" style={{ gap: 4 }}>
        {data.tokens.map((t) => (
          <div key={t.address} className="tokrow">
            <div style={{ minWidth: 0 }}>
              <span className="nm" style={{ fontWeight: 600 }}>{t.name || t.symbol}</span>
              <div className="px mono muted small">{tokenMoney(t.priceUsd)} {t.change24h ? <Change x={t.change24h} /> : null}</div>
            </div>
            <div className="what row" style={{ gap: 14, flexWrap: 'nowrap' }}>
              <div className={`risk-meter l${t.level}`} role="img" aria-label={`Safety: ${t.label}, ${t.level} of 4`}>
                {[1, 2, 3, 4].map((i) => <span key={i} className={i <= t.level ? 'on' : ''} />)}
              </div>
              <span className="small"><strong>{t.label}.</strong>{t.top10 >= 0.3 ? ` Top 10 wallets hold ${Math.round(t.top10 * 100)}%.` : ''}</span>
            </div>
            <Link to={`/token/${t.address}`} className="small" style={{ whiteSpace: 'nowrap' }}>Safety check</Link>
          </div>
        ))}
      </div>
    </section>
  )
}
