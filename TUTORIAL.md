# Hindcast: how to use it for maximum value

This guide covers how to run the app, what to look at each day, how to read every screen, and how to test the system
on the past before trusting it with money. Section 7 is a worked demo on real data.

> **New to investing?** Start with [docs/WALKTHROUGH.md](docs/WALKTHROUGH.md). It walks through Apple, Bitcoin and the
> PUMP meme coin step by step, explaining every number in plain language and ending with how to reach a decision.

> **The one rule:** only act on signals the system has *proven* on that asset and horizon, size them small, and let
> the live track record, not any single forecast, decide how much you trust it.

---

## 0. The short version: using it to its full strength

The platform has five engines. Each answers a different question; the strength comes from using them together, in
this order:

| # | Engine | Question it answers | Trust it when |
|---|---|---|---|
| 1 | **Rankings** (Opportunities) | *Which* assets should I hold right now? | The ranking's status is **Validated** |
| 2 | **Direction forecast** (asset page) | Will *this* asset go up, by how much, and how wrong could I be? | The horizon says BUY/SELL with **Validated edge** |
| 3 | **Risk plan, earnings and options** (asset page) | How big should the position be, where's the stop, and is an event coming? | Always: this is your risk control |
| 4 | **News and AI narrative** (asset page) | Has something changed that the price models can't see yet? | As a warning light, not a signal |
| 5 | **Replay and Track record** | Did any of this actually work? | Always: this is how you decide what to trust |
| 6 | **Token safety report** (paste a contract address) | Is this meme coin a trap? | Always for meme coins; there is no forecast for them |
| 7 | **Research** (button on any asset or token page) | What is this project or company really, and what has changed? | As a fact check with sources, never as a price signal |
| 8 | **Business vs price** and **Chart levels** (US stock pages) and **Strategy tests** (Opportunities) | Is the company stronger than when the stock was higher? Where are the support and Fibonacci levels? Do these methods work? | Business data and support levels: as context. The tests found **no edge** in "stronger business, lower price" and nothing special about 0.618 |
| 9 | **Journal** | Is my *portfolio* too risky, and are my decisions any good? | Always: record every idea here first |

**The playbook the evidence supports today:**

1. **Pick from a validated ranking, not from gut feel.** Opportunities → Rankings → your universe → the *Next week*
   horizon (the most consistent in testing). If the status is Validated, the top fifth is your candidate list. If no
   ranking for your universe is validated, there is no trade this week.
2. **Vet each candidate on its asset page** with the checklist in section 4. Drop anything with earnings inside your
   holding period (unless you want that risk), a model range much narrower than the options-implied move, or bad
   news the models can't see.
3. **Size with the risk plan:** stop at the 10th-percentile outcome, and the "size that risks 1% of your account"
   figure (1% ÷ distance to the stop). Record it in the **Journal**, which checks total risk (≤ 6%), position size
   (≤ 20%) and positions that move together (which count as one bet).
4. **Hold for the horizon, then re-rank.** A ranking predicts the next week, not forever. Rebalance on the same day
   each week: keep names still in the top fifth, replace the rest. That's exactly what the ranking backtest simulates
   (with costs), so your results can match the evidence.
5. **Use direction signals as a filter, not a trigger.** A validated BUY on a top-ranked asset is the strongest
   setup. A NO EDGE direction on a top-ranked asset is fine. A validated SELL on a top-ranked asset means skip it.
6. **Let the evidence update you.** Every week, check Track record (live hit rate by horizon and signal). Every
   month, replay the assets you trade (section 6). Stop using anything whose live record falls below its backtest.
7. **Paper-trade the first 4–8 weeks** in the Journal ("Record a paper trade" on any analysis or token page), then
   compare the closed trades with what happened. The drift monitor automatically demotes any forecast whose live
   record falls below the base rate. To learn faster, play **practice rounds** (Journal → Practice on past days):
   decide on ten hidden real past days and see each month that followed at once. Your practice buys feed your
   personal model (About you → What it's learned), which gives an early read from 10 trades and runs the full test
   from 30.
8. **Meme coins:** paste the contract address from the project's official source (never buy by name: the search
   flags copycats and fakes), read the safety report, and treat anything you buy as a lottery ticket sized so that
   losing all of it is acceptable.

**Make it stronger over time:**
- **Turn on the AI analyst** (`ANTHROPIC_API_KEY` in `.env`). News judgments and the written narrative are much
  better than the word list, and the track record measures whether news improves the forecasts.
- **Add your assets** to the watchlist so they're analysed twice a day, and to the scanner lists in
  `backend/internal/engine/scanner.go` so the cross-asset models learn from them.
- **Keep it running.** Models retrain daily, earnings dates stay current, and the track record only grows while the
  server runs. A week of live forecasts is worth more than any backtest.
- **Turn on Telegram alerts** (`TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID`) to hear about signal changes, demotions and
  journal stops without opening the app.
- **Settings worth knowing** (`.env`): `RUN_TIMES` (when it runs), `LLM_EFFORT` (Claude cost vs depth),
  `RETRAIN_AFTER` (how often models retrain), `POOLED_YEARS` (training window), `CACHE_MAX_AGE` (how fresh prices
  must be).

---

## 1. Start it

```bash
cd ~/Documents/financial-market
PATH="/opt/homebrew/bin:$PATH" make run      # then open http://localhost:8080
```

- Leave it running: it analyses your watchlist at the times in `RUN_TIMES` (yours: 07:30 and 22:30), retrains the
  cross-asset models daily, keeps earnings dates current and scores old predictions automatically.
- Everything is stored in your Postgres database `market_oracle`. Back it up with `pg_dump market_oracle > backup.sql`.
- To get the AI news analyst, put `ANTHROPIC_API_KEY=...` in `.env` and restart. Without it, news is scored by a
  word list, and everything else works the same.

---

## 2. A daily routine (10–15 minutes)

| When | Where | What to do |
|---|---|---|
| Morning, after the 07:30 run | **Watchlist** | Scan the badges. Ignore every NO EDGE. Note any BUY/SELL whose status is "Validated edge". |
| | **Opportunities → Rankings** | Pick the universe you trade and the horizon you hold for (a week suits most people). Only use a ranking whose status is **Validated**. The top of the list is your candidate shortlist. |
| For each candidate | **Asset page** | Run the pre-trade checklist below. Click **Run full analysis** first if the report is more than a few hours old. |
| Weekly | **Track record** | Check the hit rate by horizon and by signal. If a horizon's live record is below its backtest after 50+ forecasts, stop trading it. |
| Monthly | **Replay → Replay many dates** | Re-test the assets you trade most. |

---

## 3. Reading the screens

### Watchlist
Each horizon shows a **signal** and the **probability the price is higher** at that horizon.
- **STRONG BUY / BUY / SELL / STRONG SELL**: the method beat a naive forecast on this asset at this horizon in
  out-of-sample testing, and today's probability is far enough from 50/50.
- **HOLD**: proven method, no strong view today.
- **LEAN UP / LEAN DOWN**: some evidence, not enough to trust. Information only. Don't trade it.
- **NO EDGE**: the method has *not* proven itself here. The number is shown for transparency only. Don't trade it.

### Asset page, top to bottom
1. **Horizon cards.** Probability, status badge, conviction (0–100), expected move, 80% range, median price target,
   base rate, and the options market's implied move. An orange **earnings** tag means a report falls inside the
   horizon.
2. **"Why the model says…" panel**, with five tabs:
   - **Why:** the reasoning chain in plain English, plus *how the probability is built* (base rate → each model →
     blend → calibration → news → final).
   - **Drivers:** each input's push up or down in percentage points. Click a row for its explanation. Below that is
     the cross-asset model's view, marked "in the blend" or "not used: no proven skill yet".
   - **Analogs:** the most similar past days and what happened next.
   - **Backtest:** *the most important tab.* Hit rate against the naive guess, calibration, accuracy at each
     confidence level, and whether trading the signals would have beaten buy-and-hold.
   - **Risk plan:** stop, target, reward-to-risk and suggested position size.
3. **Price chart** with the selected horizon's 10th / median / 90th percentile lines.
4. **Analyst narrative and news.** Each headline shows its direction, relevance and reasoning. News can only nudge
   the forecast by a few points.
5. **Indicator readings** (textbook interpretation), **historical evidence** (raw counts of what followed today's
   conditions in this asset's past), **earnings** (next date, typical move, beat rate, drift after beats and misses),
   **options market** (expected move to each expiry) and **model inputs**.

### Opportunities
- **Rankings** answer "which of these will beat their peers?", measured after removing market exposure (beta), so
  a high-volatility stock can't win just by being high-volatility. Check four things:
  1. The status is **Validated**.
  2. The **decile chart** slopes upward (higher-ranked assets really did better).
  3. The **top-fifth basket** beat the universe after costs.
  4. The **beta-neutral edge** is positive.
- **Direction scan** runs each asset's own model across a list and keeps proven, positive-expectancy signals,
  after a correction for testing many assets at once (the p (FDR) column).

### Track record
Every forecast is stored unchanged and scored when its date passes. This is the only fully honest test, and it
outranks every backtest. It also shows whether the news overlay helps (forecasts with and without it).

### Replay
A time machine: pick a symbol and a past date to see what the system would have said that day, next to what
actually happened. Section 6 explains how to use it properly.

---

## 4. Pre-trade checklist

Only consider a trade when **all** of these are true:

- [ ] The signal is **BUY/SELL** (not HOLD or NO EDGE) and the status is **Validated edge**.
- [ ] **Backtest tab:** at *today's* confidence level (the highlighted row) the hit rate clearly beats the naive
      guess on at least ~200 forecasts.
- [ ] **Backtest tab:** the strategy column shows the signals earned more than holding, or similar returns with a
      smaller drawdown.
- [ ] **Why tab:** the component models agree (or the disagreement is explained).
- [ ] **No earnings inside your holding period**, or you have accepted that risk. The earnings panel shows the
      typical move.
- [ ] The options market's implied move isn't far wider than the model's range. If it is, the market knows about a
      risk the model can't see.
- [ ] For stocks, the asset ranks in the top part of a **validated** ranking for your horizon. Two independent views
      agreeing is worth more than either alone.
- [ ] Your position size is no bigger than the suggested size (quarter-Kelly, capped), and you will use the stop.

If you can't tick every box, the right trade is no trade. Sitting out is a position.

---

## 5. Risk rules

1. Risk at most 1–2% of your capital per trade between entry and stop. Position size = risk ÷ (entry − stop).
2. Never move a stop further away.
3. Don't hold through earnings unless that is your plan.
4. Several crypto BUYs at once are really one bet: crypto moves together. Treat them as a single position.
5. Start with paper trades (track them, no money) for 4–8 weeks. Go live only with what has worked live.

---

## 6. Testing the system yourself with Replay

**One date** (Replay → Replay one date): enter a symbol and a past date, or use the presets (COVID crash low, the
2022 bear-market low, a year ago). You'll see:
- **Outcome cards:** what each horizon predicted, what actually happened, whether a BUY/SELL call was right, whether
  the price finished inside the 80% range, and whether the stop or the target was hit first.
- **The path chart:** actual prices after the date against each horizon's forecast range.
- **The full analysis as it looked that day:** the same tabs as the asset page.

**Many dates** (Replay → Replay many dates): e.g. the last two years, every 30 days. How to read the summary:

| Column | Meaning | Good sign |
|---|---|---|
| Probability side right | How often P(up) > 50% matched reality | **Above** the "went up" column (guessing "up" every time) |
| BUY/SELL calls / Calls right | How often the actual signals were right | Clearly above 50% over 15+ calls |
| Avg return per call | Average return in the call's direction | Positive |
| Inside 80% range | How often the price finished inside the forecast range | Near **80%**: the ranges are honest |

**Rules for testing honestly:**
- Don't cherry-pick dates. Use regular schedules. A model that is right on the dates you remember proves nothing.
- Test at least one calm period, one crash and one rally.
- 24 dates is a small sample. The **Backtest tab** covers thousands of dates for the same asset and is the stronger
  evidence. Replay is for seeing the system work, and for catching anything that looks wrong.
- Replays leave out news and the options market (no free history exists), so they are a slightly weaker version of
  what you get today.

---

## 7. Worked demo (run on 5 Oct 2026)

### 7.1 One date: SPY at the April 2025 tariff-crash low
Open `http://localhost:8080/replay?symbol=SPY&date=2025-04-08`.

| Horizon | What it said on 8 Apr 2025 | What happened | Verdict |
|---|---|---|---|
| Next day | 55% up, **NO EDGE** | +10.5% (the tariff-pause rally) | Leaned the right way; correctly not a trade signal |
| Next week | 61% up, **LEAN UP** (weak evidence) | +8.3% | Right direction, but not proven enough to be a BUY |
| Next month | 64% up, **NO EDGE** | +13.8% | Leaned right; base rate was 67% anyway |
| Next year | 78% up, **NO EDGE** | +38.4% | Leaned right; base rate was 83% anyway |

Lessons:
- Every move landed **outside** the 80% range: crash rebounds are exactly the extreme events ranges miss. That's
  why stops and position sizing matter more than any single forecast.
- "Leaned right" alone proves little when the base rate already points up. The Backtest tab shows that for SPY the
  1-month method was right 63.7% of the time vs 64.2% for always guessing "up". No skill, so NO EDGE was the honest call.

### 7.2 Many dates: a stock, an ETF and a crypto, every 30 days from Sep 2024 to Aug 2026 (24 dates each)

| Asset | Horizon | Probability side right | Went up (bar to beat) | Inside 80% range |
|---|---|---|---|---|
| SPY | 1 week | 75% | 75% | 83% |
| NVDA | 1 month | 58% | 58% | 83% |
| BTC-USD | 1 month | 58% | 58% | 83% |
| BTC-USD | 1 year | 21% | 21% | **57%** |

What the demo shows:
1. **No hidden skill in single-asset direction.** On every asset, "probability side right" equals the "went up" rate:
   the model mostly leans with the base rate. That's the honest result for daily price data, and it's why those
   horizons show NO EDGE.
2. **The ranges are mostly honest:** 79–88% of outcomes landed inside the 80% range at day, week and month
   horizons. They are too narrow for crypto at one year (57%): treat long crypto ranges as optimistic.
3. **The demo changed the system.** The first run issued 24 one-year BTC **BUY** signals on "weak evidence"; they
   were right only 21% of the 14 times we could check (BTC was lower a year later). Weak evidence was just the base
   rate in disguise, so since this demo only *validated* horizons can say BUY/SELL, and weak evidence shows
   **LEAN UP / LEAN DOWN** (not a trade). Over these two years, the system now issues **no** directional trade calls
   on SPY, NVDA or BTC, which would have kept you out of the bad BTC trades.

### 7.3 Where the actionable edge is: rankings (trained 5 Oct 2026)

Out-of-sample results, after 0.1% costs, with the market's overall move removed (beta-neutral):

| Universe | Horizon | Status | Rank correlation (IC) | Top fifth vs universe | Top-fifth basket a year vs universe |
|---|---|---|---|---|---|
| **Crypto (16 coins)** | 1 day | **Validated** | 0.078 (t 11.1) | +0.07% a day | +8.1% vs −1.0% |
| **Crypto (16 coins)** | 1 week | **Validated** | 0.097 (t 5.0) | +0.26% a week | +17.8% vs −1.2% |
| Crypto | 1 month | No edge | 0.047 (t 1.3) | −0.81% | |
| ETFs and sectors (24) | 1 day / 1 week | Weak | 0.033 / 0.028 | +0.04% / +0.02% | |
| S&P 500, point-in-time (596) | 1 day | Weak | 0.027 (t 7.4) | +0.01% | |
| S&P 500, point-in-time (596) | 1 week / 1 month | No edge | 0.027 / 0.025 | −0.02% / −0.18% | |
| Commodities (10) | all | Weak or no edge | ≤ 0.05 | | |
| Meme coins (24) | 1 day | Weak | 0.109 (t 10.3) | +0.48% a day, but break-even cost 0.45% vs 0.5% assumed | −42% vs −26% (after 0.5% costs) |
| Meme coins (24) | 1 week | Weak | 0.056 (t 1.9) | +0.65% | −47% vs −47% |

Boosted trees were deployed only for ETFs at 1 day. Everywhere else they didn't beat the linear model, so it ranks
alone. The crypto basket earned more per unit of risk (Sharpe 0.56 vs 0.31) but **fell 69% at its worst, just like
the coins themselves**. A good ranking chooses *which* coins to hold; it doesn't protect you from a crypto crash. For
stocks, picking the S&P 500's relative winners from price data alone showed no tradable edge once survivorship bias
and market exposure were removed.

## 8. Getting more out of it

- **Add the assets you actually trade** to the watchlist. Analysis is per asset, and some assets have an edge while
  others don't.
- **Use the weekly horizon first.** In testing so far, the weekly rankings were the most consistent, and next-day
  rankings often lost their edge to trading costs.
- **Trust the ranking more than the direction call** for stocks. Predicting "up or down" is close to a coin flip for
  most stocks; picking the relatively stronger ones is easier.
- **Watch the Claude spend** under Opportunities → Models & data, and adjust `RUN_TIMES` or `LLM_EFFORT` if needed.
- **Re-read DECISIONS.md section 7** for the next upgrades (§8.8 covers the meme-coin reports, volatility forecasts,
  drift monitor, journal and alerts).
