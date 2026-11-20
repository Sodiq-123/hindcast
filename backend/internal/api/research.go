package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/research"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// researchRow is one line of the Research home list: the latest report and
// how the price has moved since.
type researchRow struct {
	store.ResearchSubject
	PriceNow float64 `json:"priceNow,omitempty"`
	PriceAt  string  `json:"priceAt,omitempty"`
}

func (s *Server) researchOverview(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"enabled": s.Research != nil}
	if subs, err := s.Store.ResearchSubjects(r.Context()); err == nil {
		rows := make([]researchRow, len(subs))
		ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
		var wg sync.WaitGroup
		for i, x := range subs {
			rows[i].ResearchSubject = x
			if i >= 20 {
				continue // older subjects: no price lookup
			}
			wg.Add(1)
			go func(row *researchRow) {
				defer wg.Done()
				row.PriceNow, row.PriceAt = s.researchPrice(ctx, row.Subject)
			}(&rows[i])
		}
		wg.Wait()
		cancel()
		out["subjects"] = rows
	}
	if s.Research != nil {
		out["spent"], out["budget"], out["model"], out["estimate"] = s.Research.MonthSpend(r.Context()), s.Research.Budget, s.Research.Model(), research.EstimateUSD
		used, limit := s.allowance(r, "research")
		out["allowance"] = map[string]int{"used": used, "limit": limit}
	}
	writeJSON(w, 200, out)
}

// researchPrice is a researched subject's latest price and its date
// (YYYY-MM-DD), or zero when it can't be found.
func (s *Server) researchPrice(ctx context.Context, subject string) (float64, string) {
	today := time.Now().UTC().Format("2006-01-02")
	switch {
	case research.IsCoinID(subject):
		if s.Research != nil {
			if p, err := s.Research.CoinPrice(ctx, strings.TrimPrefix(subject, "cg:")); err == nil && p > 0 {
				return p, today
			}
		}
	case token.LooksLikeAddress(subject):
		if q, err := s.Tokens.Quote(ctx, subject); err == nil && q.PriceUSD > 0 {
			return q.PriceUSD, today
		}
	default:
		// Series tops up a stale cache, so this is the latest close.
		if ser, err := s.Engine.Market.Series(ctx, subject, false); err == nil && len(ser.Bars) > 0 {
			b := ser.Bars[len(ser.Bars)-1]
			return b.Close, b.Date
		}
	}
	return 0, ""
}

type researchHistoryItem struct {
	ID           int64   `json:"id"`
	CreatedAt    string  `json:"createdAt"`
	Fundamentals string  `json:"fundamentals"`
	Price        float64 `json:"price"`
	CostUSD      float64 `json:"costUsd"`
}

func (s *Server) researchSubject(w http.ResponseWriter, r *http.Request) {
	subject := researchKey(r.PathValue("subject"))
	out := map[string]any{"subject": subject, "enabled": s.Research != nil}
	hist, err := s.Store.ResearchHistory(r.Context(), subject, 50)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	items := []researchHistoryItem{}
	for _, h := range hist {
		items = append(items, researchHistoryItem{h.ID, h.CreatedAt.Format("2006-01-02T15:04:05Z"), h.Fundamentals, h.Price, h.Cost.USD})
	}
	out["history"] = items
	if len(hist) > 0 {
		out["latest"] = hist[0]
	}
	if s.Research != nil {
		out["job"] = s.Research.Job(subject)
		out["spent"], out["budget"], out["estimate"] = s.Research.MonthSpend(r.Context()), s.Research.Budget, research.EstimateUSD
		used, limit := s.allowance(r, "research")
		out["allowance"] = map[string]int{"used": used, "limit": limit}
	}
	// Today's price, to show how it has moved since each report.
	if p, at := s.researchPrice(r.Context(), subject); p > 0 {
		out["priceNow"], out["priceAt"] = p, at
	}
	if len(hist) > 0 {
		out["identity"] = hist[0].IdentityWarning()
	}
	writeJSON(w, 200, out)
}

func (s *Server) researchReport(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rep, err := s.Store.ResearchReport(r.Context(), id)
	if err != nil {
		writeErr(w, 404, "report not found")
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) researchStart(w http.ResponseWriter, r *http.Request) {
	if s.Research == nil {
		writeErr(w, 503, "research needs ANTHROPIC_API_KEY in .env")
		return
	}
	// Each member has a monthly allowance (reports cost real money); the
	// server-wide monthly budget applies to everyone.
	if used, limit := s.allowance(r, "research"); limit >= 0 && used >= limit {
		writeErr(w, 429, fmt.Sprintf("You've used your %d research report%s for this month. Reports already made are free to read; ask an admin if you need more.", limit, plural(limit)))
		return
	}
	job, err := s.Research.Start(r.Context(), researchKey(r.PathValue("subject")))
	if err == nil && !user(r).Admin() {
		_ = s.Store.CountAction(r.Context(), uid(r), "research")
	}
	if err != nil {
		code := 422
		if errors.Is(err, r.Context().Err()) {
			code = 500
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, 202, job)
}

func researchKey(s string) string {
	s = strings.TrimSpace(s)
	if research.IsCoinID(s) {
		return strings.ToLower(s)
	}
	if token.LooksLikeAddress(s) {
		return s
	}
	return strings.ToUpper(s)
}

// coinSearch finds coins on CoinGecko on any blockchain (for research).
func (s *Server) coinSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 || s.Tokens.Registry == nil || token.LooksLikeAddress(q) {
		writeJSON(w, 200, []any{})
		return
	}
	res, err := s.Tokens.Registry.Coins(r.Context(), q)
	if err != nil {
		writeJSON(w, 200, []any{})
		return
	}
	if len(res) > 5 {
		res = res[:5]
	}
	writeJSON(w, 200, res)
}
