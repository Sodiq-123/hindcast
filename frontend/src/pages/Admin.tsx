import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { auth, type Invite, type UserRow } from '../api'
import { useMe } from '../auth'
import { Loading, Panel } from '../components'
import { ago } from '../format'

function Copy({ text }: { text: string }) {
  const [done, setDone] = useState(false)
  return (
    <button className="small" onClick={() => navigator.clipboard.writeText(text).then(() => { setDone(true); setTimeout(() => setDone(false), 1500) })}>
      {done ? 'Copied' : 'Copy'}
    </button>
  )
}

export default function Admin() {
  const [err, setErr] = useState('')
  return (
    <>
      <header style={{ maxWidth: 760 }}>
        <h1 style={{ fontSize: 36 }}>Admin</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>Invite people, and manage who can sign in. You can’t see anyone’s watchlist or journal here, only how many items they have.</p>
      </header>
      {err && <div className="callout bad">{err}</div>}
      <Invites onError={setErr} />
      <Users onError={setErr} />
    </>
  )
}

function Invites({ onError }: { onError: (e: string) => void }) {
  const [list, setList] = useState<Invite[] | null>(null)
  const [note, setNote] = useState('')
  const [uses, setUses] = useState(20)
  const [days, setDays] = useState(14)
  const [role, setRole] = useState('member')
  const [made, setMade] = useState<{ link: string; code: string } | null>(null)
  const load = useCallback(() => auth.invites().then(setList).catch((e) => onError((e as Error).message)), [onError])
  useEffect(() => {
    load()
  }, [load])

  const create = async (e: FormEvent) => {
    e.preventDefault()
    try {
      setMade(await auth.createInvite({ note, maxUses: uses, days, role }))
      setNote('')
      load()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  return (
    <Panel title="Invites" sub="One link can be shared with a whole group of testers: set how many people may use it and for how long.">
      <div className="stack">
        <form className="form-grid" onSubmit={create} style={{ alignItems: 'end' }}>
          <label>Who it's for (only you see this)<input value={note} maxLength={200} onChange={(e) => setNote(e.target.value)} placeholder="e.g. Twitter testers" /></label>
          <label>People who can use it<input type="number" min={1} max={1000} value={uses} onChange={(e) => setUses(Number(e.target.value))} /></label>
          <label>Valid for (days)<input type="number" min={1} max={90} value={days} onChange={(e) => setDays(Number(e.target.value))} /></label>
          <label>They join as<select value={role} onChange={(e) => setRole(e.target.value)}><option value="member">Member</option><option value="admin">Admin</option></select></label>
          <div><button className="primary">Create invite link</button></div>
        </form>
        {role === 'admin' && <div className="callout warn">Admins can run scans, retrain models, change anyone's role and switch accounts off. Keep admin invites to people you trust.</div>}
        {made && (
          <div className="callout good stack">
            <strong>Invite link created. Copy it now: it isn't shown again (only a fingerprint of it is stored).</strong>
            <div className="row"><div className="secret" style={{ flex: 1 }}>{made.link}</div><Copy text={made.link} /></div>
          </div>
        )}
        {!list ? <Loading label="Loading…" /> : list.length > 0 && (
          <div className="table-wrap">
            <table>
              <thead><tr><th>For</th><th>Role</th><th className="r">Used</th><th>Expires</th><th>Made by</th><th /></tr></thead>
              <tbody>
                {list.map((i) => {
                  const dead = i.revoked || i.uses >= i.maxUses || new Date(i.expiresAt) < new Date()
                  return (
                    <tr key={i.id} className={dead ? 'faint' : ''}>
                      <td>{i.note || <span className="faint">no note</span>}</td>
                      <td>{i.role}</td>
                      <td className="num r">{i.uses} / {i.maxUses}</td>
                      <td className="small">{i.revoked ? 'revoked' : new Date(i.expiresAt).toLocaleDateString()}</td>
                      <td className="small muted">{i.createdBy}</td>
                      <td className="r">{!dead && <button className="small danger ghost" onClick={() => auth.revokeInvite(i.id).then(load)}>Revoke</button>}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </Panel>
  )
}

function Users({ onError }: { onError: (e: string) => void }) {
  const me = useMe()
  const [list, setList] = useState<UserRow[] | null>(null)
  const [link, setLink] = useState<{ email: string; link: string } | null>(null)
  const load = useCallback(() => auth.users().then(setList).catch((e) => onError((e as Error).message)), [onError])
  useEffect(() => {
    load()
  }, [load])
  const act = (p: Promise<unknown>) => p.then(load).catch((e) => onError((e as Error).message))

  return (
    <Panel title="People" sub={list ? `${list.length} account${list.length === 1 ? '' : 's'}` : ''} pad={false}>
      {link && (
        <div className="callout good stack" style={{ margin: 12 }}>
          <strong>Reset link for {link.email}. It works once, for 24 hours. Send it to them privately.</strong>
          <div className="row"><div className="secret" style={{ flex: 1 }}>{link.link}</div><Copy text={link.link} /></div>
        </div>
      )}
      {!list ? <Loading label="Loading…" /> : (
        <div className="table-wrap">
          <table>
            <thead><tr><th>Person</th><th>Role</th><th>Setup</th><th className="r">Watching</th><th className="r">Journal</th><th>Joined</th><th>Last sign-in</th><th /></tr></thead>
            <tbody>
              {list.map((u) => (
                <tr key={u.id} className={u.disabled ? 'faint' : ''}>
                  <td>
                    <div>{u.name || u.email}{u.id === me.user.id && <span className="badge accent" style={{ marginLeft: 6 }}>you</span>}{u.disabled && <span className="badge down" style={{ marginLeft: 6 }}>switched off</span>}</div>
                    {u.name && <div className="small muted">{u.email}</div>}
                  </td>
                  <td>{u.role}</td>
                  <td className="small">{u.profileStatus === 'done' ? 'done' : u.profileStatus === 'skipped' ? 'skipped' : 'not yet'}</td>
                  <td className="num r">{u.watching}</td>
                  <td className="num r">{u.positions}</td>
                  <td className="small muted">{new Date(u.createdAt).toLocaleDateString()}</td>
                  <td className="small muted">{u.lastLoginAt ? ago(u.lastLoginAt) : 'never'}</td>
                  <td className="r">
                    {u.id !== me.user.id && (
                      <div className="row" style={{ justifyContent: 'flex-end', gap: 6 }}>
                        <button className="small" onClick={() => act(auth.updateUser(u.id, { role: u.role === 'admin' ? 'member' : 'admin' }))}>
                          {u.role === 'admin' ? 'Make member' : 'Make admin'}
                        </button>
                        <button className="small" onClick={() => auth.resetLink(u.id).then((r) => setLink({ email: u.email, link: r.link })).catch((e) => onError((e as Error).message))}>Reset link</button>
                        <button className="small" onClick={() => act(auth.updateUser(u.id, { disabled: !u.disabled }))}>{u.disabled ? 'Switch on' : 'Switch off'}</button>
                        <button className="small danger ghost" onClick={() => confirm(`Delete ${u.email} and their watchlist and journal? This can't be undone.`) && act(auth.deleteUser(u.id))}>Delete</button>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  )
}
