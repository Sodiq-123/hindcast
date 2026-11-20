// Package scheduler runs the watchlist analysis, prediction resolution and
// opportunity scan at fixed local times each day.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/alert"
	"github.com/sodiq/financial-market/backend/internal/earnings"
	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/journal"
	"github.com/sodiq/financial-market/backend/internal/pooled"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
	"github.com/sodiq/financial-market/backend/internal/tracker"
)

type Scheduler struct {
	Engine    *engine.Engine
	Scanner   *engine.Scanner
	Store     *store.Store
	Tracker   *tracker.Tracker
	Log       *slog.Logger
	Times     []string
	TZ        *time.Location
	Scan      bool
	Universes []string
	Horizon   string
	Earnings  *earnings.Service // optional
	Pooled    *pooled.Trainer   // optional
	Retrain   time.Duration     // retrain pooled models when older than this
	Alerts    *alert.Notifier   // optional (nil-safe)
	Journal   *journal.Service  // optional: checked for stops and targets
	Tokens    *token.Service    // optional: watched DEX tokens are re-checked each run

	// Test hooks (nil or zero in normal use).
	clock func() time.Time
	tick  time.Duration
	run   func(ctx context.Context, withScan bool)

	mu      sync.Mutex
	running bool
	LastRun time.Time
	LastErr string
}

type Status struct {
	Running bool      `json:"running"`
	LastRun time.Time `json:"lastRun"`
	LastErr string    `json:"lastErr,omitempty"`
	NextRun time.Time `json:"nextRun"`
	Times   []string  `json:"times"`
}

func (s *Scheduler) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{Running: s.running, LastRun: s.LastRun, LastErr: s.LastErr, NextRun: s.next(time.Now()), Times: s.Times}
}

func (s *Scheduler) next(from time.Time) time.Time {
	from = from.In(s.TZ)
	var best time.Time
	for _, hm := range s.Times {
		t, err := time.ParseInLocation("15:04", hm, s.TZ)
		if err != nil {
			continue
		}
		c := time.Date(from.Year(), from.Month(), from.Day(), t.Hour(), t.Minute(), 0, 0, s.TZ)
		if !c.After(from) {
			c = c.AddDate(0, 0, 1)
		}
		if best.IsZero() || c.Before(best) {
			best = c
		}
	}
	return best
}

// Start blocks until ctx is cancelled.
//
// It checks the wall clock every tick instead of sleeping until the next run:
// on macOS a sleeping computer pauses Go's timers, so a timer set for 07:30
// at 22:30 the night before fires hours late (or not that day at all). A run
// missed while the computer slept happens as soon as it wakes.
func (s *Scheduler) Start(ctx context.Context) {
	now, tick, run := s.clock, s.tick, s.run
	if now == nil {
		now = time.Now
	}
	if tick <= 0 {
		tick = 30 * time.Second
	}
	if run == nil {
		run = s.RunNow
	}
	n := s.next(now())
	if n.IsZero() {
		s.Log.Warn("scheduler: no valid RUN_TIMES configured")
		return
	}
	s.Log.Info("scheduler: next run", "at", n.Format(time.RFC1123))
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			at := now()
			if at.Before(n) {
				continue
			}
			if late := at.Sub(n); late > 2*time.Minute {
				s.Log.Info("scheduler: catching up a run missed while the computer slept", "due", n.Format(time.RFC1123), "late", late.Round(time.Minute).String())
			}
			run(ctx, s.Scan)
			n = s.next(now())
			s.Log.Info("scheduler: next run", "at", n.Format(time.RFC1123))
		}
	}
}

// RunNow performs a full cycle. It is safe to call concurrently; a second
// call while one is running returns immediately.
func (s *Scheduler) RunNow(ctx context.Context, withScan bool) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.LastRun = time.Now()
		s.mu.Unlock()
	}()

	var lastErr string
	if n, err := s.Tracker.Resolve(ctx); err != nil {
		lastErr = err.Error()
	} else {
		s.Log.Info("resolved predictions", "count", n)
		s.Alerts.Drift(ctx, s.Tracker.Drifting())
	}
	if s.Earnings != nil {
		if err := s.Earnings.RefreshUpcoming(ctx, 45); err != nil {
			s.Log.Warn("earnings refresh failed", "err", err)
		}
	}
	// Retrain cross-asset models before analysing, so today's analyses use them.
	if s.Pooled != nil && s.Pooled.Age() > s.Retrain {
		if err := s.Pooled.Train(ctx); err != nil {
			s.Log.Warn("pooled training failed", "err", err)
			lastErr = "model training: " + err.Error()
		}
	}
	// Everyone's watchlists are analysed; Telegram alerts (configured by the
	// server's owner) only cover the owner's own watchlist and journal.
	syms, err := s.Store.AllWatched(ctx)
	if err != nil {
		lastErr = err.Error()
	}
	owner, ownerErr := s.Store.OwnerID(ctx)
	ownerSyms := map[string]bool{}
	if ownerErr == nil {
		mine, _ := s.Store.Watchlist(ctx, owner)
		for _, sym := range mine {
			ownerSyms[sym] = true
		}
		toks, _ := s.Store.WatchTokens(ctx, owner)
		for _, t := range toks {
			ownerSyms[t.Address] = true
		}
	}
	// Nobody waits on the scheduled run, so its Claude calls go through the
	// Batch API at half price (with a synchronous fallback inside).
	for _, it := range s.Engine.AnalyzeBatch(ctx, syms, engine.Options{Force: true, UseLLM: true}) {
		if it.Err != nil {
			s.Log.Warn("analysis failed", "symbol", it.Symbol, "err", it.Err)
			lastErr = it.Symbol + ": " + it.Err.Error()
			continue
		}
		prev, _ := s.Store.LatestReport(ctx, it.Symbol)
		if err := s.Store.SaveReport(ctx, it.Report); err != nil {
			lastErr = err.Error()
		}
		if ownerSyms[it.Symbol] {
			s.Alerts.SignalChanges(ctx, prev, it.Report)
		}
		s.Log.Info("analysed", "symbol", it.Symbol, "headline", it.Report.Headline, "ms", it.Report.ElapsedMs)
	}
	if withScan {
		res, err := s.Scanner.Run(ctx, s.Universes, syms, s.Horizon, nil)
		if err == nil {
			_ = s.Store.SaveScan(ctx, res)
		} else if err != engine.ErrScanRunning {
			lastErr = err.Error()
		}
	}
	if s.Tokens != nil {
		ts, err := s.Store.AllWatchTokens(ctx)
		if err != nil {
			lastErr = err.Error()
		}
		for _, t := range ts {
			prev, _ := s.Store.LatestTokenReport(ctx, t.Address, 365*24*time.Hour)
			rep, err := s.Tokens.Report(ctx, t.Address)
			if err != nil {
				s.Log.Warn("token re-check failed", "address", t.Address, "err", err)
				continue
			}
			if err := s.Store.SaveTokenReport(ctx, rep); err != nil {
				lastErr = err.Error()
			}
			if ownerSyms[t.Address] {
				s.Alerts.TokenChanges(ctx, prev, rep)
			}
			s.Log.Info("token re-checked", "symbol", rep.Symbol, "verdict", rep.Verdict, "score", rep.RiskScore)
		}
	}
	if s.Journal != nil && s.Alerts.Enabled() && ownerErr == nil {
		if sum, err := s.Journal.Summary(ctx, owner); err == nil {
			s.Alerts.Journal(ctx, sum)
		}
	}
	if err := s.Store.PurgeExpired(ctx); err != nil {
		s.Log.Warn("purging expired sessions", "err", err)
	}
	s.mu.Lock()
	s.LastErr = lastErr
	s.mu.Unlock()
}
