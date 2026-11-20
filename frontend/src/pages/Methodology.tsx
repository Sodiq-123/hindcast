import { useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'

const FLOW: [string, string][] = [
  ['Data', 'Decades of daily prices, the market around each asset, earnings and news.'],
  ['Two models', 'One learns how each input has mattered; one finds the most similar past days.'],
  ['Tested on the past', 'Rebuilt about 40 times through history, each time seeing only what was known then.'],
  ['Only what passes', 'A signal shows only if it beat simple guessing in that test. Otherwise: no proven edge.'],
  ['Scored live', 'Every forecast is saved and scored when its date arrives. Signals that slip get switched off.'],
]

const MARKS: [string, string, string][] = [
  ['✓', 'up', 'Tested edge: beat simple guessing out of sample, by enough to be unlikely to be luck.'],
  ['◐', 'hold', 'Weak evidence: some sign of skill, not enough to rely on.'],
  ['○', 'faint', 'Not proven: shown so you can see it, not to act on.'],
]

const WORDS: [string, string][] = [
  ['Out of sample', 'Tested on data the model never saw while learning. The only kind of test worth trusting.'],
  ['Base rate', 'How often something usually happens, e.g. how often a stock is higher a month later. The bar any forecast has to beat.'],
  ['80% range', 'Where 8 in 10 similar past cases ended up. One in five lands outside it, by design.'],
  ['Brier score', 'How close stated chances were to what happened. 0.25 is a coin flip; lower is better.'],
  ['AUC', 'How well the model ranks ups above downs. 0.5 is chance; 0.52 is the minimum it must reach here.'],
  ['Walk-forward', 'Testing that steps through history, retraining each time on the past only, like living through it.'],
  ['Hindcast', 'A forecast made for the past, to check the method before trusting it. Every signal here passed hundreds of them, which is where the app’s name comes from.'],
]

const SECTIONS: { title: string; sum: string; body: ReactNode }[] = [
  {
    title: 'Data',
    sum: 'Full daily history, the asset’s own market, rates, the dollar and, for crypto, sentiment.',
    body: (
      <ul className="list">
        <li>Full daily history (often 20–45 years) from Yahoo Finance for global stocks, ETFs, indices, crypto and futures. Twelve Data and CoinGecko are fallbacks. Prices are split- and dividend-adjusted.</li>
        <li>Each asset’s own benchmark (S&amp;P 500, FTSE, DAX, Nikkei, BTC for altcoins…), plus VIX, the US 10-year yield and the dollar index. Crypto also gets the Fear &amp; Greed index.</li>
        <li>Foreign-market inputs are lagged a day when that market closes after the asset’s market, so the model never sees information it could not have had.</li>
      </ul>
    ),
  },
  {
    title: 'Inputs',
    sum: 'About 25 measures of momentum, trend, stretch, volatility, volume and the wider market.',
    body: (
      <ul className="list">
        <li>Momentum: 1-day, 1-week, 1-month and 3-month returns, 12-1 month momentum.</li>
        <li>Trend: distance from 20/50/200-day averages, golden/death cross, directional ADX.</li>
        <li>Mean reversion: RSI, Bollinger z-score, stochastic, MACD histogram.</li>
        <li>Volatility, volume and on-balance-volume flow, 52-week range position.</li>
        <li>Benchmark trend, relative strength, VIX regime, rate and dollar moves, crypto sentiment.</li>
      </ul>
    ),
  },
  {
    title: 'Models',
    sum: 'A factor model and a similar-days model, blended and calibrated so stated chances match reality.',
    body: (
      <ul className="list">
        <li><strong>Factor model</strong> (ridge-regularised logistic regression): learns how much each input has mattered <em>for this asset</em> at each horizon. Its contributions add up exactly, which is what makes the “Drivers” breakdown possible.</li>
        <li><strong>Similar days</strong> (k-nearest neighbours): finds the most similar past days across all inputs and reports what happened next. It captures non-linear patterns and is easy to sanity-check.</li>
        <li>The two are blended, then calibrated so the stated chances match observed frequencies. Calibration can only shrink confidence, never inflate it.</li>
      </ul>
    ),
  },
  {
    title: 'The test every signal must pass',
    sum: 'Walk-forward testing with a purge gap; it must beat the base rate, rank better than chance and be significant.',
    body: (
      <ul className="list">
        <li>Walk-forward testing: the model is retrained about 40 times through history, each time using only data available before the forecast date, with a purge gap so training labels never overlap the test period.</li>
        <li>A horizon is a <strong>tested edge</strong> only if it beat the base-rate forecast (Brier skill above 0), ranks outcomes better than chance (AUC ≥ 0.52), is statistically significant (p below 0.10) and has at least 20 independent test periods.</li>
        <li>Scans apply a Benjamini–Hochberg false-discovery correction across all assets tested, so one lucky result among a hundred tests is demoted.</li>
        <li>An automated check confirms the system finds no edge in pure random walks.</li>
      </ul>
    ),
  },
  {
    title: 'News',
    sum: 'Headlines nudge a forecast by a few points at most, and the track record measures whether that helps.',
    body: (
      <ul className="list">
        <li>Headlines about the asset and the macro backdrop (rates, inflation, geopolitics, oil, crypto regulation) from Google News and Yahoo Finance.</li>
        <li>With an Anthropic API key, Claude judges each headline’s relevance, direction, impact and time-frame, explains the link, and writes the analyst’s case. Without one, a finance-specific word list is used.</li>
        <li>News shifts the forecast by at most a few percentage points and fades with age. It can’t be backtested (there’s no free historical news archive), so the track record scores forecasts with and without it.</li>
      </ul>
    ),
  },
  {
    title: 'Cross-asset models and rankings',
    sum: 'Models trained on many assets at once, and rankings of which assets should beat their peers.',
    body: (
      <ul className="list">
        <li><strong>Direction:</strong> one model per asset class (equities, crypto, commodities) trained on about 80 assets at once. Each input is measured against that asset’s own last two years. It joins a forecast only once it has beaten guessing in testing.</li>
        <li><strong>Ranking:</strong> per group (stocks = the S&amp;P 500 as it was on each date), it predicts which assets will beat the middle of their peers <em>after removing market exposure</em>, so it can’t win just by picking volatile stocks. It is judged by rank correlation and by whether a top-fifth basket beat the whole group after costs.</li>
        <li>Extra inputs: the VIX term structure, SKEW and VVIX, crypto futures funding rates, and the earnings beat streak. Both retrain daily with the same purged walk-forward test.</li>
      </ul>
    ),
  },
  {
    title: 'Earnings',
    sum: 'Every US report since 2010; ranges widen when a report falls inside a forecast.',
    body: (
      <ul className="list">
        <li>Every US earnings report since 2010 (date, timing, EPS, consensus, surprise) from Nasdaq’s public calendar, stored locally.</li>
        <li>Model inputs: drift after the last surprise and price reaction, and the effect before an announcement.</li>
        <li>When a report falls inside a horizon, the forecast range widens by the stock’s typical earnings move. Tested on 35,000 past reports, the widened 80% range held about 82% of the time.</li>
      </ul>
    ),
  },
  {
    title: 'Using what it tells you',
    sum: 'Act only on tested edges, size small, set a stop, and watch the live record.',
    body: (
      <ul className="list">
        <li>Prefer tested edges where the models agree and similar days point the same way.</li>
        <li>Plan the exit before you buy: a stop where you’d admit you were wrong, and a size where hitting it costs about 1% of your account. The <Link to="/journal">Journal</Link> does the sums.</li>
        <li>Watch the <Link to="/track-record">Track record</Link>. If a signal’s live record falls behind its backtest over 100+ forecasts, stop using it; the drift check does this automatically.</li>
      </ul>
    ),
  },
]

export default function Methodology() {
  const [open, setOpen] = useState<Record<number, boolean>>({})
  const all = SECTIONS.every((_, i) => open[i])
  return (
    <div className="stack" style={{ gap: 20 }}>
      <header style={{ maxWidth: 760 }}>
        <h1 style={{ fontSize: 36 }}>How it works</h1>
        <p className="answer-more" style={{ marginTop: 8 }}>What the app does with the data, how it tests itself, and how to read what it tells you.</p>
      </header>

      <section className="panel answer">
        <div className="answer-top">
          <div style={{ maxWidth: 820 }}>
            <p className="tag" style={{ color: 'var(--accent-ink)', marginBottom: 8 }}>The short answer</p>
            <h2 className="answer-say">It measures its own accuracy, and only shows what has earned trust.</h2>
            <p className="answer-more">
              No one can predict markets with certainty. Honest accuracy for next-day direction on busy markets is about 51–56%; anything claiming 70–90% is overfitted or peeking at the future.
              The edge comes from acting only on tested signals, sizing small and cutting losers, not from being right every time.
            </p>
          </div>
        </div>
        <ol className="flow" aria-label="From data to a live score">
          {FLOW.map(([t, d], i) => (
            <li key={t}><span className="n">{i + 1}</span><strong>{t}</strong><span className="small muted">{d}</span></li>
          ))}
        </ol>
      </section>

      <div className="grid-2-even">
        <section className="panel pad-card stack" style={{ gap: 12 }}>
          <h2>What the marks mean</h2>
          {MARKS.map(([m, cls, t]) => <div key={m} className="reason"><span className={`n ${cls}`} style={{ color: `var(--${cls === 'faint' ? 'faint' : cls})` }}>{m}</span><span>{t}</span></div>)}
          <p className="small muted">Green and red always come with ▲ and ▼. Personal settings change how much is explained, never a forecast, a test result or a warning.</p>
        </section>
        <section className="panel pad-card stack" style={{ gap: 10 }}>
          <h2>Words you’ll see</h2>
          <dl className="glossary">
            {WORDS.map(([w, d]) => <div key={w}><dt>{w}</dt><dd>{d}</dd></div>)}
          </dl>
        </section>
      </div>

      <section className="panel" style={{ overflow: 'hidden' }}>
        <div className="panel-head">
          <div><h2>The detail</h2><p className="sub">Each step in full, for when you want to check the working.</p></div>
          <button className="linkish" onClick={() => setOpen(Object.fromEntries(SECTIONS.map((_, i) => [i, !all])))}>{all ? 'Close all' : 'Open all'}</button>
        </div>
        {SECTIONS.map((s, i) => (
          <div key={s.title} className="rsec">
            <button className="rsec-h how-h" onClick={() => setOpen({ ...open, [i]: !open[i] })} aria-expanded={!!open[i]}>
              <span className="rsec-t"><span className="mono faint" style={{ width: 22 }}>{i + 1}</span><span><span className="rsec-title">{s.title}</span><span className="rsec-sum">{s.sum}</span></span></span>
              <span className="small rsec-act">{open[i] ? 'Hide' : 'Show'}</span>
            </button>
            {open[i] && <div className="rsec-body how-body">{s.body}</div>}
          </div>
        ))}
      </section>
    </div>
  )
}
