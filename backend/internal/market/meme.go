package market

import "strings"

// MemeCoin ties a meme coin's Yahoo ticker to its CoinGecko id. Yahoo
// often lists several coins under one ticker (two "PUMP"s, for example), so
// every entry here was checked by price and launch date against CoinGecko's
// record of the official contract (5 Oct 2026).
type MemeCoin struct {
	Ticker string // Yahoo symbol
	ID     string // CoinGecko id
	Name   string
}

// MemeCoins is the meme-coin ranking universe. Coins that Yahoo rounds to a
// few significant digits (SHIB, PEPE, FLOKI, BONK, MOG, MOODENG) fall back to
// CoinGecko and so only contribute their most recent year.
var MemeCoins = []MemeCoin{
	{"DOGE-USD", "dogecoin", "Dogecoin"},
	{"SHIB-USD", "shiba-inu", "Shiba Inu"},
	{"PEPE24478-USD", "pepe", "Pepe"},
	{"FLOKI-USD", "floki", "FLOKI"},
	{"BONK-USD", "bonk", "Bonk"},
	{"WIF-USD", "dogwifcoin", "dogwifhat"},
	{"POPCAT28782-USD", "popcat", "Popcat"},
	{"BOME-USD", "book-of-meme", "BOOK OF MEME"},
	{"MEW30126-USD", "cat-in-a-dogs-world", "cat in a dogs world"},
	{"BRETT29743-USD", "based-brett", "Brett"},
	{"TURBO-USD", "turbo", "Turbo"},
	{"MEME28301-USD", "memecoin-2", "Memecoin"},
	{"MYRO-USD", "myro", "MYRO"},
	{"MOTHER-USD", "mother-iggy", "Mother Iggy"},
	{"NEIRO32521-USD", "neiro-3", "Neiro"},
	{"FARTCOIN-USD", "fartcoin", "Fartcoin"},
	{"GOAT33440-USD", "goatseus-maximus", "Goatseus Maximus"},
	{"PNUT-USD", "peanut-the-squirrel", "Peanut the Squirrel"},
	{"ELIZA34079-USD", "ai16z", "ai16z"},
	{"TRUMP35336-USD", "official-trump", "Official Trump"},
	{"PENGU34466-USD", "pudgy-penguins", "Pudgy Penguins"},
	{"PUMP36507-USD", "pump-fun", "Pump.fun"},
	{"MOG-USD", "mog-coin", "Mog Coin"},
	{"MOODENG33076-USD", "moo-deng", "Moo Deng"},
}

// MemeTickers returns the Yahoo tickers of the meme universe.
func MemeTickers() []string {
	out := make([]string, len(MemeCoins))
	for i, m := range MemeCoins {
		out[i] = m.Ticker
	}
	return out
}

// MemeByID finds a meme coin by its CoinGecko id.
func MemeByID(id string) (MemeCoin, bool) {
	for _, m := range MemeCoins {
		if m.ID == id {
			return m, true
		}
	}
	return MemeCoin{}, false
}

// MemeByBase finds the verified ticker for a base symbol such as "PUMP".
func MemeByBase(base string) (MemeCoin, bool) {
	base = strings.ToUpper(strings.TrimSpace(base))
	for _, m := range MemeCoins {
		b := strings.TrimSuffix(m.Ticker, "-USD")
		b = strings.TrimRight(b, "0123456789")
		if b == base {
			return m, true
		}
	}
	return MemeCoin{}, false
}

// VerifyMemeResults puts the verified ticker first when a search matches a
// known meme coin, and labels other coins Yahoo lists under the same
// ticker, so "PUMP" leads to Pump.fun rather than an unrelated "PUMP".
func VerifyMemeResults(query string, res []SearchResult) []SearchResult {
	q := strings.ToUpper(strings.TrimSpace(query))
	q = strings.TrimSuffix(q, "-USD")
	m, ok := MemeByBase(strings.TrimRight(q, "0123456789"))
	if !ok {
		for _, c := range MemeCoins {
			if strings.EqualFold(c.Name, strings.TrimSpace(query)) || strings.EqualFold(c.ID, strings.TrimSpace(query)) {
				m, ok = c, true
			}
		}
	}
	if !ok {
		return res
	}
	base := strings.TrimRight(strings.TrimSuffix(m.Ticker, "-USD"), "0123456789")
	out := []SearchResult{{Symbol: m.Ticker, Name: m.Name + " (verified coin)", Exchange: "CCC", Type: "Cryptocurrency"}}
	for _, r := range res {
		if r.Symbol == m.Ticker {
			continue
		}
		rb := strings.TrimRight(strings.TrimSuffix(strings.ToUpper(r.Symbol), "-USD"), "0123456789")
		if strings.EqualFold(r.Type, "cryptocurrency") && rb == base {
			r.Name += " (a different coin, not " + m.Name + ")"
		}
		out = append(out, r)
	}
	return out
}

// MemeCoinForTicker finds a meme coin by its exact Yahoo ticker.
func MemeCoinForTicker(ticker string) (MemeCoin, bool) {
	for _, m := range MemeCoins {
		if strings.EqualFold(m.Ticker, ticker) {
			return m, true
		}
	}
	return MemeCoin{}, false
}
