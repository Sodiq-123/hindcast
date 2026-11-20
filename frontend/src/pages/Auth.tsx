import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { auth, type AuthConfig } from '../api'
import { useAuth } from '../auth'
import { PasswordInput } from '../components'

export function Brand() {
  return (
    <span className="brand">
      <span className="mark"><svg viewBox="0 0 24 24" fill="none" stroke="#fff" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round"><path d="M3 16l5-5 4 3 7-8" /></svg></span>
      Hindcast
    </span>
  )
}

// Illustrative ranges for the sign-in side panel (a picture of the idea, not data).
const MOTIF: [string, number, number, number][] = [['Day', 44, 56, 51], ['Week', 36, 64, 53], ['Month', 24, 78, 56], ['Year', 8, 96, 62]]

function Card({ title, lead, children }: { title: string; lead?: ReactNode; children: ReactNode }) {
  return (
    <div className="auth-wrap">
      <aside className="auth-side" aria-hidden="true">
        <Brand />
        <div>
          <h2 className="auth-claim">Honest forecasts that show their working.</h2>
          <p className="auth-def"><strong>hind·cast</strong> <em>noun</em> · a forecast made for the past, to test whether the method works before anyone trusts it.</p>
          <ul className="auth-points">
            <li>Every forecast is a range, not a promise.</li>
            <li>Only signals that passed testing are worth acting on, and the app says which.</li>
            <li>Every forecast is scored when its date arrives.</li>
          </ul>
        </div>
        <div className="auth-motif">
          {MOTIF.map(([l, a, b, m]) => (
            <div key={l} className="am-row"><span>{l}</span><span className="am-track"><i style={{ left: `${a}%`, right: `${100 - b}%` }} /><b style={{ left: `${m}%` }} /></span></div>
          ))}
        </div>
      </aside>
      <div className="auth-main">
        <div className="auth-card">
          <span className="auth-brand-phone"><Brand /></span>
          <h1>{title}</h1>
          {lead && <p className="lead">{lead}</p>}
          {children}
        </div>
      </div>
    </div>
  )
}

function useConfig() {
  const [cfg, setCfg] = useState<AuthConfig | null>(null)
  useEffect(() => {
    auth.config().then(setCfg).catch(() => {})
  }, [])
  return cfg
}

const MIN_PASSWORD = 10

export function Login() {
  const { setMe } = useAuth()
  const cfg = useConfig()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const nav = useNavigate()

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      setMe(await auth.login(email, password))
      nav('/', { replace: true })
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (cfg?.needsSetup) {
    return (
      <Card title="Set up Hindcast" lead="No account exists yet.">
        <div className="callout info">
          The first account becomes the admin and takes over the existing watchlist and journal. Open the sign-up link printed in the
          server's log (it contains a one-time setup code).
        </div>
      </Card>
    )
  }
  return (
    <Card title="Sign in">
      <form className="stack" onSubmit={submit}>
        <label className="field">Email<input type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} autoFocus /></label>
        <label className="field">Password<PasswordInput autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} /></label>
        {err && <div className="callout bad">{err}</div>}
        <button className="primary" disabled={busy}>{busy ? <span className="spinner" /> : 'Sign in'}</button>
      </form>
      <div className="auth-links">
        <Link to="/forgot">Forgot your password?</Link>
        <Link to="/signup">{cfg?.signup === 'open' ? 'Create an account' : 'Have an invite?'}</Link>
      </div>
    </Card>
  )
}

export function Signup() {
  const { setMe } = useAuth()
  const cfg = useConfig()
  const [params] = useSearchParams()
  const [invite, setInvite] = useState(params.get('invite') ?? '')
  const [inviteOk, setInviteOk] = useState<boolean | null>(null)
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)
  const nav = useNavigate()

  useEffect(() => {
    if (!invite) {
      setInviteOk(null)
      return
    }
    const id = setTimeout(() => auth.inviteValid(invite).then((r) => setInviteOk(r.valid)).catch(() => setInviteOk(null)), 300)
    return () => clearTimeout(id)
  }, [invite])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (password.length < MIN_PASSWORD) {
      setErr(`Use at least ${MIN_PASSWORD} characters for the password.`)
      return
    }
    setBusy(true)
    setErr('')
    try {
      setMe(await auth.signup({ email, name, password, invite }))
      nav('/welcome', { replace: true })
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const needsInvite = cfg?.needsSetup || cfg?.signup !== 'open'
  const lead = cfg?.needsSetup
    ? 'This first account becomes the admin and takes over the existing watchlist and journal.'
    : needsInvite
      ? 'Joining needs an invite from an admin.'
      : undefined
  return (
    <Card title={cfg?.needsSetup ? 'Create the admin account' : 'Create your account'} lead={lead}>
      <form className="stack" onSubmit={submit}>
        <label className="field">Your name (optional)<input autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} maxLength={80} autoFocus /></label>
        <label className="field">Email<input type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} /></label>
        <label className="field">
          Password
          <PasswordInput autoComplete="new-password" required minLength={MIN_PASSWORD} value={password} onChange={(e) => setPassword(e.target.value)} />
          <span className="hint">At least {MIN_PASSWORD} characters. A few unrelated words is easy to remember and hard to guess.</span>
        </label>
        {(needsInvite || invite) && (
          <label className="field">
            {cfg?.needsSetup ? 'Setup code' : 'Invite code'}
            <input value={invite} onChange={(e) => setInvite(e.target.value.trim())} required={needsInvite} spellCheck={false} />
            {inviteOk === false && <span className="hint down">This code isn't valid: it may have expired or been used up.</span>}
            {inviteOk === true && <span className="hint up">Code accepted.</span>}
          </label>
        )}
        {err && <div className="callout bad">{err}</div>}
        <button className="primary" disabled={busy}>{busy ? <span className="spinner" /> : 'Create account'}</button>
      </form>
      <div className="auth-links">
        <span className="muted">Already have an account?</span>
        <Link to="/login">Sign in</Link>
      </div>
    </Card>
  )
}

export function Forgot() {
  const cfg = useConfig()
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setErr('')
    try {
      await auth.forgot(email)
      setSent(true)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (cfg && !cfg.email) {
    return (
      <Card title="Forgot your password?">
        <div className="callout info">
          This server can't send emails yet. Ask an admin for a password reset link: they can make one for you from the Admin page.
        </div>
        <div className="auth-links"><Link to="/login">Back to sign in</Link></div>
      </Card>
    )
  }
  return (
    <Card title="Forgot your password?" lead={sent ? undefined : "Enter your email and we'll send a link to choose a new one."}>
      {sent ? (
        <div className="callout good">If that email has an account, a reset link is on its way. It works once, for one hour. Check your spam folder too.</div>
      ) : (
        <form className="stack" onSubmit={submit}>
          <label className="field">Email<input type="email" autoComplete="email" required value={email} onChange={(e) => setEmail(e.target.value)} autoFocus /></label>
          {err && <div className="callout bad">{err}</div>}
          <button className="primary" disabled={busy}>{busy ? <span className="spinner" /> : 'Send the link'}</button>
        </form>
      )}
      <div className="auth-links"><Link to="/login">Back to sign in</Link></div>
    </Card>
  )
}

export function Reset() {
  const [params] = useSearchParams()
  const [token] = useState(params.get('token') ?? '')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [done, setDone] = useState(false)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  // Keep the one-time token out of the address bar and browser history.
  useEffect(() => {
    if (params.get('token')) window.history.replaceState(null, '', '/reset')
  }, [params])

  const submit = async (e: FormEvent) => {
    e.preventDefault()
    if (password !== confirm) {
      setErr("The two passwords don't match.")
      return
    }
    setBusy(true)
    setErr('')
    try {
      await auth.reset(token, password)
      setDone(true)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (!token) {
    return (
      <Card title="Reset your password">
        <div className="callout bad">This page needs the link from your reset email (or from an admin).</div>
        <div className="auth-links"><Link to="/forgot">Ask for a new link</Link></div>
      </Card>
    )
  }
  return (
    <Card title="Choose a new password">
      {done ? (
        <>
          <div className="callout good">Password changed. You've been signed out everywhere; sign in with the new password.</div>
          <Link to="/login" className="btn" style={{ justifyContent: 'center' }}>Sign in</Link>
        </>
      ) : (
        <form className="stack" onSubmit={submit}>
          <label className="field">New password<PasswordInput autoComplete="new-password" required minLength={MIN_PASSWORD} value={password} onChange={(e) => setPassword(e.target.value)} autoFocus /></label>
          <label className="field">Type it again<PasswordInput autoComplete="new-password" required value={confirm} onChange={(e) => setConfirm(e.target.value)} /></label>
          {err && <div className="callout bad">{err}</div>}
          <button className="primary" disabled={busy}>{busy ? <span className="spinner" /> : 'Save the new password'}</button>
        </form>
      )}
    </Card>
  )
}
