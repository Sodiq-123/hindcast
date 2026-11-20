// Package strategies runs the evidence tests for two popular approaches
// across the S&P 500 (point in time) and keeps today's screen:
//   - "stronger business, lower price" (fundamentals improved while the
//     stock fell from its high), from SEC filings;
//   - buying at the 0.618 Fibonacci retracement, against nearby
//     non-Fibonacci levels.
package strategies

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/fundamentals"
	"github.com/sodiq/financial-market/backend/internal/levels"
	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/pooled"
)

type KV interface {
	Setting(ctx context.Context, key string, v any) (bool, error)
	SetSetting(ctx context.Context, key string, v any) error
}

type Service struct {
	Market *market.Service
	Fund   *fundamentals.Service
	Pooled *pooled.Trainer
	Store  KV
	Log    *slog.Logger

	mu   sync.Mutex
	prog Progress
}

type Progress struct {
	Running   bool      `json:"running"`
	Stage     string    `json:"stage"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	LastRun   time.Time `json:"lastRun"`
	LastError string    `json:"lastError,omitempty"`
}

// ScreenRow is a stock matching the pattern today.
type ScreenRow struct {
	Symbol string                   `json:"symbol"`
	Name   string                   `json:"name"`
	Cmp    *fundamentals.Comparison `json:"cmp"`
}

type Results struct {
	At          time.Time              `json:"at"`
	Value       *fundamentals.Backtest `json:"value"`
	Levels      *levels.Test           `json:"levels"`
	Screen      []ScreenRow            `json:"screen"`
	Evaluated   int                    `json:"evaluated"` // stocks evaluable today
	PriceStocks int                    `json:"priceStocks"`
	FundStocks  int                    `json:"fundStocks"`
	Missing     int                    `json:"missing"` // former or current members with no SEC data or prices
}

const resultsKey = "strategy_results"

func (s *Service) Progress() Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prog
}

func (s *Service) set(f func(*Progress)) {
	s.mu.Lock()
	f(&s.prog)
	s.mu.Unlock()
}

func (s *Service) Results(ctx context.Context) *Results {
	var r Results
	if ok, err := s.Store.Setting(ctx, resultsKey, &r); err != nil || !ok {
		return nil
	}
	return &r
}

// Start runs the tests in the background.
func (s *Service) Start() error {
	s.mu.Lock()
	if s.prog.Running {
		s.mu.Unlock()
		return fmt.Errorf("already running")
	}
	s.prog = Progress{Running: true, Stage: "starting", LastRun: s.prog.LastRun}
	s.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
		defer cancel()
		if _, err := s.run(ctx); err != nil {
			s.Log.Warn("strategy tests failed", "err", err)
			s.set(func(p *Progress) { p.LastError = err.Error() })
		}
	}()
	return nil
}

// Run runs the tests synchronously (CLI).
func (s *Service) Run(ctx context.Context) (*Results, error) {
	s.mu.Lock()
	if s.prog.Running {
		s.mu.Unlock()
		return nil, fmt.Errorf("already running")
	}
	s.prog = Progress{Running: true, Stage: "starting"}
	s.mu.Unlock()
	return s.run(ctx)
}

// Weekly returns a symbol's weekly bars.
func Weekly(ser *market.Series) []levels.Bar {
	n := len(ser.Bars)
	d, o, h, l, c := make([]string, n), make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	for i, b := range ser.Bars {
		d[i], o[i], h[i], l[i], c[i] = b.Date, b.Open, b.High, b.Low, b.Close
	}
	return levels.Weekly(d, o, h, l, c)
}

// PricesOf converts a series to daily closes.
func PricesOf(ser *market.Series) fundamentals.Prices {
	p := fundamentals.Prices{Dates: make([]string, len(ser.Bars)), Closes: make([]float64, len(ser.Bars))}
	for i, b := range ser.Bars {
		p.Dates[i], p.Closes[i] = b.Date, b.Close
	}
	return p
}

func (s *Service) run(ctx context.Context) (*Results, error) {
	defer s.set(func(p *Progress) { p.Running = false; p.Stage = ""; p.LastRun = time.Now() })
	s.set(func(p *Progress) { p.Stage = "loading S&P 500 membership" })
	members, err := s.Pooled.SP500(ctx)
	if err != nil {
		return nil, fmt.Errorf("S&P 500 membership: %w", err)
	}
	from := "2012-01-01" // SEC XBRL data is complete from about 2011
	syms := members.SymbolsSince(from)
	sort.Strings(syms)
	s.set(func(p *Progress) {
		p.Total = len(syms)
		p.Stage = "loading prices and SEC filings (first run downloads ~500 companies; about 10–20 minutes)"
	})

	res := &Results{At: time.Now().UTC()}
	var weekly [][]levels.Bar
	var items []fundamentals.Item
	today := time.Now().UTC().Format("2006-01-02")
	for _, sym := range syms {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s.set(func(p *Progress) { p.Done++ })
		ser, err := s.Market.Series(ctx, sym, false)
		if err != nil || len(ser.Bars) < 300 {
			res.Missing++
			continue
		}
		res.PriceStocks++
		weekly = append(weekly, Weekly(ser))
		c, err := s.Fund.Get(ctx, sym, false)
		if err != nil {
			res.Missing++
			continue
		}
		res.FundStocks++
		sym := sym
		prices := PricesOf(ser)
		items = append(items, fundamentals.Item{Company: c, Prices: prices, Member: func(d string) bool { return members.MemberOn(sym, d) }})
		if members.MemberOn(sym, today) || members.MemberOn(sym, prices.Dates[len(prices.Dates)-1]) {
			if cmp := fundamentals.Compare(c, prices, len(prices.Dates)-1, fundamentals.DefaultRules); cmp != nil {
				res.Evaluated++
				if cmp.Signal {
					res.Screen = append(res.Screen, ScreenRow{Symbol: sym, Name: c.Name, Cmp: cmp})
				}
			}
		}
	}
	sort.Slice(res.Screen, func(i, j int) bool { return res.Screen[i].Cmp.FromPeak < res.Screen[j].Cmp.FromPeak })

	s.set(func(p *Progress) { p.Stage = "testing chart levels" })
	res.Levels = levels.RunTest(weekly)
	s.set(func(p *Progress) { p.Stage = "testing the stronger-business, lower-price pattern" })
	res.Value = fundamentals.RunBacktest(items, fundamentals.DefaultRules, from)
	res.Value.Notes = append(res.Value.Notes,
		fmt.Sprintf("%d of %d stocks that were in the S&P 500 since 2012 had both prices and SEC data; %d were missing (often delisted or acquired companies, so results lean towards survivors).",
			res.FundStocks, len(syms), len(syms)-res.FundStocks),
		"Figures only count from the date they were filed with the SEC. Prices are split- and dividend-adjusted; no trading costs are deducted.")
	if err := s.Store.SetSetting(ctx, resultsKey, res); err != nil {
		return nil, err
	}
	return res, nil
}
