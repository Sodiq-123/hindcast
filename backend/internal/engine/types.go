// Package engine orchestrates data, features, models and news into a fully
// explained forecast report.
package engine

import (
	"math"
	"strconv"
	"time"

	"github.com/sodiq/financial-market/backend/internal/market"
	"github.com/sodiq/financial-market/backend/internal/model"
	"github.com/sodiq/financial-market/backend/internal/news"
	"github.com/sodiq/financial-market/backend/internal/pooled"
	"github.com/sodiq/financial-market/backend/internal/vol"
)

// Report is everything the UI shows for one asset at one point in time.
type Report struct {
	ID          int64     `json:"id"`
	Symbol      string    `json:"symbol"`
	Name        string    `json:"name"`
	Class       string    `json:"class"`
	Currency    string    `json:"currency"`
	Exchange    string    `json:"exchange"`
	Provider    string    `json:"provider"`
	Benchmark   string    `json:"benchmark"`
	GeneratedAt time.Time `json:"generatedAt"`
	AsOf        string    `json:"asOf"` // date of last bar used
	Price       float64   `json:"price"`
	Change1d    float64   `json:"change1d"`
	HistoryFrom string    `json:"historyFrom"`
	HistoryBars int       `json:"historyBars"`
	ElapsedMs   int64     `json:"elapsedMs"`
	UsedLLM     bool      `json:"usedLlm"`
	LLMModel    string    `json:"llmModel,omitempty"`

	Headline   string              `json:"headline"` // one-line bottom line
	Horizons   []HorizonForecast   `json:"horizons"`
	Indicators []IndicatorReading  `json:"indicators"`
	Evidence   []ConditionalStat   `json:"evidence"`
	News       NewsSection         `json:"news"`
	Narrative  string              `json:"narrative"`
	BullCase   []string            `json:"bullCase"`
	BearCase   []string            `json:"bearCase"`
	Catalysts  []string            `json:"catalysts"`
	Risks      []string            `json:"risks"`
	Warnings   []string            `json:"warnings"`
	Features   FeatureInfo         `json:"features"`
	Chart      Chart               `json:"chart"`
	Earnings   EarningsInfo        `json:"earnings"`
	Options    []market.Expiry     `json:"options,omitempty"` // options-implied moves by expiry (US stocks/ETFs)
	Replay     *ReplayInfo         `json:"replay,omitempty"`  // set when the analysis replays a past date
	Ranks      []pooled.SymbolRank `json:"ranks"`             // position in cross-asset rankings
}

type FeatureInfo struct {
	Used    []string `json:"used"`
	Dropped []string `json:"dropped"`
}

// HorizonForecast is the forecast and its complete justification for one horizon.
type HorizonForecast struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Bars       int    `json:"bars"`
	TargetDate string `json:"targetDate"`
	Available  bool   `json:"available"`
	Reason     string `json:"reason,omitempty"` // why unavailable

	PUp        float64  `json:"pUp"`    // final probability of a higher price at the horizon
	PModel     float64  `json:"pModel"` // calibrated quantitative probability (before news)
	PRaw       float64  `json:"pRaw"`   // uncalibrated ensemble
	PLogistic  float64  `json:"pLogistic"`
	PAnalogs   float64  `json:"pAnalogs"`
	PCross     *float64 `json:"pCross,omitempty"` // cross-asset model (nil if not trained)
	BaseRate   float64  `json:"baseRate"`
	NewsShift  float64  `json:"newsShift"` // probability points added by news (+/-)
	Signal     string   `json:"signal"`    // STRONG BUY | BUY | HOLD | SELL | STRONG SELL | NO EDGE
	Status     string   `json:"status"`    // validated | weak | no_edge
	StatusNote string   `json:"statusNote"`

	Conviction      int                  `json:"conviction"` // 0-100
	ConvictionParts map[string]float64   `json:"convictionParts"`
	TrackAtConf     *model.ConfidenceRow `json:"trackAtConfidence,omitempty"`

	ExpectedReturn float64            `json:"expectedReturn"`
	PriceTargets   map[string]float64 `json:"priceTargets"` // p10..p90 prices
	ReturnRange    map[string]float64 `json:"returnRange"`  // p10..p90 simple returns
	Risk           RiskPlan           `json:"risk"`

	Drivers     []Driver         `json:"drivers"`
	GroupImpact []GroupImpact    `json:"groupImpact"`
	Analogs     AnalogSummary    `json:"analogs"`
	Validation  model.Validation `json:"validation"`
	Why         []string         `json:"why"` // ordered plain-English reasoning chain

	CrossAsset     *CrossAssetInfo `json:"crossAsset,omitempty"`
	EarningsInside bool            `json:"earningsInside"`
	Options        *OptionsView    `json:"options,omitempty"`
	EarningsNote   string          `json:"earningsNote,omitempty"`
	Vol            *VolView        `json:"vol,omitempty"`
	Drift          *Drift          `json:"drift,omitempty"` // live record contradicts the backtest
}

// VolView is the volatility forecast for one horizon and whether it replaced
// the 63-day historical volatility in the forecast range.
type VolView struct {
	vol.Forecast
	Applied bool    `json:"applied"` // HAR beat the old scaling out of sample, so the range uses it
	Ratio   float64 `json:"ratio"`   // HAR horizon σ ÷ old horizon σ
	Note    string  `json:"note"`
}

// Drift is the live (real-time) record of one symbol and horizon when it is
// worse than always forecasting the base rate.
type Drift struct {
	N         int     `json:"n"`         // resolved live forecasts (one per day)
	Brier     float64 `json:"brier"`     // live Brier score
	BaseBrier float64 `json:"baseBrier"` // Brier of always predicting the historical base rate
	HitRate   float64 `json:"hitRate"`
	Demoted   bool    `json:"demoted"` // validated/weak signal turned into NO EDGE
	FirstAsOf string  `json:"firstAsOf"`
	LastAsOf  string  `json:"lastAsOf"`
}

// OptionsView compares the options market's priced-in move with the model's
// forecast range for one horizon.
type OptionsView struct {
	Expiry      string  `json:"expiry"`
	ImpliedMove float64 `json:"impliedMove"` // expected absolute move to the target date
	ImpliedVol  float64 `json:"impliedVol"`
	ModelMove   float64 `json:"modelMove"` // model's expected absolute move (from its 10–90% range)
}

// CrossAssetInfo explains the pooled cross-asset component of a forecast.
type CrossAssetInfo struct {
	Pool      string                `json:"pool"`
	Used      bool                  `json:"used"` // joined the blend (only if it showed skill)
	Assets    int                   `json:"assets"`
	InSample  bool                  `json:"inSample"` // this asset was in the training pool
	P         float64               `json:"p"`
	Overall   model.Metrics         `json:"overall"`           // pool-wide out-of-sample record
	OnAsset   *model.Metrics        `json:"onAsset,omitempty"` // its record on this asset
	TestFrom  string                `json:"testFrom"`
	TestTo    string                `json:"testTo"`
	TrainedAt time.Time             `json:"trainedAt"`
	Drivers   []pooled.Contribution `json:"drivers"`
}

type RiskPlan struct {
	StopLoss      float64 `json:"stopLoss"`
	TakeProfit    float64 `json:"takeProfit"`
	RewardRisk    float64 `json:"rewardRisk"`
	KellyFraction float64 `json:"kellyFraction"` // full Kelly (for reference)
	SuggestedSize float64 `json:"suggestedSize"` // quarter-Kelly, capped, as fraction of capital
	OnePctSize    float64 `json:"onePctSize"`    // position (fraction of capital) that loses 1% of capital at the stop
	Note          string  `json:"note"`
}

// Driver is one model input's contribution to this forecast.
type Driver struct {
	Key          string  `json:"key"`
	Label        string  `json:"label"`
	Group        string  `json:"group"`
	Value        float64 `json:"value"`        // raw feature value
	Z            float64 `json:"z"`            // standard deviations from this asset's norm
	Weight       float64 `json:"weight"`       // learned coefficient (log-odds per 1σ)
	Contribution float64 `json:"contribution"` // log-odds contribution
	ProbImpact   float64 `json:"probImpact"`   // ≈ probability points
	Text         string  `json:"text"`
}

type GroupImpact struct {
	Group      string  `json:"group"`
	ProbImpact float64 `json:"probImpact"`
}

type AnalogSummary struct {
	K            int      `json:"k"`
	UpRate       float64  `json:"upRate"`
	MedianReturn float64  `json:"medianReturn"`
	MeanReturn   float64  `json:"meanReturn"`
	Examples     []Analog `json:"examples"`
}

type Analog struct {
	Date       string  `json:"date"`
	Similarity float64 `json:"similarity"` // 0..1
	Return     float64 `json:"return"`     // realised simple return over the horizon
}

// IndicatorReading is a classic technical/macro reading with its textbook
// interpretation (which the model may or may not agree with for this asset).
type IndicatorReading struct {
	Name    string  `json:"name"`
	Group   string  `json:"group"`
	Value   float64 `json:"value"`
	Display string  `json:"display"`
	Signal  string  `json:"signal"` // bullish | bearish | neutral
	Note    string  `json:"note"`
}

// ConditionalStat answers "historically, when this asset was in today's
// condition, what happened next?"
type ConditionalStat struct {
	Condition string                 `json:"condition"`
	Note      string                 `json:"note"`
	ByHorizon map[string]CondOutcome `json:"byHorizon"`
}

type CondOutcome struct {
	N            int     `json:"n"`
	UpRate       float64 `json:"upRate"`
	AvgReturn    float64 `json:"avgReturn"`
	AllUpRate    float64 `json:"allUpRate"`
	AllAvgReturn float64 `json:"allAvgReturn"`
}

type NewsSection struct {
	ScoredBy   string                    `json:"scoredBy"` // llm | lexicon | none
	Articles   []news.Article            `json:"articles"`
	Aggregates map[string]news.Aggregate `json:"aggregates"`
	Error      string                    `json:"error,omitempty"`
}

type Chart struct {
	Dates  []string    `json:"dates"`
	Open   []float64   `json:"open"`
	High   []float64   `json:"high"`
	Low    []float64   `json:"low"`
	Close  []float64   `json:"close"`
	Volume []float64   `json:"volume"`
	SMA50  []NullFloat `json:"sma50"`
	SMA200 []NullFloat `json:"sma200"`
}

// NullFloat marshals NaN/Inf as JSON null (encoding/json rejects them).
type NullFloat float64

func (f NullFloat) MarshalJSON() ([]byte, error) {
	x := float64(f)
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return []byte("null"), nil
	}
	return strconv.AppendFloat(nil, x, 'f', -1, 64), nil
}
