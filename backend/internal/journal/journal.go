// Package journal is a paper-trading journal with portfolio-level risk
// checks: risk to stop per position and in total, position size limits,
// correlated positions that are really one bet, and a drawdown
// circuit-breaker. It records decisions so they can be judged later; it never
// places real orders.
package journal

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// Settings are the account's risk rules.
type Settings struct {
	Account      float64 `json:"account"`      // starting capital
	RiskPerTrade float64 `json:"riskPerTrade"` // fraction of the account lost if a stop is hit
	MaxOpenRisk  float64 `json:"maxOpenRisk"`  // total risk to stops across open positions
	MaxPosition  float64 `json:"maxPosition"`  // largest single position, fraction of the account
	// Trading fees per side (buy and sell), including the typical spread.
	FeeStock  float64 `json:"feeStock"`  // stocks, ETFs, funds
	FeeCrypto float64 `json:"feeCrypto"` // coins bought on an exchange (BTC-USD, ...)
	FeeToken  float64 `json:"feeToken"`  // DEX tokens: swap fee plus slippage
}

var Defaults = Settings{Account: 10_000, RiskPerTrade: 0.01, MaxOpenRisk: 0.06, MaxPosition: 0.20,
	FeeStock: 0.001, FeeCrypto: 0.002, FeeToken: 0.01}

// FeeRate is the fee per side for a new trade.
func (st Settings) FeeRate(kind, symbol string) float64 {
	switch {
	case kind == "token":
		return st.FeeToken
	case market.IsCrypto(symbol):
		return st.FeeCrypto
	}
	return st.FeeStock
}

// Row is a position with its live valuation.
type Row struct {
	store.Position
	Price      float64 `json:"price"`
	PriceErr   string  `json:"priceErr,omitempty"`
	Cost       float64 `json:"cost"`
	Value      float64 `json:"value"`
	Fees       float64 `json:"fees"` // buy fee + sell fee (estimated at today's price while open)
	PnL        float64 `json:"pnl"`  // after fees`
	PnLPct     float64 `json:"pnlPct"`
	Weight     float64 `json:"weight"`     // value ÷ equity
	RiskToStop float64 `json:"riskToStop"` // money lost from here if the stop is hit (whole value if no stop)
	RiskPct    float64 `json:"riskPct"`    // RiskToStop ÷ equity
	StopHit    bool    `json:"stopHit"`
	TargetHit  bool    `json:"targetHit"`
	Days       int     `json:"days"`
}

type Warning struct {
	Level  string `json:"level"` // red | amber | green
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type Pair struct {
	A   string  `json:"a"`
	B   string  `json:"b"`
	Rho float64 `json:"rho"`
	N   int     `json:"n"`
}

type Summary struct {
	Settings       Settings  `json:"settings"`
	Open           []Row     `json:"open"`
	Closed         []Row     `json:"closed"`
	Equity         float64   `json:"equity"` // account + realised + open P&L
	Invested       float64   `json:"invested"`
	Cash           float64   `json:"cash"`
	OpenPnL        float64   `json:"openPnl"`
	Realized       float64   `json:"realized"`
	FeesPaid       float64   `json:"feesPaid"` // buy fees on every trade plus sell fees on closed ones
	OpenRisk       float64   `json:"openRisk"`
	OpenRiskPct    float64   `json:"openRiskPct"`
	PeakEquity     float64   `json:"peakEquity"`
	Drawdown       float64   `json:"drawdown"` // from the peak of closed-trade equity
	LossStreak     int       `json:"lossStreak"`
	WinRate        float64   `json:"winRate"`
	SizeMultiplier float64   `json:"sizeMultiplier"` // 1, or 0.5 while the circuit-breaker is on
	Correlated     []Pair    `json:"correlated"`
	Warnings       []Warning `json:"warnings"`
	AsOf           time.Time `json:"asOf"`
}

type Service struct {
	Store  *store.Store
	Market *market.Service
	Tokens *token.Service
}

// Settings returns a person's journal settings (the defaults until saved).
func (s *Service) Settings(ctx context.Context, uid int64) Settings {
	st := Defaults
	if ok, err := s.Store.Setting(ctx, store.JournalSettingsKey(uid), &st); err != nil || !ok {
		return Defaults
	}
	return st
}

func (s *Service) SaveSettings(ctx context.Context, uid int64, st Settings) error {
	if st.Account <= 0 || st.RiskPerTrade <= 0 || st.RiskPerTrade > 0.1 || st.MaxOpenRisk <= 0 || st.MaxOpenRisk > 0.5 || st.MaxPosition <= 0 || st.MaxPosition > 1 {
		return fmt.Errorf("invalid settings: account > 0, risk per trade 0–10%%, max open risk 0–50%%, max position 0–100%%")
	}
	for _, f := range []float64{st.FeeStock, st.FeeCrypto, st.FeeToken} {
		if f < 0 || f > 0.05 {
			return fmt.Errorf("invalid settings: fees must be between 0%% and 5%% per side")
		}
	}
	return s.Store.SetSetting(ctx, store.JournalSettingsKey(uid), st)
}

// Price returns the latest price and, for assets, the daily series.
func (s *Service) Price(ctx context.Context, kind, symbol string) (float64, *market.Series, error) {
	if kind == "token" {
		q, err := s.Tokens.Quote(ctx, symbol)
		if err != nil {
			return 0, nil, err
		}
		return q.PriceUSD, nil, nil
	}
	ser, err := s.Market.Series(ctx, symbol, false)
	if err != nil {
		return 0, nil, err
	}
	if len(ser.Bars) == 0 {
		return 0, nil, fmt.Errorf("no prices for %s", symbol)
	}
	p := ser.Bars[len(ser.Bars)-1].Close
	if ser.Meta.Price > 0 {
		p = ser.Meta.Price
	}
	return p, ser, nil
}

// Summary values every position in a person's journal and runs the
// portfolio risk checks.
func (s *Service) Summary(ctx context.Context, uid int64) (*Summary, error) {
	ps, err := s.Store.Positions(ctx, uid)
	if err != nil {
		return nil, err
	}
	st := s.Settings(ctx, uid)
	sum := &Summary{Settings: st, Open: []Row{}, Closed: []Row{}, Correlated: []Pair{}, Warnings: []Warning{}, SizeMultiplier: 1, AsOf: time.Now().UTC()}

	// Price open positions concurrently (each symbol once).
	type quote struct {
		p   float64
		ser *market.Series
		err error
	}
	quotes := map[string]*quote{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range ps {
		k := p.Kind + ":" + p.Symbol
		if p.ClosedAt != nil || quotes[k] != nil {
			continue
		}
		q := &quote{}
		quotes[k] = q
		wg.Add(1)
		go func(kind, sym string) {
			defer wg.Done()
			pr, ser, err := s.Price(ctx, kind, sym)
			mu.Lock()
			q.p, q.ser, q.err = pr, ser, err
			mu.Unlock()
		}(p.Kind, p.Symbol)
	}
	wg.Wait()

	// Closed trades in closing order: realised P&L, equity curve, streaks.
	var closed []store.Position
	for _, p := range ps {
		if p.ClosedAt != nil && p.Exit != nil {
			closed = append(closed, p)
		}
	}
	sort.SliceStable(closed, func(i, j int) bool { return *closed[i].ClosedAt < *closed[j].ClosedAt })
	eq, peak, wins := st.Account, st.Account, 0
	for _, p := range closed {
		r := row(p, *p.Exit)
		sum.Closed = append(sum.Closed, r)
		sum.Realized += r.PnL
		sum.FeesPaid += r.Fees
		eq += r.PnL
		peak = math.Max(peak, eq)
		if r.PnL > 0 {
			wins++
			sum.LossStreak = 0
		} else {
			sum.LossStreak++
		}
	}
	if len(closed) > 0 {
		sum.WinRate = float64(wins) / float64(len(closed))
	}
	sum.PeakEquity = peak
	sum.Drawdown = eq/peak - 1

	series := map[string]*market.Series{}
	for _, p := range ps {
		if p.ClosedAt != nil {
			continue
		}
		q := quotes[p.Kind+":"+p.Symbol]
		price := p.Entry
		r := row(p, price)
		if q.err != nil {
			r.PriceErr = q.err.Error()
		} else {
			r = row(p, q.p)
			if q.ser != nil {
				series[p.Symbol] = q.ser
			}
		}
		sum.Open = append(sum.Open, r)
		sum.OpenPnL += r.PnL
		sum.Invested += r.Value
		sum.FeesPaid += p.FeeOpen
		sum.Cash -= r.Cost + p.FeeOpen
	}
	// Equity is what the account would be worth if everything were sold now,
	// after fees; cash is what's left after paying for open positions.
	sum.Equity = st.Account + sum.Realized + sum.OpenPnL
	sum.Cash += st.Account + sum.Realized
	for i := range sum.Open {
		r := &sum.Open[i]
		if sum.Equity > 0 {
			r.Weight = r.Value / sum.Equity
			r.RiskPct = r.RiskToStop / sum.Equity
		}
		sum.OpenRisk += r.RiskToStop
	}
	if sum.Equity > 0 {
		sum.OpenRiskPct = sum.OpenRisk / sum.Equity
	}
	sum.Correlated = correlations(series)
	sum.Warnings = warnings(sum)
	return sum, nil
}

// ClosedRows values a person's closed trades (after fees), oldest close first,
// without fetching any prices.
func ClosedRows(ps []store.Position) []Row {
	var out []Row
	for _, p := range ps {
		if p.ClosedAt != nil && p.Exit != nil {
			out = append(out, row(p, *p.Exit))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return *out[i].ClosedAt < *out[j].ClosedAt })
	return out
}

func row(p store.Position, price float64) Row {
	r := Row{Position: p, Price: price}
	r.Cost = p.Entry * p.Quantity
	r.Value = price * p.Quantity
	sell := r.Value * p.FeeRate // estimated while open
	if p.FeeClose != nil {
		sell = *p.FeeClose
	}
	r.Fees = p.FeeOpen + sell
	r.PnL = r.Value - r.Cost - r.Fees
	if r.Cost > 0 {
		r.PnLPct = r.PnL / (r.Cost + p.FeeOpen)
	}
	if p.ClosedAt == nil {
		if p.Stop != nil {
			r.RiskToStop = math.Max(0, (price-*p.Stop)*p.Quantity) + *p.Stop*p.Quantity*p.FeeRate
			r.StopHit = price <= *p.Stop
		} else {
			r.RiskToStop = r.Value // no stop: everything is at risk
		}
		r.TargetHit = p.Target != nil && price >= *p.Target
	}
	end := time.Now()
	if p.ClosedAt != nil {
		if t, err := time.Parse("2006-01-02", *p.ClosedAt); err == nil {
			end = t
		}
	}
	if t, err := time.Parse("2006-01-02", p.OpenedAt); err == nil {
		r.Days = int(end.Sub(t).Hours() / 24)
	}
	return r
}

// correlations finds pairs of open assets whose daily returns over the last
// 90 shared trading days are strongly correlated.
func correlations(series map[string]*market.Series) []Pair {
	rets := map[string]map[string]float64{}
	var syms []string
	for sym, ser := range series {
		m := map[string]float64{}
		b := ser.Bars
		for i := max(1, len(b)-130); i < len(b); i++ {
			if b[i-1].Close > 0 && b[i].Close > 0 {
				m[b[i].Date] = math.Log(b[i].Close / b[i-1].Close)
			}
		}
		rets[sym] = m
		syms = append(syms, sym)
	}
	sort.Strings(syms)
	out := []Pair{}
	for i := range syms {
		for j := i + 1; j < len(syms); j++ {
			a, b := rets[syms[i]], rets[syms[j]]
			var dates []string
			for d := range a {
				if _, ok := b[d]; ok {
					dates = append(dates, d)
				}
			}
			sort.Strings(dates)
			if len(dates) > 90 {
				dates = dates[len(dates)-90:]
			}
			if len(dates) < 40 {
				continue
			}
			x, y := make([]float64, len(dates)), make([]float64, len(dates))
			for k, d := range dates {
				x[k], y[k] = a[d], b[d]
			}
			if rho := corr(x, y); rho >= 0.6 {
				out = append(out, Pair{A: syms[i], B: syms[j], Rho: rho, N: len(dates)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rho > out[j].Rho })
	return out
}

func corr(x, y []float64) float64 {
	n := float64(len(x))
	var mx, my float64
	for i := range x {
		mx += x[i]
		my += y[i]
	}
	mx, my = mx/n, my/n
	var sxy, sxx, syy float64
	for i := range x {
		sxy += (x[i] - mx) * (y[i] - my)
		sxx += (x[i] - mx) * (x[i] - mx)
		syy += (y[i] - my) * (y[i] - my)
	}
	if sxx == 0 || syy == 0 {
		return 0
	}
	return sxy / math.Sqrt(sxx*syy)
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func warnings(s *Summary) []Warning {
	w := []Warning{}
	add := func(level, title, detail string) { w = append(w, Warning{level, title, detail}) }
	st := s.Settings

	if s.LossStreak >= 3 || s.Drawdown <= -0.10 {
		s.SizeMultiplier = 0.5
		why := fmt.Sprintf("%d losing trades in a row", s.LossStreak)
		if s.Drawdown <= -0.10 {
			why = fmt.Sprintf("the account is %s below its peak", pct(-s.Drawdown))
		}
		add("red", "Circuit-breaker: halve new position sizes", "Because "+why+". Losing streaks are when judgement is worst and the urge to win it back is strongest. Trade half size until a winning trade (and a new equity high) resets it.")
	}
	if s.OpenRiskPct > st.MaxOpenRisk {
		add("red", "Total risk "+pct(s.OpenRiskPct)+" of the account", fmt.Sprintf("If every open position hit its stop (or went to zero where there is no stop) you would lose %s, above your %s limit. Close or shrink something before adding more.", pct(s.OpenRiskPct), pct(st.MaxOpenRisk)))
	} else if len(s.Open) > 0 {
		add("green", "Total risk "+pct(s.OpenRiskPct)+" of the account", fmt.Sprintf("Within your %s limit for losses if every stop were hit.", pct(st.MaxOpenRisk)))
	}
	for _, r := range s.Open {
		name := r.Symbol
		if r.Kind == "token" && r.Name != "" {
			name = r.Name
		}
		switch {
		case r.PriceErr != "":
			add("amber", name+": no live price", "Valued at the entry price. "+r.PriceErr)
		case r.StopHit:
			add("red", name+": stop hit", fmt.Sprintf("Price %.6g is at or below your stop %.6g. The plan you made with a clear head says exit.", r.Price, *r.Stop))
		case r.TargetHit:
			add("green", name+": target reached", fmt.Sprintf("Price %.6g reached your target %.6g. Take profit or raise the stop to protect the gain.", r.Price, *r.Target))
		}
		if r.Stop == nil {
			lvl := "amber"
			if r.Kind == "token" {
				lvl = "red"
			}
			add(lvl, name+": no stop set", "Without a stop the whole position counts as at risk. Decide in advance the price at which you admit you were wrong.")
		} else if r.RiskPct > 1.5*st.RiskPerTrade {
			add("amber", name+": risk "+pct(r.RiskPct)+" of the account", fmt.Sprintf("Above your %s per-trade rule. Shrink it, or accept that one bad trade costs more than planned.", pct(st.RiskPerTrade)))
		}
		if r.Weight > st.MaxPosition {
			add("amber", name+": "+pct(r.Weight)+" of the account", fmt.Sprintf("Larger than your %s single-position limit. A gap through the stop (overnight news, a token rug) would hurt more than the stop suggests.", pct(st.MaxPosition)))
		}
	}
	for _, p := range s.Correlated {
		lvl := "amber"
		if p.Rho >= 0.8 {
			lvl = "red"
		}
		add(lvl, fmt.Sprintf("%s and %s move together (correlation %.2f)", p.A, p.B, p.Rho),
			fmt.Sprintf("Over the last %d shared trading days they rose and fell together, so in a sell-off both stops are likely to be hit at once. Treat them as one bet when judging total risk.", p.N))
	}
	if n := countKind(s.Open, "token"); n > 0 {
		var v float64
		for _, r := range s.Open {
			if r.Kind == "token" {
				v += r.Value
			}
		}
		if s.Equity > 0 && v/s.Equity > 0.05 {
			add("amber", "Meme tokens are "+pct(v/s.Equity)+" of the account", "These can lose most of their value in hours, and stops may not fill in a crash. Many experienced traders keep them to a few percent at most.")
		}
	}
	order := map[string]int{"red": 0, "amber": 1, "green": 2}
	sort.SliceStable(w, func(i, j int) bool { return order[w[i].Level] < order[w[j].Level] })
	return w
}

func countKind(rs []Row, kind string) int {
	n := 0
	for _, r := range rs {
		if strings.EqualFold(r.Kind, kind) {
			n++
		}
	}
	return n
}
