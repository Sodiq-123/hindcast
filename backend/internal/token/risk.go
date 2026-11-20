package token

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
)

// PriceSource supplies daily closes for market symbols (BTC-USD, a meme
// coin's verified Yahoo ticker, ...). The market service implements it.
type PriceSource interface {
	DailyCloses(ctx context.Context, symbol string) (dates []string, closes []float64, err error)
}

// Move is a return over a period ending on Date.
type Move struct {
	Date   string  `json:"date"`
	Return float64 `json:"return"`
}

// BenchStat describes how the token moves with a major coin.
type BenchStat struct {
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	Days        int     `json:"days"`        // shared days
	Corr        float64 `json:"corr"`        // correlation of daily returns
	Beta        float64 `json:"beta"`        // token move per 1% benchmark move
	DownDays    int     `json:"downDays"`    // days the benchmark fell 3%+
	TokenOnDown float64 `json:"tokenOnDown"` // token's average return on those days
	BenchOnDown float64 `json:"benchOnDown"` // benchmark's average return on those days
}

// RiskStats measures (not forecasts) how much the token moves. With a year
// of daily data these are well estimated, unlike a direction forecast.
type RiskStats struct {
	Source        string      `json:"source"`
	From          string      `json:"from"`
	To            string      `json:"to"`
	Days          int         `json:"days"`
	TotalReturn   float64     `json:"totalReturn"`
	AnnualVol     float64     `json:"annualVol"`
	TypicalDay    float64     `json:"typicalDay"` // median absolute daily move
	WorstDay      Move        `json:"worstDay"`
	BestDay       Move        `json:"bestDay"`
	WorstWeek     Move        `json:"worstWeek"`  // worst 7-day return
	WorstMonth    Move        `json:"worstMonth"` // worst 30-day return
	MaxDrawdown   float64     `json:"maxDrawdown"`
	PeakDate      string      `json:"peakDate"`
	TroughDate    string      `json:"troughDate"`
	FromHigh      float64     `json:"fromHigh"`
	BigDayShare   float64     `json:"bigDayShare"`   // share of days moving 10%+ either way
	CrashWeekRate float64     `json:"crashWeekRate"` // share of 7-day stretches down 20%+
	Bad1in20Day   float64     `json:"bad1in20Day"`   // 5th percentile daily return
	Bad1in20Week  float64     `json:"bad1in20Week"`  // 5th percentile 7-day return
	SizeFor1Pct   float64     `json:"sizeFor1Pct"`   // position where a 1-in-20 bad week costs 1% of the account
	Bench         []BenchStat `json:"bench"`
}

// risk gathers the longest reliable daily history and measures it.
func (s *Service) risk(ctx context.Context, rep *Report) {
	dates, closes, source := s.tokenHistory(ctx, rep)
	if len(closes) < 60 {
		rep.Notes = append(rep.Notes, "Too little daily history for risk statistics.")
		return
	}
	st := computeRisk(dates, closes)
	st.Source = source
	if s.Prices != nil {
		benches := [][2]string{{"BTC-USD", "Bitcoin"}}
		if rep.Chain == "solana" {
			benches = append(benches, [2]string{"SOL-USD", "Solana"})
		} else {
			benches = append(benches, [2]string{"ETH-USD", "Ethereum"})
		}
		for _, b := range benches {
			bd, bc, err := s.Prices.DailyCloses(ctx, b[0])
			if err != nil {
				continue
			}
			if bs, ok := benchStat(dates, closes, bd, bc); ok {
				bs.Symbol, bs.Name = b[0], b[1]
				st.Bench = append(st.Bench, bs)
			}
		}
	}
	rep.Risk = st
}

// tokenHistory prefers, in order: the verified Yahoo ticker (years of data),
// CoinGecko's daily prices (one year), the DEX pool's daily candles (about six
// months).
func (s *Service) tokenHistory(ctx context.Context, rep *Report) ([]string, []float64, string) {
	if rep.Listing != nil && s.Prices != nil {
		if m, ok := market.MemeByID(rep.Listing.ID); ok {
			if d, c, err := s.Prices.DailyCloses(ctx, m.Ticker); err == nil && len(c) >= 60 {
				return lastN(d, c, 730)
			}
		}
	}
	if rep.Listing != nil && s.Registry != nil {
		var r struct {
			Prices [][2]float64 `json:"prices"`
		}
		u := fmt.Sprintf("https://api.coingecko.com/api/v3/coins/%s/market_chart?vs_currency=usd&days=365&interval=daily", rep.Listing.ID)
		if err := s.Registry.get(ctx, u, &r); err == nil && len(r.Prices) >= 60 {
			byDay := map[string]float64{}
			for _, p := range r.Prices {
				if p[1] > 0 {
					byDay[market.CoinGeckoDay(int64(p[0]))] = p[1]
				}
			}
			d, c := sortedSeries(byDay)
			return d, c, "CoinGecko (daily, last year)"
		}
	}
	if h := rep.History; h != nil && h.Interval == "day" {
		byDay := map[string]float64{}
		for _, c := range h.Candles {
			byDay[time.Unix(c.Time, 0).UTC().Format("2006-01-02")] = c.Close
		}
		d, c := sortedSeries(byDay)
		return d, c, "DEX pool candles (GeckoTerminal, about 6 months)"
	}
	return nil, nil, ""
}

func lastN(d []string, c []float64, n int) ([]string, []float64, string) {
	if len(c) > n {
		d, c = d[len(d)-n:], c[len(c)-n:]
	}
	return d, c, "Yahoo Finance (daily)"
}

func sortedSeries(byDay map[string]float64) ([]string, []float64) {
	d := make([]string, 0, len(byDay))
	for k := range byDay {
		d = append(d, k)
	}
	sort.Strings(d)
	c := make([]float64, len(d))
	for i, k := range d {
		c[i] = byDay[k]
	}
	return d, c
}

func computeRisk(dates []string, c []float64) *RiskStats {
	n := len(c)
	st := &RiskStats{From: dates[0], To: dates[n-1], Days: n, TotalReturn: c[n-1]/c[0] - 1}
	var lr, simple, absd []float64
	st.WorstDay.Return, st.BestDay.Return = math.Inf(1), math.Inf(-1)
	big := 0
	for i := 1; i < n; i++ {
		r := c[i]/c[i-1] - 1
		simple = append(simple, r)
		lr = append(lr, math.Log(c[i]/c[i-1]))
		absd = append(absd, math.Abs(r))
		if r < st.WorstDay.Return {
			st.WorstDay = Move{dates[i], r}
		}
		if r > st.BestDay.Return {
			st.BestDay = Move{dates[i], r}
		}
		if math.Abs(r) >= 0.10 {
			big++
		}
	}
	st.BigDayShare = float64(big) / float64(len(simple))
	st.AnnualVol = stdev(lr) * math.Sqrt(365)
	st.TypicalDay = quantile(absd, 0.5)
	st.Bad1in20Day = quantile(simple, 0.05)

	worst := func(k int) (Move, []float64) {
		m := Move{Return: math.Inf(1)}
		var all []float64
		for i := k; i < n; i++ {
			r := c[i]/c[i-k] - 1
			all = append(all, r)
			if r < m.Return {
				m = Move{dates[i], r}
			}
		}
		return m, all
	}
	var weeks []float64
	st.WorstWeek, weeks = worst(7)
	st.WorstMonth, _ = worst(30)
	if len(weeks) > 0 {
		crash := 0
		for _, w := range weeks {
			if w <= -0.20 {
				crash++
			}
		}
		st.CrashWeekRate = float64(crash) / float64(len(weeks))
		st.Bad1in20Week = quantile(weeks, 0.05)
		if st.Bad1in20Week < 0 {
			st.SizeFor1Pct = math.Min(1, 0.01/-st.Bad1in20Week)
		}
	}
	peak, peakDate := c[0], dates[0]
	for i := range c {
		if c[i] > peak {
			peak, peakDate = c[i], dates[i]
		}
		if dd := c[i]/peak - 1; dd < st.MaxDrawdown {
			st.MaxDrawdown, st.PeakDate, st.TroughDate = dd, peakDate, dates[i]
		}
	}
	st.FromHigh = c[n-1]/peak - 1
	return st
}

func benchStat(td []string, tc []float64, bd []string, bc []float64) (BenchStat, bool) {
	ret := func(d []string, c []float64) map[string]float64 {
		m := map[string]float64{}
		for i := 1; i < len(c); i++ {
			if c[i] > 0 && c[i-1] > 0 {
				m[d[i]] = c[i]/c[i-1] - 1
			}
		}
		return m
	}
	tr, br := ret(td, tc), ret(bd, bc)
	var x, y []float64
	var bs BenchStat
	for d, t := range tr {
		b, ok := br[d]
		if !ok {
			continue
		}
		x, y = append(x, b), append(y, t)
		if b <= -0.03 {
			bs.DownDays++
			bs.TokenOnDown += t
			bs.BenchOnDown += b
		}
	}
	if len(x) < 60 {
		return bs, false
	}
	bs.Days = len(x)
	mx, my := mean(x), mean(y)
	var sxy, sxx, syy float64
	for i := range x {
		sxy += (x[i] - mx) * (y[i] - my)
		sxx += (x[i] - mx) * (x[i] - mx)
		syy += (y[i] - my) * (y[i] - my)
	}
	if sxx > 0 && syy > 0 {
		bs.Corr = sxy / math.Sqrt(sxx*syy)
		bs.Beta = sxy / sxx
	}
	if bs.DownDays > 0 {
		bs.TokenOnDown /= float64(bs.DownDays)
		bs.BenchOnDown /= float64(bs.DownDays)
	}
	return bs, true
}

func mean(x []float64) float64 {
	s := 0.0
	for _, v := range x {
		s += v
	}
	return s / float64(len(x))
}

func stdev(x []float64) float64 {
	m := mean(x)
	s := 0.0
	for _, v := range x {
		s += (v - m) * (v - m)
	}
	return math.Sqrt(s / float64(len(x)-1))
}

func quantile(x []float64, q float64) float64 {
	c := append([]float64(nil), x...)
	sort.Float64s(c)
	pos := q * float64(len(c)-1)
	i := int(pos)
	if i+1 >= len(c) {
		return c[i]
	}
	return c[i] + (pos-float64(i))*(c[i+1]-c[i])
}
