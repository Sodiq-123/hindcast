package engine

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/model"
	"github.com/sodiq/financial-market/backend/internal/vol"
)

func pct(x float64) string  { return fmt.Sprintf("%+.1f%%", 100*x) }
func pctu(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }
func pp(x float64) string   { return fmt.Sprintf("%+.1f pp", 100*x) }

// num formats a price-like number readably at any magnitude (86,106 not 8.611e+04).
func num(x float64) string {
	a := math.Abs(x)
	switch {
	case a >= 1000:
		s := fmt.Sprintf("%.0f", math.Abs(x))
		for i := len(s) - 3; i > 0; i -= 3 {
			s = s[:i] + "," + s[i:]
		}
		if x < 0 {
			s = "-" + s
		}
		return s
	case a >= 1:
		return fmt.Sprintf("%.2f", x)
	case a == 0:
		return "0"
	}
	return fmt.Sprintf("%.4g", x)
}

// describe renders a feature's current raw value in trader language.
func describe(key string, v float64) string {
	switch key {
	case "ret_1":
		return "Last session " + pct(math.Exp(v)-1)
	case "ret_5":
		return "Past week " + pct(math.Exp(v)-1)
	case "ret_21":
		return "Past month " + pct(math.Exp(v)-1)
	case "ret_63":
		return "Past 3 months " + pct(math.Exp(v)-1)
	case "mom_12_1":
		return "12-month momentum (excluding last month) " + pct(math.Exp(v)-1)
	case "dist_sma20":
		return fmt.Sprintf("Price %s its 20-day average", aboveBelow(v))
	case "dist_sma50":
		return fmt.Sprintf("Price %s its 50-day average", aboveBelow(v))
	case "dist_sma200":
		return fmt.Sprintf("Price %s its 200-day average", aboveBelow(v))
	case "sma50_200":
		regime := "golden-cross regime"
		if v < 0 {
			regime = "death-cross regime"
		}
		return fmt.Sprintf("50-day average %s the 200-day (%s)", aboveBelow(v), regime)
	case "adx_dir":
		side := "buyers"
		if v < 0 {
			side = "sellers"
		}
		adx := math.Abs(v) * 100
		strength := "weak/no trend"
		if adx >= 40 {
			strength = "very strong trend"
		} else if adx >= 25 {
			strength = "established trend"
		} else if adx >= 20 {
			strength = "emerging trend"
		}
		return fmt.Sprintf("ADX %.0f, %s in control (%s)", adx, side, strength)
	case "rsi14":
		r := 50 + 50*v
		zone := "neutral zone"
		switch {
		case r >= 70:
			zone = "overbought (>70)"
		case r <= 30:
			zone = "oversold (<30)"
		case r >= 60:
			zone = "bullish zone"
		case r <= 40:
			zone = "bearish zone"
		}
		return fmt.Sprintf("RSI(14) %.1f, %s", r, zone)
	case "bb_z20":
		return fmt.Sprintf("Price %.2fσ %s its 20-day mean (Bollinger bands sit at ±2σ)", math.Abs(v), map[bool]string{true: "above", false: "below"}[v >= 0])
	case "stoch14":
		k := 50 + 50*v
		return fmt.Sprintf("Stochastic %%K %.0f (where price sits in its 14-day range)", k)
	case "macd_hist":
		dir := "positive — upside momentum building"
		if v < 0 {
			dir = "negative — downside momentum building"
		}
		return fmt.Sprintf("MACD histogram %s (%.2f× ATR)", dir, v)
	case "vol_21":
		return fmt.Sprintf("Annualised 1-month volatility %.0f%%", 100*v)
	case "vol_ratio":
		return fmt.Sprintf("Short-term volatility %s its 6-month norm", aboveBelow(math.Exp(v)-1))
	case "atr_pct":
		return fmt.Sprintf("Average true range %.2f%% of price per day", 100*v)
	case "dist_52w_high":
		if v > -0.005 {
			return "At a 52-week high"
		}
		return fmt.Sprintf("%.1f%% below the 52-week high", -100*v)
	case "dist_52w_low":
		return fmt.Sprintf("%.1f%% above the 52-week low", 100*v)
	case "volume_z":
		return fmt.Sprintf("Volume %.1fσ %s its 20-day norm", math.Abs(v), map[bool]string{true: "above", false: "below"}[v >= 0])
	case "obv_flow":
		dir := "net buying"
		if v < 0 {
			dir = "net selling"
		}
		return fmt.Sprintf("On-balance volume shows %s over 20 days (%+.2f)", dir, v)
	case "bench_ret_21":
		return "Benchmark past month " + pct(math.Exp(v)-1)
	case "bench_trend":
		regime := "bull-market regime"
		if v < 0 {
			regime = "bear-market regime"
		}
		return fmt.Sprintf("Benchmark %s its 200-day average (%s)", aboveBelow(v), regime)
	case "rel_strength_63":
		if v >= 0 {
			return fmt.Sprintf("Outperformed its benchmark by %.1f%% over 3 months", 100*v)
		}
		return fmt.Sprintf("Underperformed its benchmark by %.1f%% over 3 months", -100*v)
	case "vix_z":
		mood := "normal"
		if v > 1 {
			mood = "elevated fear"
		} else if v < -1 {
			mood = "complacency"
		}
		return fmt.Sprintf("VIX %.1fσ vs its 1-year norm (%s)", v, mood)
	case "vix_chg_5":
		return "VIX past week " + pct(math.Exp(v)-1)
	case "tnx_chg_21":
		return fmt.Sprintf("US 10-year yield %+.2f pts over the month", v)
	case "dxy_chg_21":
		return "US dollar index past month " + pct(math.Exp(v)-1)
	case "vix_term":
		state := "normal upward-sloping curve (calm)"
		if v > 0 {
			state = "inverted: near-term fear above longer-term (stress)"
		}
		return fmt.Sprintf("VIX is %.0f%% %s its 3-month counterpart, %s", math.Abs(math.Exp(v)-1)*100, map[bool]string{true: "above", false: "below"}[v > 0], state)
	case "skew_z":
		return fmt.Sprintf("SKEW index %.1fσ vs its 1-year norm (demand for crash protection)", v)
	case "vvix_z":
		return fmt.Sprintf("VVIX %.1fσ vs its 1-year norm (uncertainty about volatility itself)", v)
	case "funding_7d":
		crowd := "longs paying shorts"
		if v < 0 {
			crowd = "shorts paying longs"
		}
		return fmt.Sprintf("Perpetual funding %.1f%% a year (%s)", 100*v, crowd)
	case "funding_z":
		return fmt.Sprintf("Funding %.1fσ vs its 3-month norm (leverage crowding)", v)
	case "beat4":
		return fmt.Sprintf("Beat consensus in %.0f of the last 4 quarters", (v/2+0.5)*4)
	case "fng":
		return fmt.Sprintf("Crypto Fear & Greed %.0f (%s)", 50+50*v, fngLabel(50+50*v))
	case "fng_chg_7":
		return fmt.Sprintf("Fear & Greed %+.0f points this week", 50*v)
	}
	return fmt.Sprintf("%.4f", v)
}

func aboveBelow(v float64) string {
	if v >= 0 {
		return fmt.Sprintf("%.1f%% above", 100*v)
	}
	return fmt.Sprintf("%.1f%% below", -100*v)
}

func fngLabel(v float64) string {
	switch {
	case v < 25:
		return "Extreme Fear"
	case v < 45:
		return "Fear"
	case v <= 55:
		return "Neutral"
	case v <= 75:
		return "Greed"
	}
	return "Extreme Greed"
}

// buildDrivers converts logistic contributions into ranked, explained drivers.
func buildDrivers(m *features.Matrix, live *model.Live, horizonLabel string) ([]Driver, []GroupImpact) {
	last := len(m.Close) - 1
	p := live.PLogistic
	slope := p * (1 - p) // dp/dlogit at the current forecast
	var ds []Driver
	groups := map[string]float64{}
	for j, def := range m.Defs {
		c := live.Contributions[j]
		d := Driver{
			Key: def.Key, Label: def.Label, Group: def.Group,
			Value: m.X[last][j], Z: live.Z[j], Weight: live.Weights[j],
			Contribution: c, ProbImpact: slope * c,
		}
		d.Text = driverText(d, horizonLabel)
		ds = append(ds, d)
		groups[def.Group] += d.ProbImpact
	}
	sort.Slice(ds, func(a, b int) bool { return math.Abs(ds[a].Contribution) > math.Abs(ds[b].Contribution) })
	var gs []GroupImpact
	for g, v := range groups {
		gs = append(gs, GroupImpact{g, v})
	}
	sort.Slice(gs, func(a, b int) bool { return math.Abs(gs[a].ProbImpact) > math.Abs(gs[b].ProbImpact) })
	return ds, gs
}

func driverText(d Driver, horizon string) string {
	level := "near its normal level"
	switch {
	case d.Z > 1.5:
		level = "far above normal"
	case d.Z > 0.5:
		level = "above normal"
	case d.Z < -1.5:
		level = "far below normal"
	case d.Z < -0.5:
		level = "below normal"
	}
	if math.Abs(d.Weight) < 0.005 {
		return fmt.Sprintf("%s — %s for this asset (%+.1fσ). The model learned this has had essentially no predictive value for the %s horizon.", describe(d.Key, d.Value), level, d.Z, strings.ToLower(horizon))
	}
	rel := "higher"
	if d.Weight < 0 {
		rel = "lower"
	}
	push := "raises"
	if d.Contribution < 0 {
		push = "lowers"
	}
	return fmt.Sprintf("%s — %s for this asset (%+.1fσ). Historically, higher readings here have preceded %s odds of a rise over the %s horizon, so this %s the forecast by %s.",
		describe(d.Key, d.Value), level, d.Z, rel, strings.ToLower(horizon), push, pp(math.Abs(d.ProbImpact)))
}

// readings are textbook interpretations of the latest indicators.
func readings(m *features.Matrix, price float64) []IndicatorReading {
	last := len(m.Close) - 1
	r := func(k string) float64 {
		if a, ok := m.Raw[k]; ok && last < len(a) {
			return a[last]
		}
		return math.NaN()
	}
	var out []IndicatorReading
	add := func(x IndicatorReading) {
		if !math.IsNaN(x.Value) && !math.IsInf(x.Value, 0) {
			out = append(out, x)
		}
	}
	sig := func(b bool, bullish, bearish string) string {
		if b {
			return bullish
		}
		return bearish
	}
	for _, s := range []struct{ k, name string }{{"sma20", "20-day average"}, {"sma50", "50-day average"}, {"sma200", "200-day average"}} {
		v := r(s.k)
		add(IndicatorReading{Name: "Price vs " + s.name, Group: "Trend", Value: v, Display: fmt.Sprintf("%s (price %s)", num(v), aboveBelow(price/v-1)),
			Signal: sig(price >= v, "bullish", "bearish"), Note: "Price above a rising average is the classic definition of an uptrend."})
	}
	if s50, s200 := r("sma50"), r("sma200"); !math.IsNaN(s50) && !math.IsNaN(s200) {
		add(IndicatorReading{Name: "Golden / death cross", Group: "Trend", Value: s50/s200 - 1, Display: fmt.Sprintf("50d %s 200d", aboveBelow(s50/s200-1)),
			Signal: sig(s50 >= s200, "bullish", "bearish"), Note: "50-day above 200-day = long-term uptrend regime."})
	}
	if v := r("rsi"); !math.IsNaN(v) {
		s, note := "neutral", "Between 30 and 70: no extreme."
		if v >= 70 {
			s, note = "bearish", "Overbought: pullbacks are common, although strong trends can stay overbought for weeks."
		} else if v <= 30 {
			s, note = "bullish", "Oversold: bounces are common, although downtrends can stay oversold."
		}
		add(IndicatorReading{Name: "RSI (14)", Group: "Momentum", Value: v, Display: fmt.Sprintf("%.1f", v), Signal: s, Note: note})
	}
	if v, sg := r("macd"), r("macd_signal"); !math.IsNaN(v) && !math.IsNaN(sg) {
		add(IndicatorReading{Name: "MACD vs signal", Group: "Momentum", Value: v - sg, Display: fmt.Sprintf("MACD %s, signal %s", num(v), num(sg)),
			Signal: sig(v >= sg, "bullish", "bearish"), Note: sig(v >= sg, "MACD above its signal line: upside momentum is strengthening.", "MACD below its signal line: upside momentum is fading (or downside momentum building).")})
	}
	if v := r("bbz"); !math.IsNaN(v) {
		s := "neutral"
		if v > 2 {
			s = "bearish"
		} else if v < -2 {
			s = "bullish"
		}
		add(IndicatorReading{Name: "Bollinger position", Group: "Mean reversion", Value: v, Display: fmt.Sprintf("%+.2fσ", v), Signal: s, Note: "Beyond ±2σ is statistically stretched; mean reversion is more likely in range-bound markets."})
	}
	if v := r("stoch"); !math.IsNaN(v) {
		s := "neutral"
		if v > 80 {
			s = "bearish"
		} else if v < 20 {
			s = "bullish"
		}
		add(IndicatorReading{Name: "Stochastic %K", Group: "Mean reversion", Value: v, Display: fmt.Sprintf("%.0f", v), Signal: s, Note: "Above 80 overbought, below 20 oversold within the recent range."})
	}
	if v, p, mi := r("adx"), r("plus_di"), r("minus_di"); !math.IsNaN(v) {
		s := "neutral"
		if v >= 20 {
			s = sig(p >= mi, "bullish", "bearish")
		}
		add(IndicatorReading{Name: "ADX trend strength", Group: "Trend", Value: v, Display: fmt.Sprintf("%.1f (+DI %.1f / −DI %.1f)", v, p, mi), Signal: s, Note: "ADX above 25 means a real trend exists; DI lines show its direction."})
	}
	if v := r("vol21"); !math.IsNaN(v) {
		add(IndicatorReading{Name: "Volatility (1m, annualised)", Group: "Risk", Value: v, Display: pctu(v), Signal: "neutral", Note: "Higher volatility widens both the upside and the downside range."})
	}
	if hi, lo := r("hi52"), r("lo52"); !math.IsNaN(hi) && hi > lo {
		pos := (price - lo) / (hi - lo)
		s := "neutral"
		if pos > 0.9 {
			s = "bullish"
		} else if pos < 0.1 {
			s = "bearish"
		}
		add(IndicatorReading{Name: "52-week range position", Group: "Trend", Value: pos, Display: fmt.Sprintf("%.0f%% of range (low %s – high %s)", 100*pos, num(lo), num(hi)), Signal: s, Note: "Stocks near 52-week highs tend to keep outperforming (momentum), contrary to intuition."})
	}
	if v := r("volume_z"); !math.IsNaN(v) {
		s := "neutral"
		if v > 2 {
			s = "bullish"
			if r("ret_5") < 0 {
				s = "bearish"
			}
		}
		add(IndicatorReading{Name: "Volume vs normal", Group: "Volume", Value: v, Display: fmt.Sprintf("%+.1fσ", v), Signal: s, Note: "Unusual volume confirms the direction of the move it accompanies."})
	}
	if v := r("bench_trend"); !math.IsNaN(v) {
		add(IndicatorReading{Name: "Benchmark regime", Group: "Market", Value: v, Display: "Benchmark " + aboveBelow(v) + " its 200-day average", Signal: sig(v >= 0, "bullish", "bearish"), Note: "Most assets fall in bear markets; the tide lifts or sinks all boats."})
	}
	if v := r("vix"); !math.IsNaN(v) {
		s := "neutral"
		if v > 25 {
			s = "bearish"
		} else if v < 15 {
			s = "bullish"
		}
		add(IndicatorReading{Name: "VIX", Group: "Macro", Value: v, Display: fmt.Sprintf("%.1f", v), Signal: s, Note: "Above 25 = stressed market; below 15 = calm. Extreme spikes often mark short-term bottoms."})
	}
	if v := r("tnx"); !math.IsNaN(v) {
		add(IndicatorReading{Name: "US 10-year yield", Group: "Macro", Value: v, Display: fmt.Sprintf("%.2f%%", v), Signal: "neutral", Note: "Rising yields pressure growth stocks and long-duration assets."})
	}
	if v := r("dxy"); !math.IsNaN(v) {
		add(IndicatorReading{Name: "US dollar index", Group: "Macro", Value: v, Display: fmt.Sprintf("%.2f", v), Signal: "neutral", Note: "A strong dollar tends to weigh on commodities, crypto and non-US assets."})
	}
	if v, v3 := r("vix"), r("vix3m"); !math.IsNaN(v) && !math.IsNaN(v3) && v3 > 0 {
		s := "neutral"
		if v > v3 {
			s = "bearish"
		}
		add(IndicatorReading{Name: "VIX term structure", Group: "Macro", Value: v / v3, Display: fmt.Sprintf("VIX %.1f vs VIX3M %.1f", v, v3), Signal: s, Note: "Inverted (1-month above 3-month) only happens in acute stress; it has often marked short-term lows as well as sell-offs."})
	}
	if v := r("skew"); !math.IsNaN(v) {
		add(IndicatorReading{Name: "SKEW index", Group: "Macro", Value: v, Display: fmt.Sprintf("%.1f", v), Signal: "neutral", Note: "Price of far out-of-the-money puts: high = investors paying up for crash protection."})
	}
	if v := r("funding_7d"); !math.IsNaN(v) {
		s := "neutral"
		if v > 0.3 {
			s = "bearish"
		} else if v < 0 {
			s = "bullish"
		}
		add(IndicatorReading{Name: "Perpetual funding (7d, annualised)", Group: "Sentiment", Value: v, Display: fmt.Sprintf("%.1f%%", 100*v), Signal: s, Note: "Very high funding = crowded leveraged longs (contrarian bearish); negative funding = crowded shorts."})
	}
	if v := r("fng"); !math.IsNaN(v) {
		s := "neutral"
		if v < 25 {
			s = "bullish"
		} else if v > 75 {
			s = "bearish"
		}
		add(IndicatorReading{Name: "Crypto Fear & Greed", Group: "Sentiment", Value: v, Display: fmt.Sprintf("%.0f (%s)", v, fngLabel(v)), Signal: s, Note: "Contrarian: extreme fear has historically been a better time to buy than extreme greed."})
	}
	return out
}

// conditionalEvidence checks how this asset behaved after past days that
// shared today's notable conditions.
func conditionalEvidence(m *features.Matrix, horizons map[string]int) []ConditionalStat {
	n := len(m.Close)
	last := n - 1
	raw := m.Raw
	val := func(k string, i int) float64 {
		if a, ok := raw[k]; ok {
			return a[i]
		}
		return math.NaN()
	}
	type cond struct {
		name, note string
		test       func(i int) bool
	}
	var conds []cond
	add := func(name, note string, f func(i int) bool) {
		if f(last) {
			conds = append(conds, cond{name, note, f})
		}
	}
	above := func(a, b string) func(int) bool {
		return func(i int) bool { x, y := val(a, i), val(b, i); return !math.IsNaN(x) && !math.IsNaN(y) && x > y }
	}
	below := func(a, b string) func(int) bool {
		return func(i int) bool { x, y := val(a, i), val(b, i); return !math.IsNaN(x) && !math.IsNaN(y) && x < y }
	}
	add("Price above 200-day average", "Long-term uptrend", above("close", "sma200"))
	add("Price below 200-day average", "Long-term downtrend", below("close", "sma200"))
	add("50-day above 200-day (golden-cross regime)", "", above("sma50", "sma200"))
	add("50-day below 200-day (death-cross regime)", "", below("sma50", "sma200"))
	rsiBand := func(lo, hi float64) func(int) bool {
		return func(i int) bool { r := val("rsi", i); return r >= lo && r < hi }
	}
	add("RSI overbought (≥70)", "", rsiBand(70, 101))
	add("RSI oversold (≤30)", "", rsiBand(-1, 30))
	add("RSI 55–70 (bullish momentum)", "", rsiBand(55, 70))
	add("RSI 30–45 (weak momentum)", "", rsiBand(30, 45))
	add("Within 3% of 52-week high", "Breakout territory", func(i int) bool { h := val("hi52", i); return h > 0 && val("close", i) >= 0.97*h })
	add("More than 25% below 52-week high", "Deep drawdown", func(i int) bool { h := val("hi52", i); return h > 0 && val("close", i) <= 0.75*h })
	add("MACD above signal line", "", above("macd", "macd_signal"))
	add("MACD below signal line", "", below("macd", "macd_signal"))
	add("Unusually high volume (>2σ)", "", func(i int) bool { return val("volume_z", i) > 2 })
	add("Bollinger stretch above +2σ", "", func(i int) bool { return val("bbz", i) > 2 })
	add("Bollinger stretch below −2σ", "", func(i int) bool { return val("bbz", i) < -2 })
	add("VIX above 25 (stressed market)", "", func(i int) bool { return val("vix", i) > 25 })
	add("VIX below 15 (calm market)", "", func(i int) bool { return val("vix", i) < 15 })
	add("Extreme fear (F&G < 25)", "", func(i int) bool { return val("fng", i) < 25 })
	add("Extreme greed (F&G > 75)", "", func(i int) bool { return val("fng", i) > 75 })
	add("Down more than 10% in the past month", "", func(i int) bool { r := val("ret_21", i); return !math.IsNaN(r) && r < math.Log(0.9) })
	add("Up more than 15% in the past month", "", func(i int) bool { r := val("ret_21", i); return !math.IsNaN(r) && r > math.Log(1.15) })

	start := m.Start
	if start < 1 {
		start = 1
	}
	var out []ConditionalStat
	for _, c := range conds {
		cs := ConditionalStat{Condition: c.name, Note: c.note, ByHorizon: map[string]CondOutcome{}}
		for key, h := range horizons {
			var o CondOutcome
			var allN, condN, allUp, condUp, allRet, condRet float64
			for i := start; i+h < n; i++ {
				r := m.Close[i+h]/m.Close[i] - 1
				up := 0.0
				if r > 0 {
					up = 1
				}
				allN++
				allUp += up
				allRet += r
				if c.test(i) {
					condN++
					condUp += up
					condRet += r
				}
			}
			if condN >= 10 && allN > 0 {
				o = CondOutcome{N: int(condN), UpRate: condUp / condN, AvgReturn: condRet / condN, AllUpRate: allUp / allN, AllAvgReturn: allRet / allN}
				cs.ByHorizon[key] = o
			}
		}
		if len(cs.ByHorizon) > 0 {
			out = append(out, cs)
		}
	}
	return out
}

// classify applies the validation gate and maps probability to a signal.
func classify(v model.Validation, p float64) (status, note, signal string) {
	e := v.Ensemble
	switch {
	case e.BrierSkill > 0 && e.AUC >= 0.52 && e.PValue < 0.10 && e.NEffective >= 20:
		status = "validated"
		note = fmt.Sprintf("Beat the naive base-rate forecast out-of-sample (Brier skill %+.2f%%, AUC %.3f, %d independent periods, p=%.3f).", 100*e.BrierSkill, e.AUC, e.NEffective, e.PValue)
	case e.BrierSkill > 0:
		status = "weak"
		reasons := []string{}
		if e.AUC < 0.52 {
			reasons = append(reasons, fmt.Sprintf("AUC only %.3f", e.AUC))
		}
		if e.PValue >= 0.10 {
			reasons = append(reasons, fmt.Sprintf("edge not statistically significant (p=%.2f)", e.PValue))
		}
		if e.NEffective < 20 {
			reasons = append(reasons, fmt.Sprintf("only %d independent test periods", e.NEffective))
		}
		note = "Some out-of-sample skill, but " + strings.Join(reasons, "; ") + ". Shown as a lean, not a trade signal."
	default:
		status = "no_edge"
		note = fmt.Sprintf("Did NOT beat the naive base-rate forecast out-of-sample (Brier skill %+.2f%%). For this asset and horizon the method has no demonstrated edge — the probability is shown for transparency only.", 100*e.BrierSkill)
	}
	edge := p - 0.5
	switch status {
	case "no_edge":
		signal = "NO EDGE"
	case "validated":
		switch {
		case edge >= 0.10:
			signal = "STRONG BUY"
		case edge >= 0.04:
			signal = "BUY"
		case edge <= -0.10:
			signal = "STRONG SELL"
		case edge <= -0.04:
			signal = "SELL"
		default:
			signal = "HOLD"
		}
	default:
		// Weak evidence never produces a trade signal. Replays showed weak
		// "BUY"s were mostly the base rate in disguise (BTC 1-year: right 21%
		// of 14 times), so the lean is shown but not actionable.
		switch {
		case edge >= 0.06:
			signal = "LEAN UP"
		case edge <= -0.06:
			signal = "LEAN DOWN"
		default:
			signal = "HOLD"
		}
	}
	return
}

// trackAt finds "when the model was at least this confident" in the OOS table.
func trackAt(v model.Validation, pRaw float64) *model.ConfidenceRow {
	edge := math.Abs(pRaw - 0.5)
	var best *model.ConfidenceRow
	for i := range v.Confidence {
		row := v.Confidence[i]
		if row.MinEdge <= edge+1e-12 && row.N >= 30 {
			r := row
			best = &r
		}
	}
	return best
}

func conviction(status string, pUp float64, track *model.ConfidenceRow, v model.Validation, pLR, pKNN float64) (int, map[string]float64) {
	parts := map[string]float64{}
	parts["edge"] = math.Min(1, math.Abs(pUp-0.5)/0.15)
	if track != nil {
		parts["trackRecord"] = math.Max(0, math.Min(1, (track.Accuracy-0.5)/0.15))
	}
	parts["significance"] = math.Max(0, math.Min(1, (0.10-v.Ensemble.PValue)/0.10))
	if (pLR > 0.5) == (pKNN > 0.5) {
		parts["agreement"] = 1
	} else {
		parts["agreement"] = 0.3
	}
	if status == "no_edge" {
		return 0, parts
	}
	score := 100 * (0.35*parts["edge"] + 0.35*parts["trackRecord"] + 0.2*parts["significance"] + 0.1*parts["agreement"])
	if status == "weak" {
		score *= 0.5
	}
	return int(math.Round(score)), parts
}

// riskPlan derives stop, target and position size from the forecast
// distribution. Sizing is quarter-Kelly, capped at 20% of capital, and zero
// unless the signal is a validated or weak BUY.
func riskPlan(price, pUp float64, q map[string]float64, analogs []model.Neighbor, scale float64, signal string) RiskPlan {
	rp := RiskPlan{
		StopLoss:   price * math.Exp(q["p10"]),
		TakeProfit: price * math.Exp(q["p75"]),
	}
	if loss := price - rp.StopLoss; loss > 0 {
		rp.RewardRisk = (rp.TakeProfit - price) / loss
	}
	var win, lose, nw, nl float64
	for _, n := range analogs {
		r := math.Exp(n.RetNorm*scale) - 1
		if r > 0 {
			win += r
			nw++
		} else {
			lose -= r
			nl++
		}
	}
	if nw > 0 && nl > 0 {
		W, L := win/nw, lose/nl
		rp.KellyFraction = pUp/L - (1-pUp)/W
	}
	if strings.Contains(signal, "BUY") && rp.KellyFraction > 0 {
		rp.SuggestedSize = math.Min(0.20, rp.KellyFraction/4)
		rp.Note = "Quarter-Kelly sizing from the analog win/loss distribution, capped at 20% of capital. Full Kelly is shown for reference only — it is far too aggressive given estimation error."
	} else {
		rp.Note = "No long position suggested for this signal."
	}
	return rp
}

// volAdjust forecasts volatility for the horizon with the HAR model and, only
// if that beat the 63-day historical volatility out of sample, rescales the
// forecast range around its median to the HAR forecast.
func volAdjust(hf *HorizonForecast, mat *features.Matrix, last int, q map[string]float64) map[string]float64 {
	f := vol.Model(mat.Close[:last+1], mat.Dates[:last+1], hf.Bars)
	if f == nil {
		return q
	}
	v := &VolView{Forecast: *f}
	hf.Vol = v
	old := mat.VolDaily[last] * math.Sqrt(float64(hf.Bars))
	e := f.Eval
	switch {
	case !e.Better:
		v.Note = fmt.Sprintf("Volatility forecast: about ±%.1f%% a day. It did not reliably beat the 63-day historical volatility in testing (forecast loss %.3f vs %.3f over %d independent periods), so the range keeps the old scaling.",
			100*f.Daily, e.QLike, e.NaiveQLike, e.Independent)
		return q
	case old <= 0:
		return q
	}
	v.Applied = true
	v.Ratio = math.Max(0.5, math.Min(2, f.Over/old))
	v.Note = fmt.Sprintf("Volatility forecast: about ±%.1f%% a day (63-day history: ±%.1f%%). In testing since %s it beat the historical estimate (forecast loss %.3f vs %.3f) and its 10–90%% ranges held %.0f%% of outcomes (target 80%%), so the forecast range is scaled to it (×%.2f).",
		100*f.Daily, 100*f.NaiveDaily, e.TestFrom, e.QLike, e.NaiveQLike, 100*e.Coverage, v.Ratio)
	med := q["p50"]
	out := make(map[string]float64, len(q))
	for k, x := range q {
		out[k] = med + (x-med)*v.Ratio
	}
	return out
}
