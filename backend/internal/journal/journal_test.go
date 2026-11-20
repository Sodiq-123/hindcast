package journal

import (
	"math"
	"testing"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/store"
)

func f(x float64) *float64 { return &x }

func TestRowRisk(t *testing.T) {
	r := row(store.Position{Entry: 100, Quantity: 10, Stop: f(90), OpenedAt: "2026-01-01"}, 95)
	if r.PnL != -50 || r.RiskToStop != 50 || r.StopHit {
		t.Errorf("got pnl %v risk %v stopHit %v", r.PnL, r.RiskToStop, r.StopHit)
	}
	if r := row(store.Position{Entry: 1, Quantity: 1000, OpenedAt: "2026-01-01"}, 2); r.RiskToStop != 2000 {
		t.Errorf("no stop: whole value at risk, got %v", r.RiskToStop)
	}
	if r := row(store.Position{Entry: 100, Quantity: 1, Stop: f(90), OpenedAt: "2026-01-01"}, 89); !r.StopHit {
		t.Error("stop should be hit")
	}
}

func TestWarnings(t *testing.T) {
	s := &Summary{Settings: Defaults, LossStreak: 3, OpenRiskPct: 0.09, Equity: 10000, SizeMultiplier: 1,
		Open:       []Row{{Position: store.Position{Symbol: "X", Kind: "token"}, Weight: 0.3, Value: 3000, RiskPct: 0.3}},
		Correlated: []Pair{{A: "AAPL", B: "MSFT", Rho: 0.85, N: 90}}}
	w := warnings(s)
	if s.SizeMultiplier != 0.5 {
		t.Error("circuit-breaker should halve sizes after 3 losses")
	}
	titles := map[string]string{}
	for _, x := range w {
		titles[x.Title] = x.Level
	}
	for _, want := range []string{"X: no stop set", "AAPL and MSFT move together (correlation 0.85)", "Total risk 9.0% of the account", "Meme tokens are 30.0% of the account"} {
		if _, ok := titles[want]; !ok {
			t.Errorf("missing warning %q in %v", want, titles)
		}
	}
	if w[0].Level != "red" {
		t.Error("red warnings first")
	}
}

func TestCorrelations(t *testing.T) {
	mk := func(sign float64) *market.Series {
		s := &market.Series{}
		p := 100.0
		for i := range 120 {
			p *= math.Exp(sign * 0.01 * math.Sin(float64(i)*1.7))
			s.Bars = append(s.Bars, market.Bar{Date: "2026-" + string(rune('A'+i/26)) + string(rune('a'+i%26)), Close: p})
		}
		return s
	}
	got := correlations(map[string]*market.Series{"A": mk(1), "B": mk(1), "C": mk(-1)})
	if len(got) != 1 || got[0].A != "A" || got[0].B != "B" || got[0].Rho < 0.99 {
		t.Errorf("got %+v", got)
	}
}

func TestFees(t *testing.T) {
	// $1,000 into a token at 1% per side, entry $1: 990.099 tokens, $9.90 buy fee.
	qty := 1000 / (1 * 1.01)
	p := store.Position{Entry: 1, Quantity: qty, FeeRate: 0.01, FeeOpen: qty * 0.01, OpenedAt: "2026-01-01"}
	// Price unchanged: selling now costs both fees, about 2%.
	r := row(p, 1)
	if math.Abs(r.PnL-(-2*qty*0.01)) > 1e-9 || r.PnL > -19.7 || r.PnL < -19.9 {
		t.Errorf("flat price should lose both fees (~$19.80), got %.4f", r.PnL)
	}
	// Closed at $1.10: proceeds 1089.11, sell fee 10.89, net gain 1089.11-990.10-9.90-10.89 = 78.22.
	exit, sellFee := 1.10, qty*1.10*0.01
	p.Exit, p.FeeClose = &exit, &sellFee
	closed := "2026-02-01"
	p.ClosedAt = &closed
	r = row(p, exit)
	if math.Abs(r.PnL-78.22) > 0.01 {
		t.Errorf("net gain after fees: got %.2f, want 78.22", r.PnL)
	}
	if math.Abs(r.Fees-(qty*0.01+sellFee)) > 1e-9 {
		t.Errorf("fees %.4f", r.Fees)
	}
}

func TestFeeRateByKind(t *testing.T) {
	st := Defaults
	if st.FeeRate("asset", "AAPL") != 0.001 || st.FeeRate("asset", "BTC-USD") != 0.002 || st.FeeRate("token", "pumpCm") != 0.01 {
		t.Error("fee rates by asset type")
	}
}
