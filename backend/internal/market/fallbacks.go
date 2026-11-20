package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- CoinGecko

// CoinGecko is a crypto-only fallback. The public API only serves the last
// 365 days, so yearly forecasts are unavailable when it is the source.
type CoinGecko struct {
	c      *politeClient
	apiKey string
	ids    map[string]string
}

func NewCoinGecko(apiKey string) *CoinGecko {
	return &CoinGecko{c: newPoliteClient(2500 * time.Millisecond), apiKey: apiKey, ids: map[string]string{}}
}

func (g *CoinGecko) Name() string { return "coingecko" }

func (g *CoinGecko) headers() map[string]string {
	if g.apiKey == "" {
		return nil
	}
	return map[string]string{"x-cg-demo-api-key": g.apiKey}
}

func (g *CoinGecko) coinID(ctx context.Context, base string) (string, error) {
	if id, ok := g.ids[base]; ok {
		return id, nil
	}
	body, err := g.c.get(ctx, "https://api.coingecko.com/api/v3/search?query="+url.QueryEscape(base), g.headers())
	if err != nil {
		return "", err
	}
	var r struct {
		Coins []struct {
			ID     string `json:"id"`
			Symbol string `json:"symbol"`
			Rank   int    `json:"market_cap_rank"`
		} `json:"coins"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return "", err
	}
	best, bestRank := "", 1<<30
	for _, c := range r.Coins {
		if strings.EqualFold(c.Symbol, base) && c.Rank > 0 && c.Rank < bestRank {
			best, bestRank = c.ID, c.Rank
		}
	}
	if best == "" {
		return "", ErrNotFound
	}
	g.ids[base] = best
	return best, nil
}

func (g *CoinGecko) Fetch(ctx context.Context, symbol string) (*Series, error) {
	if !IsCrypto(symbol) {
		return nil, ErrNotFound
	}
	parts := strings.SplitN(strings.ToUpper(symbol), "-", 2)
	base, quote := parts[0], strings.ToLower(parts[1])
	// Yahoo disambiguates some tickers with a numeric id (PEPE24478-USD).
	if t := strings.TrimRight(base, "0123456789"); len(t) >= 2 && t != base {
		base = t
	}
	if quote == "usdt" {
		quote = "usd"
	}
	id, err := g.coinID(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("coingecko %s: %w", symbol, err)
	}
	u := fmt.Sprintf("https://api.coingecko.com/api/v3/coins/%s/market_chart?vs_currency=%s&days=365&interval=daily", id, quote)
	body, err := g.c.get(ctx, u, g.headers())
	if err != nil {
		return nil, fmt.Errorf("coingecko %s: %w", symbol, err)
	}
	var r struct {
		Prices  [][2]float64 `json:"prices"`
		Volumes [][2]float64 `json:"total_volumes"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	vol := map[string]float64{}
	for _, v := range r.Volumes {
		vol[CoinGeckoDay(int64(v[0]))] = v[1]
	}
	// market_chart gives one price per day; OHLC is approximated from
	// consecutive closes (open = previous close). ATR-style features are
	// therefore rougher with this provider.
	var bars []Bar
	idx := map[string]int{}
	for _, p := range r.Prices {
		d := CoinGeckoDay(int64(p[0]))
		b := Bar{Date: d, Open: p[1], High: p[1], Low: p[1], Close: p[1], Volume: vol[d]}
		if len(bars) > 0 {
			prev := bars[len(bars)-1].Close
			b.Open = prev
			b.High = max(prev, p[1])
			b.Low = min(prev, p[1])
		}
		if j, ok := idx[d]; ok {
			bars[j] = b
			continue
		}
		idx[d] = len(bars)
		bars = append(bars, b)
	}
	if len(bars) == 0 {
		return nil, ErrNotFound
	}
	return &Series{
		Meta: Meta{
			Symbol: strings.ToUpper(symbol), Name: id, Currency: strings.ToUpper(quote),
			Exchange: "CoinGecko aggregate", Class: ClassCrypto, Timezone: "UTC",
			Price: bars[len(bars)-1].Close, PriceTime: time.Now(), Provider: "coingecko",
		},
		Bars: bars, FetchedAt: time.Now(),
	}, nil
}

// --------------------------------------------------------------- Twelve Data

// TwelveData is a keyed fallback (free tier: 800 calls/day) covering most
// global exchanges. Only used when TWELVEDATA_API_KEY is set.
type TwelveData struct {
	c      *politeClient
	apiKey string
}

func NewTwelveData(apiKey string) *TwelveData {
	return &TwelveData{c: newPoliteClient(8 * time.Second), apiKey: apiKey}
}

func (t *TwelveData) Name() string { return "twelvedata" }

var tdExchange = map[string]string{
	"L": "LSE", "DE": "XETR", "PA": "Euronext", "AS": "Euronext", "BR": "Euronext",
	"T": "JPX", "HK": "HKEX", "NS": "NSE", "BO": "BSE", "TO": "TSX", "V": "TSXV",
	"AX": "ASX", "SA": "Bovespa", "JO": "JSE", "SW": "SIX", "MC": "BME", "KS": "KRX",
	"SS": "SSE", "SZ": "SZSE", "SI": "SGX", "MI": "MTA", "ST": "OMX", "CO": "OMXC",
	"OL": "OSE", "HE": "OMXH", "NZ": "NZX", "MX": "BMV", "TW": "TWSE",
}

var tdIndex = map[string]string{"^GSPC": "SPX", "^IXIC": "IXIC", "^DJI": "DJI", "^VIX": "VIX"}

func (t *TwelveData) mapSymbol(symbol string) (sym, exch string, ok bool) {
	s := strings.ToUpper(symbol)
	if v, ok := tdIndex[s]; ok {
		return v, "", true
	}
	if strings.HasPrefix(s, "^") || strings.Contains(s, "=") {
		return "", "", false
	}
	if IsCrypto(s) {
		return strings.Replace(s, "-", "/", 1), "", true
	}
	if i := strings.LastIndex(s, "."); i > 0 {
		ex, ok := tdExchange[s[i+1:]]
		if !ok {
			return "", "", false
		}
		return s[:i], ex, true
	}
	return s, "", true
}

func (t *TwelveData) Fetch(ctx context.Context, symbol string) (*Series, error) {
	if t.apiKey == "" {
		return nil, ErrNotFound
	}
	sym, exch, ok := t.mapSymbol(symbol)
	if !ok {
		return nil, ErrNotFound
	}
	q := url.Values{"symbol": {sym}, "interval": {"1day"}, "outputsize": {"5000"}, "apikey": {t.apiKey}, "adjust": {"all"}}
	if exch != "" {
		q.Set("exchange", exch)
	}
	body, err := t.c.get(ctx, "https://api.twelvedata.com/time_series?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("twelvedata %s: %w", symbol, err)
	}
	var r struct {
		Status string `json:"status"`
		Code   int    `json:"code"`
		Msg    string `json:"message"`
		Meta   struct {
			Currency string `json:"currency"`
			Exchange string `json:"exchange"`
			TZ       string `json:"exchange_timezone"`
			Type     string `json:"type"`
		} `json:"meta"`
		Values []map[string]string `json:"values"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if r.Status == "error" {
		if r.Code == 429 {
			return nil, ErrRateLimited
		}
		return nil, fmt.Errorf("twelvedata %s: %s: %w", symbol, r.Msg, ErrNotFound)
	}
	pf := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
	bars := make([]Bar, 0, len(r.Values))
	for _, v := range r.Values {
		d := v["datetime"]
		if len(d) > 10 {
			d = d[:10]
		}
		bars = append(bars, Bar{Date: d, Open: pf(v["open"]), High: pf(v["high"]), Low: pf(v["low"]), Close: pf(v["close"]), Volume: pf(v["volume"])})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })
	if len(bars) == 0 {
		return nil, ErrNotFound
	}
	class := ClassEquity
	switch {
	case IsCrypto(symbol):
		class = ClassCrypto
	case strings.Contains(strings.ToLower(r.Meta.Type), "etf"):
		class = ClassETF
	case strings.HasPrefix(symbol, "^"):
		class = ClassIndex
	}
	offset := 0
	if loc, err := time.LoadLocation(r.Meta.TZ); err == nil {
		_, offset = time.Now().In(loc).Zone()
	}
	return &Series{
		Meta: Meta{
			Symbol: strings.ToUpper(symbol), Name: strings.ToUpper(symbol), Currency: r.Meta.Currency,
			Exchange: r.Meta.Exchange, Class: class, Timezone: r.Meta.TZ, GMTOffset: offset,
			Price: bars[len(bars)-1].Close, PriceTime: time.Now(), Provider: "twelvedata",
		},
		Bars: bars, FetchedAt: time.Now(),
	}, nil
}

// ------------------------------------------------------------------ Nasdaq

// NasdaqPrices is a free fallback for US-listed stocks and ETFs (no key).
// It serves ten years of split-adjusted (not dividend-adjusted) daily bars.
// Major US indices are proxied by their ETFs (^GSPC → SPY).
type NasdaqPrices struct {
	c *politeClient
}

func NewNasdaqPrices() *NasdaqPrices {
	return &NasdaqPrices{c: newPoliteClient(700 * time.Millisecond)}
}

func (n *NasdaqPrices) Name() string { return "nasdaq" }

var nasdaqProxies = map[string]string{"^GSPC": "SPY", "^IXIC": "QQQ", "^DJI": "DIA", "^RUT": "IWM"}

func (n *NasdaqPrices) Fetch(ctx context.Context, symbol string) (*Series, error) {
	sym := strings.ToUpper(symbol)
	name := ""
	if p, ok := nasdaqProxies[sym]; ok {
		name = sym + " (proxied by " + p + ")"
		sym = p
	}
	if strings.ContainsAny(sym, "^=.") || IsCrypto(sym) {
		return nil, ErrNotFound
	}
	to := time.Now().UTC()
	from := to.AddDate(-10, 0, 0)
	var rows []map[string]string
	class := ClassEquity
	for _, ac := range []string{"stocks", "etf"} {
		u := fmt.Sprintf("https://api.nasdaq.com/api/quote/%s/historical?assetclass=%s&fromdate=%s&todate=%s&limit=9999",
			url.PathEscape(strings.ReplaceAll(sym, "-", ".")), ac, from.Format("2006-01-02"), to.Format("2006-01-02"))
		body, err := n.c.get(ctx, u, map[string]string{"Accept": "application/json"})
		if err != nil {
			return nil, fmt.Errorf("nasdaq %s: %w", symbol, err)
		}
		var r struct {
			Data *struct {
				TradesTable struct {
					Rows []map[string]string `json:"rows"`
				} `json:"tradesTable"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &r) == nil && r.Data != nil && len(r.Data.TradesTable.Rows) > 0 {
			rows = r.Data.TradesTable.Rows
			if ac == "etf" {
				class = ClassETF
			}
			break
		}
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("nasdaq %s: %w", symbol, ErrNotFound)
	}
	pf := func(s string) float64 {
		v, _ := strconv.ParseFloat(strings.NewReplacer("$", "", ",", "").Replace(strings.TrimSpace(s)), 64)
		return v
	}
	var bars []Bar
	for _, row := range rows {
		d, err := time.Parse("01/02/2006", row["date"])
		if err != nil {
			continue
		}
		b := Bar{Date: d.Format("2006-01-02"), Open: pf(row["open"]), High: pf(row["high"]), Low: pf(row["low"]), Close: pf(row["close"]), Volume: pf(row["volume"])}
		if b.Close <= 0 {
			continue
		}
		if b.Open <= 0 {
			b.Open = b.Close
		}
		if b.High <= 0 {
			b.High = max(b.Open, b.Close)
		}
		if b.Low <= 0 {
			b.Low = min(b.Open, b.Close)
		}
		bars = append(bars, b)
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].Date < bars[j].Date })
	if name == "" {
		name = sym
	}
	offset := -4 * 3600
	if loc, err := time.LoadLocation("America/New_York"); err == nil {
		_, offset = time.Now().In(loc).Zone()
	}
	return &Series{
		Meta: Meta{Symbol: strings.ToUpper(symbol), Name: name, Currency: "USD", Exchange: "US (Nasdaq data)", Class: class,
			Timezone: "America/New_York", GMTOffset: offset, Price: bars[len(bars)-1].Close, PriceTime: time.Now(), Provider: "nasdaq"},
		Bars: bars, FetchedAt: time.Now(),
	}, nil
}

// CoinGeckoDay dates a CoinGecko daily point. Its daily prices are stamped at
// 00:00 UTC, which is the close of the previous day (checked against Yahoo:
// CoinGecko's 5 Oct 00:00 BTC price equals Yahoo's 4 Oct close). Dating it on
// the stamp's own day would shift every coin one day late against Yahoo-priced
// assets, leaking the future into cross-asset comparisons.
func CoinGeckoDay(ms int64) string {
	t := time.UnixMilli(ms).UTC()
	if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
		t = t.AddDate(0, 0, -1)
	}
	return t.Format("2006-01-02")
}
