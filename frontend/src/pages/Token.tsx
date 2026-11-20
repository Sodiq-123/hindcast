import { useCallback, useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, type SymbolRank, type TokenFlag, type TokenReport, type TokenRisk } from '../api'
import { TokenChart } from '../charts'
import { BackLink, Loading, Panel, Stat } from '../components'
import { ago, pct, spct, tone } from '../format'

const usd = (x: number) =>
  !Number.isFinite(x) || x <= 0 ? '—' : x >= 1e9 ? `$${(x / 1e9).toFixed(2)}B` : x >= 1e6 ? `$${(x / 1e6).toFixed(2)}M` : x >= 1e3 ? `$${(x / 1e3).toFixed(1)}K` : `$${x.toFixed(2)}`
const tokenPrice = (x: number) =>
  !x ? '—' : x >= 1 ? `$${x.toFixed(4)}` : `$${x.toFixed(Math.min(20, 3 - Math.floor(Math.log10(x))))}`
const CHAIN: Record<string, string> = { solana: 'Solana', ethereum: 'Ethereum', base: 'Base' }
const short = (a: string) => `${a.slice(0, 6)}…${a.slice(-4)}`

export default function Token() {
  const { address = '' } = useParams()
  const [rep, setRep] = useState<TokenReport | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const load = useCallback(
    (refresh: boolean) => {
      setBusy(true)
      setErr('')
      api.token(address, refresh).then(setRep).catch((e) => setErr((e as Error).message)).finally(() => setBusy(false))
    },
    [address],
  )
  useEffect(() => {
    setRep(null)
    load(false)
  }, [load])

  if (err && !rep) return <div className="callout bad">Could not build a report for {address}: {err}</div>
  if (!rep) return <Loading label="Checking the contract, holders, liquidity and price history…" />

  const counts = { red: 0, amber: 0, green: 0 }
  rep.flags.forEach((f) => counts[f.level]++)
  const reds = rep.flags.filter((f) => f.level === 'red')
  const ambers = rep.flags.filter((f) => f.level === 'amber')
  const greens = rep.flags.filter((f) => f.level === 'green')
  const verdictTone = rep.riskScore >= 60 ? 'down' : rep.riskScore >= 15 ? 'hold' : 'up'
  const h = rep.history
  const ch = rep.change.h24 ?? 0
  return (
    <div className="stack" style={{ gap: 20 }}>
      <div className="stack" style={{ gap: 10 }}>
        <BackLink fallback="/" />
        <header className="asset-head">
          <div>
            <div className="row" style={{ alignItems: 'baseline', gap: 12 }}>
              <h1 style={{ fontSize: 36 }}>{rep.name}</h1>
              <span className="mono muted small">{rep.symbol} · {rep.chainLabel} token</span>
            </div>
            <p className="row" style={{ alignItems: 'baseline', gap: 12, marginTop: 6, flexWrap: 'wrap' }}>
              <span className="mono" style={{ fontSize: 26, fontWeight: 500, letterSpacing: '-0.02em' }}>{tokenPrice(rep.priceUsd)}</span>
              {ch !== 0 && <span className={`mono ${ch > 0 ? 'up' : 'down'}`}>{ch > 0 ? '▲' : '▼'} {Math.abs(ch * 100).toFixed(1)}% <span className="small faint">24h</span></span>}
              <span className="small faint">
                <span className="mono" title={rep.address}>{short(rep.address)}</span>{' '}
                <button className="linkish small" onClick={() => navigator.clipboard?.writeText(rep.address)}>copy</button> · pool on {rep.dexId} · <a href={rep.pairUrl} target="_blank" rel="noreferrer">DexScreener ↗</a> · checked {ago(rep.fetchedAt)}
              </span>
            </p>
          </div>
          <div className="row" style={{ gap: 8, flexWrap: 'wrap' }}>
            <WatchButton address={rep.address} />
            <Link className="btn" to={`/research/${rep.address}`}>Research</Link>
            <Link className="btn" to={`/journal?symbol=${rep.address}&signal=${encodeURIComponent(`Token check: ${rep.verdict} (${rep.riskScore}/100)`)}`}>Paper trade</Link>
            <button onClick={() => load(true)} disabled={busy}>{busy ? <><span className="spinner" /> Checking…</> : 'Re-check'}</button>
          </div>
        </header>
      </div>

      <IdentityBanner rep={rep} />

      <section className="panel answer">
        <div className="answer-top">
          <div style={{ maxWidth: 820 }}>
            <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
            <h2 className={`answer-say ${verdictTone}`}>{rep.verdict.replace(/\.$/, '')}.</h2>
            <p className="answer-more">{rep.summary}</p>
          </div>
        </div>
        <div className="answer-stats four">
          <div>
            <p className="tag faint">Warning signs</p>
            <div className="risk-scale" role="img" aria-label={`Warning-sign score ${rep.riskScore} out of 100`}>
              <i style={{ width: `${Math.min(100, rep.riskScore)}%`, background: `var(--${verdictTone === 'hold' ? 'hold' : verdictTone})` }} />
            </div>
            <p className="small muted">Score {rep.riskScore}/100 · not a chance of a rug pull</p>
          </div>
          <div>
            <p className="tag faint">Checks</p>
            <p className="mono mid"><span className="down">{counts.red} red</span> · <span className="hold">{counts.amber} amber</span></p>
            <p className="small muted">{counts.green} passed</p>
          </div>
          <div>
            <p className="tag faint">Liquidity</p>
            <p className="mono mid">{usd(rep.totalLiquidity)}</p>
            <p className="small muted">{rep.marketCap || rep.fdv ? `${pct(rep.totalLiquidity / (rep.marketCap || rep.fdv), 1)} of market cap · under ~2% is thin` : 'across all pools'}</p>
          </div>
          <div>
            <p className="tag faint">Top 10 wallets hold</p>
            <p className="mono mid">{pct(rep.top10, 0)}</p>
            <p className="small muted">largest single wallet {pct(rep.top1, 1)}</p>
          </div>
        </div>
      </section>

      <div className="why-grid">
        <section className="panel pad-card stack" style={{ gap: 4 }}>
          <h2 style={{ marginBottom: 8 }}>The checks, worst first</h2>
          {[...reds, ...ambers].map((f, i) => <FlagRow key={i} f={f} />)}
          {reds.length + ambers.length === 0 && <p className="muted">No red or amber signs.</p>}
          {greens.length > 0 && <Passed flags={greens} />}
          {rep.notes?.map((n) => <p key={n} className="small faint" style={{ marginTop: 8 }}>ℹ {n}</p>)}
          <p className="small faint" style={{ marginTop: 10 }}>Red = a known way to lose your money (scam mechanics, extreme concentration, no liquidity). Amber = a risk to understand before buying. Each red counts 25 towards the score, each amber 8.</p>
        </section>
        <section className="panel pad-card stack" style={{ gap: 14 }}>
          <h2>The numbers</h2>
          <div className="tok-grid">
            <div><p className="tag faint">Market cap</p><p className="mono">{usd(rep.marketCap || rep.fdv)}</p>{rep.fdv && rep.fdv !== rep.marketCap ? <p className="tiny faint">fully diluted {usd(rep.fdv)}</p> : null}</div>
            <div><p className="tag faint">Main pool</p><p className="mono">{usd(rep.liquidity)}</p><p className="tiny faint">{rep.pairs} pool{rep.pairs === 1 ? '' : 's'} in all</p></div>
            <div><p className="tag faint">Volume, 24h</p><p className="mono">{usd(rep.volume24h)}</p><p className="tiny faint">{rep.buys24h.toLocaleString()} buys / {rep.sells24h.toLocaleString()} sells</p></div>
            <div><p className="tag faint">Age</p><p className="mono">{rep.ageDays >= 365 ? `${(rep.ageDays / 365).toFixed(1)} years` : `${Math.round(rep.ageDays)} days`}</p>{rep.launched && <p className="tiny faint">first pool {rep.launched.slice(0, 10)}</p>}</div>
            <div><p className="tag faint">Holders</p><p className="mono">{rep.holderCount ? rep.holderCount.toLocaleString() : '—'}</p></div>
            <div><p className="tag faint">Largest holder</p><p className="mono">{pct(rep.top1)}</p><p className="tiny faint">pools, locked and burned excluded</p></div>
          </div>
          <p className="small muted" style={{ lineHeight: 1.6 }}><strong style={{ color: 'var(--text)' }}>No price forecast.</strong> {rep.noForecast.replace(/^No price forecast[:.]\s*/i, '').replace(/^./, (c) => c.toUpperCase())}</p>
        </section>
      </div>

      <section className="panel pad-card">
        <h2>Price history</h2>
        <p className="small muted" style={{ margin: '4px 0 12px' }}>{h ? `${h.interval === 'day' ? 'Daily' : 'Hourly'} candles from the main pool, about ${Math.round(h.windowDays)} days (free data only goes back about 6 months).` : 'Unavailable.'}</p>
        {h ? (
          <div className="stack">
            <TokenChart candles={h.candles} />
            <div className="grid grid-4">
              <Stat label="7 days" value={spct(h.return7d)} tone={tone(h.return7d ?? 0)} />
              <Stat label="30 days" value={spct(h.return30d)} tone={tone(h.return30d ?? 0)} />
              <Stat label="From the high" value={spct(h.fromHigh)} tone={tone(h.fromHigh)} hint={`high ${tokenPrice(h.high)}`} />
              <Stat label="Typical daily move" value={`±${pct(h.typicalDayMove, 0)}`} hint={`annualised volatility ${pct(h.annualizedVol, 0)}`} />
            </div>
          </div>
        ) : <div className="empty">No price history available.</div>}
      </section>

      {rep.risk && <RiskPanel r={rep.risk} symbol={rep.symbol} />}
      <MemeRank address={rep.address} symbol={rep.symbol} />
      {rep.holders && rep.holders.length > 0 && <Holders holders={rep.holders} />}
    </div>
  )
}

function Passed({ flags }: { flags: TokenFlag[] }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button className="linkish" style={{ alignSelf: 'flex-start', marginTop: 8, fontSize: 14 }} onClick={() => setOpen(!open)} aria-expanded={open}>
        {open ? 'Hide the passed checks' : `${flags.length} check${flags.length === 1 ? '' : 's'} passed`}
      </button>
      {open && flags.map((f, i) => <FlagRow key={i} f={f} />)}
    </>
  )
}

function Holders({ holders }: { holders: NonNullable<TokenReport['holders']> }) {
  const [open, setOpen] = useState(false)
  return (
    <section className="panel" style={{ overflow: 'hidden' }}>
      <div className="panel-head">
        <div><h2>Largest holders</h2><p className="sub">Pools, locked and burned balances are labelled and left out of the concentration figures. A big unlabelled wallet could be the team, an exchange or a whale: check it on a block explorer.</p></div>
        <button className="linkish" onClick={() => setOpen(!open)} aria-expanded={open}>{open ? 'Hide' : `Show ${holders.length}`}</button>
      </div>
      {open && (
        <div className="table-wrap">
          <table>
            <thead><tr><th>#</th><th>Wallet</th><th className="r">Share of supply</th><th>Type</th></tr></thead>
            <tbody>
              {holders.map((x, i) => (
                <tr key={x.address + i}>
                  <td className="faint">{i + 1}</td>
                  <td className="mono small" title={x.address}>{short(x.address)}</td>
                  <td className="num r">{pct(x.percent, 2)}</td>
                  <td className="small muted">{[x.isPool && 'liquidity pool', x.locked && 'locked', x.tag].filter(Boolean).join(' · ') || 'wallet'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

function WatchButton({ address }: { address: string }) {
  const [state, setState] = useState<'idle' | 'busy' | 'done'>('idle')
  useEffect(() => {
    api.watchTokens().then((ts) => ts.some((t) => t.address === address) && setState('done')).catch(() => {})
  }, [address])
  return (
    <button
      disabled={state !== 'idle'}
      onClick={() => {
        setState('busy')
        api.addWatch(address).then(() => setState('done')).catch(() => setState('idle'))
      }}
    >
      {state === 'done' ? 'In watchlist' : '+ Watchlist'}
    </button>
  )
}

const day = (d: string) => new Date(d + 'T00:00:00Z').toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' })

/** Measured risk over the longest reliable daily history: how much it moves, not where. */
function RiskPanel({ r, symbol }: { r: TokenRisk; symbol: string }) {
  return (
    <Panel
      title={`How much ${symbol} moves: measured over ${r.days} days`}
      sub={`${day(r.from)} to ${day(r.to)} · ${r.source}. These figures describe the past; a year of daily data measures them well, unlike a direction forecast.`}
    >
      <div className="stack">
        <div className="grid grid-4">
          <Stat label="Typical day" value={`±${pct(r.typicalDay)}`} hint={`annualised volatility ${pct(r.annualVol, 0)} (Bitcoin ~40–60%, Apple ~25–30%)`} />
          <Stat label="Worst day" value={spct(r.worstDay.return)} tone="down" hint={day(r.worstDay.date)} />
          <Stat label="Worst week (7 days)" value={spct(r.worstWeek.return)} tone="down" hint={`to ${day(r.worstWeek.date)}`} />
          <Stat label="Worst month (30 days)" value={spct(r.worstMonth.return)} tone="down" hint={`to ${day(r.worstMonth.date)}`} />
          <Stat label="Biggest fall from a high" value={spct(r.maxDrawdown)} tone="down" hint={r.peakDate ? `${day(r.peakDate)} → ${day(r.troughDate)}` : undefined} />
          <Stat label="Best day" value={spct(r.bestDay.return)} tone="up" hint={day(r.bestDay.date)} />
          <Stat label="Days moving 10%+" value={pct(r.bigDayShare, 0)} hint="either direction" />
          <Stat label="Weeks falling 20%+" value={pct(r.crashWeekRate, 0)} hint="share of all 7-day stretches" />
        </div>
        <div className="callout warn">
          <strong>1-in-20 bad outcomes:</strong> a day of {spct(r.bad1in20Day)} or worse, and a week of {spct(r.bad1in20Week)} or worse, happened 5% of the time.
          {r.sizeFor1Pct > 0 && (
            <> To keep a 1-in-20 bad week under <strong>1% of your account</strong>, hold at most <strong>{pct(r.sizeFor1Pct)}</strong> of the account in {symbol}. Worse weeks than that do happen (its worst was {spct(r.worstWeek.return)}).</>
          )}
        </div>
        {r.bench && r.bench.length > 0 && (
          <table>
            <thead>
              <tr><th>Compared with</th><th className="r">Moves together (correlation)</th><th className="r">Typical move per 1% of it (beta)</th><th>On its bad days</th></tr>
            </thead>
            <tbody>
              {r.bench.map((b) => (
                <tr key={b.symbol}>
                  <td>{b.name}</td>
                  <td className="num r">{b.corr.toFixed(2)}</td>
                  <td className="num r">{b.beta.toFixed(2)}%</td>
                  <td className="small muted">
                    {b.downDays > 0
                      ? `On the ${b.downDays} days ${b.name} fell 3% or more (avg ${spct(b.benchOnDown)}), ${symbol} moved ${spct(b.tokenOnDown)} on average.`
                      : `${b.name} never fell 3% in a day over this period.`}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <p className="tiny faint">Correlation runs from −1 to 1: near 1 means it rises and falls with the other coin. A beta of 2 means it typically moves twice as much. Neither says which way it will move next.</p>
      </div>
    </Panel>
  )
}

/** The token's place in the meme-coin ranking, if it is one of the ranked coins. */
function MemeRank({ address, symbol }: { address: string; symbol: string }) {
  const [ranks, setRanks] = useState<SymbolRank[] | null>(null)
  useEffect(() => {
    api.tokenRanks(address).then(setRanks).catch(() => setRanks([]))
  }, [address])
  if (!ranks || ranks.length === 0) return null
  const label: Record<string, string> = { validated: 'Validated edge', weak: 'Weak evidence', no_edge: 'No proven edge' }
  return (
    <Panel title={`${symbol} in the meme-coin ranking`} sub="Which meme coins are likely to beat the others, from a model trained on 24 meme coins together and tested out-of-sample.">
      <div className="stack">
        <div className="grid grid-3">
          {ranks.map((r) => (
            <Stat key={r.horizon} label={`${r.horizon === '1d' ? 'Next day' : r.horizon === '1w' ? 'Next week' : 'Next month'} · ${label[r.status] ?? r.status}`}
              value={`#${r.item.rank} of ${r.item.of}`} tone={r.status === 'validated' ? 'up' : ''} hint={`P(beats the median meme coin) ${pct(r.item.pOutperform)}`} />
          ))}
        </div>
        <p className="small muted">
          Only a <strong>Validated edge</strong> ranking means anything; otherwise the order is shown for transparency.{' '}
          <Link to="/scanner?universe=meme&h=1w">See the full meme-coin ranking and its test results →</Link>
        </p>
      </div>
    </Panel>
  )
}

/** Is this the coin people mean by its ticker? Shown first, before any analysis. */
function IdentityBanner({ rep }: { rep: TokenReport }) {
  const sym = rep.symbol.toUpperCase()
  const rank = (r?: number) => (r ? ` (#${r} by market cap)` : '')
  const l = rep.listing
  const o = rep.lookalike
  if (l && !o)
    return (
      <div className="callout good">
        <strong>✓ This is the original {sym}.</strong> CoinGecko lists this exact address as <a href={l.url} target="_blank" rel="noreferrer">{l.name}</a>
        {rank(l.rank)}. Being listed confirms which token this is; it says nothing about whether it's safe. Read the checks below for that.
      </div>
    )
  if (o?.elsewhere)
    return (
      <div className="callout bad">
        <strong>⚠ This is NOT {o.name}.</strong> CoinGecko's {sym} is {o.name}{rank(o.rank)}, which isn't on Solana, Ethereum or Base at all, so any {sym} token on{' '}
        {rep.chainLabel} is a lookalike.{' '}
        <Link to={`/research/cg:${o.id}`}>Research the real {o.name} →</Link>
      </div>
    )
  if (o)
    return (
      <div className={`callout ${l ? 'warn' : 'bad'}`}>
        <strong>{l ? `⚠ A bigger, different coin also uses the ticker ${sym}.` : `⚠ This is NOT the listed ${sym}.`}</strong>{' '}
        {l ? `This one is listed as ${l.name}${rank(l.rank)}. ` : 'It copies the ticker at a different address. '}
        CoinGecko's {sym} is <strong>{o.name}</strong>{rank(o.rank)} at <span className="mono">{o.address}</span> on {CHAIN[o.chain] ?? o.chain}.{' '}
        <Link to={`/token/${o.address}`}>Open the original →</Link>
      </div>
    )
  if (rep.flags.some((f) => f.area === 'Identity'))
    return (
      <div className="callout warn">
        <strong>⚠ Can't confirm this is the original {sym}.</strong> It isn't listed on CoinGecko, so nothing independent ties this address to the name. Copy the
        address from the project's official website or account and check it matches: <span className="mono">{rep.address}</span>
      </div>
    )
  return null
}

function FlagRow({ f }: { f: TokenFlag }) {
  return (
    <div className="check-row tok-flag">
      <span className={`lvl ${f.level}`} aria-label={f.level === 'red' ? 'red sign' : f.level === 'amber' ? 'amber sign' : 'passed'} />
      <span><strong>{f.title}</strong> <span className="tiny faint">· {f.area}</span><span className="small muted" style={{ display: 'block', marginTop: 2 }}>{f.detail}</span></span>
    </div>
  )
}
