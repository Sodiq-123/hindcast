package market

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Provider fetches full daily history for a symbol.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, symbol string) (*Series, error)
}

// Service tries providers in order, caching results on disk. If every
// provider fails it serves the last cached copy (flagged stale) rather than
// nothing, because a slightly old history is far better than no analysis.
type Service struct {
	providers []Provider
	yahoo     *Yahoo
	cache     Cache
	maxAge    time.Duration
	log       *slog.Logger

	inflight sync.Map // symbol -> *sync.Mutex, avoids duplicate concurrent fetches
	fngMu    sync.Mutex
	fng      map[string]float64
	fngAt    time.Time
	fngC     *politeClient
	fundC    *politeClient
	optC     *politeClient

	coolMu   sync.Mutex
	cooldown map[string]time.Time // provider → skip until
}

// providerCooldown is how long a rate-limited provider is skipped. Without
// it every symbol would burn ~30s of retries against a provider that is
// refusing us before reaching a fallback.
const providerCooldown = 10 * time.Minute

func (s *Service) coolingDown(p string) bool {
	s.coolMu.Lock()
	defer s.coolMu.Unlock()
	return time.Now().Before(s.cooldown[p])
}

func (s *Service) markRateLimited(p string) {
	s.coolMu.Lock()
	defer s.coolMu.Unlock()
	if s.cooldown == nil {
		s.cooldown = map[string]time.Time{}
	}
	s.cooldown[p] = time.Now().Add(providerCooldown)
}

func NewService(cache Cache, maxAge time.Duration, twelveDataKey, coinGeckoKey string, log *slog.Logger) *Service {
	y := NewYahoo()
	// CBOE first: it only answers for its own indices and is the primary source.
	ps := []Provider{NewCBOE(), y}
	if twelveDataKey != "" {
		ps = append(ps, NewTwelveData(twelveDataKey))
	}
	ps = append(ps, NewNasdaqPrices(), NewCoinGecko(coinGeckoKey))
	return &Service{providers: ps, yahoo: y, cache: cache, maxAge: maxAge, log: log, fngC: newPoliteClient(time.Second)}
}

// NewServiceWithProviders is used by tests and offline runs.
func NewServiceWithProviders(cache Cache, maxAge time.Duration, log *slog.Logger, ps ...Provider) *Service {
	return &Service{providers: ps, cache: cache, maxAge: maxAge, log: log, fngC: newPoliteClient(time.Second)}
}

// Series returns daily history. force=true bypasses the freshness check.
func (s *Service) Series(ctx context.Context, symbol string, force bool) (*Series, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, errors.New("empty symbol")
	}
	muAny, _ := s.inflight.LoadOrStore(symbol, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	cached, fresh := s.cache.Get(symbol, s.maxAge)
	if cached != nil && Coarse(cached) {
		cached, fresh = nil, false // never serve or splice quantised data
	}
	if fresh && !force {
		return cached, nil
	}
	if ser, ok := s.topUp(ctx, symbol, cached); ok {
		return ser, nil
	}
	var errs []string
	for _, p := range s.providers {
		if s.coolingDown(p.Name()) {
			errs = append(errs, p.Name()+": cooling down after rate limit")
			continue
		}
		ser, err := p.Fetch(ctx, symbol)
		if err == nil && Coarse(ser) {
			err = fmt.Errorf("prices are rounded too coarsely to analyse (%s)", p.Name())
			ser = nil
		}
		if errors.Is(err, ErrRateLimited) {
			s.markRateLimited(p.Name())
			s.log.Warn("provider rate-limited; skipping it for a while", "provider", p.Name(), "for", providerCooldown)
		}
		if err == nil && len(ser.Bars) > 0 {
			// Prefer a longer cached history over a short fallback history
			// (e.g. CoinGecko's 365 days) by splicing the fresh tail on.
			if cached != nil && len(cached.Bars) > len(ser.Bars) && cached.Meta.Provider != ser.Meta.Provider {
				ser = splice(cached, ser)
			}
			_ = s.cache.Put(symbol, ser)
			return ser, nil
		}
		errs = append(errs, fmt.Sprintf("%s: %v", p.Name(), err))
		if ctx.Err() != nil {
			break
		}
	}
	if cached != nil {
		s.log.Warn("serving stale cache", "symbol", symbol, "age", time.Since(cached.FetchedAt).Round(time.Minute), "errors", errs)
		return cached, nil
	}
	return nil, fmt.Errorf("no provider could serve %s (%s)", symbol, strings.Join(errs, "; "))
}

// RecentFetcher is implemented by providers that can return just the latest
// bars.
type RecentFetcher interface {
	FetchRecent(ctx context.Context, symbol string, since time.Time) (*Series, error)
}

// topUp refreshes a cached history by downloading only its last few weeks.
// Adjusted prices change across the whole history after a dividend or
// split; if the overlapping bars no longer match, it gives up and the caller
// downloads everything again.
func (s *Service) topUp(ctx context.Context, symbol string, cached *Series) (*Series, bool) {
	if cached == nil || len(cached.Bars) < 30 {
		return nil, false
	}
	var rf RecentFetcher
	var name string
	for _, p := range s.providers {
		if p.Name() == cached.Meta.Provider {
			rf, _ = p.(RecentFetcher)
			name = p.Name()
		}
	}
	if rf == nil || s.coolingDown(name) {
		return nil, false
	}
	last, err := time.Parse("2006-01-02", cached.Last().Date)
	if err != nil {
		return nil, false
	}
	recent, err := rf.FetchRecent(ctx, symbol, last.AddDate(0, 0, -21))
	if errors.Is(err, ErrRateLimited) {
		s.markRateLimited(name)
	}
	if err != nil || len(recent.Bars) == 0 {
		return nil, false
	}
	idx := make(map[string]int, len(cached.Bars))
	for i, b := range cached.Bars {
		idx[b.Date] = i
	}
	overlap, lastDate := 0, cached.Last().Date
	for _, b := range recent.Bars {
		// The cached last bar may have been an unfinished session, so it is
		// not used to check adjustments.
		if i, ok := idx[b.Date]; ok && b.Date != lastDate {
			if math.Abs(cached.Bars[i].Close/b.Close-1) > 1e-6 {
				return nil, false // history was re-adjusted: full refetch
			}
			overlap++
		}
	}
	if overlap < 3 {
		return nil, false
	}
	out := *cached
	out.Bars = append([]Bar(nil), cached.Bars...)
	for _, b := range recent.Bars {
		if i, ok := idx[b.Date]; ok {
			out.Bars[i] = b
		} else if b.Date > lastDate {
			out.Bars = append(out.Bars, b)
		}
	}
	out.Meta.Price, out.Meta.PriceTime = recent.Meta.Price, recent.Meta.PriceTime
	out.FetchedAt = time.Now()
	_ = s.cache.Put(symbol, &out)
	return &out, true
}

// Coarse reports whether a history is too rounded to analyse. Yahoo quotes
// sub-cent coins (PEPE, BONK, SHIB…) to two significant figures, so most
// days show "no change" and every model learns that the price never rises.
// Over the last year, more than 15% unchanged closes is rejected.
func Coarse(s *Series) bool {
	n := len(s.Bars)
	if n < 60 {
		return false
	}
	start := max(1, n-365)
	flat := 0
	for i := start; i < n; i++ {
		if s.Bars[i].Close == s.Bars[i-1].Close {
			flat++
		}
	}
	return float64(flat)/float64(n-start) > 0.15
}

func splice(old, fresh *Series) *Series {
	first := fresh.Bars[0].Date
	out := *fresh
	out.Bars = nil
	for _, b := range old.Bars {
		if b.Date < first {
			out.Bars = append(out.Bars, b)
		}
	}
	out.Bars = append(out.Bars, fresh.Bars...)
	out.Meta.Name = firstNonEmpty(old.Meta.Name, fresh.Meta.Name)
	out.Meta.GMTOffset = old.Meta.GMTOffset
	return &out
}

func (s *Service) Search(ctx context.Context, q string) ([]SearchResult, error) {
	if s.yahoo == nil {
		return nil, errors.New("search unavailable")
	}
	return s.yahoo.Search(ctx, q)
}

// Benchmark picks the market index that best represents systematic risk for
// the instrument, keyed off the Yahoo exchange suffix.
func Benchmark(symbol string, class AssetClass) string {
	s := strings.ToUpper(symbol)
	if class == ClassCrypto || IsCrypto(s) {
		if strings.HasPrefix(s, "BTC-") {
			return "^IXIC" // bitcoin trades as a high-beta tech/risk asset
		}
		return "BTC-USD"
	}
	if strings.HasPrefix(s, "^") || strings.HasSuffix(s, "=F") {
		return "^GSPC"
	}
	suffix := ""
	if i := strings.LastIndex(s, "."); i > 0 {
		suffix = s[i+1:]
	}
	m := map[string]string{
		"L": "^FTSE", "DE": "^GDAXI", "F": "^GDAXI", "PA": "^FCHI", "AS": "^AEX", "BR": "^BFX",
		"MI": "FTSEMIB.MI", "MC": "^IBEX", "SW": "^SSMI", "ST": "^OMX", "T": "^N225",
		"HK": "^HSI", "SS": "000001.SS", "SZ": "399001.SZ", "KS": "^KS11", "TW": "^TWII",
		"NS": "^NSEI", "BO": "^BSESN", "AX": "^AXJO", "NZ": "^NZ50", "TO": "^GSPTSE",
		"SA": "^BVSP", "MX": "^MXX", "JO": "^J203.JO", "SI": "^STI", "KL": "^KLSE",
		"JK": "^JKSE", "BK": "^SET.BK", "IS": "XU100.IS", "TA": "TA35.TA", "SR": "^TASI.SR",
	}
	if b, ok := m[suffix]; ok {
		return b
	}
	return "^GSPC"
}

// MacroSymbols are cross-asset series used as model inputs for every asset:
// volatility regime, rates, the dollar, and broad risk appetite.
var MacroSymbols = []string{"^VIX", "^TNX", "DX-Y.NYB", "^GSPC"}

// FearGreed returns the alternative.me crypto Fear & Greed index history
// keyed by UTC date. Cached for 6 hours.
func (s *Service) FearGreed(ctx context.Context) (map[string]float64, error) {
	s.fngMu.Lock()
	defer s.fngMu.Unlock()
	if s.fng != nil && time.Since(s.fngAt) < 6*time.Hour {
		return s.fng, nil
	}
	body, err := s.fngC.get(ctx, "https://api.alternative.me/fng/?limit=0&format=json", nil)
	if err != nil {
		if s.fng != nil {
			return s.fng, nil
		}
		return nil, err
	}
	var r struct {
		Data []struct {
			Value     string `json:"value"`
			Timestamp string `json:"timestamp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(r.Data))
	for _, d := range r.Data {
		v, _ := strconv.ParseFloat(d.Value, 64)
		ts, _ := strconv.ParseInt(d.Timestamp, 10, 64)
		out[time.Unix(ts, 0).UTC().Format("2006-01-02")] = v
	}
	s.fng, s.fngAt = out, time.Now()
	return out, nil
}
