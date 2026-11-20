import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { auth, type AccentName, type Prefs, type ProfileInput, type ProfileView, type SessionInfo, type ThemeChoice } from '../api'
import { useAuth, useMe } from '../auth'
import { Loading, Panel, PasswordInput } from '../components'
import { ago } from '../format'
import { LearnedSection } from '../learn'
import { ACCENTS, applyLook } from '../theme'
import { Choices, DROPS, EffectsTable, EXPERIENCE, GOALS, HORIZONS, INTERESTS } from './Welcome'

const WORDING = [
  { v: '', label: 'Decide from my answers' },
  { v: 'beginner', label: 'Plain words', sub: 'No jargon.' },
  { v: 'intermediate', label: 'Some terms', sub: 'The precise wording on Today.' },
  { v: 'advanced', label: 'With the numbers', sub: 'Also the test statistics behind each forecast.' },
]

const DETAIL = [
  { v: '', label: 'Decide from my answers' },
  { v: 'simple', label: 'Simple', sub: 'Forecast, chart and summary first; technical panels folded away.' },
  { v: 'full', label: 'Full detail', sub: 'Every panel open.' },
]

export default function AboutYou() {
  const [view, setView] = useState<ProfileView | null>(null)
  const [err, setErr] = useState('')
  const load = useCallback(() => auth.profile().then(setView).catch((e) => setErr((e as Error).message)), [])
  useEffect(() => {
    load()
  }, [load])

  return (
    <div className="stack" style={{ gap: 20 }}>
      <header style={{ maxWidth: 760 }}>
        <h1 style={{ fontSize: 36 }}>About you</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>Everything the app stores about you, what it does with it, and how to change or delete it.</p>
      </header>
      {err && <div className="callout bad">{err}</div>}
      <div className="about-grid">
        <nav className="about-nav" aria-label="On this page">
          {SECTIONS.map(([id, label]) => <a key={id} href={`#${id}`}>{label}</a>)}
        </nav>
        <div className="stack" style={{ gap: 20, minWidth: 0 }}>
          <div id="look" className="anchor"><LookPanel /></div>
          <div id="answers" className="anchor">{!view ? <Loading label="Loading…" /> : <ProfilePanel view={view} onSaved={setView} />}</div>
          <div id="learned" className="anchor"><LearnedSection /></div>
          <section id="privacy" className="panel pad-card anchor">
            <h2>What’s recorded, and what isn’t</h2>
            <div className="stack" style={{ gap: 12, marginTop: 14 }}>
              <div className="reason"><span className="n">1</span><span><strong>Kept:</strong> your answers above, when you last opened Today (for “since you last looked”), and what you did with its suggestions, so it doesn’t repeat itself.</span></div>
              <div className="reason"><span className="n">2</span><span><strong>Noticed, to make suggestions:</strong> which asset pages you open, which watchlist view you pick, when you open the technical detail, and when you open Track record or Replay. Kept for 90 days. Pause it or make it forget under <a href="#learned">What it’s learned</a>.</span></div>
              <div className="reason"><span className="n">3</span><span><strong>Not kept:</strong> your searches, what you type, how long you read, or anything outside this app.</span></div>
              <div className="reason"><span className="n">4</span><span><strong>Private:</strong> your watchlist and journal. Other people, admins included, see only how many items you have, not what they are. Whoever runs the server can technically read its database, as with any website.</span></div>
            </div>
          </section>
          <div id="account" className="anchor"><AccountPanel /></div>
          <div id="devices" className="anchor"><SessionsPanel /></div>
          <div id="data" className="anchor"><DataPanel /></div>
        </div>
      </div>
    </div>
  )
}

const SECTIONS: [string, string][] = [['look', 'Your look'], ['answers', 'Your answers'], ['learned', 'What it’s learned'], ['privacy', 'What’s recorded'], ['account', 'Account'], ['devices', 'Signed-in devices'], ['data', 'Your data']]

const THEMES: { v: ThemeChoice; label: string }[] = [
  { v: 'auto', label: 'Auto' },
  { v: 'light', label: 'Light' },
  { v: 'dark', label: 'Dark' },
]

/** Light/dark/auto and an accent colour, saved to the account so every device matches. */
function LookPanel() {
  const { me, setMe } = useAuth()
  const [err, setErr] = useState('')
  if (!me) return null
  const prefs = me.prefs
  const choose = async (next: Prefs) => {
    setErr('')
    applyLook(next)
    setMe({ ...me, prefs: next })
    try {
      await auth.savePrefs(next)
    } catch (e) {
      setErr((e as Error).message)
      applyLook(prefs)
      setMe({ ...me, prefs })
    }
  }
  return (
    <Panel title="Your look" sub="Saved to your account, so your phone and computer match. Auto follows your device's light or dark setting.">
      <div className="stack" style={{ gap: 18 }}>
        <div className="row" style={{ gap: 16 }}>
          <span className="small muted" style={{ width: 70 }}>Mode</span>
          <div className="seg" role="radiogroup" aria-label="Light or dark">
            {THEMES.map((t) => (
              <button key={t.v} role="radio" aria-checked={prefs.theme === t.v} className={prefs.theme === t.v ? 'on' : ''} onClick={() => choose({ ...prefs, theme: t.v })}>{t.label}</button>
            ))}
          </div>
        </div>
        <div className="row" style={{ gap: 16, alignItems: 'flex-start' }}>
          <span className="small muted" style={{ width: 70, paddingTop: 14 }}>Accent</span>
          <div className="swatches" role="radiogroup" aria-label="Accent colour">
            {(Object.keys(ACCENTS) as AccentName[]).map((k) => (
              <button key={k} role="radio" aria-checked={prefs.accent === k} className={`swatch ${prefs.accent === k ? 'on' : ''}`} onClick={() => choose({ ...prefs, accent: k })}>
                <i style={{ background: ACCENTS[k].hex }} />
                {ACCENTS[k].label}
              </button>
            ))}
          </div>
        </div>
        <p className="small muted">Green and red always come with ▲ and ▼, so ups and downs read the same whatever colours you pick.</p>
        {err && <div className="callout bad">{err}</div>}
      </div>
    </Panel>
  )
}

function ProfilePanel({ view, onSaved }: { view: ProfileView; onSaved: (v: ProfileView) => void }) {
  const { refresh } = useAuth()
  const [editing, setEditing] = useState(false)
  const p = view.profile
  const [form, setForm] = useState<ProfileInput>({ experience: p.experience, goals: p.goals ?? [], horizon: p.horizon, interests: p.interests ?? [], dropReaction: p.dropReaction, detail: p.detail, wording: p.wording ?? '', status: p.status || 'done' })
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const set = <K extends keyof ProfileInput>(k: K, v: ProfileInput[K]) => setForm((x) => ({ ...x, [k]: v }))

  const save = async () => {
    setBusy(true)
    setErr('')
    try {
      onSaved(await auth.saveProfile({ ...form, status: 'done' }))
      await refresh()
      setEditing(false)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Panel
      title="Your answers and what they change"
      sub={p.updatedAt && !p.updatedAt.startsWith('0001') ? `Last changed ${ago(p.updatedAt)}` : 'Not set up yet'}
      actions={!editing && (
        <>
          {p.status !== 'skipped' && <Link to="/welcome" className="btn">{p.quiz ? 'Retake the setup' : 'Take the setup'}</Link>}
          <button onClick={() => setEditing(true)}>Edit answers</button>
        </>
      )}
    >
      {p.status === 'skipped' && !editing && (
        <div className="earn-band" style={{ marginBottom: 14, alignItems: 'center' }}>
          <span><strong>You skipped the setup,</strong> <span className="muted">so the app is using its defaults for you (shown below). Two minutes of questions make Today and your watchlist yours.</span></span>
          <Link to="/welcome" className="btn primary" style={{ flex: 'none' }}>Take the setup</Link>
        </div>
      )}
      {!editing ? (
        <EffectsTable effects={view.effects} />
      ) : (
        <div className="stack" style={{ gap: 20 }}>
          <div className="stack"><strong>Experience</strong><Choices opts={EXPERIENCE} value={[form.experience]} onChange={([v]) => set('experience', v as ProfileInput['experience'])} /></div>
          <div className="stack"><strong>Goals</strong><Choices multi opts={GOALS} value={form.goals} onChange={(v) => set('goals', v)} /></div>
          <div className="stack"><strong>Time horizon</strong><Choices opts={HORIZONS} value={[form.horizon]} onChange={([v]) => set('horizon', v as ProfileInput['horizon'])} /></div>
          <div className="stack"><strong>Interests</strong><Choices multi opts={INTERESTS} value={form.interests} onChange={(v) => set('interests', v)} /></div>
          <div className="stack"><strong>After a 30% fall you would…</strong><Choices opts={DROPS} value={[form.dropReaction]} onChange={([v]) => set('dropReaction', v as ProfileInput['dropReaction'])} /></div>
          <div className="stack"><strong>How it’s explained</strong><Choices opts={WORDING} value={[form.wording]} onChange={([v]) => set('wording', v as ProfileInput['wording'])} /></div>
          <div className="stack"><strong>How much is shown</strong><Choices opts={DETAIL} value={[form.detail]} onChange={([v]) => set('detail', v as ProfileInput['detail'])} /></div>
          {err && <div className="callout bad">{err}</div>}
          <div className="row">
            <button className="primary" disabled={busy} onClick={save}>{busy ? <span className="spinner" /> : 'Save'}</button>
            <button className="ghost" onClick={() => setEditing(false)}>Cancel</button>
          </div>
        </div>
      )}
    </Panel>
  )
}

function AccountPanel() {
  const me = useMe()
  const { setMe } = useAuth()
  const [name, setName] = useState(me.user.name)
  const [email, setEmail] = useState(me.user.email)
  const [pw, setPw] = useState('')
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const [cur, setCur] = useState('')
  const [next, setNext] = useState('')
  const [pwMsg, setPwMsg] = useState<{ ok: boolean; text: string } | null>(null)
  const emailChanged = email.trim().toLowerCase() !== me.user.email

  const saveAccount = async (e: FormEvent) => {
    e.preventDefault()
    try {
      setMe(await auth.updateAccount(name, email, pw))
      setPw('')
      setMsg({ ok: true, text: 'Saved.' })
    } catch (e) {
      setMsg({ ok: false, text: (e as Error).message })
    }
  }
  const savePassword = async (e: FormEvent) => {
    e.preventDefault()
    try {
      await auth.changePassword(cur, next)
      setCur('')
      setNext('')
      setPwMsg({ ok: true, text: 'Password changed. Other devices have been signed out.' })
    } catch (e) {
      setPwMsg({ ok: false, text: (e as Error).message })
    }
  }

  return (
    <Panel title="Account" sub={`${me.user.role === 'admin' ? 'Admin' : 'Member'} since ${new Date(me.user.createdAt).toLocaleDateString()}`}>
      <div className="grid grid-2">
        <form className="stack" onSubmit={saveAccount}>
          <label className="field">Name<input value={name} maxLength={80} onChange={(e) => setName(e.target.value)} /></label>
          <label className="field">Email<input type="email" required value={email} onChange={(e) => setEmail(e.target.value)} /></label>
          {emailChanged && <label className="field">Current password (needed to change your email)<PasswordInput autoComplete="current-password" required value={pw} onChange={(e) => setPw(e.target.value)} /></label>}
          {msg && <div className={`callout ${msg.ok ? 'good' : 'bad'}`}>{msg.text}</div>}
          <div><button>Save</button></div>
        </form>
        <form className="stack" onSubmit={savePassword}>
          <label className="field">Current password<PasswordInput autoComplete="current-password" required value={cur} onChange={(e) => setCur(e.target.value)} /></label>
          <label className="field">New password<PasswordInput autoComplete="new-password" required minLength={10} value={next} onChange={(e) => setNext(e.target.value)} /><span className="hint">At least 10 characters.</span></label>
          {pwMsg && <div className={`callout ${pwMsg.ok ? 'good' : 'bad'}`}>{pwMsg.text}</div>}
          <div><button>Change password</button></div>
        </form>
      </div>
    </Panel>
  )
}

function device(ua: string) {
  const os = /iPhone|iPad/.test(ua) ? 'iPhone/iPad' : /Android/.test(ua) ? 'Android' : /Mac OS X/.test(ua) ? 'Mac' : /Windows/.test(ua) ? 'Windows' : /Linux/.test(ua) ? 'Linux' : ''
  const br = /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Firefox\//.test(ua) ? 'Firefox' : /Safari\//.test(ua) ? 'Safari' : ''
  return [br, os].filter(Boolean).join(' on ') || ua.slice(0, 60) || 'Unknown device'
}

function SessionsPanel() {
  const [list, setList] = useState<SessionInfo[] | null>(null)
  const load = useCallback(() => auth.sessions().then(setList).catch(() => setList([])), [])
  useEffect(() => {
    load()
  }, [load])
  const others = (list ?? []).filter((s) => !s.current).length
  return (
    <Panel title="Signed-in devices" sub="Sign out anything you don't recognise, then change your password."
      actions={others > 0 && <button className="danger" onClick={() => auth.revokeSession(0).then(load)}>Sign out all other devices</button>} pad={false}>
      {!list ? <Loading label="Loading…" /> : (
        <div>
          {list.map((s) => (
            <div key={s.id} className="device-row">
              <span><strong>{device(s.userAgent)}</strong> {s.current && <span className="chip-sm flat">this device</span>}<span className="small faint" style={{ display: 'block' }}>{s.ip || 'address unknown'}</span></span>
              <span className="small muted">Signed in {new Date(s.createdAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })}<span style={{ display: 'block' }}>active {ago(s.lastSeenAt)}</span></span>
              <span className="r">{!s.current && <button className="small danger ghost" onClick={() => auth.revokeSession(s.id).then(load)}>Sign out</button>}</span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}

function DataPanel() {
  const { setMe } = useAuth()
  const nav = useNavigate()
  const [confirming, setConfirming] = useState(false)
  const [pw, setPw] = useState('')
  const [err, setErr] = useState('')

  const del = async (e: FormEvent) => {
    e.preventDefault()
    try {
      await auth.deleteAccount(pw)
      setMe(null)
      nav('/login', { replace: true })
    } catch (e) {
      setErr((e as Error).message)
    }
  }

  return (
    <Panel title="Your data">
      <div className="stack">
        <div className="folded">
          <span className="small muted">Download everything above, plus your watchlist and journal, as one JSON file.</span>
          <a className="btn" href="/api/account/export" download>Download my data</a>
        </div>
        <div className="folded">
          <span className="small muted">Delete your account, profile, watchlist and journal. This can't be undone.</span>
          {!confirming && <button className="danger" onClick={() => setConfirming(true)}>Delete my account</button>}
        </div>
        {confirming && (
          <form className="stack" onSubmit={del} style={{ maxWidth: 420 }}>
            <label className="field">Enter your password to confirm<PasswordInput autoComplete="current-password" required value={pw} onChange={(e) => setPw(e.target.value)} autoFocus /></label>
            {err && <div className="callout bad">{err}</div>}
            <div className="row">
              <button className="danger">Delete everything</button>
              <button type="button" className="ghost" onClick={() => setConfirming(false)}>Cancel</button>
            </div>
          </form>
        )}
      </div>
    </Panel>
  )
}
