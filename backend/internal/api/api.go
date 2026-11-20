// Package api exposes the engine over a JSON HTTP API and serves the built
// React frontend.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/alert"
	"github.com/sodiq/financial-market/backend/internal/earnings"
	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/fundamentals"
	"github.com/sodiq/financial-market/backend/internal/journal"
	"github.com/sodiq/financial-market/backend/internal/learn"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/pooled"
	"github.com/sodiq/financial-market/backend/internal/research"
	"github.com/sodiq/financial-market/backend/internal/scheduler"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/strategies"
	"github.com/sodiq/financial-market/backend/internal/token"
	"github.com/sodiq/financial-market/backend/internal/tracker"
)

type Server struct {
	Engine      *engine.Engine
	Scanner     *engine.Scanner
	Store       *store.Store
	Tracker     *tracker.Tracker
	Scheduler   *scheduler.Scheduler
	Pooled      *pooled.Trainer
	Earnings    *earnings.Service
	Tokens      *token.Service
	Journal     *journal.Service
	Learn       *learn.Service
	Alerts      *alert.Notifier
	Research    *research.Service // nil without an Anthropic key
	Fund        *fundamentals.Service
	Strategies  *strategies.Service
	Log         *slog.Logger
	Accounts    *Accounts
	FrontendDir string
	BaseCtx     context.Context
}

func (s *Server) Handler() http.Handler {
	s.Accounts.init()
	mux := http.NewServeMux()
	// Accounts (the first group works signed out; see publicAPI).
	mux.HandleFunc("GET /api/auth/config", s.authConfig)
	mux.HandleFunc("GET /api/auth/invite", s.inviteCheck)
	mux.HandleFunc("POST /api/auth/signup", s.signup)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("POST /api/auth/forgot", s.forgot)
	mux.HandleFunc("POST /api/auth/reset", s.reset)
	mux.HandleFunc("GET /api/auth/me", s.me)
	mux.HandleFunc("PUT /api/account", s.updateAccount)
	mux.HandleFunc("PUT /api/account/password", s.changePassword)
	mux.HandleFunc("GET /api/account/sessions", s.sessions)
	mux.HandleFunc("DELETE /api/account/sessions/{id}", s.revokeSession)
	mux.HandleFunc("GET /api/account/export", s.exportData)
	mux.HandleFunc("POST /api/account/delete", s.deleteAccount)
	mux.HandleFunc("GET /api/profile", s.getProfile)
	mux.HandleFunc("PUT /api/profile", s.saveProfile)
	mux.HandleFunc("GET /api/profile/quiz", s.quiz)
	mux.HandleFunc("PUT /api/account/prefs", s.savePrefs)
	mux.HandleFunc("GET /api/today", s.today)
	mux.HandleFunc("POST /api/today/step", s.todayStep)
	mux.HandleFunc("GET /api/admin/users", s.admin(s.adminUsers))
	mux.HandleFunc("PUT /api/admin/users/{id}", s.admin(s.adminUpdateUser))
	mux.HandleFunc("DELETE /api/admin/users/{id}", s.admin(s.adminDeleteUser))
	mux.HandleFunc("POST /api/admin/users/{id}/reset-link", s.admin(s.adminResetLink))
	mux.HandleFunc("GET /api/admin/invites", s.admin(s.adminInvites))
	mux.HandleFunc("POST /api/admin/invites", s.admin(s.adminCreateInvite))
	mux.HandleFunc("DELETE /api/admin/invites/{id}", s.admin(s.adminRevokeInvite))

	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("GET /api/search", s.search)
	mux.HandleFunc("GET /api/report/{symbol}", s.latestReport)
	mux.HandleFunc("POST /api/report/{symbol}/run", s.runReport)
	mux.HandleFunc("GET /api/report/{symbol}/history", s.reportHistory)
	mux.HandleFunc("GET /api/reports/{id}", s.reportByID)
	mux.HandleFunc("GET /api/watchlist", s.watchlist)
	mux.HandleFunc("POST /api/watchlist", s.addWatch)
	mux.HandleFunc("GET /api/watchlist/tokens", s.watchTokens)
	mux.HandleFunc("DELETE /api/watchlist/{symbol}", s.removeWatch)
	mux.HandleFunc("GET /api/scan/universes", s.universes)
	mux.HandleFunc("POST /api/scan", s.admin(s.startScan))
	mux.HandleFunc("GET /api/scan/progress", s.scanProgress)
	mux.HandleFunc("GET /api/scan/latest", s.latestScan)
	mux.HandleFunc("GET /api/track-record", s.trackRecord)
	mux.HandleFunc("POST /api/track-record/resolve", s.admin(s.resolve))
	mux.HandleFunc("POST /api/run-all", s.admin(s.runAll))
	mux.HandleFunc("GET /api/models", s.models)
	mux.HandleFunc("GET /api/llm/usage", s.admin(s.llmUsage))
	mux.HandleFunc("POST /api/replay/{symbol}", s.replay)
	mux.HandleFunc("GET /api/token/{address}", s.tokenReport)
	mux.HandleFunc("GET /api/token-search", s.tokenSearch)
	mux.HandleFunc("GET /api/token/{address}/ranks", s.tokenRanks)
	mux.HandleFunc("GET /api/journal", s.journalSummary)
	mux.HandleFunc("POST /api/journal", s.journalOpen)
	mux.HandleFunc("POST /api/journal/{id}/close", s.journalClose)
	mux.HandleFunc("PUT /api/journal/{id}/plan", s.journalPlan)
	mux.HandleFunc("DELETE /api/journal/{id}", s.journalDelete)
	mux.HandleFunc("PUT /api/journal/settings", s.journalSettings)
	mux.HandleFunc("POST /api/learn/event", s.learnEvent)
	mux.HandleFunc("GET /api/learn/suggestion", s.learnSuggestion)
	mux.HandleFunc("POST /api/learn/suggestion/{id}", s.learnAnswer)
	mux.HandleFunc("GET /api/learn", s.learnOverview)
	mux.HandleFunc("PUT /api/learn/prefs", s.learnPrefs)
	mux.HandleFunc("POST /api/learn/forget", s.learnForget)
	mux.HandleFunc("GET /api/learn/export", s.learnExport)
	mux.HandleFunc("GET /api/learn/reminders", s.learnReminders)
	mux.HandleFunc("POST /api/learn/reminders", s.learnReminderAnswer)
	mux.HandleFunc("GET /api/practice", s.practice)
	mux.HandleFunc("POST /api/practice/start", s.practiceStart)
	mux.HandleFunc("POST /api/practice/{id}", s.practiceDecide)
	mux.HandleFunc("POST /api/alerts/test", s.admin(s.alertTest))
	mux.HandleFunc("GET /api/quote", s.quote)
	mux.HandleFunc("GET /api/fundamentals/{symbol}", s.businessVsPrice)
	mux.HandleFunc("GET /api/levels/{symbol}", s.chartLevels)
	mux.HandleFunc("GET /api/strategies", s.strategyResults)
	mux.HandleFunc("POST /api/strategies/run", s.admin(s.strategyRun))
	mux.HandleFunc("GET /api/research", s.researchOverview)
	mux.HandleFunc("GET /api/coin-search", s.coinSearch)
	mux.HandleFunc("GET /api/research/report/{id}", s.researchReport)
	mux.HandleFunc("GET /api/research/{subject}", s.researchSubject)
	mux.HandleFunc("POST /api/research/{subject}", s.researchStart)
	mux.HandleFunc("POST /api/replay/{symbol}/series", s.replaySeries)
	mux.HandleFunc("POST /api/models/train", s.admin(s.trainModels))
	mux.HandleFunc("GET /api/rankings", s.rankings)
	mux.HandleFunc("POST /api/earnings/backfill", s.admin(s.backfill))
	if st, err := os.Stat(s.FrontendDir); err == nil && st.IsDir() {
		mux.Handle("/", spa(s.FrontendDir))
	}
	return s.logging(securityHeaders(cors(s.authn(mux))))
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	model := ""
	if s.Engine.Analyst != nil {
		model = s.Engine.Analyst.Model()
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"admin":     user(r).Admin(),
		"llm":       s.Engine.Analyst != nil,
		"llmModel":  model,
		"alerts":    s.Alerts.Enabled(),
		"scheduler": s.Scheduler.Status(),
		"scan":      s.Scanner.Progress(),
		"training":  s.Pooled.Progress(),
		"earnings":  s.Earnings.Progress(),
		"time":      time.Now(),
	})
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, 200, []any{})
		return
	}
	res, err := s.Engine.Market.Search(r.Context(), q)
	if err != nil {
		// Search is a convenience; let the user type a raw symbol instead.
		res = nil
	}
	writeJSON(w, 200, market.VerifyMemeResults(q, res))
}

func (s *Server) latestReport(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Store.LatestReport(r.Context(), r.PathValue("symbol"))
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "no report yet")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, s.withPlain(r, rep))
}

func (s *Server) reportByID(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rep, err := s.Store.Report(r.Context(), id)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, s.withPlain(r, rep))
}

func (s *Server) runReport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	opt := engine.Options{Force: q.Get("force") == "1", UseLLM: q.Get("llm") != "0"}
	if !s.heavy(w, r) {
		return
	}
	// Members get a daily number of AI-assisted refreshes; after that the
	// analysis runs on the built-in news scoring.
	if opt.UseLLM && s.Engine.Analyst != nil {
		if used, limit := s.allowance(r, "ai_report"); limit >= 0 && used >= limit {
			opt.UseLLM = false
			w.Header().Set("X-AI-Allowance", "used")
		}
	}
	rep, err := s.Engine.Analyze(r.Context(), r.PathValue("symbol"), opt)
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	if opt.UseLLM && rep.UsedLLM && !user(r).Admin() {
		_ = s.Store.CountAction(r.Context(), uid(r), "ai_report")
	}
	if err := s.Store.SaveReport(r.Context(), rep); err != nil {
		s.Log.Error("save report", "err", err)
	}
	writeJSON(w, 200, s.withPlain(r, rep))
}

func (s *Server) reportHistory(w http.ResponseWriter, r *http.Request) {
	h, err := s.Store.ReportHistory(r.Context(), r.PathValue("symbol"), 60)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, h)
}

type watchItem struct {
	Symbol      string         `json:"symbol"`
	Name        string         `json:"name,omitempty"`
	Price       float64        `json:"price,omitempty"`
	Change1d    float64        `json:"change1d,omitempty"`
	Currency    string         `json:"currency,omitempty"`
	AsOf        string         `json:"asOf,omitempty"`
	GeneratedAt *time.Time     `json:"generatedAt,omitempty"`
	ReportID    int64          `json:"reportId,omitempty"`
	Horizons    []watchHorizon `json:"horizons"`
	Warnings    int            `json:"warnings"`
}

type watchHorizon struct {
	Key        string  `json:"key"`
	Signal     string  `json:"signal"`
	PUp        float64 `json:"pUp"`
	Status     string  `json:"status"`
	Conviction int     `json:"conviction"`
	Available  bool    `json:"available"`
}

func (s *Server) watchlist(w http.ResponseWriter, r *http.Request) {
	syms, err := s.Store.Watchlist(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []watchItem{}
	for _, sym := range syms {
		it := watchItem{Symbol: sym, Horizons: []watchHorizon{}}
		if rep, err := s.Store.LatestReport(r.Context(), sym); err == nil {
			g := rep.GeneratedAt
			it.Name, it.Price, it.Change1d, it.Currency, it.AsOf, it.GeneratedAt, it.ReportID = rep.Name, rep.Price, rep.Change1d, rep.Currency, rep.AsOf, &g, rep.ID
			it.Warnings = len(rep.Warnings)
			for _, h := range rep.Horizons {
				it.Horizons = append(it.Horizons, watchHorizon{h.Key, h.Signal, h.PUp, h.Status, h.Conviction, h.Available})
			}
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

func (s *Server) addWatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Symbol string `json:"symbol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Symbol) == "" {
		writeErr(w, 400, "symbol required")
		return
	}
	sym := strings.TrimSpace(body.Symbol)
	if token.LooksLikeAddress(sym) {
		// A DEX token: confirm it exists and store its canonical address.
		q, err := s.Tokens.Quote(r.Context(), sym)
		if err != nil {
			writeErr(w, 422, err.Error())
			return
		}
		if err := s.Store.AddWatchToken(r.Context(), uid(r), q.Address, q.Chain); err != nil {
			writeWatchErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]string{"ok": "added", "kind": "token", "address": q.Address})
		return
	}
	if err := s.Store.AddWatch(r.Context(), uid(r), sym); err != nil {
		writeWatchErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "added", "kind": "asset"})
}

func writeWatchErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrWatchFull) {
		writeErr(w, 422, err.Error())
		return
	}
	writeErr(w, 500, err.Error())
}

func (s *Server) removeWatch(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.RemoveWatch(r.Context(), uid(r), r.PathValue("symbol")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"ok": "removed"})
}

func (s *Server) universes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, engine.Universes)
}

func (s *Server) startScan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Universes []string `json:"universes"`
		Symbols   []string `json:"symbols"`
		Horizon   string   `json:"horizon"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Horizon == "" {
		body.Horizon = "1w"
	}
	if len(body.Universes) == 0 && len(body.Symbols) == 0 {
		body.Universes = []string{"us_large_caps", "crypto"}
	}
	if s.Scanner.Progress().Running {
		writeErr(w, 409, "a scan is already running")
		return
	}
	go func() {
		res, err := s.Scanner.Run(s.BaseCtx, body.Universes, body.Symbols, body.Horizon, func(rep *engine.Report) {
			_ = s.Store.SaveReport(s.BaseCtx, rep)
		})
		if err != nil {
			s.Log.Warn("scan failed", "err", err)
			return
		}
		if err := s.Store.SaveScan(s.BaseCtx, res); err != nil {
			s.Log.Error("save scan", "err", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	writeJSON(w, 202, s.Scanner.Progress())
}

func (s *Server) scanProgress(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Scanner.Progress())
}

func (s *Server) latestScan(w http.ResponseWriter, r *http.Request) {
	res, err := s.Store.LatestScan(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "no scan yet")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) trackRecord(w http.ResponseWriter, r *http.Request) {
	rec, err := s.Tracker.Summary(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Server) resolve(w http.ResponseWriter, r *http.Request) {
	n, err := s.Tracker.Resolve(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]int{"resolved": n})
}

func (s *Server) runAll(w http.ResponseWriter, r *http.Request) {
	scan := r.URL.Query().Get("scan") == "1"
	go s.Scheduler.RunNow(s.BaseCtx, scan)
	writeJSON(w, 202, map[string]string{"ok": "started"})
}

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"direction": s.Pooled.DirSummaries(),
		"training":  s.Pooled.Progress(),
		"earnings":  s.Earnings.Progress(),
	})
}

// replay re-runs the analysis as of a past date. Replays are never saved, so
// they can't contaminate the live track record.
func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	if !s.heavy(w, r) {
		return
	}
	date := r.URL.Query().Get("date")
	if _, err := time.Parse("2006-01-02", date); err != nil {
		writeErr(w, 400, "date must be YYYY-MM-DD")
		return
	}
	rep, err := s.Engine.Analyze(r.Context(), r.PathValue("symbol"), engine.Options{AsOf: date})
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, s.withPlain(r, rep))
}

// replaySeries replays a symbol every `every` calendar days between from and
// to (at most 40 dates) and summarises how the calls turned out.
func (s *Server) replaySeries(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, err1 := time.Parse("2006-01-02", q.Get("from"))
	to, err2 := time.Parse("2006-01-02", q.Get("to"))
	every, _ := strconv.Atoi(q.Get("every"))
	if err1 != nil || err2 != nil || !from.Before(to) {
		writeErr(w, 400, "from and to must be YYYY-MM-DD with from < to")
		return
	}
	if every < 1 {
		every = 30
	}
	var dates []string
	for d := from; !d.After(to); d = d.AddDate(0, 0, every) {
		dates = append(dates, d.Format("2006-01-02"))
	}
	if len(dates) > 40 {
		writeErr(w, 400, fmt.Sprintf("%d dates requested; the maximum is 40 (increase the step)", len(dates)))
		return
	}
	if !s.heavy(w, r) {
		return
	}
	writeJSON(w, 200, s.Engine.ReplayMany(r.Context(), r.PathValue("symbol"), dates))
}

// tokenReport returns the safety report for a Solana, Ethereum or Base
// token. Reports are reused for 10 minutes unless refresh=1.
func (s *Server) tokenReport(w http.ResponseWriter, r *http.Request) {
	addr := r.PathValue("address")
	if r.URL.Query().Get("refresh") != "1" {
		if rep, err := s.Store.LatestTokenReport(r.Context(), addr, 10*time.Minute); err == nil {
			writeJSON(w, 200, rep)
			return
		}
	}
	if !s.heavy(w, r) {
		return
	}
	rep, err := s.Tokens.Report(r.Context(), addr)
	if err != nil {
		code := 502
		if errors.Is(err, token.ErrNotFound) || !token.LooksLikeAddress(addr) {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	if err := s.Store.SaveTokenReport(r.Context(), rep); err != nil {
		s.Log.Warn("save token report", "err", err)
	}
	writeJSON(w, 200, rep)
}

func (s *Server) tokenSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || token.LooksLikeAddress(q) {
		writeJSON(w, 200, []any{})
		return
	}
	res, err := s.Tokens.Search(r.Context(), q)
	if err != nil {
		writeJSON(w, 200, []any{})
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) llmUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.UsageSummary(r.Context())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, u)
}

func (s *Server) trainModels(w http.ResponseWriter, r *http.Request) {
	if s.Pooled.Progress().Running {
		writeErr(w, 409, "training already running")
		return
	}
	go func() {
		if err := s.Pooled.Train(s.BaseCtx); err != nil {
			s.Log.Warn("training failed", "err", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	writeJSON(w, 202, s.Pooled.Progress())
}

func (s *Server) rankings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Pooled.Rankings())
}

func (s *Server) backfill(w http.ResponseWriter, r *http.Request) {
	go func() {
		if err := s.Earnings.Backfill(s.BaseCtx); err != nil {
			s.Log.Warn("earnings backfill", "err", err)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	writeJSON(w, 202, s.Earnings.Progress())
}

// ------------------------------------------------------------------ helpers

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	b, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"encode: ` + strings.ReplaceAll(err.Error(), `"`, `'`) + `"}`))
		return
	}
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if strings.HasPrefix(origin, "http://localhost") || strings.HasPrefix(origin, "http://127.0.0.1") {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/scan/progress" && r.URL.Path != "/api/status" {
			s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.code, "ms", time.Since(t).Milliseconds())
		}
	})
}

// statusWriter remembers the response code for the request log.
type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func spa(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func (s *Server) alertTest(w http.ResponseWriter, r *http.Request) {
	if err := s.Alerts.Send(r.Context(), "✅ Hindcast alerts are working."); err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// watchTokens lists the DEX tokens on the watchlist with their latest check.
func (s *Server) watchTokens(w http.ResponseWriter, r *http.Request) {
	ts, err := s.Store.WatchTokens(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type item struct {
		Address string       `json:"address"`
		Chain   string       `json:"chain"`
		Brief   *token.Brief `json:"brief,omitempty"`
	}
	out := []item{}
	for _, t := range ts {
		it := item{Address: t.Address, Chain: t.Chain}
		if rep, err := s.Store.LatestTokenReport(r.Context(), t.Address, 365*24*time.Hour); err == nil {
			b := rep.Brief()
			it.Brief = &b
		}
		out = append(out, it)
	}
	writeJSON(w, 200, out)
}

// tokenRanks returns a token's place in the meme-coin ranking, if it is one
// of the ranked coins.
func (s *Server) tokenRanks(w http.ResponseWriter, r *http.Request) {
	out := []pooled.SymbolRank{}
	if s.Tokens.Registry != nil {
		if l, err := s.Tokens.Registry.ByAddressNoRank(r.Context(), r.PathValue("address")); err == nil && l != nil {
			if m, ok := market.MemeByID(l.ID); ok {
				for _, rk := range s.Pooled.RanksFor(m.Ticker) {
					if rk.Universe == "meme" {
						out = append(out, rk)
					}
				}
			}
		}
	}
	writeJSON(w, 200, out)
}
