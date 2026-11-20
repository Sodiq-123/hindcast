import { lazy, Suspense, useEffect, useRef, useState, type ReactNode } from 'react'
import { NavLink, Route, Routes, Link, Navigate, useLocation } from 'react-router-dom'
import { api, type Status } from './api'
import { useAuth, useMe } from './auth'
import { Brand, Loading, SearchBox } from './components'
import { ago } from './format'
import { Forgot, Login, Reset, Signup } from './pages/Auth'
import { useLookSync } from './theme'
// Pages load on demand so the chart library isn't in the first download.
const Today = lazy(() => import('./pages/Today'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const Asset = lazy(() => import('./pages/Asset'))
const Scanner = lazy(() => import('./pages/Scanner'))
const TrackRecord = lazy(() => import('./pages/TrackRecord'))
const Methodology = lazy(() => import('./pages/Methodology'))
const Replay = lazy(() => import('./pages/Replay'))
const Token = lazy(() => import('./pages/Token'))
const Journal = lazy(() => import('./pages/Journal'))
const Practice = lazy(() => import('./pages/Practice'))
const Research = lazy(() => import('./pages/Research'))
const ResearchHome = lazy(() => import('./pages/Research').then((m) => ({ default: m.ResearchHome })))
const Welcome = lazy(() => import('./pages/Welcome'))
const AboutYou = lazy(() => import('./pages/AboutYou'))
const Admin = lazy(() => import('./pages/Admin'))

export default function App() {
  const { me } = useAuth()
  useLookSync(me?.prefs)
  if (me === undefined) return <Loading label="Loading…" />
  if (!me) {
    return (
      <Routes>
        <Route path="/login" element={<Login />} />
        <Route path="/signup" element={<Signup />} />
        <Route path="/forgot" element={<Forgot />} />
        <Route path="/reset" element={<Reset />} />
        <Route path="*" element={<Navigate to="/login" replace />} />
      </Routes>
    )
  }
  return <SignedIn />
}

// ---------- icons (one stroke style throughout) ----------
const I = ({ children }: { children: ReactNode }) => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{children}</svg>
)
export const Icon = {
  today: <I><circle cx="12" cy="12" r="4" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9 7 7M17 17l2.1 2.1M4.9 19.1 7 17M17 7l2.1-2.1" /></I>,
  watch: <I><path d="M4 6h16M4 12h16M4 18h10" /></I>,
  scan: <I><path d="M3 17l5-5 4 4 8-8" /><path d="M15 8h5v5" /></I>,
  research: <I><circle cx="11" cy="11" r="7" /><path d="M20 20l-4-4" /></I>,
  journal: <I><path d="M6 3h12v18l-6-4-6 4z" /></I>,
  track: <I><path d="M4 20V10M10 20V4M16 20v-7M22 20H2" /></I>,
  replay: <I><path d="M3 12a9 9 0 1 0 3-6.7L3 8" /><path d="M3 3v5h5" /><path d="M12 8v4l3 2" /></I>,
  how: <I><path d="M3 7l9-4 9 4-9 4z" /><path d="M7 9v6c3 2 7 2 10 0V9" /></I>,
  more: <I><circle cx="5" cy="12" r="1.3" /><circle cx="12" cy="12" r="1.3" /><circle cx="19" cy="12" r="1.3" /></I>,
  you: <I><circle cx="12" cy="8" r="4" /><path d="M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6" /></I>,
  admin: <I><path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z" /></I>,
  out: <I><path d="M15 4h4v16h-4M10 17l5-5-5-5M15 12H3" /></I>,
}

const MAIN = [
  { to: '/', label: 'Today', icon: Icon.today, end: true },
  { to: '/watchlist', label: 'Watchlist', icon: Icon.watch },
  { to: '/scanner', label: 'Opportunities', icon: Icon.scan },
  { to: '/research', label: 'Research', icon: Icon.research },
  { to: '/journal', label: 'Journal', icon: Icon.journal },
]
const LEARN = [
  { to: '/track-record', label: 'Track record', icon: Icon.track },
  { to: '/replay', label: 'Replay', icon: Icon.replay },
  { to: '/methodology', label: 'How it works', icon: Icon.how },
]
const TABS = [MAIN[0], MAIN[1], MAIN[3], MAIN[4]]

function SignedIn() {
  const me = useMe()
  const loc = useLocation()
  const [status, setStatus] = useState<Status | null>(null)
  const [search, setSearch] = useState(false)
  useEffect(() => {
    const load = () => api.status().then(setStatus).catch(() => setStatus(null))
    load()
    const id = setInterval(load, 15000)
    return () => clearInterval(id)
  }, [])
  useEffect(() => {
    setSearch(false)
    window.scrollTo(0, 0)
  }, [loc.pathname])

  return (
    <div className="shell">
      <aside className="side" aria-label="Main">
        <Brand />
        {MAIN.map((n) => <NavLink key={n.to} to={n.to} end={n.end} className="navlink">{n.icon}{n.label}</NavLink>)}
        <div className="group">Learn</div>
        {LEARN.map((n) => <NavLink key={n.to} to={n.to} className="navlink">{n.icon}{n.label}</NavLink>)}
        <div className="grow" />
        <StatusLine status={status} />
        <UserMenu />
      </aside>

      <div className="main">
        <header className="mobile-top">
          <Brand />
          <div className="actions">
            <button className="iconbtn" aria-label="Search" aria-expanded={search} onClick={() => setSearch(!search)}>{Icon.research}</button>
            <Link to="/about-you" className="avatar" aria-label="About you">{initials(me.user.name || me.user.email)}</Link>
          </div>
        </header>
        {search && <div style={{ padding: '8px 16px 0' }}><SearchBox width={9999} /></div>}
        {loc.pathname !== '/' && (
          <div className="topline">
            <SearchBox />
          </div>
        )}

        <main className="page">
          <Suspense fallback={<Loading label="Loading…" />}>
            {/* New people answer the setup questions first (they can skip). */}
            {me.profile.status === '' && !['/welcome', '/about-you', '/reset'].includes(loc.pathname) ? <Navigate to="/welcome" replace /> : (
              <Routes>
                <Route path="/welcome" element={<Welcome />} />
                <Route path="/about-you" element={<AboutYou />} />
                {me.user.role === 'admin' && <Route path="/admin" element={<Admin />} />}
                <Route path="/reset" element={<Reset />} />
                <Route path="/login" element={<Navigate to="/" replace />} />
                <Route path="/signup" element={<Navigate to="/" replace />} />
                <Route path="/" element={<Today />} />
                <Route path="/watchlist" element={<Dashboard status={status} />} />
                <Route path="/asset/:symbol" element={<Asset status={status} />} />
                <Route path="/scanner" element={<Scanner />} />
                <Route path="/track-record" element={<TrackRecord />} />
                <Route path="/methodology" element={<Methodology />} />
                <Route path="/replay" element={<Replay />} />
                <Route path="/token/:address" element={<Token />} />
                <Route path="/journal" element={<Journal />} />
                <Route path="/journal/practice" element={<Practice />} />
                <Route path="/research" element={<ResearchHome />} />
                <Route path="/research/:subject" element={<Research />} />
              </Routes>
            )}
          </Suspense>
        </main>
      </div>

      <TabBar />
    </div>
  )
}

function TabBar() {
  const me = useMe()
  const { signOut } = useAuth()
  const loc = useLocation()
  const [open, setOpen] = useState(false)
  useEffect(() => setOpen(false), [loc.pathname])
  const inMore = [...LEARN.map((n) => n.to), '/scanner', '/about-you', '/admin'].some((p) => loc.pathname.startsWith(p))
  return (
    <>
      <nav className="tabbar" aria-label="Main">
        {TABS.map((n) => <NavLink key={n.to} to={n.to} end={n.end}>{n.icon}{n.label}</NavLink>)}
        <button className={inMore ? 'active' : ''} onClick={() => setOpen(true)} aria-haspopup="dialog">{Icon.more}More</button>
      </nav>
      <div className={`sheet-wrap ${open ? 'open' : ''}`} onClick={() => setOpen(false)}>
        <div className="sheet" role="dialog" aria-label="More" onClick={(e) => e.stopPropagation()}>
          <div className="grip" />
          <NavLink to="/scanner" className="navlink">{Icon.scan}Opportunities</NavLink>
          {LEARN.map((n) => <NavLink key={n.to} to={n.to} className="navlink">{n.icon}{n.label}</NavLink>)}
          <NavLink to="/about-you" className="navlink">{Icon.you}About you</NavLink>
          {me.user.role === 'admin' && <NavLink to="/admin" className="navlink">{Icon.admin}Admin</NavLink>}
          <button className="navlink" onClick={signOut}>{Icon.out}Sign out</button>
        </div>
      </div>
    </>
  )
}

function StatusLine({ status }: { status: Status | null }) {
  if (!status) return <div className="status-line"><span className="dot down" />Can't reach the server</div>
  const s = status.scheduler
  const title = `Scheduled runs at ${s.times.join(', ')}. Last run: ${s.lastRun && !s.lastRun.startsWith('0001') ? ago(s.lastRun) : 'never'}. Next: ${new Date(s.nextRun).toLocaleString()}`
  return (
    <div className="status-line" title={title}>
      {s.running || status.scan.running ? <span className="spinner" style={{ width: 12, height: 12 }} /> : <span className="dot" style={{ color: status.llm ? 'var(--accent-ink)' : 'var(--faint)' }} />}
      {s.running ? 'Updating analyses…' : status.llm ? 'AI analyst on' : 'AI analyst off'}
    </div>
  )
}

const initials = (label: string) => label.split(/[\s@.]+/).filter(Boolean).slice(0, 2).map((w) => w[0]?.toUpperCase()).join('')

function UserMenu() {
  const me = useMe()
  const { signOut } = useAuth()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const loc = useLocation()
  useEffect(() => setOpen(false), [loc.pathname])
  useEffect(() => {
    const close = (e: MouseEvent) => ref.current && !ref.current.contains(e.target as Node) && setOpen(false)
    document.addEventListener('mousedown', close)
    return () => document.removeEventListener('mousedown', close)
  }, [])
  const label = me.user.name || me.user.email
  return (
    <div className="usermenu" ref={ref}>
      <button onClick={() => setOpen(!open)} aria-expanded={open}>
        <span className="avatar">{initials(label)}</span>
        <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{me.user.name || 'You'}</span>
      </button>
      {open && (
        <div className="menu">
          <div className="who">
            <div style={{ fontWeight: 600 }}>{me.user.name || 'Signed in'}</div>
            <div className="small muted">{me.user.email}</div>
          </div>
          <Link to="/about-you">About you &amp; your look</Link>
          {me.user.role === 'admin' && <Link to="/admin">Admin</Link>}
          <button onClick={signOut}>Sign out</button>
        </div>
      )}
    </div>
  )
}
