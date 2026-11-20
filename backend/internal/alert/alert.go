// Package alert sends Telegram messages when something needs attention:
// a trade signal appears, flips or disappears on the watchlist, live
// monitoring demotes a horizon, a journal position hits its stop or target,
// or the losing-streak circuit-breaker turns on.
package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/engine"
	"github.com/sodiq/financial-market/backend/internal/journal"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
	"github.com/sodiq/financial-market/backend/internal/tracker"
)

type Notifier struct {
	Token  string
	Chat   string
	AppURL string
	Store  *store.Store
	Log    *slog.Logger
	http   *http.Client
}

// New returns nil when Telegram isn't configured; every method is nil-safe.
func New(token, chat, appURL string, st *store.Store, log *slog.Logger) *Notifier {
	if token == "" || chat == "" {
		return nil
	}
	return &Notifier{Token: token, Chat: chat, AppURL: appURL, Store: st, Log: log, http: &http.Client{Timeout: 15 * time.Second}}
}

func (n *Notifier) Enabled() bool { return n != nil }

// Send posts one HTML-formatted message.
func (n *Notifier) Send(ctx context.Context, text string) error {
	if n == nil {
		return fmt.Errorf("telegram alerts are not configured (set TELEGRAM_BOT_TOKEN and TELEGRAM_CHAT_ID)")
	}
	body, _ := json.Marshal(map[string]any{"chat_id": n.Chat, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+n.Token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.http.Do(req)
	if err != nil {
		// The URL contains the bot token; don't let it reach the logs.
		return fmt.Errorf("telegram: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Description string `json:"description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("telegram: %s (%s)", resp.Status, e.Description)
	}
	return nil
}

// once sends a message unless the same key was sent within `within`.
func (n *Notifier) once(ctx context.Context, key string, within time.Duration, text string) {
	if n == nil {
		return
	}
	if fresh, err := n.Store.MarkAlert(ctx, key, within); err != nil || !fresh {
		return
	}
	if err := n.Send(ctx, text); err != nil {
		n.Log.Warn("alert not sent", "key", key, "err", err)
	}
}

func actionable(signal string) bool {
	return strings.Contains(signal, "BUY") || strings.Contains(signal, "SELL")
}

// SignalChanges alerts when a horizon gains, loses or flips a trade signal
// between the previous saved report and the new one.
func (n *Notifier) SignalChanges(ctx context.Context, prev, cur *engine.Report) {
	if n == nil || cur == nil {
		return
	}
	old := map[string]string{}
	if prev != nil {
		for _, h := range prev.Horizons {
			old[h.Key] = h.Signal
		}
	}
	var lines []string
	for _, h := range cur.Horizons {
		was, now := old[h.Key], h.Signal
		if !h.Available || was == now || !(actionable(was) || actionable(now)) {
			continue
		}
		if was == "" {
			was = "none"
		}
		lines = append(lines, fmt.Sprintf("• %s: %s → <b>%s</b> (P(up) %.0f%%, %s)", h.Label, html.EscapeString(was), html.EscapeString(now), 100*h.PUp, h.Status))
	}
	if len(lines) == 0 {
		return
	}
	text := fmt.Sprintf("📈 <b>%s</b> signal change at %.6g\n%s\n%s\n<a href=\"%s/asset/%s\">Open the full analysis</a>",
		html.EscapeString(cur.Symbol), cur.Price, strings.Join(lines, "\n"), html.EscapeString(cur.Headline), n.AppURL, cur.Symbol)
	n.once(ctx, "signal|"+cur.Symbol+"|"+cur.AsOf+"|"+strings.Join(lines, ""), 7*24*time.Hour, text)
}

// Drift alerts once a month per newly drifting symbol and horizon.
func (n *Notifier) Drift(ctx context.Context, rows []tracker.DriftRow) {
	for _, d := range rows {
		n.once(ctx, "drift|"+d.Symbol+"|"+d.Horizon, 30*24*time.Hour, fmt.Sprintf(
			"⚠️ <b>%s %s</b>: the last %d live forecasts scored worse than the base rate (Brier %.3f vs %.3f). Its signals are shown as NO EDGE until the live record recovers.",
			html.EscapeString(d.Symbol), d.Horizon, d.N, d.Brier, d.BaseBrier))
	}
}

// Journal alerts on stops, targets and the circuit-breaker.
func (n *Notifier) Journal(ctx context.Context, sum *journal.Summary) {
	if n == nil || sum == nil {
		return
	}
	for _, r := range sum.Open {
		name := r.Symbol
		if r.Name != "" {
			name = r.Name
		}
		id := fmt.Sprint(r.ID)
		switch {
		case r.StopHit:
			n.once(ctx, "stop|"+id, 3*24*time.Hour, fmt.Sprintf("🛑 <b>%s</b> is at %.6g, at or below your stop %.6g (P&L %+.1f%%). Your plan says exit. <a href=\"%s/journal\">Journal</a>",
				html.EscapeString(name), r.Price, *r.Stop, 100*r.PnLPct, n.AppURL))
		case r.TargetHit:
			n.once(ctx, "target|"+id, 3*24*time.Hour, fmt.Sprintf("🎯 <b>%s</b> reached your target %.6g (P&L %+.1f%%). Take profit or raise the stop. <a href=\"%s/journal\">Journal</a>",
				html.EscapeString(name), *r.Target, 100*r.PnLPct, n.AppURL))
		}
	}
	if sum.SizeMultiplier < 1 {
		n.once(ctx, "breaker", 7*24*time.Hour, fmt.Sprintf("🧯 Circuit-breaker on: %d losing trades in a row, account %.1f%% below its peak. Halve new position sizes until a winning trade.",
			sum.LossStreak, -100*sum.Drawdown))
	}
}

// TokenChanges alerts when a watched token's safety picture gets worse: a new
// kind of red flag, a worse verdict, or its identity no longer checks out.
func (n *Notifier) TokenChanges(ctx context.Context, prev, cur *token.Report) {
	if n == nil || cur == nil || prev == nil {
		return
	}
	var lines []string
	old := prev.RedKinds()
	for _, f := range cur.Flags {
		if f.Level == "red" && !old[digitsKey(f.Title)] {
			lines = append(lines, "• 🔴 "+html.EscapeString(f.Title))
		}
	}
	if cur.RiskScore >= prev.RiskScore+15 {
		lines = append(lines, fmt.Sprintf("• Warning score %d → %d (%s)", prev.RiskScore, cur.RiskScore, html.EscapeString(cur.Verdict)))
	}
	if len(lines) == 0 {
		return
	}
	text := fmt.Sprintf("🪙 <b>%s</b> (%s) safety check got worse:\n%s\n<a href=\"%s/token/%s\">Open the report</a>",
		html.EscapeString(cur.Symbol), cur.ChainLabel, strings.Join(lines, "\n"), n.AppURL, cur.Address)
	n.once(ctx, "token|"+cur.Address+"|"+strings.Join(lines, ""), 3*24*time.Hour, text)
}

func digitsKey(title string) string {
	r := &token.Report{Flags: []token.Flag{{Level: "red", Title: title}}}
	for k := range r.RedKinds() {
		return k
	}
	return title
}
