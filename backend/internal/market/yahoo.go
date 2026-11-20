package market

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Yahoo uses the public chart endpoint. One request returns full daily
// history (range=max) with split/dividend adjusted closes for stocks, ETFs,
// indices, crypto (BTC-USD), futures (GC=F) and FX on ~70 exchanges.
type Yahoo struct {
	c     *politeClient
	hosts []string
}

func NewYahoo() *Yahoo {
	return &Yahoo{
		c:     newPoliteClient(700 * time.Millisecond),
		hosts: []string{"query1.finance.yahoo.com", "query2.finance.yahoo.com"},
	}
}

func (y *Yahoo) Name() string { return "yahoo" }

type yahooChart struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency             string  `json:"currency"`
				Symbol               string  `json:"symbol"`
				ExchangeName         string  `json:"exchangeName"`
				FullExchangeName     string  `json:"fullExchangeName"`
				InstrumentType       string  `json:"instrumentType"`
				RegularMarketTime    int64   `json:"regularMarketTime"`
				GMTOffset            int     `json:"gmtoffset"`
				ExchangeTimezoneName string  `json:"exchangeTimezoneName"`
				RegularMarketPrice   float64 `json:"regularMarketPrice"`
				LongName             string  `json:"longName"`
				ShortName            string  `json:"shortName"`
			} `json:"meta"`
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Open   []*float64 `json:"open"`
					High   []*float64 `json:"high"`
					Low    []*float64 `json:"low"`
					Close  []*float64 `json:"close"`
					Volume []*float64 `json:"volume"`
				} `json:"quote"`
				AdjClose []struct {
					AdjClose []*float64 `json:"adjclose"`
				} `json:"adjclose"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

func (y *Yahoo) Fetch(ctx context.Context, symbol string) (*Series, error) {
	return y.fetchFrom(ctx, symbol, 0)
}

// FetchRecent returns bars from `since` onwards (used to top up a cached
// history instead of downloading decades again).
func (y *Yahoo) FetchRecent(ctx context.Context, symbol string, since time.Time) (*Series, error) {
	return y.fetchFrom(ctx, symbol, since.Unix())
}

func (y *Yahoo) fetchFrom(ctx context.Context, symbol string, period1 int64) (*Series, error) {
	q := url.Values{}
	// range=max silently downsamples to quarterly bars; explicit periods
	// return full daily history.
	q.Set("period1", strconv.FormatInt(period1, 10))
	q.Set("period2", strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10))
	q.Set("interval", "1d")
	q.Set("events", "div,splits")
	q.Set("includeAdjustedClose", "true")
	var body []byte
	var err error
	for _, h := range y.hosts {
		u := fmt.Sprintf("https://%s/v8/finance/chart/%s?%s", h, url.PathEscape(symbol), q.Encode())
		body, err = y.c.get(ctx, u, nil)
		if err == nil || err == ErrNotFound {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("yahoo %s: %w", symbol, err)
	}
	var yc yahooChart
	if err := json.Unmarshal(body, &yc); err != nil {
		return nil, fmt.Errorf("yahoo decode: %w", err)
	}
	if yc.Chart.Error != nil {
		return nil, fmt.Errorf("yahoo %s: %s: %w", symbol, yc.Chart.Error.Description, ErrNotFound)
	}
	if len(yc.Chart.Result) == 0 || len(yc.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, fmt.Errorf("yahoo %s: empty result: %w", symbol, ErrNotFound)
	}
	r := yc.Chart.Result[0]
	m := r.Meta
	loc, lerr := time.LoadLocation(m.ExchangeTimezoneName)
	if lerr != nil {
		loc = time.FixedZone("exch", m.GMTOffset)
	}
	quote := r.Indicators.Quote[0]
	var adj []*float64
	if len(r.Indicators.AdjClose) > 0 {
		adj = r.Indicators.AdjClose[0].AdjClose
	}
	bars := make([]Bar, 0, len(r.Timestamp))
	seen := map[string]int{}
	for i, ts := range r.Timestamp {
		cl := at(quote.Close, i)
		if cl <= 0 {
			continue
		}
		factor := 1.0
		if a := at(adj, i); a > 0 {
			factor = a / cl
		}
		o, h, l := at(quote.Open, i), at(quote.High, i), at(quote.Low, i)
		if o <= 0 {
			o = cl
		}
		if h <= 0 {
			h = max(o, cl)
		}
		if l <= 0 {
			l = min(o, cl)
		}
		b := Bar{
			Date:   time.Unix(ts, 0).In(loc).Format("2006-01-02"),
			Open:   o * factor,
			High:   h * factor,
			Low:    l * factor,
			Close:  cl * factor,
			Volume: at(quote.Volume, i),
		}
		// Yahoo occasionally emits the live bar twice; keep the latest.
		if j, ok := seen[b.Date]; ok {
			bars[j] = b
			continue
		}
		seen[b.Date] = len(bars)
		bars = append(bars, b)
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("yahoo %s: no bars: %w", symbol, ErrNotFound)
	}
	if !looksDaily(bars) {
		return nil, fmt.Errorf("yahoo %s: response is not daily bars", symbol)
	}
	name := m.LongName
	if name == "" {
		name = m.ShortName
	}
	price := m.RegularMarketPrice
	if price == 0 {
		price = bars[len(bars)-1].Close
	}
	return &Series{
		Meta: Meta{
			Symbol:    strings.ToUpper(m.Symbol),
			Name:      name,
			Currency:  m.Currency,
			Exchange:  firstNonEmpty(m.FullExchangeName, m.ExchangeName),
			Class:     classFromYahoo(m.InstrumentType),
			Timezone:  m.ExchangeTimezoneName,
			GMTOffset: m.GMTOffset,
			Price:     price,
			PriceTime: time.Unix(m.RegularMarketTime, 0),
			Provider:  "yahoo",
		},
		Bars:      bars,
		FetchedAt: time.Now(),
	}, nil
}

// SearchResult is one symbol-lookup hit.
type SearchResult struct {
	Symbol   string `json:"symbol"`
	Name     string `json:"name"`
	Exchange string `json:"exchange"`
	Type     string `json:"type"`
}

func (y *Yahoo) Search(ctx context.Context, query string) ([]SearchResult, error) {
	u := "https://query2.finance.yahoo.com/v1/finance/search?quotesCount=10&newsCount=0&q=" + url.QueryEscape(query)
	body, err := y.c.get(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			ShortName string `json:"shortname"`
			LongName  string `json:"longname"`
			ExchDisp  string `json:"exchDisp"`
			TypeDisp  string `json:"typeDisp"`
		} `json:"quotes"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(r.Quotes))
	for _, q := range r.Quotes {
		if q.Symbol == "" {
			continue
		}
		out = append(out, SearchResult{q.Symbol, firstNonEmpty(q.LongName, q.ShortName), q.ExchDisp, q.TypeDisp})
	}
	return out, nil
}

// looksDaily guards against providers silently returning coarser bars.
func looksDaily(bars []Bar) bool {
	if len(bars) < 30 {
		return true
	}
	tail := bars[len(bars)-30:]
	first, _ := time.Parse("2006-01-02", tail[0].Date)
	last, _ := time.Parse("2006-01-02", tail[len(tail)-1].Date)
	return last.Sub(first) < 70*24*time.Hour
}

func classFromYahoo(t string) AssetClass {
	switch strings.ToUpper(t) {
	case "EQUITY":
		return ClassEquity
	case "ETF":
		return ClassETF
	case "INDEX":
		return ClassIndex
	case "CRYPTOCURRENCY":
		return ClassCrypto
	case "FUTURE":
		return ClassFuture
	case "MUTUALFUND":
		return ClassFund
	}
	return ClassUnknown
}

func at(xs []*float64, i int) float64 {
	if i < len(xs) && xs[i] != nil {
		return *xs[i]
	}
	return 0
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
