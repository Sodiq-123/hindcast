import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, type ResearchFinding, type ResearchOverview, type ResearchReport, type ResearchSubject, type ResearchView, type TokenReport } from '../api'
import { BackLink, Loading, SearchBox, type SearchPick } from '../components'
import { ago, price } from '../format'

const isCoin = (s: string) => s.startsWith('cg:')
const isAddr = (s: string) => /^0x[0-9a-fA-F]{40}$/.test(s) || /^[1-9A-HJ-NP-Za-km-z]{32,44}$/.test(s)
const short = (s: string) => (isCoin(s) ? s.slice(3) : isAddr(s) ? `${s.slice(0, 4)}…${s.slice(-4)}` : s)
const usd = (x?: number) => (x == null ? '—' : `$${x.toFixed(2)}`)
const KIND: Record<string, string> = { stock: 'Stock', crypto: 'Crypto', token: 'Token' }
const VERDICT: Record<string, [string, string]> = {
  strong: ['up', 'Strong'],
  mixed: ['hold', 'Mixed'],
  weak: ['down', 'Weak'],
  insufficient: ['flat', 'Not enough info'],
}
// Reports made before 19 Nov 2026 have no one-line answer; this stands in.
const FALLBACK: Record<string, string> = {
  strong: 'Strong fundamentals: the evidence about the project is mostly good.',
  mixed: 'Mixed fundamentals: real strengths, and real problems.',
  weak: 'Weak fundamentals: the evidence raises more problems than it settles.',
  insufficient: 'Not enough verified information to judge this one.',
}
const NOT_PRICE = 'This is about the project, not the price. It isn’t advice to buy or sell, and a sound project can still fall in price.'
const MS_DAY = 86400000

const day = (iso: string) => {
  const t = new Date(iso.length === 10 ? iso + 'T12:00:00Z' : iso)
  if (Number.isNaN(t.getTime())) return iso
  return t.toLocaleDateString(undefined, { day: 'numeric', month: 'short', ...(t.getFullYear() !== new Date().getFullYear() ? { year: 'numeric' } : {}) })
}
const daysAgo = (iso: string, now: number) => Math.max(0, Math.floor((now - Date.parse(iso)) / MS_DAY))
const agoDays = (n: number) => (n === 0 ? 'today' : n === 1 ? 'yesterday' : `${n} days ago`)
const money = (x: number, kind: string) => (kind === 'stock' ? price(x) : `$${price(x)}`)

function Since({ from, to }: { from?: number; to?: number }) {
  if (!from || !to) return <span className="faint">—</span>
  const ch = to / from - 1
  if (Math.abs(ch) < 0.0005) return <span className="muted">0.0%</span>
  return <span className={ch > 0 ? 'up' : 'down'}>{ch > 0 ? '▲' : '▼'} {Math.abs(ch * 100).toFixed(1)}%</span>
}

// ---------------------------------------------------------------- home

/** Research home: search, your allowance, and every report so far. */
export function ResearchHome() {
  const [o, setO] = useState<ResearchOverview | null>(null)
  const [now] = useState(() => Date.now())
  const nav = useNavigate()
  useEffect(() => {
    api.researchOverview().then(setO).catch(() => setO({ enabled: false }))
  }, [])
  const open = (p: SearchPick) => nav(`/research/${encodeURIComponent(p.kind === 'asset' ? p.symbol : p.kind === 'coin' ? `cg:${p.id}` : p.address)}`)
  if (!o) return <Loading label="Loading research…" />
  const a = o.allowance
  return (
    <div className="stack" style={{ gap: 22 }}>
      <header style={{ maxWidth: 760 }}>
        <h1 style={{ fontSize: 36 }}>Research</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>
          A sourced report on one stock, coin or token: what it really is, who’s behind it, whether people use it, red flags, and the honest case for and against.
        </p>
      </header>
      <div className="research-search">
        <SearchBox coins placeholder="Research a stock, coin or token address…" onPick={open} />
        {o.enabled && a && (
          <p className="small muted" style={{ marginTop: 10 }}>
            {a.limit >= 0 ? (
              <>You’ve used <strong>{a.used} of {a.limit}</strong> research reports this month. Reading reports is always free.</>
            ) : (
              <>Spent on research this month: <strong>{usd(o.spent)}</strong> of your {usd(o.budget)} budget · about {usd(o.estimate)} a report · {o.model}</>
            )}
          </p>
        )}
      </div>
      {!o.enabled && <div className="callout warn">Research needs <code>ANTHROPIC_API_KEY</code> in your <code>.env</code>.</div>}

      <div className="why-grid research-home">
        <section className="panel" style={{ overflow: 'hidden' }}>
          <div className="panel-head"><div><h2>Reports so far</h2><p className="sub">Fundamentals describe the project, not the price.</p></div></div>
          {!o.subjects || o.subjects.length === 0 ? (
            <div className="empty">Nothing yet. Search above, or press “Research” on any asset or token page.</div>
          ) : (
            o.subjects.map((s) => <SubjectRow key={s.subject} s={s} now={now} />)
          )}
        </section>
        <section className="panel pad-card stack" style={{ gap: 14 }}>
          <h2>How it works</h2>
          <div className="reason"><span className="n">1</span><span>Claude searches the web and reads the pages: the project’s own site and docs, filings, news and forums.</span></div>
          <div className="reason"><span className="n">2</span><span>Every finding links to its source, and it says plainly what it couldn’t check.</span></div>
          <div className="reason"><span className="n">3</span><span>Run it again later and the new report opens with what changed.</span></div>
          <p className="small faint">Takes a few minutes. Runs only when you ask.</p>
        </section>
      </div>
    </div>
  )
}

function SubjectRow({ s, now }: { s: ResearchSubject; now: number }) {
  const [cls, label] = VERDICT[s.fundamentals] ?? ['flat', s.fundamentals]
  return (
    <Link to={`/research/${encodeURIComponent(s.subject)}`} className="rsub">
      <span style={{ minWidth: 0 }}>
        <span className="rsub-name">{s.name || short(s.subject)}</span>
        <span className="mono small faint" style={{ display: 'block', marginTop: 2 }}>{s.kind === 'token' ? short(s.subject) : s.symbol || short(s.subject)} · {KIND[s.kind] ?? s.kind}</span>
        {s.identity && <span className="small down" style={{ display: 'block', marginTop: 4 }}>⚠ {s.identity}</span>}
      </span>
      <span><span className={`chip-sm ${cls}`}>{label}</span></span>
      <span className="small muted nowrap">{day(s.last)} · {agoDays(daysAgo(s.last, now))}</span>
      <span className="mono small r" title={s.priceAt ? `Price on ${day(s.priceAt)} against the price when researched` : undefined}>
        <Since from={s.price} to={s.priceNow} />{s.priceNow ? <span className="faint"> since</span> : null}
      </span>
    </Link>
  )
}

// ---------------------------------------------------------------- one subject

/** One subject's research: the short answer first, then the evidence. */
export default function Research() {
  const { subject = '' } = useParams()
  const [v, setV] = useState<ResearchView | null>(null)
  const [shown, setShown] = useState<ResearchReport | null>(null)
  const [err, setErr] = useState('')
  const [tok, setTok] = useState<TokenReport | null>(null)
  const [confirming, setConfirming] = useState(false)
  const [now] = useState(() => Date.now())
  useEffect(() => {
    setTok(null)
    if (isAddr(subject)) api.token(subject).then(setTok).catch(() => {})
  }, [subject])
  // A token that isn't the listed coin with its ticker is usually a copycat.
  const notOriginal = tok && !tok.listing ? tok : null

  const load = useCallback(() => {
    api
      .research(subject)
      .then((x) => {
        setV(x)
        setShown((cur) => (cur && x.latest && cur.id !== x.latest.id && x.history.some((h) => h.id === cur.id) ? cur : x.latest ?? null))
      })
      .catch((e) => setErr((e as Error).message))
  }, [subject])
  useEffect(() => {
    setShown(null)
    setConfirming(false)
    load()
  }, [load])
  const running = !!v?.job?.running
  useEffect(() => {
    if (!running) return
    const id = setInterval(load, 5000)
    return () => clearInterval(id)
  }, [running, load])

  const start = async () => {
    setErr('')
    try {
      await api.startResearch(subject)
      setConfirming(false)
      load()
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  if (!v) return err ? <div className="callout bad">{err}</div> : <Loading label="Loading research…" />
  const r = shown
  const latest = v.latest
  const name = r?.name ?? tok?.name ?? short(subject)
  const assetLink = isAddr(subject) ? `/token/${subject}` : `/asset/${encodeURIComponent(subject)}`
  const coinURL = isCoin(subject) ? `https://www.coingecko.com/en/coins/${subject.slice(3)}` : ''
  const age = latest ? daysAgo(latest.createdAt, now) : 0
  return (
    <div className="stack" style={{ gap: 20 }}>
      <div className="stack" style={{ gap: 10 }}>
        <BackLink fallback="/research" label="Research" />
        <header className="asset-head">
          <div>
            <div className="row" style={{ alignItems: 'baseline', gap: 12 }}>
              <h1 style={{ fontSize: 36 }}>{name}</h1>
              <span className="mono muted small">{r?.symbol ?? short(subject)} · {KIND[r?.kind ?? (isAddr(subject) ? 'token' : '')] ?? 'Research'}</span>
            </div>
            {r && (
              <p className="small faint" style={{ marginTop: 6 }}>
                Research report, {new Date(r.createdAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })} · {r.cost.searches} web searches, {r.cost.fetches} pages read in full
                {r.cost.seconds > 0 && <>, about {Math.max(1, Math.round(r.cost.seconds / 60))} minutes</>}
              </p>
            )}
          </div>
          <div className="row" style={{ gap: 8 }}>
            {coinURL ? (
              <a className="btn" href={coinURL} target="_blank" rel="noreferrer">CoinGecko ↗</a>
            ) : (
              <Link className="btn" to={assetLink}>{isAddr(subject) ? 'Token safety →' : 'Forecast →'}</Link>
            )}
            <button className="primary" onClick={() => setConfirming(true)} disabled={!v.enabled || running || confirming}>
              {running ? <><span className="spinner" /> Researching…</> : r ? 'Run new research' : 'Run research'}
            </button>
          </div>
        </header>
      </div>

      {notOriginal && <CopycatWarning t={notOriginal} />}
      {!v.enabled && <div className="callout warn">Research needs <code>ANTHROPIC_API_KEY</code> in your <code>.env</code>.</div>}
      {err && <div className="callout bad">{err}</div>}
      {confirming && !running && <ConfirmRun v={v} name={name} hasReport={!!latest} copycat={!!notOriginal} onStart={start} onCancel={() => setConfirming(false)} />}
      {running && v.job && <Progress stage={v.job.stage} started={v.job.started} name={name} />}
      {v.job && !v.job.running && v.job.error && <div className="callout bad">The last research run failed: {v.job.error}</div>}

      {!r ? (
        !running && !confirming && (
          <section className="panel pad-card stack" style={{ gap: 10 }}>
            <h2>No research on {name} yet</h2>
            <p className="muted" style={{ lineHeight: 1.6 }}>
              Press <strong>Run research</strong> and Claude will search and read the web about this {isAddr(subject) ? 'token' : isCoin(subject) ? 'coin' : 'asset'}, then write a report where every finding links to its source.
            </p>
          </section>
        )
      ) : (
        <>
          {r.id !== latest?.id ? (
            <div className="callout warn">
              You’re viewing an older report from {day(r.createdAt)}. <button className="linkish" onClick={() => latest && setShown(latest)}>Back to the latest</button>
            </div>
          ) : (
            age > 30 && !running && !confirming && (
              <section className="earn-band">
                <span>
                  <strong>Researched {day(r.createdAt)}, {age} days ago.</strong>{' '}
                  <span className="muted">
                    A lot can change in a month
                    {v.priceNow && r.price ? <>, and the price has moved <Since from={r.price} to={v.priceNow} /> since</> : null}. Check “What would change this view” below, or{' '}
                    <button className="linkish" onClick={() => setConfirming(true)} disabled={!v.enabled}>run new research</button>.
                  </span>
                </span>
              </section>
            )
          )}
          <ReportView r={r} v={v} />
          <History v={v} shown={r} onPick={(id) => api.researchReport(id).then(setShown)} />
        </>
      )}
    </div>
  )
}

function CopycatWarning({ t }: { t: TokenReport }) {
  const l = t.lookalike
  return (
    <div className="callout bad">
      <strong>⚠ This token isn’t the listed {t.symbol}.</strong>{' '}
      {l?.elsewhere ? (
        <>
          CoinGecko’s {t.symbol} is <strong>{l.name}</strong>{l.rank ? ` (#${l.rank})` : ''}, which isn’t on Solana, Ethereum or Base at all, so this token is a lookalike.{' '}
          <Link to={`/research/cg:${l.id}`}>Research the real {l.name} →</Link>
        </>
      ) : (
        <>
          {l ? <>CoinGecko’s {t.symbol} on {({ solana: 'Solana', ethereum: 'Ethereum', base: 'Base' } as Record<string, string>)[l.chain] ?? l.chain} is {l.name}. </> : 'Nothing independent confirms it is the coin people mean by this name. '}
          If you meant the well-known coin, research <Link to={`/research/${encodeURIComponent(t.symbol.toUpperCase() + '-USD')}`}>{t.symbol.toUpperCase()}-USD</Link> instead
          {l && <> or the <Link to={`/research/${l.address}`}>listed token</Link></>}.
        </>
      )}
    </div>
  )
}

function ConfirmRun({ v, name, hasReport, copycat, onStart, onCancel }: { v: ResearchView; name: string; hasReport: boolean; copycat: boolean; onStart: () => void; onCancel: () => void }) {
  const [busy, setBusy] = useState(false)
  const a = v.allowance
  const member = a && a.limit >= 0
  const left = member ? Math.max(0, a.limit - a.used) : 0
  const out = member && left === 0
  const budgetLeft = (v.budget ?? 0) - (v.spent ?? 0)
  return (
    <section className="panel pad-card stack research-confirm" style={{ gap: 10 }}>
      <p className="tag" style={{ color: 'var(--accent-ink)' }}>{hasReport ? 'New research' : 'Research'}</p>
      <h2 className="display" style={{ fontSize: 22 }}>{hasReport ? `Run new research on ${name}?` : `Research ${name}?`}</h2>
      <p className="muted" style={{ fontSize: 15, lineHeight: 1.6 }}>
        Claude searches and reads the web for a few minutes, then writes a report with sources.
        {hasReport && ' It will open with what has changed since the last one.'}
      </p>
      {copycat && <p className="small down"><strong>⚠ This token isn’t the listed coin with its name.</strong> The report will be about this exact token, not the well-known one.</p>}
      <p style={{ fontSize: 14, lineHeight: 1.5 }}>
        {member ? (
          out ? <>You’ve used all <strong>{a.limit}</strong> of your research reports this month. Reading reports is always free.</>
            : <>Uses <strong>1 of your {a.limit}</strong> research reports this month, leaving {left - 1}. Reading reports is always free.</>
        ) : (
          <>Costs about <strong>{usd(v.estimate ?? 2.5)}</strong>. This month: {usd(v.spent ?? 0)} spent of your {usd(v.budget ?? 0)} budget ({usd(budgetLeft)} left).</>
        )}
      </p>
      <div className="row" style={{ gap: 8, marginTop: 6 }}>
        <button className="primary" disabled={busy || out} onClick={() => { setBusy(true); Promise.resolve(onStart()).finally(() => setBusy(false)) }}>
          {busy ? <><span className="spinner" /> Starting…</> : 'Start research'}
        </button>
        <button onClick={onCancel}>Not now</button>
      </div>
    </section>
  )
}

const STAGES: [string, string, string][] = [
  ['gathering', 'Gathering verified data', 'price, supply, code activity'],
  ['searching', 'Searching and reading the web', 'usually 2–6 minutes'],
  ['writing', 'Writing the report', ''],
]

function Progress({ stage, started, name }: { stage: string; started: string; name: string }) {
  const at = Math.max(0, STAGES.findIndex(([k]) => stage.startsWith(k)))
  return (
    <section className="panel pad-card stack" style={{ gap: 14 }} aria-live="polite">
      <p className="tag" style={{ color: 'var(--accent-ink)' }}>Researching {name} · started {ago(started)}</p>
      {STAGES.map(([k, label, hint], i) => (
        <div key={k} className="rstep">
          <span className={`rdot ${i < at ? 'done' : i === at ? 'now' : ''}`}>{i < at ? '✓' : i + 1}</span>
          <span className={i > at ? 'muted' : ''}>
            {i === at ? <strong>{label}</strong> : label}
            {hint && <span className="small faint"> · {hint}</span>}
          </span>
        </div>
      ))}
      <p className="small muted">You can leave this page. The report is saved when it’s done.</p>
    </section>
  )
}

// ---------------------------------------------------------------- the report

type Src = Map<number, { url: string; title?: string; retrieved: boolean }>

function Cites({ ids, src }: { ids: number[] | null; src: Src }) {
  if (!ids || ids.length === 0) return <span className="small faint"> (unverified)</span>
  return (
    <>
      {ids.map((id) => {
        const s = src.get(id)
        const label = id === 0 ? 'data' : String(id)
        return s?.url ? (
          <a key={id} className="cite" href={s.url} target="_blank" rel="noreferrer" title={s.title || s.url}>{label}</a>
        ) : (
          <span key={id} className="cite" title={s?.title}>{label}</span>
        )
      })}
    </>
  )
}

/** Text with inline "[38, 1, 0]" citations turned into source chips. */
function withCites(text: string, src: Src): ReactNode[] {
  const out: ReactNode[] = []
  let at = 0
  for (const m of text.matchAll(/\s*\[([0-9,\s]+)\]/g)) {
    out.push(text.slice(at, m.index))
    out.push(<Cites key={m.index} ids={m[1].split(',').map((x) => Number(x.trim())).filter((x) => Number.isFinite(x))} src={src} />)
    at = (m.index ?? 0) + m[0].length
  }
  out.push(text.slice(at))
  return out
}

const MARK: Record<string, [string, string]> = { positive: ['up', '▲'], negative: ['down', '▼'], neutral: ['faint', '●'] }

function Finding({ f, src }: { f: ResearchFinding; src: Src }) {
  const [cls, sym] = MARK[f.tone] ?? MARK.neutral
  return (
    <div className="find">
      <span className={`mk ${cls}`} aria-label={f.tone === 'positive' ? 'good' : f.tone === 'negative' ? 'concern' : 'neutral'}>{sym}</span>
      <div>{f.text}<Cites ids={f.sources} src={src} /></div>
    </div>
  )
}

function ReportView({ r, v }: { r: ResearchReport; v: ResearchView }) {
  const [why, setWhy] = useState(false)
  const src: Src = new Map((r.sources ?? []).map((s) => [s.id, s]))
  const sections = [...(r.sections ?? [])].sort((a, b) => (a.key === 'red_flags' ? -1 : b.key === 'red_flags' ? 1 : 0))
  const findings = sections.reduce((n, s) => n + (s.findings ?? []).length, 0)
  const gaps = sections.reduce((n, s) => n + (s.gaps ?? []).length, 0)
  const sources = r.sources ?? []
  const checked = sources.filter((s) => s.retrieved).length
  const latest = r.id === v.latest?.id
  return (
    <>
      {r.changes && r.changes.length > 0 && (
        <section className="panel pad-card stack" style={{ gap: 6 }}>
          <p className="tag" style={{ color: 'var(--accent-ink)' }}>What changed since {r.previousAt ? day(r.previousAt) : 'the last report'}</p>
          {r.changes.map((f, i) => <Finding key={i} f={f} src={src} />)}
        </section>
      )}

      <section className="panel answer">
        <div className="answer-top">
          <div style={{ maxWidth: 820 }}>
            <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
            <h2 className="answer-say">{r.headline || FALLBACK[r.fundamentals] || r.fundamentals}</h2>
            <p className="answer-more">{r.overview}</p>
            <p className="small faint" style={{ marginTop: 10 }}>{NOT_PRICE}</p>
          </div>
        </div>
        <div className="answer-stats">
          <div>
            <p className="tag faint">Fundamentals</p>
            <div className="fmeter" role="img" aria-label={`Fundamentals: ${VERDICT[r.fundamentals]?.[1] ?? r.fundamentals}`}>
              {(['weak', 'mixed', 'strong'] as const).map((k) => <span key={k} className={r.fundamentals === k ? `on ${VERDICT[k][0]}` : ''}>{VERDICT[k][1]}</span>)}
            </div>
            {r.fundamentals === 'insufficient' && <p className="small muted" style={{ marginTop: 4 }}>Not enough verified information</p>}
            <button className="linkish" style={{ marginTop: 8, fontSize: 13 }} onClick={() => setWhy(!why)} aria-expanded={why}>
              {why ? 'Hide why' : `Why ${(VERDICT[r.fundamentals]?.[1] ?? r.fundamentals).toLowerCase()}?`}
            </button>
          </div>
          <div>
            <p className="tag faint">Price since this research</p>
            {latest && v.priceNow && r.price ? (
              <>
                <p className="mono mid"><Since from={r.price} to={v.priceNow} /></p>
                <p className="small muted">{money(r.price, r.kind)} → {money(v.priceNow, r.kind)}{v.priceAt ? ` · latest price ${day(v.priceAt)}` : ''}</p>
              </>
            ) : (
              <>
                <p className="mono mid">{r.price ? money(r.price, r.kind) : '—'}</p>
                <p className="small muted">{latest ? 'No current price found' : 'Price when this report was made'}</p>
              </>
            )}
          </div>
          <div>
            <p className="tag faint">Evidence</p>
            <p className="verdict-sm">{sources.length} sources, {checked === sources.length ? 'all checked' : `${checked} checked`}</p>
            <p className="small muted">{findings} findings · {gaps} {gaps === 1 ? 'thing' : 'things'} it couldn’t check</p>
          </div>
        </div>
        {why && <p className="research-why"><strong>Why {(VERDICT[r.fundamentals]?.[1] ?? r.fundamentals).toLowerCase()}:</strong> {r.fundamentalsReason}</p>}
      </section>

      <div className="grid-2-even">
        <section className="panel pad-card stack" style={{ gap: 12 }}>
          <div><p className="tag up">▲ The case for</p><p className="small muted">The best honest argument for it</p></div>
          {(r.bullCase ?? []).map((f, i) => (
            <div key={i} className="reason"><span className="n" style={{ fontSize: 13, color: 'var(--up)' }}>▲</span><span>{f.text}<Cites ids={f.sources} src={src} /></span></div>
          ))}
        </section>
        <section className="panel pad-card stack" style={{ gap: 12 }}>
          <div><p className="tag down">▼ The case against</p><p className="small muted">The best honest argument against it</p></div>
          {(r.bearCase ?? []).map((f, i) => (
            <div key={i} className="reason"><span className="n" style={{ fontSize: 13, color: 'var(--down)' }}>▼</span><span>{f.text}<Cites ids={f.sources} src={src} /></span></div>
          ))}
        </section>
      </div>

      <div className="why-grid">
        {r.hypeCheck && (
          <section className="panel pad-card">
            <p className="tag hold" style={{ marginBottom: 6 }}>Reality check</p>
            <h2>Is the excitement backed by real use?</h2>
            <p style={{ marginTop: 10, fontSize: 15, lineHeight: 1.65 }}>{withCites(r.hypeCheck, src)}</p>
          </section>
        )}
        {(r.wouldChangeView ?? []).length > 0 && (
          <section className="panel pad-card">
            <h2>What would change this view</h2>
            <p className="small muted" style={{ marginTop: 4 }}>Things to watch for. New research checks them.</p>
            <ul className="watch-list">{(r.wouldChangeView ?? []).map((x, i) => <li key={i}>{x}</li>)}</ul>
          </section>
        )}
      </div>

      <Sections sections={sections} src={src} />

      <Sources r={r} sources={sources} checked={checked} />
    </>
  )
}

function Sections({ sections, src }: { sections: NonNullable<ResearchReport['sections']>; src: Src }) {
  const [open, setOpen] = useState<Record<string, boolean>>({ red_flags: true })
  if (sections.length === 0) return null
  const all = sections.every((s) => open[s.key])
  return (
    <section className="panel" style={{ overflow: 'hidden' }}>
      <div className="panel-head">
        <div>
          <h2>Section by section</h2>
          <p className="sub">Every finding links to where it came from. Red flags first. <span className="up">▲</span> good · <span className="down">▼</span> concern · <span className="faint">●</span> neutral</p>
        </div>
        <button className="linkish" onClick={() => setOpen(Object.fromEntries(sections.map((s) => [s.key, !all])))}>{all ? 'Close all' : 'Open all'}</button>
      </div>
      {sections.map((s) => {
        const fs = s.findings ?? []
        const n = { positive: 0, neutral: 0, negative: 0 }
        for (const f of fs) n[f.tone] = (n[f.tone] ?? 0) + 1
        const words = [n.positive && `${n.positive} good`, n.negative && `${n.negative} concern${n.negative > 1 ? 's' : ''}`, n.neutral && `${n.neutral} neutral`].filter(Boolean).join(' · ')
        const on = !!open[s.key]
        const title = s.key === 'red_flags' ? 'Red flags' : s.title.replace(/ \((Reddit|hacks|lawsuits|guidance)[^)]*\)$/, '')
        return (
          <div key={s.key} className="rsec">
            <button className="rsec-h" onClick={() => setOpen({ ...open, [s.key]: !on })} aria-expanded={on}>
              <span className="rsec-t">
                <span className={`mk ${s.key === 'red_flags' ? 'down' : 'faint'}`} aria-hidden="true">{s.key === 'red_flags' ? '▼' : '●'}</span>
                <span><span className="rsec-title">{title}</span><span className="rsec-sum">{s.summary}</span></span>
              </span>
              <span className="rsec-tone">
                <span className="tone-bar" aria-hidden="true">
                  {n.positive > 0 && <i style={{ flex: n.positive, background: 'var(--up)' }} />}
                  {n.neutral > 0 && <i style={{ flex: n.neutral, background: 'var(--border-strong)' }} />}
                  {n.negative > 0 && <i style={{ flex: n.negative, background: 'var(--down)' }} />}
                </span>
                <span className="small faint">{words || 'Nothing found'}</span>
              </span>
              <span className="small rsec-act">{on ? 'Hide' : 'Show'}</span>
            </button>
            {on && (
              <div className="rsec-body">
                {fs.length === 0 ? <p className="small faint">Nothing found.</p> : fs.map((f, i) => <Finding key={i} f={f} src={src} />)}
                {s.gaps && s.gaps.length > 0 && <p className="small faint" style={{ marginTop: 8 }}>Couldn’t check: {s.gaps.join(' · ')}</p>}
              </div>
            )}
          </div>
        )
      })}
    </section>
  )
}

function Sources({ r, sources, checked }: { r: ResearchReport; sources: NonNullable<ResearchReport['sources']>; checked: number }) {
  const [list, setList] = useState(false)
  const [more, setMore] = useState<'' | 'facts' | 'notes'>('')
  return (
    <section className="panel pad-card">
      <div className="row between" style={{ alignItems: 'flex-end', gap: 12 }}>
        <div>
          <h2>Sources</h2>
          <p className="small muted" style={{ marginTop: 4, maxWidth: 760 }}>
            {checked === sources.length ? `All ${sources.length}` : `${checked} of ${sources.length}`} turned up in Claude’s own searches or were read in full, so none is quoted from memory. “data” means figures this app checked itself.
          </p>
        </div>
        <button className="linkish" onClick={() => setList(!list)} aria-expanded={list}>{list ? 'Hide sources' : `Show all ${sources.length} sources`}</button>
      </div>
      {list && (
        <ol className="src-list">
          {sources.map((s) => (
            <li key={s.id}>
              <span className="mono faint">{s.id === 0 ? 'data' : s.id}</span>
              <span>
                {s.url ? <a href={s.url} target="_blank" rel="noreferrer">{s.title || s.url}</a> : s.title}
                {s.url && <span className="faint"> · {host(s.url)}</span>}
                {!s.retrieved && <span className="faint"> · named only, not checked</span>}
              </span>
            </li>
          ))}
        </ol>
      )}
      <div className="row" style={{ gap: 20, marginTop: 12, paddingTop: 10, borderTop: '1px solid var(--border)' }}>
        <button className="linkish" style={{ fontSize: 13.5 }} onClick={() => setMore(more === 'facts' ? '' : 'facts')} aria-expanded={more === 'facts'}>What Claude was given</button>
        <button className="linkish" style={{ fontSize: 13.5 }} onClick={() => setMore(more === 'notes' ? '' : 'notes')} aria-expanded={more === 'notes'}>Claude’s full notes</button>
      </div>
      {more && <pre className="research-pre">{more === 'facts' ? r.facts : r.notes}</pre>}
    </section>
  )
}

const host = (u: string) => {
  try {
    return new URL(u).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

function History({ v, shown, onPick }: { v: ResearchView; shown: ResearchReport; onPick: (id: number) => void }) {
  if (v.history.length <= 1) {
    return (
      <section className="panel pad-card">
        <h2>Earlier reports</h2>
        <p className="muted" style={{ marginTop: 8, lineHeight: 1.6 }}>
          This is the first report on {shown.name}. Each new one opens with what changed since the last, and this list shows how the price moved after each, so over time you can see whether the research was useful.
        </p>
      </section>
    )
  }
  return (
    <section className="panel" style={{ overflow: 'hidden' }}>
      <div className="panel-head"><div><h2>All reports</h2><p className="sub">Each report with the price when it was written and how the price has moved since.</p></div></div>
      <div className="table-wrap">
        <table>
          <thead><tr><th>Date</th><th>Fundamentals</th><th className="r">Price then</th><th className="r">Since then</th><th className="r hide-phone">Cost</th><th /></tr></thead>
          <tbody>
            {v.history.map((h) => {
              const [cls, label] = VERDICT[h.fundamentals] ?? ['flat', h.fundamentals]
              return (
                <tr key={h.id}>
                  <td className="small">{day(h.createdAt)}</td>
                  <td><span className={`chip-sm ${cls}`}>{label}</span></td>
                  <td className="num r">{price(h.price)}</td>
                  <td className="num r"><Since from={h.price} to={v.priceNow} /></td>
                  <td className="num r faint hide-phone">{usd(h.costUsd)}</td>
                  <td className="r">{shown.id === h.id ? <span className="small faint">showing</span> : <button className="small" onClick={() => onPick(h.id)}>View</button>}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </section>
  )
}
