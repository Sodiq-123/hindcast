package token

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestLooksLikeAddress(t *testing.T) {
	for q, want := range map[string]bool{
		"pumpCmXqMfrsAkQ5r49WcJnRayYRqmXz6ae8H7H9Dfn": true,
		"0x532f27101965dd16442e59d40670faf5ebb142e4":  true,
		"AAPL": false, "0x123": false, "BTC-USD": false,
	} {
		if LooksLikeAddress(q) != want {
			t.Errorf("LooksLikeAddress(%q) = %v", q, !want)
		}
	}
}

// A fake brand-new token claiming huge liquidity must not make the real,
// established token look like the copycat.
func TestSearchCopycats(t *testing.T) {
	out := classifySearchT([]SearchResult{
		{Address: "fake", Symbol: "PUMP", Liquidity: 1e9, AgeDays: 2},
		{Address: "real", Symbol: "PUMP", Liquidity: 3.7e7, AgeDays: 448},
		{Address: "copy", Symbol: "PUMP", Liquidity: 4000, AgeDays: 90},
	})
	byAddr := map[string]SearchResult{}
	for _, r := range out {
		byAddr[r.Address] = r
	}
	if out[0].Address != "real" || byAddr["real"].Warning != "" {
		t.Fatalf("established token should rank first with no warning: %+v", out)
	}
	if !strings.Contains(byAddr["fake"].Warning, "fake") || !strings.Contains(byAddr["copy"].Warning, "copycat") {
		t.Fatalf("warnings wrong: %+v", out)
	}
}

func TestAssess(t *testing.T) {
	r := &Report{Symbol: "X", ChainLabel: "Solana", Liquidity: 4000, TotalLiq: 4000, MarketCap: 1e6, AgeDays: 3, HolderCount: 40,
		Holders: []Holder{{Address: "a", Percent: 0.9}}, Top1: 0.9, Top10: 0.95}
	assess(r)
	if r.Verdict != "Very high risk" || r.RiskScore < 60 {
		t.Fatalf("obvious rug not flagged: %s %d", r.Verdict, r.RiskScore)
	}
	ok := &Report{Symbol: "Y", ChainLabel: "Base", Liquidity: 5e6, TotalLiq: 5e6, MarketCap: 1e8, AgeDays: 800, HolderCount: 200000,
		Holders: []Holder{{Address: "b", Percent: 0.03}}, Top1: 0.03, Top10: 0.15}
	assess(ok)
	if ok.RiskScore > 10 || !strings.HasPrefix(ok.Verdict, "Fewer") {
		t.Fatalf("clean token over-flagged: %s %d %+v", ok.Verdict, ok.RiskScore, ok.Flags)
	}
	if !strings.Contains(ok.NoForecast, "No price forecast") {
		t.Fatal("must explain why there is no forecast")
	}
}

func TestSearchPrefersListedOriginal(t *testing.T) {
	pumpfun := &Listing{ID: "pump-fun", Name: "Pump.fun", Symbol: "PUMP", Rank: 39}
	bigpump := &Listing{ID: "big-pump", Name: "Big Pump", Symbol: "PUMP", Rank: 2885}
	out := classifySearchT([]SearchResult{
		{Address: "fake", Symbol: "PUMP", AgeDays: 2, Liquidity: 1e9},         // fake liquidity
		{Address: "old-clone", Symbol: "PUMP", AgeDays: 400, Liquidity: 50e6}, // more liquid, unlisted
		{Address: "big", Symbol: "PUMP", AgeDays: 300, Liquidity: 1e5, Listing: bigpump},
		{Address: "real", Symbol: "PUMP", AgeDays: 448, Liquidity: 37e6, Listing: pumpfun},
	})
	if out[0].Address != "real" || !out[0].Original || out[0].Warning != "" {
		t.Fatalf("the best-ranked listing must come first as the original, got %+v", out[0])
	}
	want := map[string]string{
		"old-clone": "Not the CoinGecko-listed PUMP (Pump.fun): likely a copycat",
		"big":       "A different project with the same ticker (CoinGecko lists the bigger one as Pump.fun)",
		"fake":      "Brand-new token reporting huge liquidity: very likely fake. Verify the address on the project's official site",
	}
	for _, r := range out[1:] {
		if r.Original || r.Warning != want[r.Address] {
			t.Errorf("%s: original=%v warning %q", r.Address, r.Original, r.Warning)
		}
	}
}

func TestRedKindsIgnoreNumbers(t *testing.T) {
	a := &Report{Flags: []Flag{{Level: "red", Title: "One wallet holds 27.6%"}, {Level: "red", Title: "Liquidity only $4K"}, {Level: "amber", Title: "x"}}}
	b := &Report{Flags: []Flag{{Level: "red", Title: "One wallet holds 31.2%"}, {Level: "red", Title: "Liquidity only $3K"}}}
	ka, kb := a.RedKinds(), b.RedKinds()
	if len(ka) != 2 || len(kb) != 2 {
		t.Fatalf("got %v %v", ka, kb)
	}
	for k := range kb {
		if !ka[k] {
			t.Errorf("%q should match an earlier flag of the same kind (%v)", k, ka)
		}
	}
}

func TestComputeRiskAndBench(t *testing.T) {
	// Benchmark alternates ±4%; the token moves exactly twice as much.
	var d []string
	var b, tk []float64
	pb, pt := 100.0, 1.0
	for i := 0; i < 200; i++ {
		d = append(d, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format("2006-01-02"))
		b, tk = append(b, pb), append(tk, pt)
		r := 0.04
		if i%2 == 1 {
			r = -0.04
		}
		pb *= 1 + r
		pt *= 1 + 2*r
	}
	st := computeRisk(d, tk)
	if math.Abs(st.WorstDay.Return+0.08) > 1e-9 || math.Abs(st.BestDay.Return-0.08) > 1e-9 {
		t.Errorf("worst/best day %v %v", st.WorstDay, st.BestDay)
	}
	if st.MaxDrawdown >= 0 || st.FromHigh > 0 {
		t.Errorf("drawdown %v from high %v", st.MaxDrawdown, st.FromHigh)
	}
	bs, ok := benchStat(d, tk, d, b)
	if !ok || math.Abs(bs.Beta-2) > 1e-6 || math.Abs(bs.Corr-1) > 1e-6 {
		t.Fatalf("beta %v corr %v", bs.Beta, bs.Corr)
	}
	if bs.DownDays == 0 || math.Abs(bs.TokenOnDown+0.08) > 1e-9 {
		t.Errorf("down days %d token on down %v", bs.DownDays, bs.TokenOnDown)
	}
}

func classifySearchT(out []SearchResult) []SearchResult { return classifySearch(out, nil) }

func TestElsewhereCoinMakesLookalikes(t *testing.T) {
	qtc := Listing{ID: "quantus", Name: "Quantus", Symbol: "QTC", Rank: 519, Elsewhere: true}
	out := classifySearch([]SearchResult{
		{Address: "old", Symbol: "QTC", Name: "QTC", AgeDays: 189, Liquidity: 25000},
		{Address: "new", Symbol: "QUANTUS", Name: "Quantus", AgeDays: 1, Liquidity: 9000},
	}, map[string]Listing{"QTC": qtc})
	for _, r := range out {
		if r.Original || !strings.Contains(r.Warning, "Not the real QTC") {
			t.Errorf("%s should be flagged as a lookalike of Quantus: original=%v warning=%q", r.Address, r.Original, r.Warning)
		}
	}
}
