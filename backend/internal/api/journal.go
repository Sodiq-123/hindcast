package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/journal"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
)

func (s *Server) journalSummary(w http.ResponseWriter, r *http.Request) {
	sum, err := s.Journal.Summary(r.Context(), uid(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, sum)
}

type openReq struct {
	Symbol   string   `json:"symbol"`
	Quantity float64  `json:"quantity"`
	Amount   float64  `json:"amount"` // money to invest, if quantity is not given
	Entry    float64  `json:"entry"`  // defaults to the current price
	Stop     *float64 `json:"stop"`
	Target   *float64 `json:"target"`
	OpenedAt string   `json:"openedAt"`
	Signal   string   `json:"signal"`
	Note     string   `json:"note"`
}

func (s *Server) journalOpen(w http.ResponseWriter, r *http.Request) {
	var req openReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON: "+err.Error())
		return
	}
	p := store.Position{Symbol: strings.TrimSpace(req.Symbol), Kind: "asset", Stop: req.Stop, Target: req.Target, Signal: req.Signal, Note: req.Note, OpenedAt: req.OpenedAt}
	if p.Symbol == "" {
		writeErr(w, 400, "symbol required")
		return
	}
	var price float64
	if token.LooksLikeAddress(p.Symbol) {
		q, err := s.Tokens.Quote(r.Context(), p.Symbol)
		if err != nil {
			writeErr(w, 422, err.Error())
			return
		}
		p.Kind, p.Chain, p.Name, price = "token", q.Chain, q.Symbol, q.PriceUSD
	} else {
		p.Symbol = strings.ToUpper(p.Symbol)
		var err error
		if price, _, err = s.Journal.Price(r.Context(), "asset", p.Symbol); err != nil {
			writeErr(w, 422, err.Error())
			return
		}
	}
	p.Entry = req.Entry
	if p.Entry <= 0 {
		p.Entry = price
	}
	// The buy fee comes out of the amount, as at a real broker: $1,000 at a
	// 0.1% fee buys $999 of the asset.
	p.FeeRate = s.Journal.Settings(r.Context(), uid(r)).FeeRate(p.Kind, p.Symbol)
	p.Quantity = req.Quantity
	if p.Quantity <= 0 && req.Amount > 0 && p.Entry > 0 {
		p.Quantity = req.Amount / (p.Entry * (1 + p.FeeRate))
	}
	p.FeeOpen = p.Quantity * p.Entry * p.FeeRate
	switch {
	case p.Entry <= 0 || p.Quantity <= 0:
		writeErr(w, 400, "need a positive quantity or amount, and a price")
		return
	case p.Stop != nil && *p.Stop >= p.Entry:
		writeErr(w, 400, "the stop must be below the entry price (the journal records long positions)")
		return
	}
	if p.OpenedAt == "" {
		p.OpenedAt = time.Now().Format("2006-01-02")
	}
	if s.Learn != nil {
		p.Edge = s.Learn.Edge(r.Context(), p.Kind, p.Symbol, p.Signal)
	}
	if err := s.Store.AddPosition(r.Context(), uid(r), &p); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) journalClose(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Exit float64 `json:"exit"`
		Date string  `json:"date"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Exit <= 0 {
		ps, err := s.Store.Positions(r.Context(), uid(r))
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		for _, p := range ps {
			if p.ID == id {
				if req.Exit, _, err = s.Journal.Price(r.Context(), p.Kind, p.Symbol); err != nil {
					writeErr(w, 422, "no current price; enter the exit price: "+err.Error())
					return
				}
			}
		}
	}
	if req.Date == "" {
		req.Date = time.Now().Format("2006-01-02")
	}
	if err := s.Store.ClosePosition(r.Context(), uid(r), id, req.Date, req.Exit); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "exit": req.Exit})
}

func (s *Server) journalPlan(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Stop   *float64 `json:"stop"`
		Target *float64 `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if err := s.Store.UpdatePositionPlan(r.Context(), uid(r), id, req.Stop, req.Target); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) journalDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.Store.DeletePosition(r.Context(), uid(r), id); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) journalSettings(w http.ResponseWriter, r *http.Request) {
	var st journal.Settings
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	if err := s.Journal.SaveSettings(r.Context(), uid(r), st); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

func writeStoreErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not found")
		return
	}
	writeErr(w, 500, err.Error())
}

// quote returns a current price for a symbol or token address.
func (s *Server) quote(w http.ResponseWriter, r *http.Request) {
	sym := strings.TrimSpace(r.URL.Query().Get("symbol"))
	kind := "asset"
	if token.LooksLikeAddress(sym) {
		kind = "token"
	} else {
		sym = strings.ToUpper(sym)
	}
	p, _, err := s.Journal.Price(r.Context(), kind, sym)
	if err != nil {
		writeErr(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"symbol": sym, "kind": kind, "price": p})
}
