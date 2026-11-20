package research

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/token"
)

// gather collects verified data before Claude runs: free, fast, and used by
// Claude to check what websites and posts claim.
func (s *Service) gather(ctx context.Context, rep *Report) (string, error) {
	var b strings.Builder
	cgID := ""
	switch {
	case IsCoinID(rep.Subject):
		rep.Kind, cgID = "crypto", strings.TrimPrefix(rep.Subject, "cg:")
		fmt.Fprintf(&b, "Identified by its CoinGecko id %q (the coin itself, whichever blockchain it is on).\n", cgID)
	case token.LooksLikeAddress(rep.Subject):
		rep.Kind = "token"
		tr, err := s.Tokens.Report(ctx, rep.Subject)
		if err != nil {
			return "", fmt.Errorf("token lookup: %w", err)
		}
		rep.Name, rep.Symbol, rep.Price = tr.Name, tr.Symbol, tr.PriceUSD
		fmt.Fprintf(&b, "Contract %s on %s. Price $%.6g, market cap $%.0f (fully diluted $%.0f), liquidity $%.0f, 24h volume $%.0f, %d holders, launched %s.\n",
			tr.Address, tr.ChainLabel, tr.PriceUSD, tr.MarketCap, tr.FDV, tr.TotalLiq, tr.Volume24h, tr.HolderCount, tr.Launched.Format("2006-01-02"))
		if tr.Listing != nil {
			cgID = tr.Listing.ID
			fmt.Fprintf(&b, "Identity: CoinGecko lists this exact address as %s (rank #%d).\n", tr.Listing.Name, tr.Listing.Rank)
		} else if tr.Lookalike != nil {
			fmt.Fprintf(&b, "Identity WARNING: not the CoinGecko-listed %s; the listed one is %s at %s.\n", tr.Symbol, tr.Lookalike.Name, tr.Lookalike.Address)
		}
		fmt.Fprintf(&b, "Safety check: %s (score %d/100). Largest holder %.1f%%, top 10 %.1f%%.\n", tr.Verdict, tr.RiskScore, 100*tr.Top1, 100*tr.Top10)
		for _, f := range tr.Flags {
			if f.Level != "green" {
				fmt.Fprintf(&b, "  - %s flag: %s\n", f.Level, f.Title)
			}
		}
		if r := tr.Risk; r != nil {
			fmt.Fprintf(&b, "Measured over %d days: typical day ±%.1f%%, worst week %.0f%%, biggest fall from a high %.0f%%.\n", r.Days, 100*r.TypicalDay, 100*r.WorstWeek.Return, 100*r.MaxDrawdown)
		}
	default:
		rep.Symbol = rep.Subject
		rep.Kind = "stock"
		if market.IsCrypto(rep.Subject) {
			rep.Kind = "crypto"
		}
		if s.Assets != nil {
			if a, err := s.Assets(ctx, rep.Subject); err == nil && a != nil {
				rep.Name, rep.Price = a.Name, a.Price
				fmt.Fprintf(&b, "%s (%s), %s on %s. Price %.6g %s on %s; %d years of daily history.\n", a.Name, a.Symbol, a.Class, a.Exchange, a.Price, a.Currency, a.AsOf, a.HistoryBars/252)
				fmt.Fprintf(&b, "Platform forecast summary: %s\n", a.Headline)
				if e := a.Earnings; e.Next != nil {
					fmt.Fprintf(&b, "Next earnings report %s; past reports moved the stock ±%.1f%% on average; beat estimates %.0f%% of the time.\n", e.Next.Date, 100*e.AvgAbsMove, 100*e.BeatRate)
				}
			}
		}
		if rep.Name == "" {
			rep.Name = rep.Subject
		}
		if rep.Kind == "crypto" {
			cgID = s.coinID(ctx, rep.Subject)
		}
	}
	if cgID != "" {
		s.coinGeckoFacts(ctx, cgID, rep, &b)
	}
	if b.Len() == 0 {
		b.WriteString("(no verified data available)\n")
	}
	return b.String(), nil
}

func (s *Service) getJSON(ctx context.Context, u string, v any, header map[string]string) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "hindcast-research")
	for k, val := range header {
		req.Header.Set(k, val)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.Header, fmt.Errorf("%s: %s", u, resp.Status)
	}
	return resp.Header, json.NewDecoder(resp.Body).Decode(v)
}

func (s *Service) cgHeader() map[string]string {
	if s.CoinGeckoKey == "" {
		return nil
	}
	return map[string]string{"x-cg-demo-api-key": s.CoinGeckoKey}
}

// coinID finds the CoinGecko id for a ticker such as ZEC-USD: the verified
// meme list first, then the best-ranked coin with that exact symbol.
func (s *Service) coinID(ctx context.Context, ticker string) string {
	if m, ok := market.MemeCoinForTicker(ticker); ok {
		return m.ID
	}
	base := strings.TrimRight(strings.TrimSuffix(strings.ToUpper(ticker), "-USD"), "0123456789")
	var r struct {
		Coins []struct {
			ID     string `json:"id"`
			Symbol string `json:"symbol"`
			Rank   int    `json:"market_cap_rank"`
		} `json:"coins"`
	}
	if _, err := s.getJSON(ctx, "https://api.coingecko.com/api/v3/search?query="+url.QueryEscape(base), &r, s.cgHeader()); err != nil {
		return ""
	}
	for _, c := range r.Coins { // CoinGecko sorts by market cap rank
		if strings.EqualFold(c.Symbol, base) {
			return c.ID
		}
	}
	return ""
}

func (s *Service) coinGeckoFacts(ctx context.Context, id string, rep *Report, b *strings.Builder) {
	var c struct {
		Name        string   `json:"name"`
		Symbol      string   `json:"symbol"`
		Categories  []string `json:"categories"`
		GenesisDate string   `json:"genesis_date"`
		Description struct {
			En string `json:"en"`
		} `json:"description"`
		Links struct {
			Homepage   []string `json:"homepage"`
			Whitepaper string   `json:"whitepaper"`
			Explorers  []string `json:"blockchain_site"`
			Subreddit  string   `json:"subreddit_url"`
			Twitter    string   `json:"twitter_screen_name"`
			Telegram   string   `json:"telegram_channel_identifier"`
			ReposURL   struct {
				Github []string `json:"github"`
			} `json:"repos_url"`
		} `json:"links"`
		MarketCapRank int               `json:"market_cap_rank"`
		Platforms     map[string]string `json:"platforms"`
		MarketData    struct {
			Price       map[string]float64 `json:"current_price"`
			MarketCap   map[string]float64 `json:"market_cap"`
			FDV         map[string]float64 `json:"fully_diluted_valuation"`
			Circulating float64            `json:"circulating_supply"`
			Total       float64            `json:"total_supply"`
			Max         float64            `json:"max_supply"`
			ATH         map[string]float64 `json:"ath"`
			ATHDate     map[string]string  `json:"ath_date"`
			ATHChange   map[string]float64 `json:"ath_change_percentage"`
		} `json:"market_data"`
		Dev struct {
			Stars        int `json:"stars"`
			Forks        int `json:"forks"`
			Commits4W    int `json:"commit_count_4_weeks"`
			Contributors int `json:"pull_request_contributors"`
		} `json:"developer_data"`
	}
	u := "https://api.coingecko.com/api/v3/coins/" + url.PathEscape(id) + "?localization=false&tickers=false&market_data=true&community_data=false&developer_data=true&sparkline=false"
	if _, err := s.getJSON(ctx, u, &c, s.cgHeader()); err != nil {
		fmt.Fprintf(b, "CoinGecko profile unavailable (%v).\n", err)
		return
	}
	if rep.Name == "" || rep.Name == rep.Subject {
		rep.Name = c.Name
	}
	if rep.Symbol == "" {
		rep.Symbol = strings.ToUpper(c.Symbol)
	}
	if rep.Price == 0 {
		rep.Price = c.MarketData.Price["usd"]
	}
	md := c.MarketData
	fmt.Fprintf(b, "CoinGecko profile (%s, rank #%d): categories %s; genesis %s.\n", c.Name, c.MarketCapRank, strings.Join(c.Categories, ", "), orNA(c.GenesisDate))
	var plats []string
	for chain, addr := range c.Platforms {
		if chain != "" && addr != "" {
			plats = append(plats, chain+": "+addr)
		}
	}
	if len(plats) > 0 {
		fmt.Fprintf(b, "Official token contracts listed on CoinGecko: %s. Tokens with this name at any other address are not this coin.\n", strings.Join(plats, "; "))
	} else {
		b.WriteString("CoinGecko lists no token contracts: this is a native coin of its own blockchain.\n")
	}
	fmt.Fprintf(b, "Supply: circulating %.0f, total %.0f, max %s. Market cap $%.0f, fully diluted $%.0f. All-time high $%.6g on %s (now %.0f%% below).\n",
		md.Circulating, md.Total, maxSupply(md.Max), md.MarketCap["usd"], md.FDV["usd"], md.ATH["usd"], dateOnly(md.ATHDate["usd"]), -md.ATHChange["usd"])
	var links []string
	for _, h := range c.Links.Homepage {
		if h != "" {
			links = append(links, "website "+h)
		}
	}
	if c.Links.Whitepaper != "" {
		links = append(links, "whitepaper "+c.Links.Whitepaper)
	}
	if c.Links.Subreddit != "" {
		links = append(links, "reddit "+c.Links.Subreddit)
	}
	if c.Links.Twitter != "" {
		links = append(links, "X @"+c.Links.Twitter)
	}
	for _, g := range c.Links.ReposURL.Github {
		if g != "" {
			links = append(links, "github "+g)
		}
	}
	if len(links) > 0 {
		fmt.Fprintf(b, "Official links listed on CoinGecko: %s.\n", strings.Join(links, "; "))
	}
	if d := strings.TrimSpace(c.Description.En); d != "" {
		if len(d) > 1200 {
			d = d[:1200] + "…"
		}
		fmt.Fprintf(b, "CoinGecko description (written by or for the project, not independent): %s\n", d)
	}
	for i, g := range c.Links.ReposURL.Github {
		if i >= 2 || g == "" {
			break
		}
		s.githubFacts(ctx, g, b)
	}
}

// githubFacts summarises recent development on a public repository.
func (s *Service) githubFacts(ctx context.Context, repoURL string, b *strings.Builder) {
	u, err := url.Parse(repoURL)
	if err != nil || !strings.Contains(u.Host, "github.com") {
		return
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		return
	}
	if len(parts) == 1 { // an organisation: use its most recently pushed repo
		var repos []struct {
			FullName string `json:"full_name"`
		}
		if _, err := s.getJSON(ctx, "https://api.github.com/orgs/"+parts[0]+"/repos?sort=pushed&per_page=1", &repos, nil); err != nil || len(repos) == 0 {
			fmt.Fprintf(b, "GitHub %s: not found or rate-limited.\n", repoURL)
			return
		}
		parts = strings.Split(repos[0].FullName, "/")
	}
	full := parts[0] + "/" + parts[1]
	var r struct {
		Stars    int    `json:"stargazers_count"`
		Forks    int    `json:"forks_count"`
		Issues   int    `json:"open_issues_count"`
		Pushed   string `json:"pushed_at"`
		Created  string `json:"created_at"`
		Archived bool   `json:"archived"`
	}
	if _, err := s.getJSON(ctx, "https://api.github.com/repos/"+full, &r, nil); err != nil {
		fmt.Fprintf(b, "GitHub %s: unavailable (%v).\n", full, err)
		return
	}
	since := time.Now().AddDate(0, 0, -90).UTC().Format(time.RFC3339)
	commits := s.countPages(ctx, "https://api.github.com/repos/"+full+"/commits?per_page=1&since="+since)
	contributors := s.countPages(ctx, "https://api.github.com/repos/"+full+"/contributors?per_page=1&anon=1")
	fmt.Fprintf(b, "GitHub %s: %d stars, %d forks, %d open issues, created %s, last push %s, %s commits in the last 90 days, %s contributors%s.\n",
		full, r.Stars, r.Forks, r.Issues, dateOnly(r.Created), dateOnly(r.Pushed), commits, contributors, map[bool]string{true: ", ARCHIVED", false: ""}[r.Archived])
}

var lastPage = func(link string) int {
	for _, part := range strings.Split(link, ",") {
		if strings.Contains(part, `rel="last"`) {
			if i := strings.Index(part, "&page="); i >= 0 {
				n, _ := strconv.Atoi(strings.TrimRight(strings.SplitN(part[i+6:], ">", 2)[0], ">"))
				return n
			}
			if i := strings.Index(part, "?page="); i >= 0 {
				n, _ := strconv.Atoi(strings.SplitN(part[i+6:], ">", 2)[0])
				return n
			}
		}
	}
	return -1
}

// countPages counts items with per_page=1 via the Link header's last page.
func (s *Service) countPages(ctx context.Context, u string) string {
	var items []json.RawMessage
	h, err := s.getJSON(ctx, u, &items, nil)
	if err != nil {
		return "unknown"
	}
	if n := lastPage(h.Get("Link")); n > 0 {
		return strconv.Itoa(n)
	}
	return strconv.Itoa(len(items))
}

func orNA(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

func maxSupply(x float64) string {
	if x <= 0 {
		return "none (uncapped)"
	}
	return strconv.FormatFloat(x, 'f', 0, 64)
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return orNA(s)
}
