// Command server runs the market forecasting API, the twice-daily scheduler
// and serves the React UI.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sodiq/financial-market/backend/internal/alert"
	"github.com/sodiq/financial-market/backend/internal/api"
	"github.com/sodiq/financial-market/backend/internal/auth"
	"github.com/sodiq/financial-market/backend/internal/config"
	"github.com/sodiq/financial-market/backend/internal/earnings"
	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/fundamentals"
	"github.com/sodiq/financial-market/backend/internal/journal"
	"github.com/sodiq/financial-market/backend/internal/learn"
	"github.com/sodiq/financial-market/backend/internal/mail"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
	"github.com/sodiq/financial-market/backend/internal/news"
	"github.com/sodiq/financial-market/backend/internal/pooled"
	"github.com/sodiq/financial-market/backend/internal/research"
	"github.com/sodiq/financial-market/backend/internal/scheduler"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/strategies"
	"github.com/sodiq/financial-market/backend/internal/token"
	"github.com/sodiq/financial-market/backend/internal/tracker"
)

func main() {
	analyze := flag.String("analyze", "", "analyse one symbol, print the headline and reasoning, and exit")
	noLLM := flag.Bool("no-llm", false, "with -analyze: skip the Claude news analyst")
	train := flag.Bool("train", false, "train the cross-asset direction and ranking models, print results, and exit")
	strategyTests := flag.Bool("strategy-tests", false, "download SEC filings for the S&P 500, test the stronger-business/lower-price pattern and the Fibonacci levels, print results, and exit")
	trainMeme := flag.Bool("train-meme", false, "train only the meme-coin ranking, print results, and exit")
	backfill := flag.Bool("backfill", false, "download the earnings history (resumable), and exit")
	importDir := flag.String("import-sqlite", "", "copy data from the old SQLite/file storage in this data folder (e.g. ./data) into Postgres, and exit")
	resetFor := flag.String("reset-link", "", "print a one-hour password reset link for this account's email, and exit (for a locked-out admin)")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("cannot open the Postgres database; check DATABASE_URL", "url", redact(cfg.DatabaseURL), "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if *resetFor != "" {
		// Whoever can run this can already read the database, so it grants nothing new.
		u, _, _, err := st.Credentials(ctx, *resetFor)
		if err != nil {
			fmt.Fprintln(os.Stderr, "no account with that email:", err)
			os.Exit(1)
		}
		tok, hash, err := auth.NewToken()
		if err == nil {
			err = st.CreateReset(ctx, hash, u.ID, time.Hour)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(cfg.AppURL + "/reset?token=" + tok)
		return
	}
	if *importDir != "" {
		if err := importSQLite(ctx, st, *importDir, log); err != nil {
			fmt.Fprintln(os.Stderr, "import failed:", err)
			os.Exit(1)
		}
		return
	}
	mkt := market.NewService(st.PriceCache(), cfg.CacheMaxAge, cfg.TwelveDataKey, cfg.CoinGeckoKey, log)
	eng := &engine.Engine{
		Market:  mkt,
		News:    news.NewCollector(),
		Analyst: news.NewAnalyst(cfg.AnthropicKey, cfg.LLMModel, cfg.LLMEffort),
		Config:  model.DefaultConfig(),
		Log:     log,
	}

	if cfg.EarningsFrom != "off" {
		if eng.Earnings, err = earnings.New(st.DB(), cfg.EarningsFrom, log); err != nil {
			log.Error("earnings", "err", err)
			os.Exit(1)
		}
	}
	eng.Memory = st
	trainer := pooled.NewTrainer(eng.MatrixSource, st, engine.Universes, log)
	trainer.Config.Years = cfg.PooledYears
	eng.Pooled = trainer

	if *backfill {
		if eng.Earnings == nil {
			fmt.Fprintln(os.Stderr, "earnings disabled (EARNINGS_FROM=off)")
			os.Exit(1)
		}
		go func() {
			for ctx.Err() == nil {
				p := eng.Earnings.Progress()
				log.Info("earnings backfill", "days", p.DaysDone, "of", p.DaysTotal, "events", p.Events, "coveredFrom", p.CoveredFrom, "lastError", p.LastError)
				time.Sleep(30 * time.Second)
			}
		}()
		_ = eng.Earnings.RefreshUpcoming(ctx, 45)
		if err := eng.Earnings.Backfill(ctx); err != nil {
			log.Warn("backfill finished with errors (re-run to resume)", "err", err)
		}
		p := eng.Earnings.Progress()
		fmt.Printf("earnings: %d reports, covered from %s (%d/%d days)\n", p.Events, p.CoveredFrom, p.DaysDone, p.DaysTotal)
		return
	}
	fund := fundamentals.NewService(st, cfg.SECUserAgent)
	strat := &strategies.Service{Market: mkt, Fund: fund, Pooled: trainer, Store: st, Log: log}
	if *strategyTests {
		t0 := time.Now()
		go func() {
			for ctx.Err() == nil {
				time.Sleep(30 * time.Second)
				p := strat.Progress()
				if !p.Running {
					return
				}
				log.Info("strategy tests", "stage", p.Stage, "done", p.Done, "of", p.Total)
			}
		}()
		res, err := strat.Run(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printStrategies(res, time.Since(t0))
		return
	}
	if *trainMeme {
		t0 := time.Now()
		if err := trainer.TrainMeme(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printModels(trainer, time.Since(t0))
		return
	}
	if *train {
		t0 := time.Now()
		if err := trainer.Train(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printModels(trainer, time.Since(t0))
		return
	}

	if *analyze != "" {
		rep, err := eng.Analyze(ctx, *analyze, engine.Options{UseLLM: !*noLLM})
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		printReport(rep)
		return
	}

	trk := &tracker.Tracker{Store: st, Market: mkt, Log: log}
	if err := trk.RefreshDrift(ctx); err != nil {
		log.Warn("drift monitor", "err", err)
	}
	eng.Monitor = trk
	scn := &engine.Scanner{Engine: eng}
	tokens := token.NewService()
	tokens.Registry = token.NewRegistry(cfg.CoinGeckoKey, st)
	tokens.Prices = closesFrom{mkt}
	jr := &journal.Service{Store: st, Market: mkt, Tokens: tokens}
	alerts := alert.New(cfg.TelegramToken, cfg.TelegramChat, cfg.AppURL, st, log)
	sch := &scheduler.Scheduler{
		Engine: eng, Scanner: scn, Store: st, Tracker: trk, Log: log,
		Times: cfg.RunTimes, TZ: cfg.Timezone, Scan: cfg.ScanOnSchedule,
		Universes: cfg.ScanUniverses, Horizon: cfg.ScanHorizon,
		Earnings: eng.Earnings, Pooled: trainer, Retrain: cfg.RetrainAfter,
		Alerts: alerts, Journal: jr, Tokens: tokens,
	}
	go sch.Start(ctx)
	// Background jobs: the earnings history download (resumes where it left
	// off), then the first model training if no models exist yet.
	go func() {
		if eng.Earnings != nil {
			_ = eng.Earnings.RefreshUpcoming(ctx, 45)
			if err := eng.Earnings.Backfill(ctx); err != nil {
				log.Warn("earnings backfill incomplete; it resumes on next start", "err", err)
			}
		}
		if trainer.Age() > cfg.RetrainAfter {
			if err := trainer.Train(ctx); err != nil {
				log.Warn("initial model training failed", "err", err)
			}
		}
	}()

	rs := research.New(cfg.AnthropicKey, cfg.LLMModel)
	if rs != nil {
		rs.Store, rs.Tokens, rs.CoinGeckoKey, rs.Budget, rs.Log = st, tokens, cfg.CoinGeckoKey, cfg.ResearchBudget, log
		rs.Assets = func(ctx context.Context, symbol string) (*engine.Report, error) { return st.LatestReport(ctx, symbol) }
	}
	accounts := &api.Accounts{Signup: cfg.Signup, SecureCookie: strings.HasPrefix(cfg.AppURL, "https://"), TrustProxy: cfg.TrustProxy,
		Mail:   mail.New(cfg.SMTP.Host, cfg.SMTP.Port, cfg.SMTP.User, cfg.SMTP.Pass, cfg.SMTP.From),
		AppURL: cfg.AppURL, DefaultWatch: cfg.DefaultWatch, MemberAIRuns: cfg.MemberAIRuns, MemberResearch: cfg.MemberResearch}
	// Until the first account exists, sign-up needs a one-time code that only
	// someone who can read this server's log has. That account becomes the
	// admin and takes over the existing watchlist and journal.
	if n, err := st.CountUsers(ctx); err == nil && n == 0 {
		code, _, err := auth.NewToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		accounts.SetBootstrap(code)
		log.Warn("no accounts yet: create the admin account at " + cfg.AppURL + "/signup?invite=" + code)
	}
	lrn := &learn.Service{Store: st, BaseCtx: ctx,
		Bars: func(ctx context.Context, sym string) (*market.Series, error) { return mkt.Series(ctx, sym, false) },
		Fees: func(ctx context.Context, uid int64, kind, sym string) float64 {
			return jr.Settings(ctx, uid).FeeRate(kind, sym)
		},
		// What the app said on a past day: the month-ahead forecast of a replay
		// (trained only on data up to that day, never saved).
		PastView: func(ctx context.Context, sym, day string) (learn.AppSaid, error) {
			rep, err := eng.Analyze(ctx, sym, engine.Options{AsOf: day, NoNews: true})
			if err != nil {
				return learn.AppSaid{}, err
			}
			for _, h := range rep.Horizons {
				if h.Key == "1m" {
					return learn.AppSaidFrom(h.Available, h.Signal, h.Status, h.PUp, h.BaseRate), nil
				}
			}
			return learn.AppSaidFrom(false, "", "", 0, 0), nil
		},
	}
	srv := &api.Server{Engine: eng, Scanner: scn, Store: st, Tracker: trk, Scheduler: sch, Pooled: trainer, Earnings: eng.Earnings, Tokens: tokens, Journal: jr, Learn: lrn, Alerts: alerts, Research: rs, Fund: fund, Strategies: strat, Accounts: accounts, Log: log, FrontendDir: cfg.FrontendDir, BaseCtx: ctx}
	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      8 * time.Minute, // LLM-assisted analyses can take a couple of minutes
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()
	log.Info("listening", "addr", cfg.Addr, "llm", eng.Analyst != nil, "schedule", cfg.RunTimes, "alerts", alerts.Enabled(), "tz", cfg.Timezone.String())
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server", "err", err)
		os.Exit(1)
	}
}

func printReport(r *engine.Report) {
	fmt.Printf("\n%s (%s) %.6g %s — as of %s via %s, %d bars since %s, %dms\n", r.Name, r.Symbol, r.Price, r.Currency, r.AsOf, r.Provider, r.HistoryBars, r.HistoryFrom, r.ElapsedMs)
	fmt.Println(r.Headline)
	for _, h := range r.Horizons {
		fmt.Printf("\n== %s ==\n", h.Label)
		if !h.Available {
			fmt.Println("  unavailable:", h.Reason)
			continue
		}
		for _, line := range h.Why {
			fmt.Println("  •", line)
		}
	}
	fmt.Printf("\nNews: %d articles scored by %s\n", len(r.News.Articles), r.News.ScoredBy)
	for _, w := range r.Warnings {
		fmt.Println("⚠", w)
	}
	fmt.Println("\n" + r.Narrative)
}

func printModels(t *pooled.Trainer, took time.Duration) {
	fmt.Printf("\nTrained in %s\n\nCROSS-ASSET DIRECTION MODELS (out-of-sample)\n", took.Round(time.Second))
	fmt.Printf("%-12s %-3s %6s %8s %9s %8s %7s %7s %6s  %s\n", "pool", "h", "assets", "hit", "naive", "brierSk", "auc", "p", "n_eff", "test")
	for _, d := range t.DirSummaries() {
		fmt.Printf("%-12s %-3s %6d %7.1f%% %8.1f%% %+7.2f%% %7.3f %7.3f %6d  %s..%s\n", d.Pool, d.Horizon, d.Assets, 100*d.Accuracy, 100*d.BaseAcc, 100*d.BrierSkill, d.AUC, d.PValue, d.NEff, d.TestFrom, d.TestTo)
	}
	fmt.Printf("\nRANKING MODELS (out-of-sample, top fifth vs equal-weight universe, after costs)\n")
	fmt.Printf("%-12s %-3s %6s %7s %6s %7s %9s %9s %9s  %s\n", "universe", "h", "assets", "IC", "t", "hit", "topExcess", "CAGR", "benchCAGR", "status")
	for _, m := range t.Rankings() {
		v := m.Validation
		fmt.Printf("%-12s %-3s %6d %7.3f %6.2f %6.1f%% %+8.2f%% %+8.1f%% %+8.1f%%  %s\n", m.Universe, m.Horizon, len(m.Assets), v.MeanIC, v.ICTStat, 100*v.HitRate, 100*v.TopExcess, 100*v.Strategy.CAGR, 100*v.Strategy.BenchCAGR, v.Status)
		for i, it := range m.Live {
			if i >= 5 {
				break
			}
			fmt.Printf("      #%d %-10s P(beat median)=%.1f%%\n", it.Rank, it.Symbol, 100*it.POutperform)
		}
	}
	if p := t.Progress(); len(p.Failed) > 0 {
		fmt.Printf("\nCould not load: %v\n", p.Failed)
	}
}

// closesFrom adapts the market service to token.PriceSource.
type closesFrom struct{ m *market.Service }

func (c closesFrom) DailyCloses(ctx context.Context, symbol string) ([]string, []float64, error) {
	ser, err := c.m.Series(ctx, symbol, false)
	if err != nil {
		return nil, nil, err
	}
	d := make([]string, len(ser.Bars))
	v := make([]float64, len(ser.Bars))
	for i, b := range ser.Bars {
		d[i], v[i] = b.Date, b.Close
	}
	return d, v, nil
}

func printStrategies(r *strategies.Results, took time.Duration) {
	fmt.Printf("\nStrategy tests done in %s (%d stocks with prices, %d with SEC data, %d missing)\n", took.Round(time.Second), r.PriceStocks, r.FundStocks, r.Missing)
	if v := r.Value; v != nil {
		fmt.Printf("\nSTRONGER BUSINESS, LOWER PRICE  %s..%s, %d stocks, %d months: %s\n", v.From, v.To, v.Stocks, v.Months, v.Status)
		for _, g := range []string{"signal", "dip_only", "all"} {
			s := v.Groups[g]
			fmt.Printf("  %-9s n=%6d  excess 6m %+6.2f%%  excess 12m %+6.2f%%  beat universe 12m %4.1f%%  fell 20%%+ in 12m %4.1f%%\n", g, s.N, 100*s.Excess6m, 100*s.Excess12m, 100*s.Hit12m, 100*s.Trap12m)
		}
		for name, p := range map[string]fundamentals.Portfolio{"pattern portfolio": v.Portfolio, "dip-only portfolio": v.DipOnly} {
			fmt.Printf("  %-18s months %3d  mean excess %+5.2f%%/mo  t %5.2f  hit %4.1f%%  CAGR %+5.1f%% vs %+5.1f%%  maxDD %5.1f%% vs %5.1f%%\n",
				name, p.Months, 100*p.MeanExcess, p.TStat, 100*p.HitRate, 100*p.CAGR, 100*p.BenchCAGR, 100*p.MaxDrawdown, 100*p.BenchMaxDD)
		}
	}
	if l := r.Levels; l != nil {
		fmt.Printf("\nFIBONACCI LEVELS  %d stocks, %s..%s, break-even bounce rate %.1f%%: %s (z=%.2f)\n", l.Stocks, l.From, l.To, 100*l.BreakEven, l.Status, l.Z)
		for _, s := range l.Levels {
			fib := ""
			if s.Fib {
				fib = "fib"
			}
			fmt.Printf("  %.3f %-3s touches %5d  bounce %5.1f%%  expectancy %+5.2fR  13w %+5.2f%%  26w %+5.2f%%\n", s.Ratio, fib, s.Touches, 100*s.BounceRate, s.Expectancy, 100*s.Avg13w, 100*s.Avg26w)
		}
	}
	fmt.Printf("\nSCREEN TODAY: %d of %d current members match\n", len(r.Screen), r.Evaluated)
	for i, row := range r.Screen {
		if i >= 25 {
			break
		}
		c := row.Cmp
		fmt.Printf("  %-6s %-32.32s %+5.0f%% from %s high  revenue %+5.0f%%  op income %+5.0f%%\n", row.Symbol, row.Name, 100*c.FromPeak, c.PeakDate, 100*c.RevenueChange, 100*c.OpIncomeChange)
	}
}
