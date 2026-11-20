package token

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Registry answers "is this contract the coin people mean by this name?"
// using CoinGecko's curated listings, which record each coin's official
// contract address per chain. A copycat can copy a name, a symbol, a logo and
// even fake its liquidity, but it can't change which address CoinGecko lists.
type Registry struct {
	key  string // optional CoinGecko demo key
	kv   KV
	http *http.Client

	mu       sync.Mutex
	byAddr   map[string]string // lower(address) -> coin id
	coins    map[string]regCoin
	loadedAt time.Time
	searches map[string]cachedSearch
}

// KV persists the registry between restarts (implemented by store.Store).
type KV interface {
	Setting(ctx context.Context, key string, v any) (bool, error)
	SetSetting(ctx context.Context, key string, v any) error
}

type regCoin struct {
	Symbol string            `json:"s"`
	Name   string            `json:"n"`
	Addr   map[string]string `json:"a"` // chain -> address (supported chains only)
}

type regSnapshot struct {
	LoadedAt time.Time          `json:"loadedAt"`
	Coins    map[string]regCoin `json:"coins"`
}

type cachedSearch struct {
	at  time.Time
	res []Listing
}

// Listing is a coin as CoinGecko lists it, on one supported chain.
type Listing struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Symbol  string `json:"symbol"`
	Rank    int    `json:"rank,omitempty"` // CoinGecko market-cap rank; 0 = unranked
	Chain   string `json:"chain"`
	Address string `json:"address"`
	URL     string `json:"url"`
	// Elsewhere: CoinGecko lists the coin, but not on Solana, Ethereum or
	// Base (its own blockchain, NEAR, ...). Any token on those chains using
	// its ticker is then not the real coin.
	Elsewhere bool `json:"elsewhere,omitempty"`
}

// better reports whether a is a more established listing than b.
func (a Listing) better(b Listing) bool {
	switch {
	case a.Rank > 0 && b.Rank > 0:
		return a.Rank < b.Rank
	case a.Rank > 0 || b.Rank > 0:
		return a.Rank > 0
	}
	return false
}

const registryKey = "coingecko_registry"

func NewRegistry(key string, kv KV) *Registry {
	return &Registry{key: key, kv: kv, http: &http.Client{Timeout: 40 * time.Second}, searches: map[string]cachedSearch{}}
}

func (r *Registry) get(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if r.key != "" {
		req.Header.Set("x-cg-demo-api-key", r.key)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("coingecko: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// ensure loads the address book: from memory, else the database (if under a
// day old), else CoinGecko; a stale copy is used if CoinGecko is unreachable.
func (r *Registry) ensure(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.coins != nil && time.Since(r.loadedAt) < 24*time.Hour {
		return nil
	}
	var snap regSnapshot
	if r.coins == nil && r.kv != nil {
		if ok, err := r.kv.Setting(ctx, registryKey, &snap); err == nil && ok && len(snap.Coins) > 0 {
			r.index(snap)
			if time.Since(snap.LoadedAt) < 24*time.Hour {
				return nil
			}
		}
	}
	var list []struct {
		ID        string            `json:"id"`
		Symbol    string            `json:"symbol"`
		Name      string            `json:"name"`
		Platforms map[string]string `json:"platforms"`
	}
	if err := r.get(ctx, "https://api.coingecko.com/api/v3/coins/list?include_platform=true", &list); err != nil {
		if r.coins != nil {
			r.loadedAt = time.Now() // keep the stale copy; retry tomorrow
			return nil
		}
		return err
	}
	snap = regSnapshot{LoadedAt: time.Now().UTC(), Coins: map[string]regCoin{}}
	for _, c := range list {
		addr := map[string]string{}
		for chain, a := range c.Platforms {
			if _, ok := chains[chain]; ok && strings.TrimSpace(a) != "" {
				addr[chain] = strings.TrimSpace(a)
			}
		}
		if len(addr) > 0 {
			snap.Coins[c.ID] = regCoin{Symbol: c.Symbol, Name: c.Name, Addr: addr}
		}
	}
	r.index(snap)
	if r.kv != nil {
		_ = r.kv.SetSetting(ctx, registryKey, snap)
	}
	return nil
}

func (r *Registry) index(snap regSnapshot) {
	r.coins, r.loadedAt = snap.Coins, snap.LoadedAt
	r.byAddr = make(map[string]string, len(snap.Coins))
	for id, c := range snap.Coins {
		for _, a := range c.Addr {
			r.byAddr[strings.ToLower(a)] = id
		}
	}
}

func listing(id string, c regCoin, chain string, rank int) Listing {
	return Listing{ID: id, Name: c.Name, Symbol: strings.ToUpper(c.Symbol), Rank: rank, Chain: chain, Address: c.Addr[chain],
		URL: "https://www.coingecko.com/en/coins/" + id}
}

// ByAddressNoRank returns the listing for a contract address without its
// market-cap rank (no network call once the registry is loaded).
func (r *Registry) ByAddressNoRank(ctx context.Context, address string) (*Listing, error) {
	if err := r.ensure(ctx); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byAddr[strings.ToLower(strings.TrimSpace(address))]
	if !ok {
		return nil, nil
	}
	c := r.coins[id]
	for chain, a := range c.Addr {
		if strings.EqualFold(a, strings.TrimSpace(address)) {
			l := listing(id, c, chain, 0)
			return &l, nil
		}
	}
	return nil, nil
}

// ByAddress returns the listing for a contract address, with its rank, if
// CoinGecko lists it.
func (r *Registry) ByAddress(ctx context.Context, address string) (*Listing, error) {
	l, err := r.ByAddressNoRank(ctx, address)
	if err != nil || l == nil {
		return l, err
	}
	if ls, err := r.Search(ctx, l.Symbol); err == nil {
		for _, x := range ls {
			if x.ID == l.ID {
				l.Rank = x.Rank
			}
		}
	}
	return l, nil
}

// Search returns CoinGecko-listed coins matching q that live on a supported
// chain, most established first (one entry per coin and chain).
func (r *Registry) Search(ctx context.Context, q string) ([]Listing, error) {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return nil, nil
	}
	if err := r.ensure(ctx); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if c, ok := r.searches[q]; ok && time.Since(c.at) < time.Hour {
		r.mu.Unlock()
		return c.res, nil
	}
	r.mu.Unlock()
	var res struct {
		Coins []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
			Rank   int    `json:"market_cap_rank"`
		} `json:"coins"`
	}
	if err := r.get(ctx, "https://api.coingecko.com/api/v3/search?query="+url.QueryEscape(q), &res); err != nil {
		return nil, err
	}
	var out []Listing
	r.mu.Lock()
	for _, c := range res.Coins {
		rc, ok := r.coins[c.ID]
		if !ok {
			// Listed, but not on a chain we cover. Only ranked coins are
			// kept: unranked ones carry no weight as "the original".
			if c.Rank > 0 {
				out = append(out, Listing{ID: c.ID, Name: c.Name, Symbol: strings.ToUpper(c.Symbol), Rank: c.Rank,
					URL: "https://www.coingecko.com/en/coins/" + c.ID, Elsewhere: true})
			}
			continue
		}
		for chain := range rc.Addr {
			out = append(out, listing(c.ID, rc, chain, c.Rank))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].better(out[j]) })
	r.searches[q] = cachedSearch{at: time.Now(), res: out}
	r.mu.Unlock()
	return out, nil
}

// Official returns the most established listing using this symbol, if any.
func (r *Registry) Official(ctx context.Context, symbol string) (*Listing, error) {
	ls, err := r.Search(ctx, symbol)
	if err != nil {
		return nil, err
	}
	for _, l := range ls {
		if strings.EqualFold(l.Symbol, symbol) {
			return &l, nil
		}
	}
	return nil, nil
}

// CoinMatch is a coin from CoinGecko's search, on any blockchain.
type CoinMatch struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Symbol string `json:"symbol"`
	Rank   int    `json:"rank,omitempty"`
}

// Coins searches CoinGecko for coins on any blockchain, best-ranked first.
func (r *Registry) Coins(ctx context.Context, q string) ([]CoinMatch, error) {
	var res struct {
		Coins []struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Symbol string `json:"symbol"`
			Rank   int    `json:"market_cap_rank"`
		} `json:"coins"`
	}
	if err := r.get(ctx, "https://api.coingecko.com/api/v3/search?query="+url.QueryEscape(strings.TrimSpace(q)), &res); err != nil {
		return nil, err
	}
	out := []CoinMatch{}
	for _, c := range res.Coins {
		out = append(out, CoinMatch{ID: c.ID, Name: c.Name, Symbol: strings.ToUpper(c.Symbol), Rank: c.Rank})
	}
	return out, nil
}
