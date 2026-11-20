package token

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

func rankText(l *Listing) string {
	if l.Rank > 0 {
		return fmt.Sprintf(" (#%d by market cap)", l.Rank)
	}
	return ""
}

// identity checks whether this address is the coin people mean by its
// ticker, using CoinGecko's record of official contract addresses.
func (s *Service) identity(ctx context.Context, rep *Report) {
	if s.Registry == nil {
		return
	}
	add := func(level, title, detail string) {
		rep.Flags = append(rep.Flags, Flag{Level: level, Area: "Identity", Title: title, Detail: detail})
	}
	own, err := s.Registry.ByAddress(ctx, rep.Address)
	if err != nil {
		rep.Notes = append(rep.Notes, "Couldn't check CoinGecko's listings to confirm this is the original token ("+err.Error()+"). Verify the address on the project's official site.")
		return
	}
	official, _ := s.Registry.Official(ctx, rep.Symbol)
	sym := strings.ToUpper(rep.Symbol)
	switch {
	case own != nil && (official == nil || official.ID == own.ID || !official.better(*own)):
		rep.Listing = own
		add("green", fmt.Sprintf("The listed %s: %s%s", sym, own.Name, rankText(own)),
			"CoinGecko records this exact contract address as the official one for this coin, so this is the token people mean by the name. A listing confirms identity, not safety.")
	case own != nil:
		rep.Listing, rep.Lookalike = own, official
		where := "on another blockchain"
		if !official.Elsewhere {
			where = fmt.Sprintf("at %s on %s", official.Address, chains[official.Chain].label)
		}
		add("amber", "A bigger coin uses the same ticker",
			fmt.Sprintf("CoinGecko lists this token as %s%s, a different project from %s%s %s. Make sure this is the one you mean.",
				own.Name, rankText(own), official.Name, rankText(official), where))
	case official != nil && official.Elsewhere:
		rep.Lookalike = official
		add("red", "Not the real "+sym,
			fmt.Sprintf("CoinGecko's %s is %s%s, which isn't on Solana, Ethereum or Base at all (it lives on its own blockchain or another network). A token on %s using this ticker or name is a lookalike, most likely made to catch people searching for the real coin.",
				sym, official.Name, rankText(official), rep.ChainLabel))
	case official != nil:
		rep.Lookalike = official
		add("red", "Not the listed "+sym,
			fmt.Sprintf("CoinGecko lists %s (%s)%s at %s on %s. This token copies the ticker at a different address. Unless you deliberately want this one, it is most likely a copycat.",
				sym, official.Name, rankText(official), official.Address, chains[official.Chain].label))
	default:
		add("amber", "Not listed on CoinGecko",
			"There is no independent record that this address is the token people mean by this name. New tokens often aren't listed yet; copycats never are. Get the address from the project's official site or account.")
	}
}

// Brief is a one-row summary of a report for lists such as the watchlist.
type Brief struct {
	Address    string    `json:"address"`
	Chain      string    `json:"chain"`
	ChainLabel string    `json:"chainLabel"`
	Name       string    `json:"name"`
	Symbol     string    `json:"symbol"`
	PriceUSD   float64   `json:"priceUsd"`
	Change24h  float64   `json:"change24h"`
	MarketCap  float64   `json:"marketCap"`
	Liquidity  float64   `json:"liquidity"`
	Top10      float64   `json:"top10"`
	Verdict    string    `json:"verdict"`
	RiskScore  int       `json:"riskScore"`
	Reds       int       `json:"reds"`
	Ambers     int       `json:"ambers"`
	Identity   string    `json:"identity"` // original | lookalike | different_project | unlisted | unknown
	Listing    *Listing  `json:"listing,omitempty"`
	CheckedAt  time.Time `json:"checkedAt"`
}

func (r *Report) Brief() Brief {
	b := Brief{Address: r.Address, Chain: r.Chain, ChainLabel: r.ChainLabel, Name: r.Name, Symbol: r.Symbol, PriceUSD: r.PriceUSD,
		Change24h: r.Change["h24"], MarketCap: firstPositive(r.MarketCap, r.FDV), Liquidity: r.TotalLiq, Top10: r.Top10,
		Verdict: r.Verdict, RiskScore: r.RiskScore, Listing: r.Listing, CheckedAt: r.FetchedAt, Identity: "unknown"}
	for _, f := range r.Flags {
		switch f.Level {
		case "red":
			b.Reds++
		case "amber":
			b.Ambers++
		}
		if f.Area == "Identity" {
			b.Identity = "unlisted"
		}
	}
	switch {
	case r.Listing != nil && r.Lookalike == nil:
		b.Identity = "original"
	case r.Listing != nil:
		b.Identity = "different_project"
	case r.Lookalike != nil:
		b.Identity = "lookalike"
	}
	return b
}

var digits = regexp.MustCompile(`[0-9][0-9.,]*%?|\$[0-9.,]+[KMB]?`)

// RedKinds returns the red flags with numbers stripped ("One wallet holds
// 27.6%" -> "One wallet holds #"), so reports can be compared over time.
func (r *Report) RedKinds() map[string]bool {
	out := map[string]bool{}
	for _, f := range r.Flags {
		if f.Level == "red" {
			out[digits.ReplaceAllString(f.Title, "#")] = true
		}
	}
	return out
}
