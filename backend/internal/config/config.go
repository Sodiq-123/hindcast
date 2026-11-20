// Package config loads settings from environment variables, optionally
// seeded from a .env file.
package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr           string
	DataDir        string // only used by -import-sqlite (old file storage)
	DatabaseURL    string
	FrontendDir    string
	AnthropicKey   string
	LLMModel       string
	LLMEffort      string
	TwelveDataKey  string
	CoinGeckoKey   string
	RunTimes       []string // "HH:MM" local times for the scheduled run
	Timezone       *time.Location
	ScanOnSchedule bool
	ScanUniverses  []string
	ScanHorizon    string
	CacheMaxAge    time.Duration
	DefaultWatch   []string
	EarningsFrom   string // backfill earnings history from this date ("off" disables)
	PooledYears    int
	RetrainAfter   time.Duration
	TelegramToken  string  // bot token from @BotFather; empty disables alerts
	TelegramChat   string  // chat id to send alerts to
	AppURL         string  // base URL used in alert links
	ResearchBudget float64 // monthly USD cap for on-demand research reports
	SECUserAgent   string  // SEC EDGAR asks for "Name contact@email"

	// Accounts
	Signup         string // "invite" (an admin's invite code is needed) or "open"
	TrustProxy     bool   // believe X-Forwarded-For (only behind your own reverse proxy)
	SMTP           SMTP   // for password-reset emails; empty = admins create reset links
	MemberAIRuns   int    // AI-assisted report refreshes per member per day
	MemberResearch int    // research reports per member per calendar month
}

type SMTP struct {
	Host, Port, User, Pass, From string
}

func Load() Config {
	loadDotEnv(".env")
	loadDotEnv("../.env")
	tz, err := time.LoadLocation(get("TIMEZONE", "Local"))
	if err != nil {
		tz = time.Local
	}
	maxAge, err := time.ParseDuration(get("CACHE_MAX_AGE", "3h"))
	if err != nil {
		maxAge = 3 * time.Hour
	}
	return Config{
		Addr:           get("ADDR", ":8080"),
		DataDir:        get("DATA_DIR", "./data"),
		DatabaseURL:    get("DATABASE_URL", "postgres://localhost:5432/market_oracle?sslmode=disable"),
		FrontendDir:    get("FRONTEND_DIR", "../frontend/dist"),
		AnthropicKey:   os.Getenv("ANTHROPIC_API_KEY"),
		LLMModel:       get("LLM_MODEL", "claude-opus-5-5"),
		LLMEffort:      get("LLM_EFFORT", "high"),
		TwelveDataKey:  os.Getenv("TWELVEDATA_API_KEY"),
		CoinGeckoKey:   os.Getenv("COINGECKO_API_KEY"),
		TelegramToken:  os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChat:   os.Getenv("TELEGRAM_CHAT_ID"),
		AppURL:         strings.TrimRight(get("APP_URL", "http://localhost:8080"), "/"),
		ResearchBudget: floatEnv("RESEARCH_BUDGET_USD", 25),
		SECUserAgent:   os.Getenv("SEC_USER_AGENT"),
		Signup:         get("SIGNUP", "invite"),
		TrustProxy:     get("TRUST_PROXY", "false") == "true",
		SMTP: SMTP{Host: os.Getenv("SMTP_HOST"), Port: get("SMTP_PORT", "587"), User: os.Getenv("SMTP_USER"),
			Pass: os.Getenv("SMTP_PASSWORD"), From: os.Getenv("SMTP_FROM")},
		MemberAIRuns:   intEnv("MEMBER_AI_RUNS_PER_DAY", 10),
		MemberResearch: intEnv("MEMBER_RESEARCH_PER_MONTH", 2),
		RunTimes:       list(get("RUN_TIMES", "07:30,19:30")),
		Timezone:       tz,
		ScanOnSchedule: get("SCAN_ON_SCHEDULE", "true") == "true",
		ScanUniverses:  list(get("SCAN_UNIVERSES", "us_large_caps,global,crypto")),
		ScanHorizon:    get("SCAN_HORIZON", "1w"),
		CacheMaxAge:    maxAge,
		DefaultWatch:   list(get("DEFAULT_WATCHLIST", "SPY,AAPL,NVDA,BTC-USD,ETH-USD")),
		EarningsFrom:   get("EARNINGS_FROM", "2010-01-01"),
		PooledYears:    atoi(get("POOLED_YEARS", "16"), 16),
		RetrainAfter:   dur(get("RETRAIN_AFTER", "20h"), 20*time.Hour),
	}
}

func atoi(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func dur(s string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return def
	}
	return d
}

func get(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadDotEnv sets variables from a KEY=VALUE file without overriding
// variables already present in the environment.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

func floatEnv(k string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(k)), 64); err == nil && v >= 0 {
		return v
	}
	return def
}

// intEnv reads a non-negative integer (0 is allowed: it switches a feature off).
func intEnv(k string, def int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(k))); err == nil && v >= 0 {
		return v
	}
	return def
}
