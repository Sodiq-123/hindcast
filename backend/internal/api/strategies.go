package api

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/sodiq/financial-market/backend/internal/fundamentals"
	"github.com/sodiq/financial-market/backend/internal/levels"
	"github.com/sodiq/financial-market/backend/internal/strategies"
)

type quarterRow struct {
	fundamentals.Quarter
	Price *float64 // share price on the day the figures were known
	PE    *float64 // price ÷ earnings, with today's share count
}

// MarshalJSON adds price and P/E to the quarter's own JSON (the embedded
// Quarter's MarshalJSON would otherwise be used alone).
func (q quarterRow) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(q.Quarter)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	m["price"], m["pe"] = q.Price, q.PE
	return json.Marshal(m)
}

func ptr(x float64) *float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return nil
	}
	return &x
}

// businessVsPrice is the per-stock "Business vs price" panel.
func (s *Server) businessVsPrice(w http.ResponseWriter, r *http.Request) {
	sym := strings.ToUpper(r.PathValue("symbol"))
	c, err := s.Fund.Get(r.Context(), sym, r.URL.Query().Get("refresh") == "1")
	if err != nil {
		code := 502
		if errors.Is(err, fundamentals.ErrNoFilings) {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	ser, err := s.Engine.Market.Series(r.Context(), sym, false)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	prices := strategies.PricesOf(ser)
	out := map[string]any{"symbol": sym, "name": c.Name, "cik": c.CIK, "fetchedAt": c.FetchedAt, "rules": fundamentals.DefaultRules}
	if cmp := fundamentals.Compare(c, prices, len(prices.Dates)-1, fundamentals.DefaultRules); cmp != nil {
		out["comparison"] = cmp
	}
	shares := math.NaN()
	for i := len(c.Quarters) - 1; i >= 0; i-- {
		if q := c.Quarters[i]; !math.IsNaN(q.Shares) && q.Shares > 0 {
			shares = q.Shares
			break
		}
	}
	rows := []quarterRow{}
	start := max(0, len(c.Quarters)-28)
	for _, q := range c.Quarters[start:] {
		row := quarterRow{Quarter: q}
		if q.Known != "" {
			if p, i := prices.At(q.Known); i >= 0 {
				row.Price = ptr(p)
				if q.NetIncome > 0 && shares > 0 {
					row.PE = ptr(p * shares / q.NetIncome)
				}
			}
		}
		rows = append(rows, row)
	}
	out["quarters"] = rows
	if res := s.Strategies.Results(r.Context()); res != nil && res.Value != nil {
		out["backtest"] = map[string]any{"status": res.Value.Status, "note": res.Value.StatusNote, "portfolio": res.Value.Portfolio,
			"groups": res.Value.Groups, "from": res.Value.From, "to": res.Value.To, "at": res.At}
	}
	writeJSON(w, 200, out)
}

// chartLevels is the per-stock "Chart levels" panel.
func (s *Server) chartLevels(w http.ResponseWriter, r *http.Request) {
	sym := strings.ToUpper(r.PathValue("symbol"))
	ser, err := s.Engine.Market.Series(r.Context(), sym, false)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	out := map[string]any{"symbol": sym, "view": levels.Analyse(strategies.Weekly(ser)), "breakEven": levels.BreakEven}
	if res := s.Strategies.Results(r.Context()); res != nil && res.Levels != nil {
		out["test"] = res.Levels
		out["testAt"] = res.At
	}
	writeJSON(w, 200, out)
}

func (s *Server) strategyResults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"progress": s.Strategies.Progress(), "results": s.Strategies.Results(r.Context())})
}

func (s *Server) strategyRun(w http.ResponseWriter, r *http.Request) {
	if err := s.Strategies.Start(); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 202, s.Strategies.Progress())
}
