// Package token builds a safety and risk report for any token on Solana,
// Ethereum or Base, looked up by contract address. It is built for meme
// coins: there is too little history to forecast their price, so the report
// answers the questions that decide most meme-coin losses instead - can I
// sell it, can the creator dilute or freeze me, is there real liquidity, who
// holds the supply, and how violently does it move.
//
// Sources (free, keyless): DexScreener (pairs, price, liquidity, volume),
// GoPlus Security (contract permissions, taxes, holders), GeckoTerminal
// (price history of the main pool).
package token

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Supported chains: DexScreener id → GoPlus / GeckoTerminal ids.
var chains = map[string]struct{ goplus, gecko, label string }{
	"solana":   {"solana", "solana", "Solana"},
	"ethereum": {"1", "eth", "Ethereum"},
	"base":     {"8453", "base", "Base"},
}

var (
	evmAddr    = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	solanaAddr = regexp.MustCompile(`^[1-9A-HJ-NP-Za-km-z]{32,44}$`)
)

// LooksLikeAddress reports whether q is a contract address we can look up.
func LooksLikeAddress(q string) bool {
	q = strings.TrimSpace(q)
	return evmAddr.MatchString(q) || solanaAddr.MatchString(q)
}

type Flag struct {
	Level  string `json:"level"` // red | amber | green
	Area   string `json:"area"`  // Contract | Holders | Liquidity | Age | Price
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

type Holder struct {
	Address string  `json:"address"`
	Percent float64 `json:"percent"` // fraction of supply
	Tag     string  `json:"tag,omitempty"`
	Locked  bool    `json:"locked"`
	IsPool  bool    `json:"isPool"`
}

type Candle struct {
	Time   int64   `json:"time"` // unix seconds
	Open   float64 `json:"open"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Close  float64 `json:"close"`
	Volume float64 `json:"volume"`
}

type History struct {
	Interval       string   `json:"interval"` // day | hour
	Candles        []Candle `json:"candles"`
	Return7d       *float64 `json:"return7d,omitempty"`
	Return30d      *float64 `json:"return30d,omitempty"`
	ReturnAll      float64  `json:"returnAll"` // over the available window
	High           float64  `json:"high"`      // highest price in the window
	FromHigh       float64  `json:"fromHigh"`  // current price vs that high
	MaxDrawdown    float64  `json:"maxDrawdown"`
	AnnualizedVol  float64  `json:"annualizedVol"`
	TypicalDayMove float64  `json:"typicalDayMove"`
	WindowDays     float64  `json:"windowDays"`
}

type Report struct {
	Address     string             `json:"address"`
	Chain       string             `json:"chain"`
	ChainLabel  string             `json:"chainLabel"`
	Name        string             `json:"name"`
	Symbol      string             `json:"symbol"`
	FetchedAt   time.Time          `json:"fetchedAt"`
	PriceUSD    float64            `json:"priceUsd"`
	MarketCap   float64            `json:"marketCap"`
	FDV         float64            `json:"fdv"`
	Liquidity   float64            `json:"liquidity"` // USD, main pool
	TotalLiq    float64            `json:"totalLiquidity"`
	Volume24h   float64            `json:"volume24h"`
	Buys24h     int                `json:"buys24h"`
	Sells24h    int                `json:"sells24h"`
	Change      map[string]float64 `json:"change"` // m5, h1, h6, h24 (fractions)
	Pairs       int                `json:"pairs"`
	DexID       string             `json:"dexId"`
	PairAddress string             `json:"pairAddress"`
	PairURL     string             `json:"pairUrl"`
	Launched    time.Time          `json:"launched"`
	AgeDays     float64            `json:"ageDays"`
	HolderCount int                `json:"holderCount"`
	Top1        float64            `json:"top1"`  // largest non-pool holder
	Top10       float64            `json:"top10"` // ten largest non-pool holders
	Holders     []Holder           `json:"holders"`
	LPLocked    *float64           `json:"lpLocked,omitempty"` // EVM v2-style pools
	History     *History           `json:"history,omitempty"`
	Flags       []Flag             `json:"flags"`
	RiskScore   int                `json:"riskScore"` // 0 (fewer warning signs) … 100
	Verdict     string             `json:"verdict"`
	Summary     string             `json:"summary"`
	NoForecast  string             `json:"noForecast"`
	Notes       []string           `json:"notes,omitempty"`
	Listing     *Listing           `json:"listing,omitempty"`   // CoinGecko listing of this exact address
	Lookalike   *Listing           `json:"lookalike,omitempty"` // the listed coin this one shares a ticker with
	Risk        *RiskStats         `json:"risk,omitempty"`      // measured volatility, drawdowns and co-movement`
}

type Service struct {
	http     *http.Client
	Registry *Registry   // CoinGecko listings for identifying originals; nil disables
	Prices   PriceSource // daily closes for risk statistics; nil limits them to DEX candles
}

func NewService() *Service { return &Service{http: &http.Client{Timeout: 25 * time.Second}} }

func (s *Service) getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Hindcast/1.0")
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: http %d", req.URL.Host, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// ------------------------------------------------------------- DexScreener

type dsPair struct {
	ChainID     string `json:"chainId"`
	DexID       string `json:"dexId"`
	URL         string `json:"url"`
	PairAddress string `json:"pairAddress"`
	BaseToken   struct {
		Address string `json:"address"`
		Name    string `json:"name"`
		Symbol  string `json:"symbol"`
	} `json:"baseToken"`
	QuoteToken struct {
		Symbol string `json:"symbol"`
	} `json:"quoteToken"`
	PriceUSD string `json:"priceUsd"`
	Txns     struct {
		H24 struct{ Buys, Sells int } `json:"h24"`
	} `json:"txns"`
	Volume      struct{ H24 float64 } `json:"volume"`
	PriceChange map[string]float64    `json:"priceChange"`
	Liquidity   struct{ USD float64 } `json:"liquidity"`
	FDV         float64               `json:"fdv"`
	MarketCap   float64               `json:"marketCap"`
	PairCreated int64                 `json:"pairCreatedAt"`
	Labels      []string              `json:"labels"`
}

// SearchResult is one token from a name/symbol search.
type SearchResult struct {
	Address   string   `json:"address"`
	Chain     string   `json:"chain"`
	Name      string   `json:"name"`
	Symbol    string   `json:"symbol"`
	Liquidity float64  `json:"liquidity"`
	MarketCap float64  `json:"marketCap"`
	Volume24h float64  `json:"volume24h"`
	AgeDays   float64  `json:"ageDays"`
	Listing   *Listing `json:"listing,omitempty"` // CoinGecko listing of this exact address
	Original  bool     `json:"original"`          // the coin people most likely mean by this ticker
	Warning   string   `json:"warning,omitempty"`
}

func addPairs(best map[string]*SearchResult, pairs []dsPair) {
	for _, p := range pairs {
		if _, ok := chains[p.ChainID]; !ok {
			continue
		}
		key := p.ChainID + ":" + strings.ToLower(p.BaseToken.Address)
		cur := best[key]
		if cur == nil {
			cur = &SearchResult{Address: p.BaseToken.Address, Chain: p.ChainID, Name: p.BaseToken.Name, Symbol: p.BaseToken.Symbol}
			best[key] = cur
		}
		cur.Liquidity += p.Liquidity.USD
		cur.Volume24h += p.Volume.H24
		cur.MarketCap = math.Max(cur.MarketCap, firstPositive(p.MarketCap, p.FDV))
		if p.PairCreated > 0 {
			age := time.Since(time.UnixMilli(p.PairCreated)).Hours() / 24
			cur.AgeDays = math.Max(cur.AgeDays, age)
		}
	}
}

// Search finds tokens by name or symbol on the supported chains. The coin
// people most likely mean comes first: the best-ranked CoinGecko listing
// with that ticker, or failing that the most liquid token over a month old.
// Copycats and fakes are labelled, because impersonation is the most common
// meme-coin scam.
func (s *Service) Search(ctx context.Context, q string) ([]SearchResult, error) {
	var r struct {
		Pairs []dsPair `json:"pairs"`
	}
	if err := s.getJSON(ctx, "https://api.dexscreener.com/latest/dex/search?q="+url.QueryEscape(q), &r); err != nil {
		return nil, err
	}
	best := map[string]*SearchResult{}
	addPairs(best, r.Pairs)
	elsewhere := map[string]Listing{}

	if s.Registry != nil {
		if listed, err := s.Registry.Search(ctx, q); err == nil {
			// Make sure the best-known listed coins are in the results even
			// if DexScreener's search is flooded with lookalikes.
			added := 0
			for _, l := range listed {
				if added >= 3 || l.Rank == 0 {
					break
				}
				if l.Elsewhere {
					continue
				}
				if best[l.Chain+":"+strings.ToLower(l.Address)] != nil {
					continue
				}
				if pairs, err := s.pairs(ctx, l.Address); err == nil {
					addPairs(best, pairs)
					added++
				}
			}
			byAddr := map[string]Listing{}
			for _, l := range listed {
				if !l.Elsewhere {
					byAddr[strings.ToLower(l.Address)] = l
				}
			}
			// A ticker whose best-ranked listing is on another blockchain
			// has no original among these results.
			for _, l := range listed {
				sym := strings.ToUpper(l.Symbol)
				if _, seen := elsewhere[sym]; seen {
					continue
				}
				if l.Elsewhere {
					elsewhere[sym] = l
				} else {
					elsewhere[sym] = Listing{} // a covered chain ranks first: normal rules
				}
			}
			for _, v := range best {
				if l, ok := byAddr[strings.ToLower(v.Address)]; ok {
					v.Listing = &l
				} else if l, err := s.Registry.ByAddressNoRank(ctx, v.Address); err == nil && l != nil {
					v.Listing = l
				}
			}
		}
	}
	var out []SearchResult
	for _, v := range best {
		out = append(out, *v)
	}
	out = classifySearch(out, elsewhere)
	if len(out) > 12 {
		out = out[:12]
	}
	return out, nil
}

func firstPositive(xs ...float64) float64 {
	for _, x := range xs {
		if x > 0 {
			return x
		}
	}
	return 0
}

// ------------------------------------------------------------- the report

var ErrNotFound = errors.New("token not found on Solana, Ethereum or Base")

// pairs returns the token's pools on supported chains, most liquid first.
func (s *Service) pairs(ctx context.Context, address string) ([]dsPair, error) {
	if !LooksLikeAddress(address) {
		return nil, fmt.Errorf("%q is not a Solana or EVM (0x…) contract address", address)
	}
	var r struct {
		Pairs []dsPair `json:"pairs"`
	}
	if err := s.getJSON(ctx, "https://api.dexscreener.com/latest/dex/tokens/"+address, &r); err != nil {
		return nil, err
	}
	var pairs []dsPair
	for _, p := range r.Pairs {
		if _, ok := chains[p.ChainID]; ok && strings.EqualFold(p.BaseToken.Address, address) {
			pairs = append(pairs, p)
		}
	}
	if len(pairs) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Liquidity.USD > pairs[j].Liquidity.USD })
	return pairs, nil
}

// Quote is a token's current price from its most liquid pool.
type Quote struct {
	Address  string  `json:"address"`
	Chain    string  `json:"chain"`
	Symbol   string  `json:"symbol"`
	Name     string  `json:"name"`
	PriceUSD float64 `json:"priceUsd"`
}

func (s *Service) Quote(ctx context.Context, address string) (*Quote, error) {
	pairs, err := s.pairs(ctx, strings.TrimSpace(address))
	if err != nil {
		return nil, err
	}
	m := pairs[0]
	q := &Quote{Address: m.BaseToken.Address, Chain: m.ChainID, Symbol: m.BaseToken.Symbol, Name: m.BaseToken.Name}
	q.PriceUSD, _ = strconv.ParseFloat(m.PriceUSD, 64)
	return q, nil
}

func (s *Service) Report(ctx context.Context, address string) (*Report, error) {
	address = strings.TrimSpace(address)
	pairs, err := s.pairs(ctx, address)
	if err != nil {
		return nil, err
	}
	main := pairs[0]
	ch := chains[main.ChainID]
	rep := &Report{
		Address: main.BaseToken.Address, Chain: main.ChainID, ChainLabel: ch.label, Name: main.BaseToken.Name, Symbol: main.BaseToken.Symbol,
		FetchedAt: time.Now().UTC(), MarketCap: main.MarketCap, FDV: main.FDV, Liquidity: main.Liquidity.USD,
		Buys24h: main.Txns.H24.Buys, Sells24h: main.Txns.H24.Sells, Pairs: len(pairs), DexID: main.DexID,
		PairAddress: main.PairAddress, PairURL: main.URL, Change: map[string]float64{},
	}
	rep.PriceUSD, _ = strconv.ParseFloat(main.PriceUSD, 64)
	for k, v := range main.PriceChange {
		rep.Change[k] = v / 100
	}
	oldest := int64(0)
	poolIDs := map[string]bool{}
	for _, p := range pairs {
		rep.TotalLiq += p.Liquidity.USD
		rep.Volume24h += p.Volume.H24
		poolIDs[strings.ToLower(p.PairAddress)] = true
		if p.PairCreated > 0 && (oldest == 0 || p.PairCreated < oldest) {
			oldest = p.PairCreated
		}
	}
	if oldest > 0 {
		rep.Launched = time.UnixMilli(oldest).UTC()
		rep.AgeDays = time.Since(rep.Launched).Hours() / 24
	}

	v2 := false
	for _, l := range main.Labels {
		if strings.EqualFold(l, "v2") {
			v2 = true
		}
	}
	if !v2 && rep.Chain != "solana" {
		rep.Notes = append(rep.Notes, "The main pool uses concentrated liquidity, where a liquidity-lock check doesn't apply: liquidity providers can withdraw at any time.")
	}
	s.identity(ctx, rep)
	if err := s.security(ctx, rep, ch.goplus, poolIDs, v2); err != nil {
		rep.Notes = append(rep.Notes, "Contract security check unavailable: "+err.Error())
	}
	if h, err := s.history(ctx, ch.gecko, main.PairAddress, rep.AgeDays, rep.PriceUSD); err == nil {
		rep.History = h
	} else {
		rep.Notes = append(rep.Notes, "Price history unavailable: "+err.Error())
	}
	s.risk(ctx, rep)
	assess(rep)
	return rep, nil
}

// ---------------------------------------------------------------- GoPlus

func str(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

func status(m map[string]any, k string) bool {
	if sub, ok := m[k].(map[string]any); ok {
		return str(sub, "status") == "1"
	}
	return false
}

func num(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return math.NaN()
	}
	return v
}

func (s *Service) security(ctx context.Context, rep *Report, chainID string, poolIDs map[string]bool, v2Pool bool) error {
	var u string
	if chainID == "solana" {
		u = "https://api.gopluslabs.io/api/v1/solana/token_security?contract_addresses=" + rep.Address
	} else {
		u = fmt.Sprintf("https://api.gopluslabs.io/api/v1/token_security/%s?contract_addresses=%s", chainID, strings.ToLower(rep.Address))
	}
	var r struct {
		Code   int                       `json:"code"`
		Result map[string]map[string]any `json:"result"`
	}
	if err := s.getJSON(ctx, u, &r); err != nil {
		return err
	}
	var g map[string]any
	for _, v := range r.Result {
		g = v
	}
	if g == nil {
		return errors.New("token not in GoPlus yet")
	}
	rep.HolderCount = int(num(str(g, "holder_count")))
	if hs, ok := g["holders"].([]any); ok {
		for _, h := range hs {
			hm, _ := h.(map[string]any)
			addr := str(hm, "address")
			if addr == "" {
				addr = str(hm, "account")
			}
			ho := Holder{Address: addr, Percent: num(str(hm, "percent")), Tag: str(hm, "tag")}
			if math.IsNaN(ho.Percent) {
				continue
			}
			if v, ok := hm["is_locked"].(float64); ok && v == 1 {
				ho.Locked = true
			}
			ta := strings.ToLower(str(hm, "token_account"))
			ho.IsPool = poolIDs[strings.ToLower(addr)] || poolIDs[ta] || strings.Contains(strings.ToLower(ho.Tag), "pool") ||
				strings.Contains(strings.ToLower(ho.Tag), "uniswap") || strings.Contains(strings.ToLower(ho.Tag), "raydium")
			rep.Holders = append(rep.Holders, ho)
		}
	}
	sort.Slice(rep.Holders, func(i, j int) bool { return rep.Holders[i].Percent > rep.Holders[j].Percent })
	n := 0
	for _, h := range rep.Holders {
		if h.IsPool || h.Locked || isBurn(h.Address) {
			continue
		}
		if n == 0 {
			rep.Top1 = h.Percent
		}
		if n < 10 {
			rep.Top10 += h.Percent
		}
		n++
	}
	if len(rep.Holders) > 15 {
		rep.Holders = rep.Holders[:15]
	}

	add := func(level, title, detail string) {
		rep.Flags = append(rep.Flags, Flag{Level: level, Area: "Contract", Title: title, Detail: detail})
	}
	if chainID == "solana" {
		if status(g, "mintable") {
			add("red", "Creator can mint new tokens", "The mint authority is still active, so new supply can be created at any time, diluting every holder. On Solana this is usually revoked for legitimate tokens.")
		} else {
			add("green", "Supply is fixed", "Mint authority revoked: no one can create more tokens.")
		}
		if status(g, "freezable") {
			add("red", "Your tokens can be frozen", "A freeze authority exists: the controller can freeze token accounts, so you could be unable to sell.")
		} else {
			add("green", "No freeze authority", "No one can freeze holders' tokens.")
		}
		if status(g, "closable") {
			add("red", "Token accounts can be closed", "An authority can close accounts holding this token.")
		}
		if status(g, "balance_mutable_authority") {
			add("red", "Balances can be changed", "An authority can modify holder balances.")
		}
		if status(g, "metadata_mutable") {
			add("amber", "Name and logo can be changed", "The metadata is still editable. Harmless on its own, but a common tool for rebranding after a rug.")
		}
		if str(g, "non_transferable") == "1" {
			add("red", "Non-transferable token", "This token cannot be transferred, so you could not sell it.")
		}
		if tf, ok := g["transfer_fee"].(map[string]any); ok && len(tf) > 0 {
			add("amber", "Transfer fee", "Every transfer pays a fee to the token's controller, which also hits your sells.")
		}
		if hooks, ok := g["transfer_hook"].([]any); ok && len(hooks) > 0 {
			add("amber", "Transfer hook", "Transfers run external code that could block or tax sells.")
		}
		if status(g, "transfer_hook_upgradable") {
			add("amber", "Transfer rules can be changed later", "An authority can add or change a transfer hook, which could later restrict selling.")
		}
		return nil
	}
	// EVM (Ethereum, Base)
	if str(g, "is_honeypot") == "1" || str(g, "cannot_sell_all") == "1" {
		add("red", "Honeypot: you may not be able to sell", "A simulated sell failed or is restricted. This is the classic meme-coin scam.")
	}
	if str(g, "cannot_buy") == "1" {
		add("red", "Buying is blocked", "The contract currently blocks purchases.")
	}
	for _, t := range []struct{ key, label string }{{"buy_tax", "Buy"}, {"sell_tax", "Sell"}} {
		if v := num(str(g, t.key)); !math.IsNaN(v) && v > 0 {
			level := "amber"
			if v > 0.10 {
				level = "red"
			}
			add(level, fmt.Sprintf("%s tax %.0f%%", t.label, 100*v), "Taken on every trade. High or changeable taxes are a common trap.")
		}
	}
	owner := strings.ToLower(str(g, "owner_address"))
	renounced := owner == "" || isBurn(owner)
	if str(g, "is_mintable") == "1" {
		level := "amber"
		if !renounced {
			level = "red"
		}
		add(level, "Contract can mint new tokens", "New supply can be created, diluting holders.")
	}
	if !renounced {
		add("amber", "Owner still controls the contract", "Ownership is not renounced. Combined with any other permission below, the owner can change the rules.")
	} else {
		add("green", "Ownership renounced", "No owner can change the contract.")
	}
	for _, c := range []struct{ key, level, title, detail string }{
		{"hidden_owner", "red", "Hidden owner", "Control is held by an address that isn't visible as the owner."},
		{"can_take_back_ownership", "red", "Ownership can be reclaimed", "Renounced ownership can be taken back."},
		{"selfdestruct", "red", "Self-destruct", "The contract can be destroyed."},
		{"external_call", "amber", "External calls", "The contract calls other contracts during transfers."},
	} {
		if str(g, c.key) == "1" {
			add(c.level, c.title, c.detail)
		}
	}
	// Owner-only powers matter only while someone owns the contract: after a
	// genuine renounce they exist in the code but nobody can use them.
	trulyRenounced := renounced && str(g, "hidden_owner") != "1" && str(g, "can_take_back_ownership") != "1"
	for _, c := range []struct{ key, level, title, detail string }{
		{"is_proxy", "amber", "Upgradeable (proxy) contract", "The code can be replaced later."},
		{"transfer_pausable", "red", "Trading can be paused", "The owner can stop all transfers, so you could be stuck."},
		{"is_blacklisted", "red", "Blacklist function", "Specific wallets can be blocked from selling."},
		{"slippage_modifiable", "red", "Taxes can be changed", "The owner can raise buy/sell taxes at any time, up to 100%."},
		{"personal_slippage_modifiable", "red", "Per-wallet taxes", "Different wallets can be charged different taxes."},
		{"trading_cooldown", "amber", "Trading cooldown", "Limits how often a wallet can trade."},
	} {
		if str(g, c.key) != "1" {
			continue
		}
		if trulyRenounced {
			add("green", c.title+" (disabled)", "This power exists in the code, but ownership is renounced, so no one can use it.")
		} else {
			add(c.level, c.title, c.detail)
		}
	}
	if str(g, "is_open_source") == "0" {
		add("red", "Contract code is not verified", "Nobody can check what the contract does.")
	} else if str(g, "is_open_source") == "1" {
		add("green", "Contract code is verified", "The source code is public and readable.")
	}
	if n := num(str(g, "honeypot_with_same_creator")); n > 0 {
		add("amber", fmt.Sprintf("Creator linked to %.0f honeypot(s)", n), "GoPlus associates the creating wallet with earlier honeypot tokens. Treat with suspicion.")
	}
	// Share of liquidity-pool tokens locked or burned. Only meaningful for
	// v2-style pools; concentrated-liquidity positions (v3) are NFTs.
	if lps, ok := g["lp_holders"].([]any); ok && len(lps) > 0 && v2Pool {
		locked, selfHeld := 0.0, 0.0
		for _, l := range lps {
			lm, _ := l.(map[string]any)
			p := num(str(lm, "percent"))
			if math.IsNaN(p) {
				continue
			}
			addr := str(lm, "address")
			switch {
			case lm["is_locked"] == float64(1) || isBurn(addr):
				locked += p
			case strings.EqualFold(addr, rep.Address) && trulyRenounced && str(g, "is_open_source") == "1":
				// Pool tokens sent to the token's own contract: with
				// verified code and no owner there is nobody to withdraw
				// them, so they are as good as burned (e.g. PEPE).
				locked += p
				selfHeld += p
			}
		}
		rep.LPLocked = &locked
		if selfHeld > 0.01 {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%.1f%% of the pool tokens sit in the token's own contract. With verified code and renounced ownership nobody can withdraw them, so they are counted as locked.", 100*selfHeld))
		}
	}
	return nil
}

func isBurn(a string) bool {
	a = strings.ToLower(a)
	return a == "0x0000000000000000000000000000000000000000" || a == "0x000000000000000000000000000000000000dead" ||
		strings.HasPrefix(a, "1111111111111111111111111111")
}

// --------------------------------------------------------- GeckoTerminal

func (s *Service) history(ctx context.Context, network, pool string, ageDays, price float64) (*History, error) {
	tf, limit := "day", 1000
	if ageDays > 0 && ageDays < 21 {
		tf, limit = "hour", 1000
	}
	var r struct {
		Data struct {
			Attributes struct {
				OHLCV [][]float64 `json:"ohlcv_list"`
			} `json:"attributes"`
		} `json:"data"`
	}
	u := fmt.Sprintf("https://api.geckoterminal.com/api/v2/networks/%s/pools/%s/ohlcv/%s?limit=%d&currency=usd", network, pool, tf, limit)
	if err := s.getJSON(ctx, u, &r); err != nil {
		return nil, err
	}
	rows := r.Data.Attributes.OHLCV
	if len(rows) < 3 {
		return nil, errors.New("too few candles")
	}
	h := &History{Interval: tf}
	for i := len(rows) - 1; i >= 0; i-- { // API is newest first
		x := rows[i]
		if len(x) < 6 || x[4] <= 0 {
			continue
		}
		h.Candles = append(h.Candles, Candle{Time: int64(x[0]), Open: x[1], High: x[2], Low: x[3], Close: x[4], Volume: x[5]})
	}
	n := len(h.Candles)
	if n < 3 {
		return nil, errors.New("too few candles")
	}
	perDay := 1.0
	if tf == "hour" {
		perDay = 24
	}
	h.WindowDays = float64(n) / perDay
	last := h.Candles[n-1].Close
	if price > 0 {
		last = price
	}
	ret := func(days float64) *float64 {
		k := n - 1 - int(days*perDay)
		if k < 0 {
			return nil
		}
		v := last/h.Candles[k].Close - 1
		return &v
	}
	h.Return7d, h.Return30d = ret(7), ret(30)
	h.ReturnAll = last/h.Candles[0].Close - 1
	peak, mdd := 0.0, 0.0
	var rets []float64
	for i, c := range h.Candles {
		h.High = math.Max(h.High, c.High)
		peak = math.Max(peak, c.Close)
		mdd = math.Min(mdd, c.Close/peak-1)
		if i > 0 {
			rets = append(rets, math.Log(c.Close/h.Candles[i-1].Close))
		}
	}
	h.MaxDrawdown = mdd
	if h.High > 0 {
		h.FromHigh = last/h.High - 1
	}
	if len(rets) > 2 {
		m, v := 0.0, 0.0
		for _, x := range rets {
			m += x
		}
		m /= float64(len(rets))
		for _, x := range rets {
			v += (x - m) * (x - m)
		}
		sd := math.Sqrt(v / float64(len(rets)-1))
		h.AnnualizedVol = sd * math.Sqrt(365*perDay)
		h.TypicalDayMove = sd * math.Sqrt(perDay) * 0.8
	}
	return h, nil
}

// classifySearch orders search results and marks likely copycats and fakes.
func classifySearch(out []SearchResult, elsewhere map[string]Listing) []SearchResult {
	// The likely original of each ticker: the best-ranked CoinGecko listing
	// (it records the official contract address), otherwise the most liquid
	// token that has existed for at least a month. Reported liquidity can be
	// faked, so brand-new tokens claiming huge liquidity are never trusted.
	established := func(r SearchResult) bool { return r.AgeDays >= 30 }
	type lead struct {
		addr   string
		liq    float64
		listed *Listing
	}
	leaders := map[string]*lead{}
	for _, r := range out {
		sym := strings.ToUpper(r.Symbol)
		l := leaders[sym]
		switch {
		case r.Listing != nil && (l == nil || l.listed == nil || r.Listing.better(*l.listed) ||
			(!l.listed.better(*r.Listing) && r.Liquidity > l.liq)):
			leaders[sym] = &lead{r.Address, r.Liquidity, r.Listing}
		case r.Listing == nil && established(r) && (l == nil || (l.listed == nil && r.Liquidity > l.liq)):
			leaders[sym] = &lead{r.Address, r.Liquidity, nil}
		}
	}
	// Real coins that live on another blockchain: every token here using
	// their ticker or name is a lookalike.
	away := func(r SearchResult) *Listing {
		for sym, l := range elsewhere {
			if l.ID == "" || r.Listing != nil {
				continue
			}
			name := strings.ToLower(l.Name)
			if strings.EqualFold(r.Symbol, sym) || strings.EqualFold(r.Symbol, l.Name) ||
				(len(name) >= 4 && strings.Contains(strings.ToLower(r.Name), name)) {
				return &l
			}
		}
		return nil
	}
	for i := range out {
		r := &out[i]
		l := leaders[strings.ToUpper(r.Symbol)]
		if a := away(*r); a != nil {
			r.Warning = fmt.Sprintf("Not the real %s: CoinGecko's %s (%s, #%d) isn't on Solana, Ethereum or Base, so this is a lookalike", a.Symbol, a.Symbol, a.Name, a.Rank)
			continue
		}
		switch {
		case l != nil && r.Address == l.addr:
			r.Original = true
			if r.AgeDays < 7 && r.Listing == nil {
				r.Warning = "Launched in the last week"
			}
		case r.AgeDays < 7 && r.Liquidity > 1_000_000 && r.Listing == nil:
			r.Warning = "Brand-new token reporting huge liquidity: very likely fake. Verify the address on the project's official site"
		case l != nil && l.listed != nil && r.Listing != nil:
			r.Warning = fmt.Sprintf("A different project with the same ticker (CoinGecko lists the bigger one as %s)", l.listed.Name)
		case l != nil && l.listed != nil:
			r.Warning = fmt.Sprintf("Not the CoinGecko-listed %s (%s): likely a copycat", strings.ToUpper(r.Symbol), l.listed.Name)
		case l != nil && r.Liquidity < l.liq/20:
			r.Warning = "Same name as a larger, older token: likely a copycat"
		case l != nil:
			r.Warning = "Another, larger token uses this name: make sure this is the address you want"
		case r.AgeDays < 7:
			r.Warning = "Launched in the last week"
		case r.Liquidity < 50000:
			r.Warning = "Very little liquidity"
		}
	}
	// Originals first (listed ones by CoinGecko rank), then established
	// tokens by liquidity, then the rest.
	tier := func(r SearchResult) int {
		switch {
		case r.Original && r.Listing != nil:
			return 0
		case r.Original:
			return 1
		case r.Listing != nil:
			return 2
		case established(r):
			return 3
		}
		return 4
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ta, tb := tier(a), tier(b); ta != tb {
			return ta < tb
		}
		if a.Listing != nil && b.Listing != nil && (a.Listing.better(*b.Listing) || b.Listing.better(*a.Listing)) {
			return a.Listing.better(*b.Listing)
		}
		return a.Liquidity > b.Liquidity
	})
	return out
}
