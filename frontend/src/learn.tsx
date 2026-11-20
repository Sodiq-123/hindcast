// Step 2 of personalisation: what the app learns from what you do. It asks
// before changing anything, says why, and lets you undo, pause or forget.
import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, type Learned, type ModelRead, type ReminderSet, type Suggestion } from './api'
import { useAuth } from './auth'
import { Loading } from './components'

const money = (x: number) => `${x < 0 ? '−' : ''}$${Math.abs(x).toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
const day = (iso?: string) => (iso ? new Date(iso).toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) : '')

// ------------------------------------------------------------- Today

/** One suggestion at a time, under the greeting on Today. */
export function SuggestionCard() {
  const { refresh } = useAuth()
  const [s, setS] = useState<Suggestion | null>(null)
  const [phase, setPhase] = useState<'ask' | 'done' | 'undone' | 'later'>('ask')
  const [why, setWhy] = useState(false)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    api.suggestion().then((r) => setS(r.suggestion)).catch(() => {})
  }, [])

  if (!s) return null
  const answer = async (a: 'yes' | 'no' | 'undo') => {
    setBusy(true)
    setErr('')
    try {
      await api.answerSuggestion(s.id, a)
      setPhase(a === 'yes' ? 'done' : a === 'no' ? 'later' : 'undone')
      if (s.kind === 'horizon' || s.kind === 'detail' || s.kind === 'level') await refresh() // the settings changed
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (phase !== 'ask') {
    const text = phase === 'done' ? s.card.done : phase === 'undone' ? s.card.undo : 'Got it. It won’t ask about this again for a while. Everything it has asked is on About you.'
    return (
      <section className="panel sug-done" aria-live="polite">
        <p>{phase === 'done' && <span className="up" aria-hidden="true">✓ </span>}{text}{' '}
          {phase === 'done' && <button className="linkish" disabled={busy} onClick={() => answer('undo')}>Undo</button>}
        </p>
        {err && <p className="small down">{err}</p>}
      </section>
    )
  }
  return (
    <section className="panel sug" aria-labelledby="sug-say">
      <p className="tag sug-tag">✦ A suggestion, from what you do</p>
      <h2 id="sug-say" className="sug-say">{s.card.say}</h2>
      <p className="sug-more">{s.card.more}</p>
      <div className="row sug-actions">
        <button className="primary" disabled={busy} onClick={() => answer('yes')}>{s.card.yes}</button>
        <button disabled={busy} onClick={() => answer('no')}>{s.card.no}</button>
        <button className="linkish" onClick={() => setWhy(!why)} aria-expanded={why}>{why ? 'Hide why' : 'Why this?'}</button>
      </div>
      {why && (
        <ul className="sug-why">
          {s.card.why.map((w) => <li key={w}>{w}</li>)}
          <li>“{s.card.no}” waits {s.kind === 'remove' ? '90' : '30'} days before asking again. Everything it learns is listed on About you, where you can pause it or make it forget.</li>
        </ul>
      )}
      {err && <p className="small down">{err}</p>}
    </section>
  )
}

// ------------------------------------------------------------- Journal

/** "Worth a look" facts from your own record, shown while planning a trade. */
export function WorthALook({ symbol, amount, entry, stop, fee }: { symbol: string; amount?: number; entry?: number; stop?: number; fee: number }) {
  const [set, setSet] = useState<ReminderSet | null>(null)
  const [done, setDone] = useState('')
  const [why, setWhy] = useState(false)
  useEffect(() => {
    if (!symbol) return
    const id = setTimeout(() => api.reminders(symbol).then(setSet).catch(() => setSet(null)), 600)
    return () => clearTimeout(id)
  }, [symbol])

  if (!symbol || !set) return null
  if (done) return <p className="small muted" aria-live="polite">{done}</p>
  if (set.muted || set.items.length === 0) return null

  const gapLine = () => {
    if (!amount || !entry || !stop || stop >= entry) return ' A stop limits a loss; it doesn’t guarantee it.'
    const qty = amount / (1 + fee) / entry
    const planned = amount - qty * stop * (1 - fee)
    const gapped = amount - qty * stop * (1 - set.gapPct) * (1 - fee)
    return ` On this trade, the same gap would turn the planned ${money(planned)} loss into about ${money(gapped)}.`
  }
  const answer = (a: 'noted' | 'muted') => {
    api.answerReminder(set.class, a, set.items[0].title).catch(() => {})
    setDone(a === 'noted' ? 'Noted.' : `Reminders are off for ${set.classLabel} trades. Turn them back on under About you → What it’s learned.`)
  }
  return (
    <div className="worth" role="note" aria-label="Worth a look">
      <p className="tag worth-tag">Worth a look</p>
      {set.items.map((r) => (
        <div key={r.key} className="worth-item">
          <span className="worth-mark" aria-hidden="true">!</span>
          <span><strong>{r.title}</strong>{r.key === 'gap' ? gapLine() : r.detail}<span className="worth-count">{r.count}</span></span>
        </div>
      ))}
      <div className="row worth-actions">
        <button className="small" onClick={() => answer('noted')}>Noted</button>
        <button className="small" onClick={() => answer('muted')}>Not on {set.classLabel} trades</button>
        <button className="linkish small" onClick={() => setWhy(!why)} aria-expanded={why}>{why ? 'Hide why' : 'Why this?'}</button>
      </div>
      {why && (
        <ul className="sug-why">
          <li>These are counts from your own closed trades, not predictions.</li>
          <li>Comparisons, such as tested against untested signals, wait until each side has at least 8 trades, and appear only if the difference is unlikely to be luck.</li>
          <li>“Not on {set.classLabel} trades” turns these off for that kind of trade. Turn them back on under About you.</li>
        </ul>
      )}
    </div>
  )
}

// ------------------------------------------------------------- About you

const ANSWER: Record<string, string> = { '': 'Waiting on Today', yes: 'Yes', undone: 'Yes, then undone', noted: 'Noted', muted: 'Turned off' }

function answerText(s: Suggestion) {
  if (s.answer === 'no') {
    const kept = s.kind === 'remove' ? 'Kept it' : 'Not now'
    return s.snoozeUntil && new Date(s.snoozeUntil) > new Date() ? `${kept} · asks again ${day(s.snoozeUntil)}` : kept
  }
  return ANSWER[s.answer] ?? s.answer
}

const CLASS: Record<string, string> = { token: 'token', crypto: 'crypto', stock: 'stock and fund' }

/** "What the app has learned": everything it noticed, asked and concluded, with undo and forget. */
export function LearnedSection() {
  const { refresh } = useAuth()
  const [d, setD] = useState<Learned | null>(null)
  const [err, setErr] = useState('')
  const load = useCallback(() => api.learned().then(setD).catch((e) => setErr((e as Error).message)), [])
  useEffect(() => {
    load()
  }, [load])

  const act = async (f: () => Promise<unknown>, settings = false) => {
    setErr('')
    try {
      await f()
      if (settings) await refresh()
      await load()
    } catch (e) {
      setErr((e as Error).message)
    }
  }
  if (!d) return err ? <div className="callout bad">{err}</div> : <Loading label="Loading what it has learned…" />

  const h = d.habits
  return (
    <div className="stack" style={{ gap: 18 }}>
      <section className="panel answer learn-intro">
        <div className="answer-top">
          <div style={{ maxWidth: 760 }}>
            <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>What the app has learned</p>
            <h2 className="answer-say">It asks before it changes anything, and you can make it forget.</h2>
            <p className="answer-more">It learns only from what you do here. It never changes a forecast, a test result or a warning. The only thing pooled with other people is how often each kind of suggestion gets a yes, as everyone’s starting point.</p>
            <div className="row" style={{ gap: 12, marginTop: 14, flexWrap: 'wrap' }}>
              <button onClick={() => act(() => api.learnPrefs(!d.paused, d.muted))}>{d.paused ? 'Turn learning back on' : 'Pause learning'}</button>
              <span className="small muted">
                {d.paused ? 'Paused: nothing new is recorded and no suggestions are made. What it learned stays until you forget it.'
                  : d.since ? `Noticing since ${day(d.since)} · ${d.events} thing${d.events === 1 ? '' : 's'} recorded in the last 90 days.` : 'Nothing recorded yet.'}
              </span>
            </div>
          </div>
        </div>
      </section>
      {err && <div className="callout bad">{err}</div>}

      <section className="panel pad-card">
        <h3 className="learn-h">What it has asked you</h3>
        <p className="small muted learn-sub">Every suggestion and your answer. Undo any of them.</p>
        {d.asked.length === 0 ? (
          <p className="small muted learn-empty">Nothing yet. Suggestions appear on Today, one a day at most, once it has noticed something.</p>
        ) : (
          <div className="learn-rows">
            {d.asked.map((s) => (
              <div key={s.id} className="learn-asked">
                <span className="mono small faint">{day(s.shownAt)}</span>
                <span>{s.card.say}</span>
                <span className={`small ${s.answer === 'yes' ? 'up' : 'muted'}`}>{answerText(s)}</span>
                <span className="learn-act">
                  {s.answer === 'yes' && s.kind !== 'reminder' && <button className="linkish small" onClick={() => act(() => api.answerSuggestion(s.id, 'undo'), s.kind === 'horizon' || s.kind === 'detail' || s.kind === 'level')}>Undo</button>}
                  {s.answer === 'no' && s.snoozeUntil && new Date(s.snoozeUntil) > new Date() && <button className="linkish small" onClick={() => act(() => api.answerSuggestion(s.id, 'again'))}>Ask again</button>}
                </span>
              </div>
            ))}
          </div>
        )}
        {d.muted.length > 0 && (
          <div className="learn-muted">
            {d.muted.map((m) => (
              <p key={m} className="small">“Worth a look” reminders are off for {CLASS[m] ?? m} trades.{' '}
                <button className="linkish small" onClick={() => act(() => api.learnPrefs(d.paused, d.muted.filter((x) => x !== m)))}>Turn back on</button></p>
            ))}
          </div>
        )}
      </section>

      <section className="panel pad-card">
        <h3 className="learn-h">What it has noticed</h3>
        <p className="small muted learn-sub">From the asset pages you open, the watchlist view you pick and when you open the technical detail. Forgetting one deletes what it came from.</p>
        {d.noticed.length === 0 ? (
          <p className="small muted learn-empty">Nothing yet.</p>
        ) : (
          <div className="learn-rows">
            {d.noticed.map((n) => (
              <div key={n.key} className="learn-noticed">
                <span>{n.text}<br /><span className="small faint">{n.detail}</span></span>
                <button className="linkish small" onClick={() => act(() => api.forget(n.key))}>Forget</button>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="panel pad-card">
        <h3 className="learn-h">How it picks suggestions for you</h3>
        <p className="small muted learn-sub">It starts from what people in general accept, then moves towards your own answers. The thin line is everyone; the marker is you.</p>
        <div className="learn-rows">
          {d.picks.map((p) => {
            const verdict = p.answered === 0 ? 'no answers yet' : p.estimate > p.everyone + 0.02 ? 'shown more' : p.estimate < p.everyone - 0.02 ? 'shown less' : 'as for everyone'
            return (
              <div key={p.kind} className="learn-pick">
                <span><strong>{p.label}</strong><br /><span className="small muted">{p.answered ? `you said yes ${p.yes} of ${p.answered}` : 'not asked yet'}</span></span>
                <span className="learn-bar" role="img" aria-label={`You: ${Math.round(p.estimate * 100)}% likely to say yes. Everyone: ${Math.round(p.everyone * 100)}%.`}>
                  <i style={{ width: `${p.estimate * 100}%` }} />
                  <b style={{ left: `${p.estimate * 100}%` }} />
                  <em style={{ left: `${p.everyone * 100}%` }} />
                </span>
                <span className={`small ${p.answered === 0 || verdict === 'as for everyone' ? 'muted' : ''}`}>{verdict}</span>
              </div>
            )
          })}
        </div>
      </section>

      <section className="panel pad-card">
        <h3 className="learn-h">Your trading habits</h3>
        <p className="small muted learn-sub">From your closed paper trades. Facts appear straight away; patterns only once your record clearly shows them.</p>
        <div className="learn-habits">
          <div>
            <p className="tag" style={{ color: 'var(--accent-ink)' }}>Facts it can state now</p>
            {h.facts.length === 0 ? <p className="small muted learn-empty">None yet: close a few paper trades first.</p>
              : h.facts.map((f) => <div key={f.key} className="learn-fact"><span>{f.title}</span><span className="mono tiny faint">{f.count}</span></div>)}
          </div>
          <div>
            <p className="tag faint">Patterns it checks</p>
            {h.patterns.map((p) => (
              <div key={p.key} className="learn-pattern">
                {p.found ? (
                  <p><strong>{p.text}</strong>{p.detail}</p>
                ) : (
                  <>
                    <p>{p.title}{p.ready && <span className="small muted"> · checked: no clear pattern</span>}</p>
                    {!p.ready && <><span className="learn-prog"><i style={{ width: `${Math.max(2, p.progress * 100)}%` }} /></span><p className="mono tiny faint">{p.need}</p></>}
                  </>
                )}
              </div>
            ))}
          </div>
        </div>
      </section>

      <ModelCard m={d.model} onClear={() => { if (window.confirm('Delete all your practice rounds? They stop counting towards your personal model. Your paper trades stay.')) act(() => api.forget('practice')) }} />

      <div className="row" style={{ gap: 10, flexWrap: 'wrap' }}>
        <a className="btn" href="/api/learn/export" download>Download what it has learned</a>
        <button className="ghost danger-text" onClick={() => { if (window.confirm('Forget everything it has learned? Your answers, what it noticed and muted reminders are deleted. Your trades stay.')) act(() => api.forget('all')) }}>Forget everything</button>
      </div>
    </div>
  )
}

// ModelCard is the personal model on About you: progress until the early
// read, the early read (◐, with how unsure it is), then the full test (✓ only
// if it passes).
function ModelCard({ m, onClear }: { m: ModelRead; onClear: () => void }) {
  const f = m.finding
  const from = [m.paper > 0 && `${m.paper} paper ${m.paper === 1 ? 'trade' : 'trades'}`, m.practice > 0 && `${m.practice} practice ${m.practice === 1 ? 'buy' : 'buys'}`].filter(Boolean).join(' and ') || 'no trades yet'
  const pc = (x: number) => `${Math.round(x * 100)}%`
  const luck = (p: number) => (p >= 0.5 ? 'more likely luck than not' : `about 1 chance in ${Math.max(2, Math.round(1 / p))}`)
  const passed = m.test?.passed
  return (
    <section className="panel pad-card">
      <h3 className="learn-h">Your personal model</h3>
      <p className="small muted learn-sub">A model trained only on your own trades, to spot which of your setups tend to lose. It compares your trades on a few plain questions (after a big week? near a high? did the app say buy?), because with tens of trades anything bigger would only learn noise.</p>

      {m.stage === 'collecting' ? (
        <>
          <span className="learn-prog big" role="img" aria-label={`${m.trades} of ${m.early} trades`}><i style={{ width: `${Math.min(100, (m.trades / m.early) * 100)}%` }} /></span>
          <p className="small muted" style={{ marginTop: 8, lineHeight: 1.55 }}>
            <strong style={{ color: 'var(--text)' }}>{m.trades} of {m.early} trades</strong> ({from}). {m.early - m.trades} more for an early read, marked ◐ weak evidence, with how unsure it is.{' '}
            <Link to="/journal/practice">Practice rounds</Link> get there in minutes rather than weeks.
          </p>
        </>
      ) : (
        <>
          <div className="mr-head">
            <span className={passed ? 'mr-chip pass' : 'mr-chip'}>{passed ? '✓ Passed the full test' : m.stage === 'full' ? '◐ Full test not passed' : '◐ Early read · weak evidence'}</span>
            <span className="small muted">from {from}</span>
          </div>
          {!f ? (
            <p className="mr-title">Nothing to compare yet.</p>
          ) : (
            <>
              <p className="mr-title">{m.clear ? f.title : 'Nothing stands out yet.'}</p>
              <p className="small muted" style={{ margin: '6px 0 0', lineHeight: 1.6 }}>{m.clear ? f.detail : `The biggest difference so far: ${f.detail.charAt(0).toLowerCase()}${f.detail.slice(1)}`}</p>
              <div style={{ marginTop: 12 }}>
                <p className="tag faint" style={{ margin: 0 }}>How unsure</p>
                <span className="mr-range" role="img" aria-label={`Best guess ${pc(f.rate)}, between ${pc(f.lo)} and ${pc(f.hi)}. The rest of your trades: ${pc(f.other)}.`}>
                  <i style={{ left: `${f.lo * 100}%`, width: `${(f.hi - f.lo) * 100}%` }} />
                  <b style={{ left: `${f.rate * 100}%` }} />
                  <em style={{ left: `${f.other * 100}%` }} />
                </span>
                <p className="small muted" style={{ margin: '6px 0 0', lineHeight: 1.55 }}>
                  Its best guess for those trades: {pc(f.rate)} make money, somewhere between {pc(f.lo)} and {pc(f.hi)} (9 times in 10). The thin line is the rest of your trades, {pc(f.other)}. It could be luck: {luck(f.luck)}, counting that it checked every question.
                </p>
              </div>
            </>
          )}
          {m.test ? (
            <p className={`small ${passed ? '' : 'muted'}`} style={{ margin: '12px 0 0', lineHeight: 1.55 }}>{m.test.text}</p>
          ) : (
            <p className="small faint" style={{ margin: '12px 0 0', lineHeight: 1.55 }}>An early read is a hint, not a finding. From {m.full} trades ({m.full - m.trades} more) it runs the full test: it picks the question on your earlier trades, checks it on later ones it hasn’t seen, and must pass the luck check. It earns ✓ only if it passes.</p>
          )}
        </>
      )}
      {m.practice > 0 && (
        <div className="row" style={{ gap: 10, marginTop: 12, flexWrap: 'wrap' }}>
          <Link className="small" to="/journal/practice">More practice rounds</Link>
          <button className="ghost danger-text small" onClick={onClear}>Delete practice rounds</button>
        </div>
      )}
    </section>
  )
}
