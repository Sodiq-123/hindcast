# Decisions log: why Hindcast is built the way it is

This is the record to come back to when deciding what to change. Each decision gives the choice, the reason, and what
would make it worth revisiting. Section 7 is the roadmap: things you didn't ask for that would most improve real trading
results.

---

## 0. The most important decision: accuracy is measured, not promised

You asked for accuracy that is "not negotiable". The most useful thing this system can do with that requirement is be
honest about it, because the alternative loses money.

**What realistic accuracy looks like.** Next-day direction on liquid assets is close to a coin flip, because millions of
participants and algorithms arbitrage away anything easy. Professional quant funds make fortunes on directional hit rates
of **51–55%**, applied across many trades with tight risk control. Any tool or backtest showing 70–90% directional
accuracy is almost always (a) leaking future data, (b) overfit to the past, or (c) quoting a base rate ("stocks went up
over the year 80% of the time" is not skill).

**What this system measured on real data while I built it (4 Oct 2026):**

| Asset | Horizon | Out-of-sample hit rate | Naive baseline | Verdict |
|---|---|---|---|---|
| AAPL (8,000+ forecasts, 1993–2026) | next day | 51.3–52.1% | 50.9–51.1% | weak to validated, depending on the input set |
| AAPL | week / month / year | below baseline | — | no edge |
| BTC-USD (2,400 forecasts, 2020–2026) | all | ≈ baseline | — | no edge |
| SPY | week | 58.0% | — | weak edge |

The AAPL next-day result moved from "validated" (p = 0.03) to "weak" (p = 0.28) when one input (VIX) was fixed. That is
what a tiny edge looks like: real enough to be worth tracking, too fragile to bet the farm on.

**Product consequences, all deliberate:**

1. **A validation gate.** Every horizon of every asset is walk-forward tested before any BUY/SELL is shown. If the
   method hasn't beaten the naive forecast on *that* asset at *that* horizon, the signal is **NO EDGE**. The probability
   is still shown for transparency, but you're told not to trade it.
2. **The base rate is always shown** next to the probability, so "78% chance up over a year" can't impress you when the
   base rate is 82%.
3. **The live track record is the source of truth.** Every forecast is stored unchanged and scored when its horizon
   passes. A backtest can be fooled; your live record can't.
4. **Risk sizing is built in** (stops from the forecast distribution, quarter-Kelly sizing). With a 53% hit rate, how
   much you bet matters more than what you pick.
5. **An automated test proves the system doesn't hallucinate edges**: on four pure random walks it reports negative
   skill every time (`TestNoFalseEdgeOnRandomWalk`), and it does find a pattern planted in synthetic data
   (`TestFindsRealSignal`).

> **Recommendation:** paper-trade (track only, no money) for at least 4–8 weeks and a few hundred resolved forecasts.
> Then trade only horizons whose *live* hit rate matches their backtest, at small size.

---

## 1. Architecture

```
            ┌───────────── Go backend (single binary) ─────────────┐
 Yahoo ───► │ market (providers, cache) ─► features (~25 inputs)   │
 TwelveData │        benchmark, VIX, 10y, DXY, crypto F&G          │
 CoinGecko  │                         │                            │
            │                 model (per horizon, in parallel)     │
            │       ridge logistic  +  k-NN analogs  → calibrate   │
            │       walk-forward validation → metrics, backtest    │
 Google/    │                         │                            │
 Yahoo RSS ►│ news (collect, lexicon | Claude) ─► bounded overlay  │
            │                         │                            │
            │ engine: signals, gate, conviction, risk, "why"       │
            │ store (Postgres) · tracker · scheduler · scanner · API│
            └──────────────────────────┬───────────────────────────┘
                                       │ JSON
                            React UI (Vite, TypeScript)
```

| Decision | Why | Revisit if |
|---|---|---|
| **Go stdlib HTTP** (`net/http` 1.22 routing), no framework | Fewer dependencies, nothing to upgrade, fast | Auth or websockets are needed |
| **No ML library**: logistic regression, k-NN, metrics hand-written (~600 lines) | Every number in the UI can be traced to auditable code. Go's ML ecosystem is thin, and these models don't need one | You move to gradient-boosted trees (see §7) |
| **PostgreSQL** (pgx driver), everything in it: reports and predictions, watchlist, scans, earnings, headline judgments, Claude usage, the price-history cache (`price_series`) and the trained models (`pooled_models`) | Your choice (5 Oct 2026): one queryable source of truth, proper types (DATE, TIMESTAMPTZ, JSONB, foreign keys), easy backups (`pg_dump market_oracle`). Originally SQLite plus JSON files; `-import-sqlite ./data` migrated them (219,794 earnings rows, 111 price series, 21 models in 12 s) | — |
| **Single binary also serves the UI** | `make run` is the whole deployment | — |
| **Cached price history** (3h default; `price_series` table, one JSONB bar array per symbol, replaced as a unit because providers re-adjust the whole history after dividends) | Yahoo rate-limits hard (I hit 429s during development); restarts shouldn't refetch 45 years of data | — |
| **Everything per-asset** (model fitted on that asset's own history) | Explainable ("for AAPL, rising yields have preceded weaker months"); no cross-asset leakage to reason about | **Top roadmap item (§7.1)**: pooled models are likely more accurate |

---

## 2. Data decisions

**Yahoo Finance chart API as primary.** It is free, needs no key, and returns full daily history with split- and
dividend-adjusted closes for ~70 exchanges, plus crypto, futures, indices and FX in one format. Downsides: unofficial,
rate-limited, and occasionally changes. Mitigations:
- browser user-agent, cookie jar, per-host request spacing (700 ms), exponential backoff on 429/5xx, two hosts;
- **`period1=0&period2=now`, not `range=max`**: `range=max` *silently* returns quarterly bars (found in testing: AAPL
  came back with 169 bars instead of 11,544). A `looksDaily` guard now rejects coarse data from any provider;
- fallback chain: Twelve Data (if keyed, global coverage) → CoinGecko (crypto, 365 days only), then stale cache with
  a visible warning. A longer cached history is spliced onto a short fallback history rather than thrown away.

**Adjusted prices everywhere.** Returns include dividends and survive splits. When scoring past predictions, both entry
and exit prices come from the *current* series, so later dividend re-adjustments can't corrupt results.

**Context series for every asset:** its local benchmark (mapped from the exchange suffix: `.L`→FTSE, `.T`→Nikkei,
`.NS`→Nifty …; altcoins → BTC; BTC → Nasdaq), VIX, US 10-year yield and the dollar index. Crypto also gets the
alternative.me Fear & Greed index (history since 2018).

**Look-ahead protection for global markets.** A Tokyo stock closes before New York opens, so using the same-date S&P
close would leak the future. Rule: if the asset's market closes earlier in UTC than the external series' market, the
external series is lagged one day. Crypto daily bars close at 24:00 UTC, after every major market, so no lag is needed.
Tested in `TestAlignLag`.

**Partial bars:** if you run during market hours, the latest bar is still in progress. It is used for *live* features
(freshest information) but can never be a training label. Twice-daily runs at 07:30 and 19:30 are set outside most
markets' hours for this reason.

**NaN discipline.** Series start at different dates (VIX 1990, F&G 2018). A bug where one NaN poisoned a running
moving-average sum silently dropped VIX and volume features; it is now fixed and tested. Optional inputs with less than
60% coverage are dropped and listed in the UI rather than shrinking the training set.

**Forex excluded** as you asked. Nothing structural prevents it (Yahoo `EURUSD=X` works), but it would need its own
macro inputs (rate differentials).

---

## 3. Model decisions

### Target
"Will the price be higher after *h* bars?", a probability. *h* = 1/5/21/252 bars for markets with weekends off and
1/7/30/365 for crypto (24/7). A probability, rather than a price target, can be scored honestly (Brier score,
calibration) and maps directly onto position sizing. Price ranges come from a separate distribution model (below).

### Inputs (~25, standardised per asset)
Momentum (1d/1w/1m/3m returns, 12-1 month momentum), trend (distance from 20/50/200-day averages, golden/death cross,
signed ADX), mean reversion (RSI, Bollinger z, stochastic, MACD histogram scaled by ATR), volatility (realised vol, vol
regime, ATR%), volume (volume z-score, OBV flow), 52-week range, benchmark trend/return and relative strength, VIX
level/change, 10-year yield change, dollar change, and crypto Fear & Greed level/change.

These are deliberately the well-documented families from academic and practitioner literature (time-series momentum,
short-term reversal, 52-week-high effect, volatility regimes, risk-on/off). I avoided exotic inputs because each extra
input is another way to overfit.

### Two models, blended 50/50 (plus a gated third: the cross-asset model, §8.1)
1. **Ridge-regularised logistic regression.** Linear in the log-odds, so each input's contribution is exact and
   additive. That is what powers the "Drivers" tab ("10-year yield up 0.44 pts → −7.9 pp"). The ridge penalty λ is
   chosen on a held-out slice of the *initial* training window only (grid 1…10,000), then scaled with sample size.
   Strong regularisation is right for noisy financial data. When it picks a large λ, all drivers show ≈0, which
   honestly says "none of these inputs matter much for this asset".
2. **k-nearest-neighbour "historical analogs".** It finds the √n (25–150) most similar past days and asks what happened
   next. It catches non-linear interactions (e.g. oversold *and* in a bull market), and its evidence is checkable by
   hand: the UI lists the actual dates. It is shrunk toward the base rate with 20 pseudo-observations.

**A fixed 50/50 blend, not learned weights:** learning blend weights on the same out-of-sample data used to report
accuracy would inflate the reported accuracy. Revisit with a proper nested validation split.

### Walk-forward validation (`internal/model/walkforward.go`)
- Expanding window (capped at the most recent 5,000 rows), minimum 750 training rows, ~40 refits across the test span.
- **Purging:** a training row is only used if its label window (t → t+h) ends on or before the forecast date. Without
  this, 1-month and 1-year horizons leak heavily through overlapping labels.
- Standardisation statistics come from training rows only.
- Metrics: hit rate vs the majority-class baseline, **Brier skill score** vs the base rate (the primary skill measure,
  because it rewards calibrated confidence rather than just direction), AUC, log loss, a calibration table, and
  accuracy by confidence level.
- **Effective sample size = forecasts ÷ horizon.** 8,000 overlapping 1-year forecasts are only ~30 independent years.
  The p-value uses the effective size, which is why yearly forecasts almost never validate. That's correct, not a bug.
- **Strategy simulation:** non-overlapping holds, long when P > 53% else cash, 0.1% cost per trade, compared with buy
  and hold (total return, CAGR, Sharpe, max drawdown). Accuracy without profit is useless. AAPL showed exactly that:
  a positive-skill next-day model that badly lagged buy & hold, because it sat in cash during a 1,500× run.

### Calibration
A two-parameter Platt map fitted on out-of-sample predictions, with the **slope clamped to [0, 1] so it can only shrink
overconfidence, never amplify it**. Reported backtest metrics are on the *uncalibrated* blend, so calibration can't
flatter them.

### The validation gate (`classify` in `engine/explain.go`)
| Status | Rule |
|---|---|
| **validated** | Brier skill > 0 **and** AUC ≥ 0.52 **and** p < 0.10 **and** ≥ 20 independent test periods |
| **weak** | Brier skill > 0 but one of the above fails → **LEAN UP / LEAN DOWN** only, never BUY/SELL (since round 3: replays showed weak "BUY"s were mostly the base rate, e.g. BTC 1-year right 21% of 14 times), conviction halved |
| **no_edge** | Brier skill ≤ 0 → signal is always NO EDGE |

Signals (validated): |P − 50%| ≥ 10 pp → STRONG; ≥ 4 pp → BUY/SELL; else HOLD. Weak: ≥ 6 pp → LEAN UP/DOWN (not a trade signal).
**Consistency rule:** a BUY with a negative expected return (or a SELL with a positive one) is downgraded to HOLD,
because a better-than-even chance of a small gain with a fat left tail isn't a buy. I added this after the scanner
ranked SPY as a BUY with −0.2% expected return.

*Revisit:* p < 0.10 is lenient. See the multiple-testing warning in §6.

### Conviction score (0–100)
35% edge size (|P−0.5| ÷ 15 pp) + 35% past accuracy at today's confidence level + 20% statistical significance + 10%
model agreement. Halved for weak evidence; zero for no edge. The components are shown in the UI so it's never a black
box.

### Price ranges and risk plan
The forward-return distribution is built from the analog days' outcomes, each **rescaled by today's volatility ÷ the
volatility then** (a calm-period analog shouldn't understate today's risk). It gives the 5th–95th percentile price
targets. Stop = 10th percentile, target = 75th percentile, and size = **quarter-Kelly capped at 20%**, only for BUY
signals. Full Kelly is shown for reference but is far too aggressive given estimation error.

### Historical evidence table
Raw, model-free conditional statistics: for each notable condition true today (above the 200-day, RSI > 70, near the
52-week high, VIX > 25 …), how often this asset rose afterwards compared with an ordinary day. It's there to let you
sanity-check the model with plain counts.

---

## 4. News decisions

- **Sources:** Google News RSS searched by company name and ticker, Yahoo Finance headline RSS, plus market-wide queries
  (rates & inflation, geopolitics/tariffs/sanctions, oil/recession/jobs, crypto market and regulation). Google News
  aggregates thousands of outlets worldwide, which covers "news from anywhere" without paid APIs. Up to 60 de-duplicated
  headlines from the last 7 days.
- **Claude analyst (when `ANTHROPIC_API_KEY` is set).** Model `claude-opus-5-5` (the current flagship) at effort `high`,
  because judging financial news well is where model quality pays off. It receives the quantitative summary plus every
  headline and returns, via **structured JSON output** (schema-enforced, no fragile parsing), a judgment per article:
  relevance, direction −1…+1, impact, time-frame and a one-to-two sentence causal explanation. It also returns a
  narrative, bull case, bear case, catalysts and risks. The prompt tells it to discount stale or priced-in news, cite
  headlines by id, never invent facts, and say plainly when a horizon has no edge.
  - **Server-side refusal fallback is enabled** (`fallbacks: "default"`, beta `server-side-fallback-2026-07-01`): if a
    safety classifier declines a request (unlikely for financial news), Anthropic re-serves it on its recommended
    fallback model instead of failing. If it still fails, the report falls back to lexicon scoring and says so.
  - **Cost:** roughly $0.05–0.15 per full analysis. At 2 runs/day × 20 watchlist symbols that's about $2–6/day. Lower
    with `LLM_EFFORT=medium` or `LLM_MODEL=claude-sonnet-5-5`. Scans never call Claude.
  - Request and response format are covered by a mock-server test. **Not yet run against the live API:** no key was
    available while building. Run one full analysis after adding your key and check the News panel.
- **Lexicon fallback:** a finance-specific word and phrase list ("cuts guidance", "beats estimates", "chapter 11" …) with
  negation handling. It's crude. In testing it rated an iPhone bug-recall headline positive because of the word
  "upgraded". Use the Claude analyst for anything real.
- **News is a bounded overlay, not a model input.** There is no free historical news archive here, so news can't go
  through walk-forward validation. It therefore shifts the log-odds by at most 0.30 (next day), 0.22 (week), 0.12
  (month) or 0.05 (year), roughly ±7 pp at most for next day. The weight decays with half-lives of 1, 3, 10 and 30
  days. Every prediction stores both the pre-news and post-news probability, and the Track record page compares them,
  so **you'll find out from live data whether news helps** and can tune the caps in `news/lexicon.go → horizonNews`.

---

## 5. Product and UX decisions

- **Watchlist** shows signal and probability per horizon. Click through to the full reasoning.
- **Asset page, in order:** horizon cards (probability, status, conviction, range, base rate) → the "why" panel with five
  tabs:
  - **Why:** a numbered reasoning chain, plus how the probability is built step by step (base → linear → analogs →
    blend → calibration → news → final).
  - **Drivers:** every input's contribution in percentage points, with a plain-English sentence each.
  - **Analogs:** the most similar past dates and what happened next.
  - **Backtest:** hit rate vs baseline, calibration plot, accuracy by confidence, equity curve vs buy & hold.
  - **Risk plan.**

  Then the price chart with forecast bands, the AI narrative and bull/bear cases, the news list (each headline's
  direction, relevance and reasoning), the textbook indicator readings, the historical evidence table, and the inputs
  used. URLs carry the horizon and tab (`?h=1m&tab=Backtest`) so views can be bookmarked.
- **"Textbook signal" vs model view are shown separately.** RSI 72 is "bearish" in textbooks, but for a momentum stock
  the model may have learned it's bullish. Showing both teaches you which conventional wisdom holds for each asset.
- **Opportunities (scanner):** ranks only BUYs that passed the gate, by conviction + 200 × Brier skill + 100 ×
  expected return. When nothing qualifies it says so, because sitting out is a valid position.
- **Signals are named HOLD vs NO EDGE on purpose:** HOLD = "proven method, no strong view today"; NO EDGE = "method not
  proven here, ignore the number".
- **Scheduled at 07:30 and 19:30 local** (`RUN_TIMES`): before Europe opens / after the US close, using completed bars.
  Change it to suit your markets.

---

## 6. Known limitations, so you aren't surprised

1. **Multiple testing.** A scan covers ~50–100 assets × 4 horizons. At p < 0.10, ~10% of truly edgeless combinations
   would look "validated" by luck. *Scans now apply a false-discovery correction (§8.4).* Watchlist signals are still
   judged one asset at a time, so prefer ones whose live track record confirms them.
2. **Survivorship bias** in the default universes: today's large caps and top-16 coins are, by definition, the winners.
   Backtests on them look rosier than reality. This matters most for the crypto ranking results in §8.2.
3. **Earnings coverage is US-listed companies only** (Nasdaq calendar, from 2010). Non-US stocks get no earnings
   warnings or features. *Added in round 2 (§8.3).*
4. **Daily bars only.** No intraday, pre-market or overnight futures information.
5. **Yahoo is unofficial** and can break or rate-limit (it blocked this machine for long stretches during development).
   A rate-limited provider is now skipped for 10 minutes, and US stocks and ETFs fall back to Nasdaq's free history (10
   years, split- but not dividend-adjusted). Exchanges Yahoo doesn't cover (some African, Middle Eastern and frontier
   markets) need another provider.
6. **CoinGecko fallback** gives only a year of history: yearly crypto forecasts are unavailable when it's the source.
7. **The news overlay is unvalidated** until the live track record accumulates.
8. **No authentication.** Run it on your own machine or behind a VPN, not on the open internet.

---

## 7. Roadmap: what would most improve real trading results

Ranked by expected impact on *money made*, not on features.

> Status on 5 Oct 2026: ✅ built · ◐ partly built · ☐ not built. Items 1–4 came in round 2, 12, 15 and 16 in
> round 3 (§8.7), and 8, 9, 10 and 14 in round 4 (§8.8). See §8.8 for why 5 and 13 are not quick wins.

1. ✅ **Pooled cross-sectional models (biggest accuracy upgrade).** Train one model per asset class across hundreds of
   assets instead of one per asset. Sample size rises 100×, noise averages out, and well-documented cross-sectional
   effects (momentum, short-term reversal, low volatility) become learnable. Keep the per-asset model as a second
   opinion. Effort: medium-large (`model.Run` → pooled dataset builder plus a nightly retrain job).
2. ✅ **Rank assets instead of predicting direction.** "Which 5 of these 100 will beat the others next month?" is far more
   predictable than "will NVDA go up?", and it's exactly your "next thing to buy" question. Use excess return over the
   benchmark as the label, and long the top decile.
3. ✅ **Earnings and events calendar.** Flag or skip forecasts that span earnings; add post-earnings-announcement drift
   (one of the most robust anomalies) and analyst estimate revisions. Sources: Financial Modeling Prep, Finnhub or
   Nasdaq earnings calendars (free tiers).
4. ✅ **Stricter statistics in scans.** Apply a Benjamini–Hochberg false-discovery-rate correction across all
   asset × horizon tests in a scan, and require p < 0.05 for STRONG signals. Small code change, big honesty gain.
5. ☐ **Backtest the news.** GDELT (free, global, 15-minute updates back to 2015) provides historical headlines with tone
   scores. With it, news becomes a validated model input instead of a capped overlay. *Not easy (§8.8).*
6. ◐ **Options-market data.** Implied volatility, put/call skew and the options-implied earnings move are among the
   strongest public short-horizon signals for stocks. *Market-wide VIX term structure, SKEW and VVIX are model inputs;
   each US stock's options-implied move is shown next to the forecast (round 3). Per-stock implied volatility can't
   be a validated input without paid historical option data.*
7. ◐ **Crypto-native data.** Perpetual funding rates, open interest, liquidations, exchange net flows and stablecoin supply
   (Coinglass, Glassnode, CryptoQuant). Price-only models found no edge on BTC; these inputs are where crypto edges
   usually live. *Funding rates are inputs (round 3). Open interest, liquidations and flows need paid APIs.*
8. ✅ **Forecast volatility, not just direction.** Volatility is highly predictable (R² often 30–60%) where direction isn't.
   Use it for position sizing (target constant risk per trade) and stop placement. This alone usually improves
   risk-adjusted returns more than better direction calls.
9. ✅ **Portfolio-level risk.** Correlation-aware sizing (five crypto BUYs are really one bet), a cap on total open risk,
   and a drawdown circuit-breaker that halves size after a losing streak.
10. ✅ **Alerts.** Push a Telegram or email message when a validated signal flips, a stop is hit, or a horizon's live hit
    rate drops below its backtest.
11. ◐ **Paper trading through a broker API** (e.g. Alpaca paper trading for US stocks) to measure real fills, slippage
    and fees against the simulation. *The journal (§8.8) records paper trades at market prices; broker fills are not
    simulated.*
12. ✅ **Gradient-boosted trees** (via a Python/LightGBM sidecar or a Go GBM library) as a third ensemble member, with SHAP
    values to keep explanations. Only worth it after #1, because trees overfit small per-asset datasets.
13. ☐ **Meta-labelling.** A second model that predicts *when the first model is right* (by regime, volatility, trend
    strength) and only lets those trades through. It typically raises the hit rate on the trades you take, at the cost
    of fewer trades. *Not easy (§8.8).*
14. ✅ **Drift monitoring.** Automatically demote a horizon to NO EDGE when its rolling 100-forecast live Brier score is
    worse than the base rate.

New items from round 2's results:

15. ✅ **Beta-neutral ranking labels.** The stock ranking mostly favours high-volatility names, which in a rising market
    is a leveraged market bet rather than stock-picking skill (§8.2). Label "beat the median" on returns *minus beta ×
    market return* so the model must find genuine relative strength. Small change in `pooled/rank.go`.
16. ✅ **Bigger, point-in-time stock universe.** 56 large caps is thin for cross-sectional work. Ranking the S&P 500 (and
    using historical index membership to remove survivorship bias) is the standard setup and should strengthen IC
    considerably. Cost: ~500 price downloads per training (cache keeps it to once a day).
17. ☐ **Separate crypto training windows.** Crypto's regime changed sharply after 2020. Weight recent years more, or
    validate on 2022+ only, before trusting the crypto rankings with real size. *The best next small item.*
18. ☐ **Longer meme-coin price history.** A paid CoinGecko plan or exchange klines (Binance/Bybit, if reachable)
    would add SHIB, PEPE, BONK and FLOKI's full histories to the meme ranking and allow a monthly horizon. Worth it
    only if the daily ranking's break-even cost (0.45%) rises above realistic trading costs on more data.

---

## 8. Round 2 (4 Oct 2026): cross-asset models, ranking, earnings, scan statistics

### 8.1 Cross-asset ("pooled") direction models — `internal/pooled/direction.go`

**What:** one model per asset class (equity ≈ 80 stocks and ETFs, crypto 16, commodities 10) per horizon, trained
on all assets at once. It joins each asset's forecast as a third ensemble member.

**Key decisions**
- **Inputs are each asset's own rolling two-year z-scores** ("how unusual is this reading *for this asset*"), so a
  3% move means something different for a utility than for a crypto coin. Earnings scores and already-normalised
  inputs pass through. Missing inputs (an ETF has no earnings) enter as 0, the neutral value.
- **No global standardiser:** inputs are already unit-scale. Skipping it keeps each fold to row pointers rather than
  copies; 20 parallel folds over 300k rows would otherwise need gigabytes of RAM.
- **Same walk-forward discipline:** ~20 date-blocked folds, purged so no training label ends after the fold starts.
  Each fold's coefficients are stored, so applying the model to an asset uses, for every past date, only the fold
  that had not seen that date. Its contribution to the per-asset backtest stays genuinely out-of-sample.
- **Significance counts dates, not asset-days.** 80 assets on the same day are not 80 independent observations, so
  the effective sample is distinct dates ÷ horizon.
- **It only joins the blend if it has proven skill** (Brier skill > 0 and p < 0.10 on its own out-of-sample record).
  I added this after the first real training (below): equal-weighting a no-skill model only drags every forecast
  toward the base rate. When excluded it's still shown on the Drivers tab, marked "not used".
- **No 1-year pooled models.** Sixteen years hold ~12 independent yearly periods, too few to validate, and an
  unvalidated model must not join a forecast.

**Measured (first real training, 1 min 40 s for everything):**

| Pool | Horizon | Hit rate | Naive | Brier skill | p | Joins blend? |
|---|---|---|---|---|---|---|
| equity (80) | 1d / 1w / 1m | 52.4% / 55.3% / 58.8% | identical | +0.05% / +0.05% / −0.18% | 0.49 / 0.50 / 0.50 | no |
| crypto (16) | 1d / 1w / 1m | ≈ naive | — | ≈ 0 | > 0.25 | no |
| commodities (10) | 1d | 51.5% | 49.8% | +0.08% | **0.04** | **yes** |

Retraining after the full earnings history (2010–2026, 219,743 reports) finished changed nothing here. The ridge
penalty shrank the equity model until it predicts the base rate, which is why its hit rate equals the naive rate
exactly. The same retrain nudged the stock ranking up slightly (weekly IC t-stat 1.73 → 1.88), not enough to validate.

Honest reading: pooling technical and macro inputs did **not** produce a broad direction edge. This matches the
academic evidence that *time-series* direction from public price data is close to unpredictable. The machinery is
worth keeping: it is the right place to add inputs that do carry signal (options, crypto on-chain, fundamentals;
§7.6–7.7), and its gate means it switches itself on only once it earns it.

### 8.2 Cross-asset ranking models — `internal/pooled/rank.go`

**What:** per universe (stocks 56, ETFs 24, crypto 16, commodities 10) and horizon, the probability that each asset
beats the median of its peers. The Opportunities page leads with it.

**Key decisions**
- **Label = beat the cross-sectional median**, so the base rate is 50% by construction and market direction cancels
  out.
- **Inputs are ranked across the universe each day** (→ −1…+1). Market-wide inputs (VIX, rates) carry no
  cross-sectional information and are excluded. Risk-adjusted momentum (12-1 month return ÷ volatility) is added
  because raw momentum tends to just pick the most volatile names.
- **Judged like a quant fund would judge it:** mean rank correlation between forecast and outcome (IC), its t-stat on
  non-overlapping periods, return of the top fifth versus the equal-weight universe, a decile chart, and a top-fifth
  basket after 0.1% costs on the names replaced each rebalance.
- **Gate:** validated needs IC t ≥ 2 *and* the top-fifth basket beating the universe after costs *and* ≥ 20 periods.
  Weak = leaning the right way. No edge = the top picks didn't beat the rest.

**Measured (out-of-sample 2014–2026; crypto 2022–2026; final retrain with full earnings history):**

| Universe | Horizon | IC (t-stat) | Top fifth vs universe | Basket CAGR vs universe | Status |
|---|---|---|---|---|---|
| crypto | 1d | 0.078 (8.3) | +0.08%/day | +11.2% vs −1.1% | **validated** |
| crypto | 1w | 0.112 (4.5) | +0.48%/week | +28.6% vs −1.2% | **validated** |
| crypto | 1m | 0.074 (1.7) | −0.95%/month | −5.1% vs −2.0% | no edge |
| ETFs | 1w | 0.040 (3.0) | +0.15%/week | +14.7% vs +10.7% | **validated** |
| ETFs | 1d / 1m | 0.037 (6.3) / 0.035 (1.3) | small + | below / above universe | weak |
| stocks | 1d / 1w / 1m | 0.021 (4.7) / 0.021 (1.9) / 0.028 (1.2) | small + | 1d below universe after costs; 1w +35.8% vs +21.9% (not significant) | weak |
| commodities | 1d / 1w / 1m | 0.02–0.07 | small + | mixed | weak |

**How to read this before you risk money:**
- The **crypto** results are the strongest numbers in the whole system, which is exactly why they deserve the most
  suspicion. The universe is today's top 16 coins (survivorship), the test covers one regime (2022–2026), and IC t-stats
  that high from price data alone are unusual. Paper-trade them first (§7.17).
- The **stock** ranking's top reasons are high volatility and ATR: in a bull market that is mostly a leveraged market
  bet, not stock-picking. It's correctly rated weak. Beta-neutral labels (§7.15) and a bigger universe (§7.16) are
  the fix.
- **Next-day** rankings show high t-stats but often lose to the universe after costs, because daily rebalancing eats
  the edge. The cost-aware gate catches this; trust the weekly results over the daily ones.

### 8.3 Earnings — `internal/earnings`, `features.EarningsColumns`, `engine/earnings.go`

- **Source:** Nasdaq's public earnings calendar, one request per day, stored in Postgres: every US-listed report since
  2010 (~350k rows) with timing (before open / after close), EPS, consensus and surprise. A resumable background
  backfill (~80 minutes once, three polite workers) walks newest-first, and the next 45 days are refreshed daily
  because dates move. `go run ./cmd/server -backfill` does it ahead of time. It now lives in the `earnings` and `earnings_days` tables.
- **Reaction window depends on timing:** before-open moves the report day, after-close the next day, and unknown
  timing uses the two-day window covering both (slightly overstating moves when timing is missing).
- **Model inputs** (cross-asset models; per-asset only for short-history stocks):
  - `pead`: last EPS surprise, decaying over ~1 month, cut off after 3 months.
  - `earn_react`: last earnings-window move in volatility units, same decay. The price reaction is the
    best-documented drift signal.
  - `earn_soon`: rises over the 21 bars before a report (the "announcement premium"). Historical rows use the
    *actual* report date, which was usually but not always public in advance. This is a small, documented
    look-ahead.
- **Before coverage starts the inputs are NaN, never 0**, because "no report known" is not "no report happened".
  The one exception is the pooled models, where unknown counts as neutral. Otherwise a still-downloading history
  would wipe out every pre-coverage stock row.
- **In reports:** the next date with timing, consensus EPS, days away, and past reactions compared with an ordinary
  day. Also the beat rate, 20-day drift after beats and after misses, and the history table. When a report falls inside
  a horizon, the forecast range widens by √(1 + earnings-move² ÷ (daily vol² × bars)), STRONG signals on ≤ 1-week
  horizons are capped to plain BUY/SELL, and suggested size is halved.

### 8.4 Scan statistics — `engine/scanner.go: applyFDR`

A Benjamini–Hochberg false-discovery correction at 10% runs across every asset in a scan. Validated items whose
p-value doesn't survive are demoted to weak (STRONG → plain, conviction halved) with an explanation. One subtlety the
test documents: if *many* assets show similar small p-values, BH lets them all through. That is correct behaviour,
since collectively they're unlikely to be luck.

### 8.5 Data resilience

- **Provider cooldown:** after a 429, a provider is skipped for 10 minutes. Previously each symbol burned ~30 s of
  retries against a refusing Yahoo before reaching a fallback.
- **Wedged connections:** the first earnings download froze after ~400 days. All three workers shared one HTTP/2
  connection, Nasdaq stalled it, and every retry reused it. The client used for Nasdaq now speaks HTTP/1.1 (one
  connection per worker), and every client drops idle connections before retrying.
- **Nasdaq price fallback** for US stocks and ETFs (^GSPC, ^IXIC, ^DJI and ^RUT are proxied by their ETFs), flagged
  in the report.

### 8.6 Claude cost (5 Oct 2026): cheaper without touching model, effort or prompt quality

Profile, read from the code because the app had no usage logging and no key was available: one Claude call per
analysis. Input is mostly unique headlines, and output is per-headline judgments, the narrative and thinking. Output
costs 5× input on Opus 5.5 ($4 / $20 per million tokens, pricing page fetched 5 Oct 2026), so output dominates. Most
calls come from the twice-daily scheduled runs, which nobody waits on.

Changes, all "free wins" (same model, effort and instructions):
1. **Usage and cost logging** (`llm_usage` table, shown under Opportunities → Models & data). The bill is now
   measured, not guessed. Rates live in `news/llm.go → rates`; update them if prices change.
2. **Headlines are judged once.** Claude's verdict on each headline is stored per symbol for 30 days
   (`news_judgments`). Later runs send only *new* headlines for judgment; earlier verdicts go in as compact context, so
   the narrative still sees all the news. Headlines are collected over 7 days, so most of them repeat between the
   07:30 and 19:30 runs. Caveat: a reused verdict isn't re-judged as it ages, but the news overlay's time decay (§4)
   still fades it.
3. **Scheduled runs use the Batch API** (50% off every token). All watchlist requests go in one batch, polled every
   30 s. Anything not back within 3 hours, or any errored or refused request, falls back to a normal synchronous
   call, so no analysis loses its news judgment. Clicking "Run full analysis" stays synchronous.
4. **No explanation for irrelevant headlines:** below 0.3 relevance Claude returns an empty reasoning string. The UI
   says "judged not relevant" instead.

Deliberately not changed:
- **Prompt caching:** the shared prefix is small next to the unique headlines, and one-off interactive calls would
  pay the 1.25× cache-write surcharge without a read.
- **Effort (`high`) and model (Opus 5.5):** these trade capability for cost. `high` was my choice at build time, not
  a measured one. Anthropic's recommended starting point for Opus 5.5 is its default `medium`, and its published runs
  show `medium` matching `high`'s accuracy for much less, but that has to be checked on *this* workload before
  switching. Next step: a side-by-side run of ~10 symbols at `high` vs `medium` once a key is set (needs your
  approval for the spend).

### 8.7 Round 3 (5 Oct 2026): more data, boosted trees, beta-neutral S&P 500 rankings, Replay

**Data (roadmap item 1), what was possible for free and what wasn't:**

| Source | Used as | Validated? |
|---|---|---|
| CBOE daily history: VIX3M (2009+), VIX9D (2011+), SKEW (1990+), VVIX (2006+) | Inputs: VIX term structure (`log VIX/VIX3M`), SKEW and VVIX vs their 1-year norms | Yes, walk-forward like every input |
| Perpetual-futures funding: BitMEX for BTC/ETH (2016/2018 → mid-2023), Hyperliquid after that and for other coins (2023+) | Crypto inputs: 7-day funding (annualised) and its 3-month z-score | Yes. Values older than 3 days count as missing, because BitMEX's XBTUSD feed stopped on 16 Sep 2026 and a frozen value must not look current |
| Earnings beat streak (last four reports) | Input | Yes |
| Nasdaq option chains (live snapshot) | Each horizon's options-implied move vs the model's range, and an options panel | No. No free history exists, so it's information only |
| Analyst estimate revisions, open interest history | Not used | No free history |
| S&P 500 membership since 1996 (github.com/fja05680/sp500) | Ranking universe, point-in-time | n/a |

Bybit and OKX were unreachable from this network, hence BitMEX and Hyperliquid.

**S&P 500 point-in-time universe:** a stock is ranked only on dates it was in the index. Price histories of
companies that later left exist only while their tickers still trade, so delisted names (bankruptcies, most
acquisitions) are missing: survivorship bias is reduced, not removed. A ticker later reused by a different company is
excluded when its price history doesn't cover its membership dates.

**Beta-neutral ranking labels (item 3):** "beat the median" now means after subtracting beta × the universe return
(trailing one-year beta to the equal-weight universe). The validation gate also requires a positive beta-neutral edge
in the top fifth. Test: in a simulated bull market where stocks differ only in beta, the old label would reward
picking high-beta names; the new one reports no edge (`TestBetaIsNotAlpha`).

**Gradient-boosted trees (item 2):** `internal/gbm`, pure Go: histogram splits, depth 3, learning rate 0.05, leaves
of at least 300 rows, row and feature subsampling, and early stopping on the latest 15% of each training window
(purged). In ranking it is blended 50/50 with the linear model, with fixed weights for the same reason as §3, and each
component's rank correlation is reported separately so you can see whether the trees earn their place. Tests: it
learns an `x0 × x1` interaction a linear model can't see (66% vs ≈68% theoretical), and on noise it stops early and
stays within 3 points of 50/50.

**Replay:** any symbol, any past date. The asset's history is cut at that date; external series need no cutting
because every feature is causal; earnings results after the date are hidden; the cross-asset model uses the fold
that covers the date (its final model saw the future); news and option chains are left out (no history). Replays are
never saved to the track record. Tested in `TestReplayNoLookahead`.

**Postgres:** see §1. The import also brought over the price cache and the models.

### 8.8 Round 4 (5 Oct 2026): meme-coin safety reports, volatility forecasts, drift monitor, journal, alerts

**Search (`components.tsx: SearchBox`, `GET /api/token-search`).** One box takes a ticker, a company or coin name, or
a contract address. Names search Yahoo and DexScreener (Solana, Ethereum, Base) at the same time and show two groups.
Pasting an address skips the search and opens the token report. *Copycats were the hard part:* the same name usually
matches dozens of tokens, and reported liquidity can be faked. A brand-new "PUMP" claiming $1B of liquidity topped
the list and made the real one look like the copy.

The original is identified from **CoinGecko's listings**, which record each coin's official contract address per
chain (`token/registry.go`). The address book covers about 13,000 coins on Solana, Ethereum and Base, is refreshed
daily, and is kept in Postgres so it survives restarts and CoinGecko outages. Market-cap ranks come from CoinGecko's
search and are cached for an hour. The rules:

1. The best-ranked listing with a ticker is the original and comes first. It is also added to the results if
   DexScreener's search missed it.
2. Other tokens with that ticker are labelled "Not the CoinGecko-listed X (Name): likely a copycat", or "a different
   project with the same ticker" if CoinGecko lists them separately (e.g. Big Pump, #2885, next to Pump.fun, #39).
3. Without any listing, the fallback is the old heuristic: the most liquid token at least 30 days old, shown as
   "likely original".
4. Brand-new tokens claiming large liquidity are labelled "very likely fake".

The token report repeats the check as an *Identity* flag and a banner. Green: this exact address is the listed coin.
Red: "Not the listed X", with a link to the real one. Amber: not listed, so nothing can be confirmed. Verified on 5
Oct 2026: `pumpCmXqMfrsAkQ5r49WcJnRayYRqmXz6ae8H7H9Dfn` is CoinGecko's Pump.fun (#39). The copycat named "PUMP.FUN"
is flagged red. PEPE (#55) and BRETT (#442) rank first above their lookalikes. A listing confirms identity, not
safety.

**Coins that live elsewhere (added 8 Oct 2026).** When CoinGecko's best-ranked coin for a ticker isn't on Solana,
Ethereum or Base, every DEX token using that ticker or name is now labelled "Not the real X", and the token page shows
a red identity flag and a banner linking to research on the real coin. The case that prompted it: Quantus (QTC)
lives on its own chain and NEAR, while copycats on Base and Ethereum, one literally named `qtc.omft.near`, had only
an amber "not listed" warning, and one was even labelled "likely original". The search box also has a third group,
"Coins on any blockchain (CoinGecko)", and research can target a coin by its CoinGecko id (`/research/cg:quantus`):
it uses the coin's profile and its official contract list, and says plainly that tokens at other addresses aren't it.

**Token safety reports (`internal/token`, `/token/:address`).** Meme coins get no forecast, and the page says why:
DEX history covers months, not years, and prices are driven by attention and insiders. A model that can't be validated
isn't shown (§0). What *can* be checked are the ways traders lose money:

- *Contract* (GoPlus). On Solana: mint, freeze, close, balance-change, metadata and transfer-hook authorities, and
  transfer fees. On EVM chains: honeypot, buy/sell tax, mint, owner powers (proxy, pause, blacklist, tax change,
  cooldown, hidden owner, reclaimable ownership, self-destruct, external calls), verified code, and a creator linked
  to earlier honeypots. Owner-only powers count as harmless ("disabled") when ownership is genuinely renounced.
- *Holders.* Largest wallet and top-10 share, excluding pools and locked or burned balances, plus the holder count.
- *Liquidity.* Main pool and total depth, liquidity as a share of market cap, turnover (wash-trading hint), and the
  share of pool tokens locked or burned. The lock check applies only to v2-style pools: concentrated-liquidity
  positions are NFTs, and the report says so. LP tokens held by the token's own verified, owner-less contract count
  as locked (PEPE's case: 99.4% sits there with no way out).
- *Age and price* (GeckoTerminal candles). Drawdown from the high and typical daily swing.

Red signs weigh 25, amber 8, capped at 100. The verdict needs two red signs (or 60+) for "Very high risk". The score
counts warning signs; it is **not** a probability of a rug pull, and the page says so. Live results on 5 Oct 2026:

| Token | Verdict | Main reasons |
|---|---|---|
| PUMP (Solana) | Very high risk (66) | one wallet 27.6%, top 10 60.3%; transfer hook changeable; liquidity 1.3% of market cap |
| PUMP copycat (Solana) | Very high risk (100) | one wallet 97.1%, 26 holders, $4K liquidity, 99.5% below its high |
| PEPE (Ethereum) | Elevated risk (16) | top 10 hold 35%; liquidity 1.9% of market cap (renounced, LP effectively burned) |
| BRETT (Base) | Elevated risk (24) | creator linked to 3 honeypots; top 10 hold 33.5% |

Reports are cached for 10 minutes in `token_reports` (the "Re-check now" button bypasses this).

**Volatility forecasts (roadmap 8; `internal/vol`).** A HAR model regresses the log of future realised variance on
5-, 22- and 66-day realised variance, fitted directly for each horizon, with a smearing correction from logs back to
levels. Walk-forward test: refits each quarter over the last five years, purged by the horizon. It is compared with
what the engine used before (63-day historical volatility) on two measures: QLIKE (the standard proper loss for
variance forecasts) and how often the 10–90% range actually contained the outcome. **It replaces the old scaling of the
forecast range only where it wins**: at least 2% lower loss, at least 20 non-overlapping test periods, and range
coverage no worse than the old one's by more than 1.5 standard errors. Two test corrections made along the way: with
constant volatility HAR *should* win (its intercept shrinks a noisy 63-day estimate towards the long-run level); and
coverage differences of 2 points on 250 periods are noise, hence the standard-error tolerance. Results on real data:

| Asset | 1 day | 1 week | 1 month | 1 year |
|---|---|---|---|---|
| SPY | used (loss −4%) | used (−17%) | used (−24%) | not judged (5 periods) |
| AAPL | not used (−1.7%) | used (−8%) | used (−15%) | not judged |
| BTC-USD | not used (−1.8%) | used (−4%) | not used (HAR worse) | not judged |

The risk plan now also gives the **1% rule** size: the position at which hitting the stop costs 1% of the account
(1% ÷ distance to stop). The page caps it at a 20% maximum position, because tight 1-day stops otherwise suggest
putting almost the whole account in.

**Drift monitor (roadmap 14; `tracker/drift.go`).** For each symbol and horizon it takes the last 100 resolved live
forecasts (one per day: twice-daily runs on the same bar are one piece of evidence). If there are at least 30 of
them, covering at least 10 non-overlapping periods, and they scored worse than the base rate *by more than one
standard error*, the horizon is demoted to NO EDGE with an explanation. The live record overrides the backtest. The
first version compared against the up-rate measured afterwards. That baseline knows the future, so even a sensible
constant 65% forecast "drifted". It now compares against the base rate the model knew at the time, which is saved
with each prediction (`predictions.base_rate`; older rows use 50%). Replays are never demoted, because the live record
postdates them.

**Paper-trading journal with portfolio risk (roadmap 9, easy half of 11; `internal/journal`, `/journal`).** It records
long paper trades in stocks, ETFs, crypto or DEX tokens (by address), and values them from the same price sources. It
checks:

- risk to each stop (no stop counts the whole position as at risk);
- total risk against the limit (default 6%);
- each position against the max-position limit (20%);
- pairs with 90-day return correlation ≥ 0.6, which are really one bet (red at ≥ 0.8);
- meme tokens above 5% of the account;
- stops and targets hit;
- a circuit-breaker that halves suggested sizes after three losing trades or a 10% drawdown.

A sizing helper applies the 1% rule. *Fees (added 6 Oct 2026):* each trade pays a fee per side that includes the
spread: 0.1% for stocks and ETFs, 0.2% for crypto, 1% for DEX tokens (all editable). The buy fee comes out of the
amount; open positions show profit and loss as if sold now, both fees included; and the sell fee is charged on
closing. A trade keeps the rate it was opened with, and the 1% sizing counts both fees in the loss at the stop. By
the user's choice, stops are still not executed automatically and there's no cash limit or currency conversion. The "Record a paper trade" buttons on analysis and token pages pre-fill the
platform's stop, target and signal, so the closed-trade list becomes your own track record set against what the
platform said.

**Telegram alerts (roadmap 10; `internal/alert`).** Optional (`TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`). After each
scheduled run it sends: a watchlist signal that appears, flips or disappears (BUY/SELL only, never LEAN); new drift
demotions; journal stops and targets hit; and the circuit-breaker. Each alert has a key and is not repeated within its
window (`alerts_sent`). The bot token is never logged.

**Why roadmap 5 (news backtest) and 13 (meta-labelling) are not quick wins.**

- *GDELT:* its free query API only reaches back about three months. Full history means the raw GKG files (terabytes)
  or BigQuery (a paid Google Cloud account), plus reliably mapping headlines to tickers. Scoring years of headlines
  with Claude would cost far more than the current news analysis. A lexicon-only backtest is feasible but would
  validate a weaker scorer than the one actually used.
- *Meta-labelling:* a second model that predicts when the first is right needs many trades from a first model that
  already has an edge. Here that is only the crypto rankings. Fitting it honestly needs nested walk-forward validation
  (doubling training time), and with a few hundred independent trades it would most likely learn noise. It is worth
  revisiting once the live track record holds thousands of resolved forecasts.

### 8.9 Round 5 (5 Oct 2026): meme-coin ranking, measured token risk, a CoinGecko dating bug

**Why there's still no direction forecast for a single meme coin.** PUMP has 451 days of history. The per-asset
method needs at least 750 days to fit about 30 inputs plus a test period, and even 450 days would give only about 200
test days at 1 day, about 60 independent weeks, about 15 months and no complete year. With 200 test days only a
hit rate above about 55% could be told apart from luck, and realistic edges are 51–53%. Run through the engine,
PUMP returns "not enough history" on every horizon, which is the correct answer.

**Measured risk on the token page (`token/risk.go`).** A year of daily data measures *size* of moves well even
though it can't prove *direction*. The longest reliable daily series is used: the verified Yahoo ticker (PUMP: 451
days), else CoinGecko's year, else the pool's 180 days of DEX candles. The page shows:

- typical and annualised volatility;
- worst and best day, worst 7 and 30 days;
- biggest fall from a high;
- share of days moving 10% or more, and of weeks falling 20% or more;
- 1-in-20 bad day and week, with the position at which a 1-in-20 week costs 1% of the account;
- correlation and beta against Bitcoin and the token's chain coin, plus its average move on their 3%+ down days.

PUMP's longer history changed the picture: the 6-month window showed −45% at worst, the full history shows **−86%**
(Sep 2025 → Jun 2026), a −48% week and 1-in-20 weeks of −30% (so at most about 3.4% of an account for the 1% rule).

**Verified meme-coin list (`market/meme.go`).** Yahoo lists several coins under one ticker: `PUMP33991-USD` is an
unrelated coin at $0.00009, while Pump.fun is `PUMP36507-USD`. Twenty-four meme coins are pinned to their Yahoo ticker
and CoinGecko id, each checked by price and launch date. Asset search puts the verified ticker first, labelled
"(verified coin)", and marks other coins under the same ticker "a different coin". Six large meme coins (SHIB, PEPE,
FLOKI, BONK, MOG, MOODENG) are rounded by Yahoo, so they only contribute CoinGecko's last year. Exchange APIs with full
history (Binance, Coinbase, OKX, Kraken) don't resolve from this network, GeckoTerminal's free tier stops at 180
days and CoinGecko's at 365.

**CoinGecko dating bug (fixed).** CoinGecko stamps each daily price at 00:00 UTC, which is the *previous* day's close:
its 5 Oct 00:00 BTC price equals Yahoo's 4 Oct close, and so on for every day checked. The fallback dated it on the
stamp's own day, so every CoinGecko-priced coin was one day late against Yahoo-priced ones. Single-asset forecasts
were only mildly affected, but in a cross-asset ranking it leaked the future. `market.CoinGeckoDay` now dates the
midnight stamp to the previous day, the risk statistics use it too, and the seven cached CoinGecko series were
cleared. None of the 16 large crypto coins were priced from CoinGecko, so their rankings stand.

**Meme-coin ranking (`pooled/trainer.go: trainMeme`, `-train-meme`).** The 24 coins are ranked against each other
with the same model, the same beta-neutral label and the same pass/fail gate as the other rankings, with two
differences. Validation starts after 1 year instead of 4, because most coins are young; it runs Feb 2025 → Oct 2026.
Trading costs are **0.5% per side** instead of 0.1%, because meme coins trade on thin books and DEXs. Meme coins are
kept out of the cross-asset direction models. Every ranking now also reports its **break-even cost**: the cost per
trade that would use up the top fifth's edge.

The first run reported both horizons as validated, with an IC of 0.154 and +260% a year. That was too good to
believe, so it was stress-tested:

| Test (with the dating bug fixed, 0.5% costs) | Daily IC (t) | Weekly IC (t) | Break-even cost per side, daily / weekly |
|---|---|---|---|
| As specified: all 24 coins | **0.109 (10.3)** | 0.056 (1.9) | **0.45%** / 0.54% |
| Yahoo-priced coins only | 0.104 (9.4) | 0.069 (2.2) | 0.31% / 0.77% |
| Signal one day old | 0.046 (4.3) | 0.014 (0.5) | 0.18% / — |
| Yahoo only, one day old | 0.051 (4.4) | 0.023 (0.7) | 0.29% / 0.27% |
| Without 1-day and 1-week return inputs | 0.092 (8.5) | 0.034 (1.2) | 0.68% / 0.42% |

What it shows:

1. A third of the original daily "edge" was the dating bug.
2. There *is* a real, highly significant next-day ordering (t ≈ 10), mostly short-term reversal: coins that fell
   most yesterday tend to beat the others today.
3. Most of it is gone after one day, and its break-even cost (0.45% per side) is below what meme coins actually
   cost to trade. The top-fifth basket lost 42% a year after costs, against 26% for holding all 24.
4. The weekly ranking isn't statistically significant.

**Both horizons are "weak": real information, not a profitable strategy.** Two variants did pass the gate (Yahoo-only
weekly; no-short-return daily), but they were 2 of 12 tries chosen after seeing the results. Promoting one would be
the multiple-testing error §6.1 warns about, so the specification fixed in advance stands. Monthly ranking isn't
possible yet (too few cross-sections). The token page shows a coin's place in the ranking with its status. PUMP is
#22 of 24 today, which means nothing while the status is weak.

**Watchlist tokens.** The watchlist's add box is now the same search (it used to add the raw text as a stock ticker:
"PUMP" meant ProPetro Holding). DEX tokens are stored by exact address, shown in their own section with their safety
verdict and identity, re-checked on every scheduled run, and trigger a Telegram alert when a new kind of red flag
appears or the warning score rises 15+ points.

### 8.10 Research tab (6 Oct 2026): on-demand due diligence with sources

**What:** the *Research* button on any asset or token page (or the Research tab) produces a due-diligence report on
one coin, token or stock. It runs only when pressed, never on a schedule. **Package:** `internal/research`.

**How a run works:**

1. **Verified data first** (free). This is our own forecast or token safety report, CoinGecko's project profile
   (description, official links, supply, all-time high) and GitHub activity for the listed repositories: stars,
   commits in the last 90 days, contributors, last push, archived or not. Claude is told to trust these numbers
   and to use them to check what websites claim. On the first ZEC test, this alone found that the original
   `zcash/zcash` repository is archived and development moved to `librustzcash` (1,076 commits in 90 days).
2. **Research** (Claude Opus 5.5, effort high): up to 15 web searches and 12 page reads, using the
   `web_search_20260209` and `web_fetch_20260209` tools with dynamic filtering. The prompt asks for primary sources
   first, claims checked against evidence, an active hunt for negatives, and a source URL after every factual claim.
   It gives no buy or sell advice and makes no price prediction.
3. **Structure:** a second call, with no tools, turns the notes into fixed sections. Crypto sections: what it is;
   team; token supply and unlocks; real use and development; roadmap vs delivery; sentiment; red flags. Stock
   sections: business; financials; management; guidance vs results; sentiment; red flags. The call also produces a
   bull case, a bear case, what would change the view, a hype check and a "fundamentals" rating (strong, mixed, weak
   or insufficient), which describes the evidence, not the price. Two calls are needed because structured JSON
   output can't be combined with web-search citations.
4. **Sources:** every finding cites numbered sources. A source is marked "read" if it appeared in Claude's search
   or fetch results, and "cited" if it was only named. Source ids are validated, and findings without a source are
   labelled unverified.
5. **Memory:** each report is saved with the price at the time. The next run receives the previous report's
   summary and lists what changed. The history table shows the price move since each report, the start of a track
   record for the research itself.

**Cost control:**

- A monthly cap, `RESEARCH_BUDGET_USD` (default $25). A run is refused when this month's spend plus the typical
  cost ($2.50) would exceed it.
- Spend is logged as `RESEARCH:<subject>` in the Claude usage log, so failed runs count too.
- **Prompt caching is essential.** The server-side web tools run several model steps per request. Without caching,
  a one-search, one-page smoke test billed 196K input tokens ($0.91). With automatic caching, the same test paid
  for 28 uncached tokens, 19K written to cache and 160K read back at 5% of the price: $0.36. A full report is
  expected to cost about $1.50–3.

**Not built (by choice):** the influencer post checker. X/Twitter isn't readable directly (its API is expensive), so
community sentiment comes from whatever web search can see.

### 8.11 Strategy tests (6 Oct 2026): "stronger business, lower price" and Fibonacci levels

**Why:** the user saw a well-followed trader describe his method. Buy a stock once it has fallen to an attractive
level while the business has kept improving ("the fundamentals tell me whether to buy"), enter around the 0.618
Fibonacci retracement of the weekly chart, and exit if the next major support below breaks. Both halves were built
and then tested before being trusted.

**Company financials (`internal/fundamentals`)** come from SEC EDGAR's free XBRL "company facts": US-listed
companies, back to about 2009. Each quarter's trailing-twelve-month revenue, operating income, net income,
operating cash flow, capital spending, free cash flow, debt, cash and equity are built **point in time**:

- every figure keeps the *first* filing that reported it (later restatements don't leak backwards);
- a twelve-month figure only counts from the date its last component was filed;
- 10-Q cash flows are year-to-date, so a quarter is the difference between consecutive year-to-date figures;
- the fourth quarter is the annual figure minus the nine-month figure.

Checked against Apple's published results: FY2023 revenue $383.3B, net $97.0B, operating $114.3B; FY2024
revenue $391.0B, net $93.7B, free cash flow $108.8B. Data is cached for a week (table `fundamentals`). The SEC asks
for a contact in the user agent, set with `SEC_USER_AGENT`.

**The pattern (`fundamentals.Compare`, thresholds fixed before testing):** the price is at least 20% below its
3-year high; twelve-month revenue is at least 5% higher, and operating profit at least 10% higher (net profit if
operating isn't reported), than in the figures known at the time of that high; the company is profitable now; and
long-term debt hasn't risen more than 50%. The *Business vs price* panel on US stocks shows each check, a chart of
the figures against the share price, the quarterly table with price and P/E on each filing date, and the test
result.

**Chart levels (`internal/levels`):**

- *The leg:* a major up-leg on weekly bars, from the lowest low to the highest high of the last 3 years, rising at
  least 30%.
- *The levels:* Fibonacci retracements of that leg, and support zones from weekly pivot lows within 3% of each other.
- *The plan:* a buy zone at 0.58–0.65 of the leg, with the stop 1% under the next support zone below it that has at
  least 2 touches (the 0.786 level if there's none).
- *History:* every past 0.618 touch for that stock, with what happened next.

**The tests** (`internal/strategies`, `-strategy-tests` or the *Re-run tests* button; first run about 12 minutes) use
every stock that was in the S&P 500 since 2012: 815 tickers, of which 613 had prices and 600 had SEC data.

*Stronger business, lower price*, monthly from Jan 2012 to Aug 2026: 581 stocks, 176 months, only members at each
date, only filings made by each date.

| Group | Cases | 12 months vs average stock | Fell another 20%+ |
|---|---|---|---|
| Pattern | 4,111 | +0.7% | 16% |
| Price 20%+ lower, business not stronger | 15,486 | +1.0% | 16% |
| All | 52,077 | 0 | 12% |

The pattern portfolio (rebalanced monthly): **+10.4% a year vs +14.3%** for the average stock, monthly edge −0.21%
(t = −0.86), worst fall −35% vs −26%. Status: **no proven edge.** Checking the fundamentals added nothing over simply
buying 20% dips, and both trailed the average stock. The survivorship bias here (215 tickers missing, mostly delisted
or acquired) flatters dip-buying, so the true result is probably worse.

*Fibonacci levels*: 604 stocks, every first weekly close into each level from above. A "bounce" is a rise of 0.236 of
the leg before a further fall of 0.168, which gives a break-even rate of 41.6%.

| Level | Touches | Bounced | 13 weeks later |
|---|---|---|---|
| 0.382 (Fibonacci) | 6,756 | 55.7% | +5.7% |
| 0.45 | 5,461 | 56.9% | +6.0% |
| 0.5 (Fibonacci) | 4,783 | 56.3% | +6.2% |
| 0.55 | 4,218 | 56.2% | +6.3% |
| **0.618 (Fibonacci)** | 3,574 | **55.4%** | +6.3% |
| 0.68 | 3,151 | 56.4% | +7.1% |
| 0.75 | 2,756 | 55.0% | +6.8% |
| 0.786 (Fibonacci) | 2,532 | 54.6% | +6.1% |

0.618 versus 0.55 and 0.68: z = −0.85. **Not special: every level behaves the same.** All levels look profitable
because the test period is mostly a rising market and the sample is mostly surviving companies. That makes it
evidence for "buying pullbacks in strong stocks" in a bull market, not for Fibonacci numbers, and it shouldn't be
read as an edge.

**What's left of the method:** what the data supports is the risk discipline (a pre-set exit below support, scaling in,
small size). The screen of stocks matching the pattern today (70 of 375 evaluable members on 6 Oct 2026) is shown as
a candidate list for research, not as signals.

### 8.12 Accounts and personalisation, step 1 (17 Nov 2026)

Feedback was that the app shows a lot to everyone. Step 1 adds proper accounts, a setup quiz and an *About you* page.
The rule throughout: **personalisation changes what is shown and how it is explained, never a number.** Forecasts,
validation verdicts, the track record and warnings are identical for every user.

- **Security.** Passwords are hashed with argon2id (19 MiB, 2 passes), with at most 4 hashed at once. Sessions are
  random 256-bit tokens in an HttpOnly, SameSite=Lax cookie, stored only as SHA-256 hashes, and last 30 days. A session
  is extended when it's used, but at most once an hour. Every state-changing API call needs an `X-Requested-With`
  header, which a page on another site can't send without a CORS preflight the server refuses. Invite and reset tokens
  are also stored hashed. Sign-in is rate-limited per address (30 failures in 15 minutes) and per account (8). An unknown
  email takes as long to reject as a wrong password. Changing the password or resetting it signs out the other devices.
- **Bootstrap.** The first account needs a one-time code printed in the server log. It becomes the admin and claims the
  watchlist, journal and journal settings from before accounts existed. This was tested on a copy of the real database:
  all rows were kept.
- **What is per person:** the watchlist (capped at 60 items), journal positions and settings, profile, and sessions.
  **What is shared:** reports and predictions, scans, rankings, research reports, and token reports. The scheduled run
  analyses the union of everyone's watchlists. Telegram alerts go only to the owner (the first admin) and only for the
  owner's own watchlist and journal.
- **Admin only:** scans, model training, Run all, strategy re-runs, earnings backfill, prediction scoring, the alert test,
  Claude spend, and managing people and invites. The last active admin can't be demoted, switched off or deleted.
- **Allowances.** Members get 10 AI-assisted refreshes a day; after that, refreshes run on the lexicon scoring. They
  also get 2 research reports a month, and the server-wide `RESEARCH_BUDGET_USD` still applies. Members can also start at most 40 analyses
  in any 10 minutes.
- **Profile.** The profile records experience, goals, horizon, interests and reaction to a 30% fall, plus a 5-question
  knowledge check graded on the server.
  - **Level:** the quiz outweighs the self-rating.
  - **Simple view:** used for beginners, or for anyone who calls themselves new. The watchlist shows only their horizon,
    and asset pages fold the levels, indicators, evidence, options and model inputs panels.
  - **Defaults:** asset pages open on the person's horizon. Tips are chosen from their goals, and the starter watchlist
    from their interests (it's analysed at once, without the AI, so it costs nothing).
  - **Skipping setup** changes nothing: the full view, as before. (Changed in §8.20: it now means the simple view.)
- **Not collected:** clicks, searches or reading behaviour. Step 2 (an event log) will be listed on *About you* before
  anything is recorded.
- **Tests:** `store/users_test.go` covers bootstrap claiming, invites, sessions, resets, privacy between users and the
  last-admin rule. `api/auth_test.go` runs the whole flow over HTTP, including 401, 403 and anti-forgery checks.
  `auth` and `profile` have unit tests.

### 8.13 Redesign: "The Range" with "One thing to do today" (17 Nov 2026)

Chosen with the user from three mocked directions (brief and decision in `docs/DESIGN_BRIEF.md`).

- **Today is built on the server** (`internal/today`, `GET /api/today`) as a pure function of the watchlist, latest reports,
  token checks, open journal trades, profile and suggestion history, so wording and choices are unit-tested.
- **One suggestion a day, not a queue.** If it's skipped, the card collapses to a note with Undo and nothing replaces it.
  Steps tied to an event carry its date in the id (`earnings:NVDA:2026-11-17`), so a later event can be suggested again.
- **Honesty in the wording:** "since you last looked" only appears when there was an earlier visit (visits under 30 minutes
  apart count as one); an analysis older than three days gets a DATA line; unanalysed symbols are named; a tested model
  with no lean says so rather than "no edge"; long fund names fall back to the ticker.
- **New data kept:** `user_prefs` (theme, accent, last two visit times) and `user_steps` (done / skip per suggestion). Both
  are in the data export, deleted with the account, and disclosed on About you. They are the starting point for step 2
  (learning from behaviour).
- **Theming:** all colours are CSS tokens; existing class names were kept so every page took the new look at once. Canvas
  charts read resolved colours (`cssColor`) and redraw when the look changes.
- **Tests:** `today/today_test.go` (briefing, voice by level, earnings window and timing, holding vs not, skip/done, empty
  list, names), `store/today_test.go` (prefs, visit gaps, skip/undo/done, privacy), and the HTTP flow in `api/auth_test.go`.

### 8.14 Asset page redesign, and a test of the earnings range (19 Nov 2026)

**Layout** (agreed with the user from a mockup): the short answer first, then a price chart with a forecast cone (the
dashed target lines are gone from it), the four horizons as rows beside it, "why" in plain words beside the AI analyst's
case for a rise and a fall, then "Go deeper" tabs (Earnings, Similar days, Track record, Drivers, If you hold it,
Technical, Full reasoning) and news. Technical detail is folded for everyone; people who chose full detail open on the
Technical tab. The analyst's raw source codes (`[ccb747aa09]`) are shown as numbered boxes linking to the stories.

**Plain words come from the server** (`internal/plain`, sent as `plain` with every report, in the reader's level). It
only restates the report: the answer sentence depends on the validation status, the gap to the base rate, whether
always-guessing beat the model, similar-days disagreement and earnings inside the horizon. Unit-tested in
`plain/plain_test.go`.

**The earnings range was checked, not changed.** It looked too wide (NVIDIA's next-day 80% range ±16% against options
pricing ±8%), but that compared an 80% range with an expected move. A leaner formula (report day replaces one ordinary
day, jump measured without the ordinary days in its window) was tested against the current one on 35,000 past reports in
602 stocks (`go run ./cmd/earncheck`):

| Window | Current | Leaner |
|---|---|---|
| reaction only | 82.1% | 76.4% |
| reaction + 5 days | 81.5% | 78.1% |
| reaction + 21 days | 81.7% | 80.1% |

Target 80%. The current widening is well calibrated (slightly cautious); the leaner one under-covers short windows, so
the current one stays and `engine/earnings_widen_test.go` pins it.

**Bugs found while testing:** canvas charts crashed in dark mode because the accent colour came back as `color(srgb …)`,
which the chart library can't parse (now resolved through a one-pixel canvas); the business-vs-price chart crashed for
companies with filings published out of order (points now sorted).

### 8.15 Research page redesign (19 Nov 2026)

**Layout** (agreed with the user from a mockup, real Zcash report): the short answer first (one sentence, the overview,
"about the project, not the price"), with a Weak / Mixed / Strong scale, the price move since the research and how much
evidence there is; then the case for and against side by side; a reality check (hype against real use) beside "what
would change this view"; every section as a row with a good/concern count bar, red flags first and open; sources,
Claude's input and notes folded away; earlier reports last. A report older than 30 days gets a band saying so, with
the price move since.

**Running research** no longer uses a browser pop-up: an in-page card says how long it takes and what it uses (a
member's monthly allowance, or the cost against the budget for admins), then shows the three real stages while it runs.

**New in the data:**
- `headline`: new reports ask Claude for a one-sentence answer (at most 16 words, no price, no advice). Older reports
  show a sentence for their verdict instead.
- Copycat warnings on the Research home list ("Not the listed ZEC", "Not listed on CoinGecko") are read from the
  verified data each report was given (`Report.IdentityWarning`), so older reports get them without re-running.
- The home list shows each subject's latest verdict and price move since; the price is fetched fresh (stale caches are
  topped up), and the date of the price is shown on the report page.

### 8.16 The rest of the redesign, and a scheduler fix (19 Nov 2026)

The user asked for every remaining page to be finished before review. Journal, Watchlist and Opportunities got mockups
first (design canvas); the others were designed straight in code in the same system, and every page was then
screenshotted "as built" (computer light, phone dark) onto the canvas for review. Each page now opens with a short
answer in plain words, then the evidence, with technical detail folded away.

- **Journal:** "Where you stand" sentence, the plan for each open trade drawn as a stop → entry → target bar with
  today's price, "What your record shows" (facts from closed trades only: gaps past stops, one trade carrying the
  total, losers held longer than winners, meme-only trading, fees), a four-step new-trade form with a live plan
  sentence, rules folded. Still fees-only (no auto-stops, cash limits or currency).
- **Watchlist:** plain verdicts with ✓ ◐ ○, a chance bar with a 50% line, Day/Week/Month/Year/All, and a band when
  analyses are more than 16 hours old.
- **Opportunities:** the short answer is computed from what actually passed testing (on 19 Nov: only the crypto ranking,
  a day or a week ahead); a grid of every ranking by group and horizon; ranking reasons in plain words; the direction
  scan and both strategy tests summarised in one line each, details on demand.
- **Track record:** leads with how many forecasts *from tested models* have been scored (6, too few to judge) before the
  overall coin-flip figures; hit rates are drawn against "always guessing the more common direction".
- **Token safety, Replay, How it works, About you, Welcome, sign-in, Admin:** same pattern. Replay now returns the
  plain-language answers too (`replay` in `api.go` uses `withPlain`).

**Scheduler bug found and fixed.** No scheduled run had happened since 17 Nov 22:30: the Mac was asleep at each run
time, and the scheduler slept on a Go timer, which on macOS doesn't count time asleep, so it kept waiting. It now
checks the wall clock every 30 seconds and runs a missed slot as soon as the computer wakes (`scheduler.go`, tested in
`scheduler_test.go` with a simulated sleep).

### 8.17 The name: Hindcast (19 Nov 2026)

"Market Oracle" promised certainty, which the whole app argues against. The user chose **Hindcast** from a shortlist: a
hindcast is a forecast made for the past to test a method, which is how every signal here earns its place. Quick web
searches found no investing app of that name (not a trademark search). Only names people see changed (page title,
brand, emails, alert test, data-export file name, outgoing User-Agents, docs). Internal identifiers stay, because
changing them would break things for no visible gain: the database `market_oracle`, the `X-Requested-With:
market-oracle` forgery-protection value, and the `bin/market-oracle` binary. Sign-in and the glossary explain the word.

---

### 8.18 Step 2: learning from what you do (19 Nov 2026)

Agreed with the user: all three kinds of learning, **always ask first**, and journal reminders that start "Worth a
look:" and then state the fact plainly. The user wanted learning that is distinct for each person rather than one
rule for everyone, and asked why not machine learning. The answer, which they accepted, is a hybrid:

- **Fixed, readable rules decide what can be suggested** (`internal/learn/rules.go`): add an asset opened 4+ times in
  a week; remove one not opened for 30 days (only after 30 days of noticing); make a view the default after 8 of the
  last 10 picks; offer full detail when the technical tab is opened on 3 in 4 of 8+ asset pages.
- **Which suggestion comes first is learned per person**: each kind's chance of a yes starts from everyone else's
  rate (smoothed towards a half, worth 4 answers) and moves with the person's own answers. Kinds they keep declining
  are held back unless not asked for 60 days. At most one suggestion a day; "not now" waits 30 days (90 for removes);
  every yes can be undone.
- **Journal habits are stated only when the record shows them** (`habits.go`): facts (counts) appear at once;
  comparisons need 8 trades each side (tested vs untested signals), 5 losers and 5 winners (holding time) or 5
  stop-outs, and are named only if a permutation test puts luck below 1 in 20. Each new trade now saves whether its
  signal was a tested edge (`positions.edge`), since the signal text alone didn't say.
- **Personal model** (`model.go`, built after the user reviewed the mockups): learns from one person's closed paper
  trades and practice buys which of their setups tend to lose. With tens of trades anything bigger than one yes-or-no
  question fits noise, so it asks each of a fixed list (`setup.go`: big week up or down, near or well below a
  3-month high, jumpier than usual, tight or no stop, the app said buy, a tested buy, the asset class) and keeps the
  one that splits winners from losers most (at least 3 trades each side). The chance of luck reruns the whole search
  on 2,000 shuffles of the results, so picking the best of many questions is accounted for. An early read (◐) from
  10 trades shows the best guess with a 90% range and names a hint only below a 1-in-4 chance of luck. From 30, the
  full test picks the question on the earlier two-thirds of trades, checks it on the later third and needs luck
  below 1 in 20; only then ✓. "Made money" (after fees) is the outcome, so paper and practice trades compare alike.
- **Practice rounds** (`practice.go`, `/journal/practice`): 10 real past days per set, from a pool of 24 well-known
  stocks, funds and coins plus the person's watchlist, at least 1,300 bars into the history (the engine needs about
  830 rows after warm-up for a month-ahead reading) and with a full month after. The chart shows six months of
  closes divided by the day's close (no price level, name or date). The person picks buy or skip, a stop (5/8/12%)
  and a target (10/15/25%). The plan plays out on daily highs and lows: stop first if both are hit in one day, a gap
  below the stop fills at the open, otherwise the last close after 21 bars (30 for crypto); $1,000 a round, with the
  person's journal fee each side. What the app said is a full replay as of that day (`engine.Analyze` with `AsOf`,
  never saved), worked out in the background in order, a few seconds each, so it's ready before the person gets
  there. The app's "lean" is its own buy or sell call, as for paper trades: on most days it has no proven edge and
  makes no call. A set's summary counts outcomes, skipped days that would have worked, buys with and against the
  app's call, and a "Worth a look" hint when every losing buy shared a setup most winners didn't (never called a
  habit from one set). The user chose: practice counts towards the personal model (shown as "from N paper trades
  and M practice buys"), each round shows what the app said, and the page lives under the Journal.

What is recorded (`user_events`): asset-page visits, the watchlist view picked, the technical tab opened. Nothing
else (no searches, typing or reading time). Kept 90 days, deduplicated within 30 minutes, not recorded while paused.
About you → "What it's learned" lists every question and answer, what was noticed, how suggestions are picked, the
habits and the personal model, with undo, forget (one thing or everything), pause and download. Practice rounds
are kept apart from paper trades and are deleted separately ("Delete practice rounds"); both exports include them.

A schema comment containing a semicolon broke the schema split and would have stopped the server starting; caught
by the store tests and fixed. Note: backend tests share one test database, so run them with `-p 1` (as `make test`
does).

### 8.19 The schema in one file per area (19 Nov 2026)

The schema had grown into one 315-line string in `store.go`. At the user's request it now lives in
`backend/internal/store/schema/`, one SQL file per area (forecasts, market data, Claude, tokens, watchlist and
journal, accounts, learning), embedded in the binary and applied in file-name order on every start, as before. The
numbers carry the order: accounts come after the tables they add owner columns to. Every statement must stay safe to
run again (`IF NOT EXISTS`). To change a table, add a statement to its file rather than editing an old one. The
splitter now skips `--` comments and quoted text, so a semicolon there can no longer break startup. Checked: the
same 55 statements, and an empty database set up from the files matches one set up from the old string (only the
column order of `positions.edge` and `user_id` differs on a brand-new database, and every query names its columns).

### 8.20 Assume a beginner until someone shows otherwise (20 Nov 2026)

The user asked what a person sees by default. The answer showed a gap: someone who said nothing got beginner
**wording** but the **full** detail, a leftover from before accounts existed. So the people the app knew least about
saw the most. Agreed with the user:

- **Two separate settings, both shown on About you.** "How it's explained" (the level: plain words, some terms, with
  the numbers) and "How much is shown" (simple or full). Each can be decided from the answers or chosen by the person.
  A wording choice (`profile.Wording`) wins over the quiz, as a detail choice already did. It doesn't change the detail.
- **The starting point is the simple view.** Answers that show some experience still open everything. Someone who
  digs into the technical tabs is offered full detail by the existing rule.
- **A new suggestion offers the numbers** (`levelRule`): Track record or Replay opened on 4+ days in 30 (recorded as a
  new `page` event, listed on About you and forgettable), or, already on full detail, the deeper tabs opened on at
  least half of 8+ asset pages. It is quiet for a month after the knowledge check, and when the person chose a
  wording. The card also offers the quiz. A yes sets only the wording; undo goes back to the answers.
- **Not added:** a suggestion to go from full back to simple. Taking detail away unasked feels patronising, and the
  setting is one tap away.

## 9. Where to change things

| Want to change | File |
|---|---|
| What a new person sees, and the levels | `backend/internal/profile/profile.go` → `Level`, `Settings` |
| Learning rules (suggestions) | `backend/internal/learn/rules.go` |
| Database tables | `backend/internal/store/schema/*.sql` (one file per area, applied in order) |
| Validation thresholds / signal cut-offs | `backend/internal/engine/explain.go` → `classify` |
| News influence caps and decay | `backend/internal/news/lexicon.go` → `horizonNews` |
| Claude prompt / schema | `backend/internal/news/llm.go` |
| Model inputs | `backend/internal/features/features.go` |
| Walk-forward settings, trade cost | `backend/internal/model/walkforward.go` → `DefaultConfig` |
| Scanner universes | `backend/internal/engine/scanner.go` → `Universes` |
| Benchmarks per exchange | `backend/internal/market/service.go` → `Benchmark` |
| Cross-asset blend gate | `backend/internal/pooled/direction.go` → `Skilled` |
| Ranking universes / gate | `backend/internal/pooled/trainer.go` → `RankUniverses`; `rank.go` → `evaluateRank` |
| Earnings effects on forecasts | `backend/internal/engine/earnings.go` → `applyEarnings` |
| Claude prices used for cost tracking | `backend/internal/news/llm.go` → `rates` |
| Batch polling / timeout for scheduled runs | `backend/internal/engine/engine.go` → `AnalyzeBatch` |
| Retrain cadence, history window, earnings start | `.env`: `RETRAIN_AFTER`, `POOLED_YEARS`, `EARNINGS_FROM` |
| Schedule, model, effort, keys | `.env` (see `.env.example`) |
| Token safety thresholds and scoring | `backend/internal/token/assess.go` (contract checks in `token.go` → `security`) |
| Copycat rules in search | `backend/internal/token/token.go` → `classifySearch`; identity check in `identity.go`; CoinGecko address book in `registry.go` |
| Volatility model and its gate | `backend/internal/vol/vol.go` → `Model` (`Better`) |
| Drift monitor thresholds | `backend/internal/tracker/drift.go` (constants at the top) |
| Journal risk rules and warnings | `/journal` page (rules); `backend/internal/journal/journal.go` → `warnings` |
| Meme-coin list (tickers ↔ CoinGecko ids) | `backend/internal/market/meme.go` |
| Meme ranking cost and validation start | `backend/internal/pooled/trainer.go` → `MemeTradeCost`, `trainMeme` |
| Research prompts, sections, limits, cost estimate | `backend/internal/research/research.go` (`researchSystem`, `headings`, `limits`, `EstimateUSD`); budget `RESEARCH_BUDGET_USD` |
| Business-vs-price rules, SEC concepts | `backend/internal/fundamentals/analysis.go` (`DefaultRules`), `sec.go` (tags) |
| Fibonacci/support logic and test | `backend/internal/levels` (`lookback`, `minLeg`, `band`, `TestLevels`) |
| Token risk statistics | `backend/internal/token/risk.go` |
| Telegram alerts | `.env`: `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`, `APP_URL`; `backend/internal/alert` |
