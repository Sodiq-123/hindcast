package engine

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// Universes are the default instrument lists the scanner searches for
// opportunities. Edit freely — any Yahoo symbol works.
var Universes = map[string][]string{
	"us_large_caps": {"AAPL", "MSFT", "NVDA", "AMZN", "GOOGL", "META", "TSLA", "BRK-B", "JPM", "V", "UNH", "XOM", "LLY", "AVGO", "JNJ", "WMT", "MA", "PG", "HD", "COST", "ORCL", "NFLX", "AMD", "CRM", "BAC", "KO", "PEP", "ADBE", "CSCO", "INTC", "DIS", "MCD", "CAT", "GS", "BA", "PLTR", "UBER", "SHOP"},
	"global":        {"SPY", "QQQ", "IWM", "DIA", "EFA", "EEM", "VGK", "EWJ", "FXI", "INDA", "EWZ", "EWU", "EWG", "TLT", "HYG", "GLD", "SLV", "USO", "XLE", "XLF", "XLK", "XLV", "SMH", "ARKK"},
	"international": {"ASML", "TSM", "NVO", "SAP", "TM", "BABA", "SONY", "SHEL", "AZN", "HSBC", "UL", "BHP", "RIO", "MELI", "NU", "SE", "INFY", "HDB"},
	"crypto":        {"BTC-USD", "ETH-USD", "SOL-USD", "BNB-USD", "XRP-USD", "ADA-USD", "DOGE-USD", "AVAX-USD", "LINK-USD", "DOT-USD", "LTC-USD", "BCH-USD", "TRX-USD", "XLM-USD", "ATOM-USD", "NEAR-USD"},
	"commodities":   {"GC=F", "SI=F", "CL=F", "NG=F", "HG=F", "PL=F", "ZC=F", "ZW=F", "ZS=F", "KC=F"},
}

// ScanItem is one row of the opportunity ranking.
type ScanItem struct {
	Symbol         string  `json:"symbol"`
	Name           string  `json:"name"`
	Class          string  `json:"class"`
	Price          float64 `json:"price"`
	Horizon        string  `json:"horizon"`
	Signal         string  `json:"signal"`
	Status         string  `json:"status"`
	PUp            float64 `json:"pUp"`
	Conviction     int     `json:"conviction"`
	ExpectedReturn float64 `json:"expectedReturn"`
	HitRate        float64 `json:"hitRate"` // OOS accuracy
	BrierSkill     float64 `json:"brierSkill"`
	StrategyCAGR   float64 `json:"strategyCagr"`
	BuyHoldCAGR    float64 `json:"buyHoldCagr"`
	Score          float64 `json:"score"`
	TopReason      string  `json:"topReason"`
	PValue         float64 `json:"pValue"`
	FDRPass        bool    `json:"fdrPass"` // survived the multiple-testing correction
	Note           string  `json:"note,omitempty"`
	Error          string  `json:"error,omitempty"`
}

type ScanResult struct {
	ID         int64      `json:"id"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt time.Time  `json:"finishedAt"`
	Universes  []string   `json:"universes"`
	Horizon    string     `json:"horizon"`
	Scanned    int        `json:"scanned"`
	Buys       []ScanItem `json:"buys"`
	Avoid      []ScanItem `json:"avoid"`
	NoEdge     []ScanItem `json:"noEdge"`
	Failed     []ScanItem `json:"failed"`
}

type ScanProgress struct {
	Running bool      `json:"running"`
	Done    int       `json:"done"`
	Total   int       `json:"total"`
	Current string    `json:"current"`
	Started time.Time `json:"started"`
}

type Scanner struct {
	Engine *Engine
	mu     sync.Mutex
	prog   ScanProgress
}

func (s *Scanner) Progress() ScanProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prog
}

// Run analyses every symbol (without the LLM, for speed and cost) and ranks
// them. Only horizons with a validated or weak edge can rank as buys.
func (s *Scanner) Run(ctx context.Context, universes []string, extra []string, horizon string, onReport func(*Report)) (*ScanResult, error) {
	s.mu.Lock()
	if s.prog.Running {
		s.mu.Unlock()
		return nil, ErrScanRunning
	}
	seen := map[string]bool{}
	var syms []string
	for _, u := range universes {
		for _, sym := range Universes[u] {
			if !seen[sym] {
				seen[sym] = true
				syms = append(syms, sym)
			}
		}
	}
	for _, sym := range extra {
		sym = strings.ToUpper(sym)
		if !seen[sym] {
			seen[sym] = true
			syms = append(syms, sym)
		}
	}
	s.prog = ScanProgress{Running: true, Total: len(syms), Started: time.Now()}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.prog.Running = false
		s.mu.Unlock()
	}()

	res := &ScanResult{StartedAt: time.Now().UTC(), Universes: universes, Horizon: horizon}
	var items []ScanItem
	for _, sym := range syms {
		if ctx.Err() != nil {
			break
		}
		s.mu.Lock()
		s.prog.Current = sym
		s.mu.Unlock()
		rep, err := s.Engine.Analyze(ctx, sym, Options{UseLLM: false})
		s.mu.Lock()
		s.prog.Done++
		s.mu.Unlock()
		if err != nil {
			res.Failed = append(res.Failed, ScanItem{Symbol: sym, Error: err.Error()})
			continue
		}
		if onReport != nil {
			onReport(rep)
		}
		res.Scanned++
		items = append(items, toScanItem(rep, horizon))
	}
	applyFDR(items, 0.10)
	for _, item := range items {
		switch {
		case item.Status == "no_edge" || item.Signal == "":
			res.NoEdge = append(res.NoEdge, item)
		case strings.Contains(item.Signal, "BUY"):
			res.Buys = append(res.Buys, item)
		case strings.Contains(item.Signal, "SELL"):
			res.Avoid = append(res.Avoid, item)
		default:
			res.NoEdge = append(res.NoEdge, item)
		}
	}
	SortByOpportunity(res.Buys)
	SortByOpportunity(res.Avoid)
	res.FinishedAt = time.Now().UTC()
	return res, nil
}

func toScanItem(rep *Report, horizon string) ScanItem {
	it := ScanItem{Symbol: rep.Symbol, Name: rep.Name, Class: rep.Class, Price: rep.Price, Horizon: horizon}
	for _, h := range rep.Horizons {
		if h.Key != horizon || !h.Available {
			continue
		}
		e := h.Validation.Ensemble
		it.PValue = e.PValue
		it.Signal, it.Status, it.PUp, it.Conviction = h.Signal, h.Status, h.PUp, h.Conviction
		it.ExpectedReturn, it.HitRate, it.BrierSkill = h.ExpectedReturn, e.Accuracy, e.BrierSkill
		it.StrategyCAGR, it.BuyHoldCAGR = h.Validation.Strategy.CAGR, h.Validation.Strategy.BuyHoldCAGR
		// Score rewards conviction, demonstrated skill and favourable expected
		// return; for sells the sign is flipped so the worst setups rank first.
		dir := 1.0
		if strings.Contains(h.Signal, "SELL") {
			dir = -1
		}
		it.Score = float64(h.Conviction) + 200*math.Max(0, e.BrierSkill) + 100*dir*h.ExpectedReturn
		if len(h.Drivers) > 0 && math.Abs(h.Drivers[0].ProbImpact) >= 0.005 {
			it.TopReason = h.Drivers[0].Text
		} else {
			for _, w := range h.Why {
				if strings.HasPrefix(w, "Historical analogs") {
					it.TopReason = w
				}
			}
		}
	}
	return it
}

type scanErr string

func (e scanErr) Error() string { return string(e) }

const ErrScanRunning = scanErr("a scan is already running")

// applyFDR runs a Benjamini–Hochberg false-discovery-rate correction across
// every item in a scan. Testing 100 assets at p < 0.10 would otherwise let
// about 10 edgeless assets through by luck. Validated items that fail the
// correction are demoted to weak (STRONG signals become plain).
func applyFDR(items []ScanItem, alpha float64) {
	var ps []float64
	for _, it := range items {
		if it.Signal != "" {
			ps = append(ps, it.PValue)
		}
	}
	m := len(ps)
	if m == 0 {
		return
	}
	sort.Float64s(ps)
	cut := -1.0
	for k := m; k >= 1; k-- {
		if ps[k-1] <= float64(k)/float64(m)*alpha {
			cut = ps[k-1]
			break
		}
	}
	for i := range items {
		it := &items[i]
		if it.Signal == "" {
			continue
		}
		it.FDRPass = it.PValue <= cut
		if !it.FDRPass && it.Status == "validated" {
			it.Status = "weak"
			it.Signal = strings.NewReplacer("STRONG BUY", "LEAN UP", "STRONG SELL", "LEAN DOWN", "BUY", "LEAN UP", "SELL", "LEAN DOWN").Replace(it.Signal)
			it.Conviction /= 2
			it.Note = fmt.Sprintf("Demoted: p=%.3f is not significant once %d simultaneous tests are accounted for (Benjamini–Hochberg, 10%% FDR).", it.PValue, m)
		}
	}
}
