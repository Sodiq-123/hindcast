package pooled

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sodiq/financial-market/backend/internal/features"
	"github.com/sodiq/financial-market/backend/internal/market"
)

// Source builds the full feature matrix (with benchmark, macro and earnings
// inputs) for a symbol. The engine provides it.
type Source func(ctx context.Context, symbol string) (*features.Matrix, *market.Series, error)

// RankUniverses map ranking universes to the scanner universe lists they
// combine. Assets are only ranked against comparable assets.
// "stocks" is replaced by the point-in-time S&P 500 when its membership
// history is available.
var RankUniverses = map[string][]string{
	"stocks":      {"us_large_caps", "international"},
	"etfs":        {"global"},
	"crypto":      {"crypto"},
	"commodities": {"commodities"},
}

// ModelStore persists trained models (the Postgres store implements it).
type ModelStore interface {
	SaveModel(ctx context.Context, name, kind string, trainedAt time.Time, v any) error
	LoadModels(ctx context.Context) (map[string][][]byte, error)
}

// Trainer trains, stores and serves pooled models.
type Trainer struct {
	Source    Source
	Models    ModelStore
	Log       *slog.Logger
	Config    DirConfig
	Universes map[string][]string // scanner universes (symbol lists)

	mu   sync.RWMutex
	dir  map[string]*DirModel  // key pool/horizon
	rank map[string]*RankModel // key universe/horizon
	prog Progress
}

type Progress struct {
	Running   bool      `json:"running"`
	Stage     string    `json:"stage"`
	Done      int       `json:"done"`
	Total     int       `json:"total"`
	LastRun   time.Time `json:"lastRun"`
	LastError string    `json:"lastError,omitempty"`
	Failed    []string  `json:"failed,omitempty"`
}

func NewTrainer(src Source, models ModelStore, universes map[string][]string, log *slog.Logger) *Trainer {
	t := &Trainer{Source: src, Models: models, Log: log, Config: DefaultDirConfig(), Universes: universes,
		dir: map[string]*DirModel{}, rank: map[string]*RankModel{}}
	t.load()
	return t
}

func (t *Trainer) Progress() Progress {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.prog
}

func (t *Trainer) setProg(f func(*Progress)) {
	t.mu.Lock()
	f(&t.prog)
	t.mu.Unlock()
}

// Age returns how long ago models were last trained (large if never).
func (t *Trainer) Age() time.Duration {
	t.mu.RLock()
	defer t.mu.RUnlock()
	oldest := time.Time{}
	for _, m := range t.dir {
		if oldest.IsZero() || m.TrainedAt.Before(oldest) {
			oldest = m.TrainedAt
		}
	}
	if oldest.IsZero() {
		return 1 << 62
	}
	return time.Since(oldest)
}

// Dir returns the direction model for an asset class and horizon key.
func (t *Trainer) DirFor(class market.AssetClass, horizon string) *DirModel {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dir[PoolFor(class)+"/"+horizon]
}

// RanksFor returns the live ranking entries for a symbol, across universes.
func (t *Trainer) RanksFor(symbol string) []SymbolRank {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []SymbolRank
	for _, m := range t.rank {
		for _, it := range m.Live {
			if it.Symbol == symbol {
				out = append(out, SymbolRank{Universe: m.Universe, Horizon: m.Horizon, Status: m.Validation.Status, Item: it})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return hOrder(out[i].Horizon) < hOrder(out[j].Horizon) })
	return out
}

type SymbolRank struct {
	Universe string   `json:"universe"`
	Horizon  string   `json:"horizon"`
	Status   string   `json:"status"`
	Item     RankItem `json:"item"`
}

func hOrder(h string) int { return strings.Index("1d1w1m1y", h) }

// Rankings returns every ranking model (full detail).
func (t *Trainer) Rankings() []*RankModel {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]*RankModel, 0, len(t.rank))
	for _, m := range t.rank {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Universe != out[j].Universe {
			return out[i].Universe < out[j].Universe
		}
		return hOrder(out[i].Horizon) < hOrder(out[j].Horizon)
	})
	return out
}

// DirSummary is a compact view of a direction model for the UI.
type DirSummary struct {
	Pool       string    `json:"pool"`
	Horizon    string    `json:"horizon"`
	Assets     int       `json:"assets"`
	Rows       int       `json:"rows"`
	Accuracy   float64   `json:"accuracy"`
	BaseAcc    float64   `json:"baseAccuracy"`
	BrierSkill float64   `json:"brierSkill"`
	AUC        float64   `json:"auc"`
	PValue     float64   `json:"pValue"`
	NEff       int       `json:"nEffective"`
	TestFrom   string    `json:"testFrom"`
	TestTo     string    `json:"testTo"`
	TrainedAt  time.Time `json:"trainedAt"`
}

func (t *Trainer) DirSummaries() []DirSummary {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []DirSummary
	for _, m := range t.dir {
		v := m.Validation
		out = append(out, DirSummary{m.Pool, m.Horizon, len(m.Assets), m.Rows, v.Accuracy, v.BaseAccuracy, v.BrierSkill, v.AUC, v.PValue, v.NEffective, m.TestFrom, m.TestTo, m.TrainedAt})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pool != out[j].Pool {
			return out[i].Pool < out[j].Pool
		}
		return hOrder(out[i].Horizon) < hOrder(out[j].Horizon)
	})
	return out
}

// Train loads every universe asset and retrains all pooled models.
func (t *Trainer) Train(ctx context.Context) error {
	t.mu.Lock()
	if t.prog.Running {
		t.mu.Unlock()
		return fmt.Errorf("training already running")
	}
	t.prog = Progress{Running: true, Stage: "loading assets", LastRun: t.prog.LastRun}
	t.mu.Unlock()
	defer t.setProg(func(p *Progress) { p.Running = false; p.Stage = ""; p.LastRun = time.Now() })

	// Load every symbol once.
	seen := map[string]bool{}
	var syms []string
	for _, list := range t.Universes {
		for _, s := range list {
			if !seen[s] {
				seen[s] = true
				syms = append(syms, s)
			}
		}
	}
	sort.Strings(syms)
	directional := map[string]bool{}
	for _, s := range syms {
		directional[s] = true
	}
	// The stock ranking universe is the S&P 500 as it was on each date.
	windowStart := addYears(time.Now().UTC().Format("2006-01-02"), -t.Config.Years)
	compactFrom := addDaysStr(windowStart, -400) // a year of warm-up for betas
	members, merr := t.sp500Membership(ctx)
	if merr != nil {
		t.Log.Warn("S&P 500 membership unavailable; stock ranking falls back to the large-cap list", "err", merr)
	}
	for _, s := range members.symbolsSince(windowStart) {
		if !seen[s] {
			seen[s] = true
			syms = append(syms, s)
		}
	}
	t.setProg(func(p *Progress) { p.Total = len(syms) })
	assets := map[string]*Asset{}
	stocks := []*Asset{}
	var failed []string
	for _, s := range syms {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		mat, ser, err := t.Source(ctx, s)
		if err != nil || len(mat.Close) < 300 {
			failed = append(failed, s)
			t.Log.Warn("pooled: skip asset", "symbol", s, "err", err)
		} else {
			class := ser.Meta.Class
			if class == market.ClassUnknown && market.IsCrypto(s) {
				class = market.ClassCrypto
			}
			if directional[s] {
				assets[s] = FromMatrix(s, ser.Meta.Name, class, mat)
			}
			if ivs := members.intervalsFor(s, mat.Dates[0]); len(ivs) > 0 && class == market.ClassEquity {
				a := FromMatrixCompact(s, ser.Meta.Name, class, mat, compactFrom)
				a.Member = ivs
				stocks = append(stocks, a)
			}
		}
		t.setProg(func(p *Progress) { p.Done++ })
	}
	t.setProg(func(p *Progress) { p.Failed = failed })

	// Direction pools.
	pools := map[string][]*Asset{}
	for _, a := range assets {
		pools[PoolFor(a.Class)] = append(pools[PoolFor(a.Class)], a)
	}
	newDir := map[string]*DirModel{}
	for pool, list := range pools {
		sort.Slice(list, func(i, j int) bool { return list[i].Symbol < list[j].Symbol })
		for _, h := range market.HorizonsFor(classOfPool(pool)) {
			if h.Bars >= 252 {
				continue // see skipYearly
			}
			t.setProg(func(p *Progress) { p.Stage = fmt.Sprintf("direction model: %s %s", pool, h.Key) })
			m, err := TrainDir(pool, h, list, t.Config)
			if err != nil {
				t.Log.Warn("pooled: direction train failed", "pool", pool, "h", h.Key, "err", err)
				continue
			}
			newDir[pool+"/"+h.Key] = m
			t.save(ctx, "dir-"+pool+"-"+h.Key, "dir", m.TrainedAt, m)
			t.Log.Info("pooled direction", "pool", pool, "h", h.Key, "assets", len(list), "acc", m.Validation.Accuracy, "bss", m.Validation.BrierSkill, "auc", m.Validation.AUC)
		}
	}

	// Ranking universes.
	newRank := map[string]*RankModel{}
	for uni, parts := range RankUniverses {
		var list []*Asset
		if uni == "stocks" && len(stocks) >= 50 {
			list = stocks
			parts = nil
		}
		for _, part := range parts {
			for _, s := range t.Universes[part] {
				if a := assets[s]; a != nil {
					list = append(list, a)
				}
			}
		}
		if len(list) < minCrossSection*2 {
			continue
		}
		class := market.ClassEquity
		if uni == "crypto" {
			class = market.ClassCrypto
		}
		for _, h := range market.HorizonsFor(class) {
			if h.Bars >= 252 {
				continue // see skipYearly
			}
			t.setProg(func(p *Progress) { p.Stage = fmt.Sprintf("ranking model: %s %s", uni, h.Key) })
			m, err := TrainRank(uni, h, list, t.Config)
			if err != nil {
				t.Log.Warn("pooled: rank train failed", "universe", uni, "h", h.Key, "err", err)
				continue
			}
			newRank[uni+"/"+h.Key] = m
			t.save(ctx, "rank-"+uni+"-"+h.Key, "rank", m.TrainedAt, m)
			t.Log.Info("pooled ranking", "universe", uni, "h", h.Key, "assets", len(list), "ic", m.Validation.MeanIC, "t", m.Validation.ICTStat, "status", m.Validation.Status)
		}
	}
	t.mu.Lock()
	for k, v := range newDir {
		t.dir[k] = v
	}
	for k, v := range newRank {
		t.rank[k] = v
	}
	t.mu.Unlock()
	if err := t.trainMeme(ctx); err != nil {
		t.Log.Warn("pooled: meme ranking", "err", err)
	}
	if len(newDir) == 0 && len(newRank) == 0 {
		err := fmt.Errorf("no pooled model could be trained (%d assets loaded, %d failed)", len(assets), len(failed))
		t.setProg(func(p *Progress) { p.LastError = err.Error() })
		return err
	}
	return nil
}

// MemeTradeCost is the assumed cost per trade, each side, for meme coins.
const MemeTradeCost = 0.005

// TrainMeme trains only the meme-coin ranking (a few minutes instead of the
// full training).
func (t *Trainer) TrainMeme(ctx context.Context) error {
	t.mu.Lock()
	if t.prog.Running {
		t.mu.Unlock()
		return fmt.Errorf("training already running")
	}
	t.prog = Progress{Running: true, Stage: "meme ranking", LastRun: t.prog.LastRun}
	t.mu.Unlock()
	defer t.setProg(func(p *Progress) { p.Running = false; p.Stage = "" })
	return t.trainMeme(ctx)
}

// trainMeme ranks meme coins against each other. They are kept out of the
// cross-asset direction pools (which they would distort) and, because most
// have only one to three years of history, validation starts after one year
// instead of four. The pass/fail gate is unchanged.
func (t *Trainer) trainMeme(ctx context.Context) error {
	var list []*Asset
	var failed []string
	for _, s := range market.MemeTickers() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t.setProg(func(p *Progress) { p.Stage = "meme ranking: loading " + s })
		mat, ser, err := t.Source(ctx, s)
		if err != nil || len(mat.Close) < 300 {
			failed = append(failed, s)
			t.Log.Warn("meme ranking: skip coin", "symbol", s, "err", err)
			continue
		}
		list = append(list, FromMatrix(s, ser.Meta.Name, market.ClassCrypto, mat))
	}
	if len(list) < minCrossSection*2 {
		return fmt.Errorf("only %d meme coins loaded (failed: %v)", len(list), failed)
	}
	cfg := t.Config
	cfg.MinYears = 1
	// Meme coins trade on thin order books and DEXs: 0.5% per side covers
	// a typical fee plus spread (large caps use 0.1%).
	cfg.RankCost = MemeTradeCost
	for _, h := range market.HorizonsFor(market.ClassCrypto) {
		if h.Bars >= 252 {
			continue
		}
		t.setProg(func(p *Progress) { p.Stage = "ranking model: meme " + h.Key })
		m, err := TrainRank("meme", h, list, cfg)
		if err != nil {
			t.Log.Warn("meme ranking: train failed", "h", h.Key, "err", err)
			continue
		}
		t.save(ctx, "rank-meme-"+h.Key, "rank", m.TrainedAt, m)
		t.mu.Lock()
		t.rank["meme/"+h.Key] = m
		t.mu.Unlock()
		t.Log.Info("meme ranking", "h", h.Key, "coins", len(list), "ic", m.Validation.MeanIC, "t", m.Validation.ICTStat, "status", m.Validation.Status)
	}
	return nil
}

// skipYearly: pooled models are not trained for the 1-year horizon. A
// 16-year window holds only ~12 independent yearly periods, too few to
// validate anything, and an unvalidated model must not join the forecast.

func classOfPool(pool string) market.AssetClass {
	if pool == PoolCrypto {
		return market.ClassCrypto
	}
	return market.ClassEquity
}

func (t *Trainer) save(ctx context.Context, name, kind string, trainedAt time.Time, v any) {
	if t.Models == nil {
		return
	}
	if err := t.Models.SaveModel(ctx, name, kind, trainedAt, v); err != nil {
		t.Log.Error("pooled: save model", "model", name, "err", err)
	}
}

func (t *Trainer) load() {
	if t.Models == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	bodies, err := t.Models.LoadModels(ctx)
	if err != nil {
		t.Log.Warn("pooled: load models", "err", err)
		return
	}
	for _, b := range bodies["dir"] {
		var m DirModel
		if json.Unmarshal(b, &m) == nil {
			t.dir[m.Pool+"/"+m.Horizon] = &m
		}
	}
	for _, b := range bodies["rank"] {
		var m RankModel
		if json.Unmarshal(b, &m) == nil {
			t.rank[m.Universe+"/"+m.Horizon] = &m
		}
	}
}
