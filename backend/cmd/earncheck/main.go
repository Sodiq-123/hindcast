// Command earncheck measures how well earnings-widened forecast ranges are
// calibrated: across past reports, how often the 80% range held the actual
// move, under the old widening (normal days + whole reaction) and the new one
// (report day replaces an ordinary day; jump measured without the ordinary
// days in its window). Uses only reports before each one, so no look-ahead.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"

	"github.com/sodiq/financial-market/backend/internal/config"
	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/store"
)

type ev struct {
	b0, r int
	x     float64 // log reaction
	vd    float64 // daily σ before the report
}

type tally struct {
	n, inBase, inOld, inNew int
	wOld, wNew              float64
}

func main() {
	ctx := context.Background()
	cfg := config.Load()
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	db := st.DB()
	rows, err := db.QueryContext(ctx, `SELECT p.symbol, p.bars FROM price_series p WHERE p.n_bars > 1500 AND EXISTS (SELECT 1 FROM earnings e WHERE e.symbol = p.symbol)`)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	type sym struct {
		name string
		bars []struct {
			Date  string  `json:"date"`
			Close float64 `json:"close"`
		}
	}
	var syms []sym
	for rows.Next() {
		var s sym
		var raw []byte
		if rows.Scan(&s.name, &raw) == nil && json.Unmarshal(raw, &s.bars) == nil {
			syms = append(syms, s)
		}
	}
	rows.Close()

	horizons := []int{0, 5, 21} // 0 = just the reaction window
	res := map[int]*tally{}
	for _, h := range horizons {
		res[h] = &tally{}
	}
	used := 0
	for _, s := range syms {
		n := len(s.bars)
		dates := make([]string, n)
		c := make([]float64, n)
		for i, b := range s.bars {
			dates[i], c[i] = b.Date, b.Close
		}
		vol := make([]float64, n) // 63-bar σ of daily log returns, ending at i
		for i := 64; i < n; i++ {
			var s1, s2 float64
			for k := i - 62; k <= i; k++ {
				r := math.Log(c[k] / c[k-1])
				s1 += r
				s2 += r * r
			}
			m := s1 / 63
			vol[i] = math.Sqrt(math.Max(0, s2/63-m*m))
		}
		erows, err := db.QueryContext(ctx, `SELECT date::text, time FROM earnings WHERE symbol = $1 AND eps IS NOT NULL ORDER BY date`, s.name)
		if err != nil {
			continue
		}
		var evs []ev
		for erows.Next() {
			var d, t string
			if erows.Scan(&d, &t) != nil {
				continue
			}
			b0, r, ok := features.ReactionBars(dates, features.EarningsEvent{Date: d, Time: t})
			if !ok || b0 < 64 || vol[b0] <= 0 || c[b0] <= 0 {
				continue
			}
			evs = append(evs, ev{b0, r, math.Log(c[r] / c[b0]), vol[b0]})
		}
		erows.Close()
		sort.Slice(evs, func(i, j int) bool { return evs[i].b0 < evs[j].b0 })
		if len(evs) < 8 {
			continue
		}
		used++
		for i := 4; i < len(evs); i++ {
			var sq, jump float64
			for _, p := range evs[:i] { // earlier reports only
				sq += p.x * p.x
				jump += p.x*p.x - float64(p.r-p.b0-1)*p.vd*p.vd
			}
			rms2 := sq / float64(i)
			j2 := math.Max(0, jump/float64(i))
			e := evs[i]
			w := e.r - e.b0
			for _, h := range horizons {
				H := w + h
				if e.b0+H >= n {
					continue
				}
				R := math.Log(c[e.b0+H] / c[e.b0])
				sd := e.vd
				base := math.Sqrt(sd * sd * float64(H))
				old := math.Sqrt(sd*sd*float64(H) + rms2)
				nw := math.Sqrt(math.Max(sd*sd*float64(H-1)+j2, sd*sd*float64(H)))
				t := res[h]
				t.n++
				z := 1.2816
				if math.Abs(R) <= z*base {
					t.inBase++
				}
				if math.Abs(R) <= z*old {
					t.inOld++
				}
				if math.Abs(R) <= z*nw {
					t.inNew++
				}
				t.wOld += old
				t.wNew += nw
			}
		}
	}
	fmt.Printf("%d stocks with 8+ reports and 6+ years of prices\n\n", used)
	fmt.Printf("%-22s %8s %10s %10s %10s %12s %12s\n", "window", "reports", "no widen", "current", "leaner", "current σ", "leaner σ")
	for _, h := range horizons {
		t := res[h]
		label := "reaction only"
		if h > 0 {
			label = fmt.Sprintf("reaction + %d days", h)
		}
		f := float64(t.n)
		fmt.Printf("%-22s %8d %9.1f%% %9.1f%% %9.1f%% %11.2f%% %11.2f%%\n", label, t.n, 100*float64(t.inBase)/f, 100*float64(t.inOld)/f, 100*float64(t.inNew)/f, 100*t.wOld/f, 100*t.wNew/f)
	}
	fmt.Println("\nTarget: 80% of actual moves inside the 80% range.")
}
