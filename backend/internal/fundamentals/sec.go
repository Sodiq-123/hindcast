// Package fundamentals reads company financials from SEC EDGAR (US-listed
// companies' XBRL filings: free, official, back to about 2009) and turns them
// into point-in-time quarterly series: every number is dated by when it was
// first filed, so a backtest can only use what investors could have known.
package fundamentals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNoFilings = errors.New("no SEC filings for this symbol (only US-listed companies file with the SEC)")

// Quarter is the trailing-twelve-month picture at one fiscal quarter end.
type Quarter struct {
	End   string `json:"end"`   // fiscal quarter end (YYYY-MM-DD)
	Known string `json:"known"` // first date every TTM component had been filed

	Revenue   float64 `json:"revenue"`   // trailing twelve months
	OpIncome  float64 `json:"opIncome"`  // TTM operating income
	NetIncome float64 `json:"netIncome"` // TTM net income
	OCF       float64 `json:"ocf"`       // TTM operating cash flow
	Capex     float64 `json:"capex"`     // TTM capital expenditure (positive)
	FCF       float64 `json:"fcf"`       // OCF − capex

	Shares float64 `json:"shares"` // diluted weighted average shares in the quarter (as later restated)
	Debt   float64 `json:"debt"`   // long-term debt at quarter end
	Cash   float64 `json:"cash"`
	Equity float64 `json:"equity"`
}

// Company is a company's quarterly history.
type Company struct {
	Symbol    string    `json:"symbol"`
	CIK       int       `json:"cik"`
	Name      string    `json:"name"`
	Quarters  []Quarter `json:"quarters"` // oldest first
	FetchedAt time.Time `json:"fetchedAt"`
}

// Cache stores parsed companies (Postgres implements it).
type Cache interface {
	LoadFundamentals(ctx context.Context, symbol string) (*Company, error)
	SaveFundamentals(ctx context.Context, c *Company) error
	Setting(ctx context.Context, key string, v any) (bool, error)
	SetSetting(ctx context.Context, key string, v any) error
}

type Service struct {
	Cache     Cache
	UserAgent string // SEC asks for "Name contact@email"
	MaxAge    time.Duration

	http *http.Client
	mu   sync.Mutex
	last time.Time // SEC allows 10 requests a second; stay well below
	ciks map[string]cikEntry
}

type cikEntry struct {
	CIK  int    `json:"cik"`
	Name string `json:"name"`
}

func NewService(cache Cache, userAgent string) *Service {
	if userAgent == "" {
		userAgent = "Hindcast personal-research-tool contact@example.com"
	}
	return &Service{Cache: cache, UserAgent: userAgent, MaxAge: 7 * 24 * time.Hour, http: &http.Client{Timeout: 60 * time.Second}}
}

func (s *Service) get(ctx context.Context, u string) ([]byte, error) {
	s.mu.Lock()
	if wait := 250*time.Millisecond - time.Since(s.last); wait > 0 {
		time.Sleep(wait)
	}
	s.last = time.Now()
	s.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", s.UserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNoFilings
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SEC %s: %s", u, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

const cikKey = "sec_tickers"

// cik maps a ticker to the SEC's company id (refreshed weekly).
func (s *Service) cik(ctx context.Context, symbol string) (cikEntry, error) {
	s.mu.Lock()
	m := s.ciks
	s.mu.Unlock()
	if m == nil {
		var snap struct {
			At  time.Time           `json:"at"`
			Map map[string]cikEntry `json:"map"`
		}
		if ok, err := s.Cache.Setting(ctx, cikKey, &snap); err == nil && ok && time.Since(snap.At) < 7*24*time.Hour {
			m = snap.Map
		} else {
			body, err := s.get(ctx, "https://www.sec.gov/files/company_tickers.json")
			if err != nil {
				if snap.Map != nil {
					m = snap.Map
				} else {
					return cikEntry{}, err
				}
			} else {
				var raw map[string]struct {
					CIK    int    `json:"cik_str"`
					Ticker string `json:"ticker"`
					Title  string `json:"title"`
				}
				if err := json.Unmarshal(body, &raw); err != nil {
					return cikEntry{}, err
				}
				m = map[string]cikEntry{}
				for _, r := range raw {
					t := strings.ToUpper(r.Ticker)
					if _, dup := m[t]; !dup {
						m[t] = cikEntry{r.CIK, r.Title}
					}
				}
				_ = s.Cache.SetSetting(ctx, cikKey, map[string]any{"at": time.Now().UTC(), "map": m})
			}
		}
		s.mu.Lock()
		s.ciks = m
		s.mu.Unlock()
	}
	sym := strings.ToUpper(strings.TrimSpace(symbol))
	for _, k := range []string{sym, strings.ReplaceAll(sym, "-", "."), strings.ReplaceAll(sym, ".", "-")} {
		if e, ok := m[k]; ok {
			return e, nil
		}
	}
	return cikEntry{}, ErrNoFilings
}

// Get returns a company's quarterly history, from the cache if recent.
func (s *Service) Get(ctx context.Context, symbol string, force bool) (*Company, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if strings.HasSuffix(symbol, "-USD") || strings.ContainsAny(symbol, "=^") {
		return nil, ErrNoFilings
	}
	if !force {
		if c, err := s.Cache.LoadFundamentals(ctx, symbol); err == nil && c != nil && time.Since(c.FetchedAt) < s.MaxAge {
			return c, nil
		}
	}
	e, err := s.cik(ctx, symbol)
	if err != nil {
		return nil, err
	}
	body, err := s.get(ctx, fmt.Sprintf("https://data.sec.gov/api/xbrl/companyfacts/CIK%010d.json", e.CIK))
	if err != nil {
		return nil, err
	}
	c, err := Parse(body)
	if err != nil {
		return nil, err
	}
	c.Symbol, c.CIK, c.FetchedAt = symbol, e.CIK, time.Now().UTC()
	if c.Name == "" {
		c.Name = e.Name
	}
	if len(c.Quarters) == 0 {
		return nil, fmt.Errorf("%s: SEC filings contain no usable quarterly figures", symbol)
	}
	_ = s.Cache.SaveFundamentals(ctx, c)
	return c, nil
}

// ------------------------------------------------------------------ parsing

type fact struct {
	Start string  `json:"start"`
	End   string  `json:"end"`
	Val   float64 `json:"val"`
	Filed string  `json:"filed"`
	Form  string  `json:"form"`
}

type facts struct {
	Name  string `json:"entityName"`
	Facts map[string]map[string]struct {
		Units map[string][]fact `json:"units"`
	} `json:"facts"`
}

// concepts are tried in order; companies switch tags over the years, so the
// first one that has a value for a period wins for that period.
var (
	revenueTags = []string{"RevenueFromContractWithCustomerExcludingAssessedTax", "Revenues", "SalesRevenueNet",
		"RevenueFromContractWithCustomerIncludingAssessedTax", "SalesRevenueGoodsNet", "RevenuesNetOfInterestExpense"}
	opIncomeTags  = []string{"OperatingIncomeLoss"}
	netIncomeTags = []string{"NetIncomeLoss", "ProfitLoss", "NetIncomeLossAvailableToCommonStockholdersBasic"}
	ocfTags       = []string{"NetCashProvidedByUsedInOperatingActivities", "NetCashProvidedByUsedInOperatingActivitiesContinuingOperations"}
	capexTags     = []string{"PaymentsToAcquirePropertyPlantAndEquipment", "PaymentsToAcquireProductiveAssets"}
	sharesTags    = []string{"WeightedAverageNumberOfDilutedSharesOutstanding"}
	debtTags      = []string{"LongTermDebt", "LongTermDebtNoncurrent"}
	cashTags      = []string{"CashAndCashEquivalentsAtCarryingValue", "CashCashEquivalentsRestrictedCashAndRestrictedCashEquivalents"}
	equityTags    = []string{"StockholdersEquity", "StockholdersEquityIncludingPortionAttributableToNoncontrollingInterest"}
)

func days(a, b string) int {
	ta, e1 := time.Parse("2006-01-02", a)
	tb, e2 := time.Parse("2006-01-02", b)
	if e1 != nil || e2 != nil {
		return -1
	}
	return int(tb.Sub(ta).Hours()/24 + 0.5)
}

type pv struct {
	val   float64
	filed string
}

// quarters turns duration facts into quarter values keyed by quarter end.
// 10-Q cash-flow statements report year-to-date figures, so a quarter is
// either a reported ~3-month value or the difference of two year-to-date
// values with the same start. Each value keeps the earliest filing that
// reported it (point-in-time); a differenced value is known when both parts
// were.
func quarters(fs []fact) map[string]pv {
	// earliest filing for each (start, end)
	first := map[[2]string]pv{}
	for _, f := range fs {
		if f.Start == "" || f.End == "" || !strings.HasPrefix(f.Form, "10-") {
			continue
		}
		k := [2]string{f.Start, f.End}
		if cur, ok := first[k]; !ok || f.Filed < cur.filed {
			first[k] = pv{f.Val, f.Filed}
		}
	}
	out := map[string]pv{}
	put := func(end string, v pv) {
		if cur, ok := out[end]; !ok || v.filed < cur.filed {
			out[end] = v
		}
	}
	byStart := map[string][]string{}
	for k, v := range first {
		d := days(k[0], k[1])
		if d >= 80 && d <= 100 {
			put(k[1], v)
		}
		byStart[k[0]] = append(byStart[k[0]], k[1])
	}
	for start, ends := range byStart {
		sort.Strings(ends)
		for i := 1; i < len(ends); i++ {
			a, b := first[[2]string{start, ends[i-1]}], first[[2]string{start, ends[i]}]
			if d := days(ends[i-1], ends[i]); d < 80 || d > 100 {
				continue
			}
			if _, ok := out[ends[i]]; ok {
				continue // a direct 3-month figure is better
			}
			filed := a.filed
			if b.filed > filed {
				filed = b.filed
			}
			out[ends[i]] = pv{b.val - a.val, filed}
		}
	}
	return out
}

// instants returns point-in-time balance-sheet values by date.
func instants(fs []fact) map[string]pv {
	out := map[string]pv{}
	for _, f := range fs {
		if f.Start != "" || f.End == "" || !strings.HasPrefix(f.Form, "10-") {
			continue
		}
		if cur, ok := out[f.End]; !ok || f.Filed < cur.filed {
			out[f.End] = pv{f.Val, f.Filed}
		}
	}
	return out
}

// latestRestated keeps the most recently filed value per period (used for
// share counts, which later filings restate for splits).
func latestRestated(fs []fact) map[string]float64 {
	type lv struct {
		v     float64
		filed string
	}
	best := map[string]lv{}
	for _, f := range fs {
		d := days(f.Start, f.End)
		if d < 80 || d > 100 {
			continue
		}
		if cur, ok := best[f.End]; !ok || f.Filed > cur.filed {
			best[f.End] = lv{f.Val, f.Filed}
		}
	}
	out := map[string]float64{}
	for k, v := range best {
		out[k] = v.v
	}
	return out
}

func merged(g map[string]struct {
	Units map[string][]fact `json:"units"`
}, tags []string, unit string, f func([]fact) map[string]pv) map[string]pv {
	out := map[string]pv{}
	for _, t := range tags {
		c, ok := g[t]
		if !ok {
			continue
		}
		for k, v := range f(c.Units[unit]) {
			if _, have := out[k]; !have {
				out[k] = v
			}
		}
	}
	return out
}

// Parse turns an SEC companyfacts document into quarterly TTM series.
func Parse(body []byte) (*Company, error) {
	var doc facts
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	g := doc.Facts["us-gaap"]
	rev := merged(g, revenueTags, "USD", quarters)
	op := merged(g, opIncomeTags, "USD", quarters)
	ni := merged(g, netIncomeTags, "USD", quarters)
	ocf := merged(g, ocfTags, "USD", quarters)
	capex := merged(g, capexTags, "USD", quarters)
	debt := merged(g, debtTags, "USD", instants)
	cash := merged(g, cashTags, "USD", instants)
	eq := merged(g, equityTags, "USD", instants)
	shares := map[string]float64{}
	for _, t := range sharesTags {
		if c, ok := g[t]; ok {
			for k, v := range latestRestated(c.Units["shares"]) {
				shares[k] = v
			}
		}
	}

	// Quarter ends come from revenue or net income, whichever is available.
	endSet := map[string]bool{}
	for k := range rev {
		endSet[k] = true
	}
	for k := range ni {
		endSet[k] = true
	}
	ends := make([]string, 0, len(endSet))
	for k := range endSet {
		ends = append(ends, k)
	}
	sort.Strings(ends)

	c := &Company{Name: doc.Name}
	ttm := func(m map[string]pv, i int) (float64, string) {
		if i < 3 {
			return math.NaN(), ""
		}
		sum, filed := 0.0, ""
		for j := i - 3; j <= i; j++ {
			v, ok := m[ends[j]]
			if !ok {
				return math.NaN(), ""
			}
			sum += v.val
			if v.filed > filed {
				filed = v.filed
			}
		}
		// The four quarters must be consecutive (about a year end to end).
		if d := days(ends[i-3], ends[i]); d < 250 || d > 290 {
			return math.NaN(), ""
		}
		return sum, filed
	}
	nearest := func(m map[string]pv, end string) float64 {
		if v, ok := m[end]; ok {
			return v.val
		}
		return math.NaN()
	}
	for i := range ends {
		q := Quarter{End: ends[i]}
		var known string
		for _, x := range []struct {
			dst *float64
			m   map[string]pv
		}{{&q.Revenue, rev}, {&q.OpIncome, op}, {&q.NetIncome, ni}, {&q.OCF, ocf}, {&q.Capex, capex}} {
			v, f := ttm(x.m, i)
			*x.dst = v
			if f > known {
				known = f
			}
		}
		if math.IsNaN(q.Revenue) && math.IsNaN(q.NetIncome) {
			continue
		}
		q.Known = known
		q.FCF = q.OCF - q.Capex
		if math.IsNaN(q.Capex) {
			q.FCF = math.NaN()
		}
		q.Shares = math.NaN()
		if v, ok := shares[ends[i]]; ok {
			q.Shares = v
		}
		q.Debt, q.Cash, q.Equity = nearest(debt, ends[i]), nearest(cash, ends[i]), nearest(eq, ends[i])
		c.Quarters = append(c.Quarters, q)
	}
	return c, nil
}

// KnownAt returns the latest quarter whose TTM figures had all been filed by
// date (YYYY-MM-DD), or nil.
func (c *Company) KnownAt(date string) *Quarter {
	var best *Quarter
	for i := range c.Quarters {
		q := &c.Quarters[i]
		if q.Known != "" && q.Known <= date && (best == nil || q.End > best.End) {
			best = q
		}
	}
	return best
}

// MarshalJSON writes NaN as null.
func (q Quarter) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	for k, v := range map[string]float64{"revenue": q.Revenue, "opIncome": q.OpIncome, "netIncome": q.NetIncome, "ocf": q.OCF,
		"capex": q.Capex, "fcf": q.FCF, "shares": q.Shares, "debt": q.Debt, "cash": q.Cash, "equity": q.Equity} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			m[k] = nil
		} else {
			m[k] = v
		}
	}
	m["end"], m["known"] = q.End, q.Known
	return json.Marshal(m)
}

// UnmarshalJSON reads null as NaN.
func (q *Quarter) UnmarshalJSON(b []byte) error {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	num := func(k string) float64 {
		if v, ok := m[k].(float64); ok {
			return v
		}
		return math.NaN()
	}
	q.End, _ = m["end"].(string)
	q.Known, _ = m["known"].(string)
	q.Revenue, q.OpIncome, q.NetIncome, q.OCF = num("revenue"), num("opIncome"), num("netIncome"), num("ocf")
	q.Capex, q.FCF, q.Shares = num("capex"), num("fcf"), num("shares")
	q.Debt, q.Cash, q.Equity = num("debt"), num("cash"), num("equity")
	return nil
}
