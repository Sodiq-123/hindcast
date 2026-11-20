import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { auth, type Profile, type ProfileInput, type ProfileView, type QuizQuestion } from '../api'
import { useAuth, useMe } from '../auth'
import { Loading, Panel } from '../components'

type Opt = { v: string; label: string; sub?: string }

export const EXPERIENCE: Opt[] = [
  { v: 'new', label: 'New to investing', sub: "I haven't bought shares or crypto before, or only once or twice." },
  { v: 'some', label: 'Some experience', sub: "I've bought a few things and know the basics." },
  { v: 'experienced', label: 'Experienced', sub: 'I invest or trade regularly and know terms like P/E, stop-loss and volatility.' },
]
export const GOALS: Opt[] = [
  { v: 'grow', label: 'Grow my savings over years' },
  { v: 'income', label: 'Regular income' },
  { v: 'trade', label: 'Short-term trading' },
  { v: 'learn', label: 'Learn how markets work' },
  { v: 'protect', label: 'Protect my money from inflation or a weak currency' },
]
export const HORIZONS: Opt[] = [
  { v: '1d', label: 'Days', sub: 'In and out quickly.' },
  { v: '1w', label: 'Weeks' },
  { v: '1m', label: 'Months' },
  { v: '1y', label: 'A year or more', sub: 'Buy and hold.' },
]
export const INTERESTS: Opt[] = [
  { v: 'stocks', label: 'Individual stocks', sub: 'Apple, Nvidia, …' },
  { v: 'funds', label: 'Funds and ETFs', sub: 'One purchase that holds many companies, like the S&P 500.' },
  { v: 'crypto', label: 'Major crypto', sub: 'Bitcoin, Ethereum.' },
  { v: 'meme', label: 'Meme coins and DEX tokens', sub: 'Pump.fun, Solana and Base tokens.' },
]
export const DROPS: Opt[] = [
  { v: 'sell_all', label: 'Sell everything', sub: "I'd rather lock in the loss than risk more." },
  { v: 'sell_some', label: 'Sell some' },
  { v: 'hold', label: 'Hold and wait' },
  { v: 'buy_more', label: 'Buy more', sub: 'It is cheaper now.' },
]

export function Choices({ opts, value, onChange, multi }: { opts: Opt[]; value: string[]; onChange: (v: string[]) => void; multi?: boolean }) {
  return (
    <div className="choices">
      {opts.map((o) => {
        const on = value.includes(o.v)
        return (
          <button type="button" key={o.v} className={`choice ${on ? 'on' : ''}`} aria-pressed={on}
            onClick={() => onChange(multi ? (on ? value.filter((x) => x !== o.v) : [...value, o.v]) : [o.v])}>
            <span>{o.label}{o.sub && <span className="sub">{o.sub}</span>}</span>
          </button>
        )
      })}
    </div>
  )
}

const empty = (p: Profile): ProfileInput => ({
  experience: p.experience, goals: p.goals ?? [], horizon: p.horizon, interests: p.interests ?? [],
  dropReaction: p.dropReaction, detail: p.detail, wording: p.wording ?? '', status: p.status,
})

const STEPS = ['experience', 'goals', 'horizon', 'interests', 'drop', 'quiz'] as const

export default function Welcome() {
  const me = useMe()
  const { refresh } = useAuth()
  const nav = useNavigate()
  const [step, setStep] = useState(-1)
  const [p, setP] = useState<ProfileInput>(() => empty(me.profile))
  const [questions, setQuestions] = useState<QuizQuestion[] | null>(null)
  const [answers, setAnswers] = useState<Record<string, number>>({})
  const [result, setResult] = useState<ProfileView | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    auth.quiz().then(setQuestions).catch((e) => setErr((e as Error).message))
  }, [])

  const set = <K extends keyof ProfileInput>(k: K, v: ProfileInput[K]) => setP((x) => ({ ...x, [k]: v }))

  const save = async (status: 'done' | 'skipped', withQuiz: boolean) => {
    setBusy(true)
    setErr('')
    try {
      const r = await auth.saveProfile({ ...p, status, quizAnswers: withQuiz ? answers : undefined })
      await refresh()
      if (status === 'skipped') nav('/', { replace: true })
      else setResult(r)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (result) return <Results view={result} questions={questions ?? []} answers={answers} />

  const name = me.user.name ? `, ${me.user.name.split(' ')[0]}` : ''
  if (step < 0) {
    return (
      <div className="setup">
        <h1 style={{ fontSize: 36 }}>Welcome{name}</h1>
        <p className="answer-more" style={{ marginTop: 0 }}>
          Six quick questions, about two minutes, so the app shows what matters to you and explains things at the right level.
        </p>
        <div className="welcome-tiles">
          <div><span className="n">1</span><strong>How much is explained</strong><span className="small muted">Plain words for beginners, the numbers for experts.</span></div>
          <div><span className="n">2</span><strong>Which horizon leads</strong><span className="small muted">A day, a week, a month or a year ahead.</span></div>
          <div><span className="n">3</span><strong>What you start with</strong><span className="small muted">A starter watchlist from your interests.</span></div>
        </div>
        <p className="small muted">
          Your answers never change a forecast, a test result or a warning: those are the same for everyone. Change any answer later on <em>About you</em>, which shows exactly what each one does.
        </p>
        <div className="row">
          <button className="primary" onClick={() => setStep(0)}>Start</button>
          <button className="ghost" disabled={busy} onClick={() => save('skipped', false)}>Skip for now</button>
        </div>
        {err && <div className="callout bad">{err}</div>}
      </div>
    )
  }

  const key = STEPS[step]
  const canNext =
    (key === 'experience' && !!p.experience) || (key === 'goals' && p.goals.length > 0) || (key === 'horizon' && !!p.horizon) ||
    (key === 'interests' && p.interests.length > 0) || (key === 'drop' && !!p.dropReaction) || key === 'quiz'

  return (
    <div className="setup">
      <div className="steps">{STEPS.map((s, i) => <span key={s} className={i <= step ? 'on' : ''} />)}</div>
      <p className="tag faint">Question {step + 1} of {STEPS.length}</p>
      {key === 'experience' && (
        <Panel title="How much investing have you done?">
          <Choices opts={EXPERIENCE} value={[p.experience]} onChange={([v]) => set('experience', v as ProfileInput['experience'])} />
        </Panel>
      )}
      {key === 'goals' && (
        <Panel title="What do you want from investing?" sub="Pick all that apply.">
          <Choices multi opts={GOALS} value={p.goals} onChange={(v) => set('goals', v)} />
        </Panel>
      )}
      {key === 'horizon' && (
        <Panel title="How long do you usually hold something?">
          <Choices opts={HORIZONS} value={[p.horizon]} onChange={([v]) => set('horizon', v as ProfileInput['horizon'])} />
        </Panel>
      )}
      {key === 'interests' && (
        <Panel title="What are you interested in?" sub="Pick all that apply.">
          <Choices multi opts={INTERESTS} value={p.interests} onChange={(v) => set('interests', v)} />
        </Panel>
      )}
      {key === 'drop' && (
        <Panel title="Something you own falls 30% in a month. What would you most likely do?" sub="There's no right answer. It tells the app how you feel about falls.">
          <Choices opts={DROPS} value={[p.dropReaction]} onChange={([v]) => set('dropReaction', v as ProfileInput['dropReaction'])} />
        </Panel>
      )}
      {key === 'quiz' && (
        <Panel title="Quick knowledge check" sub="Five questions. Not a test you can fail: it sets how much the app explains. You'll see the answers at the end.">
          {!questions ? <Loading label="Loading…" /> : (
            <div className="stack" style={{ gap: 22 }}>
              {questions.map((q, i) => (
                <div key={q.id} className="stack">
                  <strong>{i + 1}. {q.text}</strong>
                  <Choices opts={q.options.map((o, j) => ({ v: String(j), label: o }))} value={answers[q.id] != null ? [String(answers[q.id])] : []}
                    onChange={([v]) => setAnswers((a) => ({ ...a, [q.id]: Number(v) }))} />
                </div>
              ))}
            </div>
          )}
        </Panel>
      )}
      {err && <div className="callout bad">{err}</div>}
      <div className="row between">
        <button className="ghost" onClick={() => setStep(step - 1)}>Back</button>
        <div className="row">
          {key === 'quiz' && <button className="ghost" disabled={busy} onClick={() => save('done', false)}>Skip the quiz</button>}
          {key === 'quiz' ? (
            <button className="primary" disabled={busy || Object.keys(answers).length === 0} onClick={() => save('done', true)}>
              {busy ? <span className="spinner" /> : 'See my answers'}
            </button>
          ) : (
            <button className="primary" disabled={!canNext} onClick={() => setStep(step + 1)}>Next</button>
          )}
        </div>
      </div>
    </div>
  )
}

function Results({ view, questions, answers }: { view: ProfileView; questions: QuizQuestion[]; answers: Record<string, number> }) {
  const q = view.profile.quiz
  return (
    <div className="setup">
      <h1>You're set up</h1>
      {q && view.correct && (
        <Panel title={`Knowledge check: ${q.score} of ${q.total}`} sub="The explanations are worth a read even for the ones you got right.">
          <div className="stack" style={{ gap: 18 }}>
            {questions.map((qu, i) => {
              const picked = answers[qu.id]
              const right = view.correct![qu.id]
              return (
                <div key={qu.id} className="stack">
                  <strong>{i + 1}. {qu.text}</strong>
                  <div className="choices">
                    {qu.options.map((o, j) => (
                      <div key={j} className={`choice ${j === right ? 'right' : j === picked ? 'wrong' : ''}`} style={{ cursor: 'default', border: '1px solid var(--border-strong)' }}>
                        {o}{j === right ? ' ✓' : j === picked ? ' (your answer)' : ''}
                      </div>
                    ))}
                  </div>
                  <p className="small muted">{qu.explain}</p>
                </div>
              )
            })}
          </div>
        </Panel>
      )}
      <Panel title="What the app will do with your answers">
        <EffectsTable effects={view.effects} />
      </Panel>
      {view.starter && view.starter.length > 0 && (
        <div className="callout info">Your watchlist starts with {view.starter.join(', ')}, based on your interests. Add or remove anything.</div>
      )}
      <div className="row">
        <Link to="/" className="btn primary">See my day</Link>
        <Link to="/about-you" className="btn">Review on About you</Link>
      </div>
    </div>
  )
}

export function EffectsTable({ effects }: { effects: ProfileView['effects'] }) {
  return (
    <div className="effects">
      <div className="head">Question</div><div className="head">Your answer</div><div className="head">What it changes</div>
      {effects.map((e) => [
        <div key={e.field + 'f'} className="eff-q">{e.field}</div>,
        <div key={e.field + 'a'} className={e.answer.startsWith('Not ') ? 'faint' : ''}>{e.answer}</div>,
        <div key={e.field + 'e'} className="small muted">{e.effect}</div>,
      ])}
    </div>
  )
}
