package market

import "testing"

func TestVerifyMemeResults(t *testing.T) {
	res := []SearchResult{
		{Symbol: "PUMP", Name: "ProPetro Holding Corp.", Type: "EQUITY", Exchange: "NYQ"},
		{Symbol: "PUMP33991-USD", Name: "PUMP USD", Type: "Cryptocurrency"},
	}
	out := VerifyMemeResults("pump", res)
	if out[0].Symbol != "PUMP36507-USD" {
		t.Fatalf("verified Pump.fun ticker should come first: %+v", out)
	}
	if out[1].Name != "ProPetro Holding Corp." || out[2].Name != "PUMP USD (a different coin, not Pump.fun)" {
		t.Errorf("labels: %+v", out)
	}
	if got := VerifyMemeResults("apple", res); len(got) != 2 {
		t.Error("non-meme queries must be untouched")
	}
}

func TestCoinGeckoDay(t *testing.T) {
	if got := CoinGeckoDay(1791158400000); got != "2026-10-04" { // 2026-10-05 00:00:00 UTC
		t.Errorf("midnight stamp is the previous day's close, got %s", got)
	}
	if got := CoinGeckoDay(1791158400000 + 3600_000*13); got != "2026-10-05" { // the intraday "now" point
		t.Errorf("intraday point keeps its day, got %s", got)
	}
}
