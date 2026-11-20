package token

import (
	"fmt"
	"math"
	"strings"
)

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }

func usd(x float64) string {
	switch {
	case x >= 1e9:
		return fmt.Sprintf("$%.2fB", x/1e9)
	case x >= 1e6:
		return fmt.Sprintf("$%.2fM", x/1e6)
	case x >= 1e3:
		return fmt.Sprintf("$%.0fK", x/1e3)
	}
	return fmt.Sprintf("$%.0f", x)
}

// assess adds holder, liquidity, age and price flags to the contract flags,
// then scores and summarises. Each red flag weighs 25 points, each amber 8;
// the score is a count of warning signs, not a probability of a rug.
func assess(r *Report) {
	add := func(level, area, title, detail string) {
		r.Flags = append(r.Flags, Flag{Level: level, Area: area, Title: title, Detail: detail})
	}

	// Holders
	if len(r.Holders) > 0 {
		switch {
		case r.Top1 > 0.20:
			add("red", "Holders", "One wallet holds "+pct(r.Top1), "A single wallet (excluding liquidity pools and locked or burned tokens) holds a large share. If it sells, the price can collapse. It may be a team treasury or an exchange, so check its label.")
		case r.Top1 > 0.10:
			add("amber", "Holders", "One wallet holds "+pct(r.Top1), "A large single holder. Watch for it selling.")
		}
		switch {
		case r.Top10 > 0.50:
			add("red", "Holders", "Top 10 wallets hold "+pct(r.Top10), "Supply is concentrated in a few hands. That makes coordinated dumps easy.")
		case r.Top10 > 0.30:
			add("amber", "Holders", "Top 10 wallets hold "+pct(r.Top10), "Moderately concentrated supply.")
		default:
			add("green", "Holders", "Top 10 wallets hold "+pct(r.Top10), "Supply is reasonably spread out.")
		}
	}
	switch {
	case r.HolderCount > 0 && r.HolderCount < 300:
		add("red", "Holders", fmt.Sprintf("Only %d holders", r.HolderCount), "Very few people own it: easy to manipulate, hard to exit.")
	case r.HolderCount > 0 && r.HolderCount < 2000:
		add("amber", "Holders", fmt.Sprintf("%d holders", r.HolderCount), "A small community.")
	}

	// Liquidity
	switch {
	case r.Liquidity < 50_000:
		add("red", "Liquidity", "Liquidity only "+usd(r.Liquidity), "Even a small sell will move the price a lot, and the pool can be drained or removed. Exiting a position of even a few thousand dollars may be costly.")
	case r.Liquidity < 250_000:
		add("amber", "Liquidity", "Liquidity "+usd(r.Liquidity), "Thin: large orders move the price noticeably.")
	default:
		add("green", "Liquidity", "Liquidity "+usd(r.Liquidity), "Enough depth for ordinary-sized trades.")
	}
	mc := firstPositive(r.MarketCap, r.FDV)
	if mc > 0 {
		ratio := r.TotalLiq / mc
		switch {
		case ratio < 0.005:
			add("red", "Liquidity", "Liquidity is "+pct(ratio)+" of market cap", "The market cap is mostly paper value: only a tiny fraction could actually be sold without crashing the price.")
		case ratio < 0.02:
			add("amber", "Liquidity", "Liquidity is "+pct(ratio)+" of market cap", "Thin relative to its valuation.")
		}
	}
	if r.Liquidity > 0 && r.Volume24h/r.Liquidity > 10 {
		add("amber", "Liquidity", fmt.Sprintf("24h volume is %.0f× liquidity", r.Volume24h/r.Liquidity), "Extremely high turnover: either a frenzy or wash trading (fake volume).")
	}
	if r.LPLocked != nil {
		switch {
		case *r.LPLocked < 0.5:
			add("red", "Liquidity", "Only "+pct(*r.LPLocked)+" of pool tokens locked or burned", "Whoever holds the pool tokens can withdraw the liquidity (a rug pull).")
		case *r.LPLocked > 0.9:
			add("green", "Liquidity", pct(*r.LPLocked)+" of pool tokens locked or burned", "Liquidity can't easily be pulled.")
		}
	}

	// Age
	switch {
	case r.AgeDays > 0 && r.AgeDays < 7:
		add("red", "Age", fmt.Sprintf("Launched %.0f days ago", r.AgeDays), "Most rug pulls happen in the first days. There's no track record at all.")
	case r.AgeDays > 0 && r.AgeDays < 60:
		add("amber", "Age", fmt.Sprintf("Launched %.0f days ago", r.AgeDays), "Young: little history to judge it by.")
	case r.AgeDays >= 365:
		add("green", "Age", fmt.Sprintf("Trading for %.1f years", r.AgeDays/365), "Has survived longer than most meme coins.")
	}

	// Price behaviour
	if h := r.History; h != nil {
		if h.FromHigh < -0.8 {
			add("amber", "Price", pct(-h.FromHigh)+" below its high", fmt.Sprintf("It has lost most of its value from the high of the last %.0f days. Collapses like this rarely fully recover.", h.WindowDays))
		}
		if h.AnnualizedVol > 1.5 {
			add("amber", "Price", fmt.Sprintf("Typical daily swing ±%.0f%%", 100*h.TypicalDayMove), "Extremely volatile: a normal day can move more than most stocks move in a month. Size any position so that losing most of it is acceptable.")
		}
	}

	score := 0
	reds, ambers := 0, 0
	for _, f := range r.Flags {
		switch f.Level {
		case "red":
			score += 25
			reds++
		case "amber":
			score += 8
			ambers++
		}
	}
	r.RiskScore = min(100, score)
	switch {
	case reds >= 2 || r.RiskScore >= 60:
		r.Verdict = "Very high risk"
	case reds == 1 || r.RiskScore >= 35:
		r.Verdict = "High risk"
	case r.RiskScore >= 15:
		r.Verdict = "Elevated risk"
	default:
		r.Verdict = "Fewer warning signs (still a high-risk asset class)"
	}
	var top []string
	for _, f := range r.Flags {
		if f.Level == "red" && len(top) < 3 {
			top = append(top, strings.ToLower(f.Title[:1])+f.Title[1:])
		}
	}
	r.Summary = fmt.Sprintf("%s on %s: %d red and %d amber warning signs.", r.Symbol, r.ChainLabel, reds, ambers)
	if len(top) > 0 {
		r.Summary += " Biggest concerns: " + strings.Join(top, "; ") + "."
	}

	days := r.AgeDays
	if r.History != nil && r.History.WindowDays > days {
		days = r.History.WindowDays
	}
	r.NoForecast = fmt.Sprintf("No price forecast: only about %.0f days of trading history are available, and the system needs several years of data to prove a forecast works before showing one. "+
		"Meme coin prices are driven mainly by attention, influencers, whales and insiders, which price history can't capture. Anyone claiming to predict them accurately is guessing. "+
		"Use this report to avoid scams and to size the position, not to time the price.", math.Max(1, days))
}
