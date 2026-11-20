// Types mirror the Go JSON structs in backend/internal/engine, model, news, store and tracker.

export interface Metrics {
  n: number
  nEffective: number
  accuracy: number
  baseAccuracy: number
  brier: number
  baseBrier: number
  brierSkill: number
  logLoss: number
  auc: number
  upRate: number
  pValue: number
}

export interface Bucket {
  lo: number
  hi: number
  n: number
  meanPred: number
  upRate: number
  avgReturn: number
}

export interface ConfidenceRow {
  minEdge: number
  n: number
  accuracy: number
  coverage: number
}

export interface Strategy {
  threshold: number
  costPerTrade: number
  periods: number
  trades: number
  exposure: number
  winRate: number
  totalReturn: number
  buyHoldReturn: number
  cagr: number
  buyHoldCagr: number
  sharpe: number
  buyHoldSharpe: number
  maxDrawdown: number
  buyHoldMaxDrawdown: number
  equity: number[] | null
  buyHold: number[] | null
  dates: string[] | null
}

export interface Validation {
  ensemble: Metrics
  logistic: Metrics
  analogs: Metrics
  external?: Metrics
  calibration: Bucket[]
  confidence: ConfidenceRow[]
  strategy: Strategy
  testFrom: string
  testTo: string
  trainRows: number
  lambda: number
}

export interface Driver {
  key: string
  label: string
  group: string
  value: number
  z: number
  weight: number
  contribution: number
  probImpact: number
  text: string
}

export interface HorizonForecast {
  key: string
  label: string
  bars: number
  targetDate: string
  available: boolean
  reason?: string
  pUp: number
  pModel: number
  pRaw: number
  pLogistic: number
  pAnalogs: number
  baseRate: number
  newsShift: number
  signal: string
  status: 'validated' | 'weak' | 'no_edge' | ''
  statusNote: string
  conviction: number
  convictionParts: Record<string, number>
  trackAtConfidence?: ConfidenceRow
  expectedReturn: number
  priceTargets: Record<string, number>
  returnRange: Record<string, number>
  risk: {
    stopLoss: number
    takeProfit: number
    rewardRisk: number
    kellyFraction: number
    suggestedSize: number
    onePctSize: number
    note: string
  }
  vol?: VolView
  drift?: Drift
  drivers: Driver[] | null
  groupImpact: { group: string; probImpact: number }[] | null
  analogs: {
    k: number
    upRate: number
    medianReturn: number
    meanReturn: number
    examples: { date: string; similarity: number; return: number }[] | null
  }
  validation: Validation
  why: string[] | null
  pCross?: number
  crossAsset?: CrossAsset
  earningsInside: boolean
  earningsNote?: string
  options?: { expiry: string; impliedMove: number; impliedVol: number; modelMove: number }
}

export interface VolView {
  horizon: number
  daily: number
  over: number
  naiveDaily: number
  longRunDaily: number
  applied: boolean
  ratio: number
  note: string
  eval: { n: number; independent: number; qlike: number; naiveQlike: number; r2: number; naiveR2: number; coverage: number; naiveCoverage: number; better: boolean; testFrom?: string }
}

export interface Drift {
  n: number
  brier: number
  baseBrier: number
  hitRate: number
  demoted: boolean
  firstAsOf: string
  lastAsOf: string
}

export interface JournalSettings {
  account: number
  riskPerTrade: number
  maxOpenRisk: number
  maxPosition: number
  feeStock: number
  feeCrypto: number
  feeToken: number
}

export interface Position {
  id: number
  symbol: string
  kind: 'asset' | 'token'
  chain?: string
  name?: string
  openedAt: string
  entry: number
  quantity: number
  stop?: number
  target?: number
  signal?: string
  note?: string
  closedAt?: string
  exit?: number
  feeRate: number
  feeOpen: number
  feeClose?: number
  fees: number
  price: number
  priceErr?: string
  cost: number
  value: number
  pnl: number
  pnlPct: number
  weight: number
  riskToStop: number
  riskPct: number
  stopHit: boolean
  targetHit: boolean
  days: number
}

export interface Journal {
  settings: JournalSettings
  open: Position[]
  closed: Position[]
  equity: number
  invested: number
  cash: number
  openPnl: number
  realized: number
  feesPaid: number
  openRisk: number
  openRiskPct: number
  peakEquity: number
  drawdown: number
  lossStreak: number
  winRate: number
  sizeMultiplier: number
  correlated: { a: string; b: string; rho: number; n: number }[]
  warnings: { level: 'red' | 'amber' | 'green'; title: string; detail: string }[]
}

export interface OpenPosition {
  symbol: string
  amount?: number
  quantity?: number
  entry?: number
  stop?: number
  target?: number
  signal?: string
  note?: string
  openedAt?: string
}

export interface ReplayOutcome {
  complete: boolean
  exitDate: string
  exitPrice: number
  return: number
  wentUp: boolean
  correct?: boolean
  directionOk: boolean
  inRange: boolean
  hitFirst: 'target' | 'stop' | 'neither'
  maxGain: number
  maxDrawdown: number
}

export interface ReplayInfo {
  asOf: string
  note: string
  outcomes: Record<string, ReplayOutcome>
  path: { date: string; close: number }[] | null
}

export interface ReplaySeries {
  symbol: string
  rows: { date: string; horizon: string; signal: string; status: string; pUp: number; return: number; complete: boolean; correct?: boolean; directionOk: boolean; inRange: boolean }[] | null
  summary: { horizon: string; dates: number; directionHit: number; upRate: number; calls: number; callHit: number; avgCallReturn: number; inRange: number }[] | null
  failed?: string[]
}

export interface TokenFlag {
  level: 'red' | 'amber' | 'green'
  area: string
  title: string
  detail: string
}

export interface FundQuarter {
  end: string
  known: string
  revenue: number | null
  opIncome: number | null
  netIncome: number | null
  ocf: number | null
  capex: number | null
  fcf: number | null
  shares: number | null
  debt: number | null
  cash: number | null
  equity: number | null
  price: number | null
  pe: number | null
}

export interface FundComparison {
  date: string
  price: number
  peakDate: string
  peakPrice: number
  fromPeak: number
  thenQuarter: string
  nowQuarter: string
  revenueChange: number | null
  opIncomeChange: number | null
  netIncomeChange: number | null
  fcfChange: number | null
  debtChange: number | null
  peThen: number | null
  peNow: number | null
  signal: boolean
  checks: { name: string; pass: boolean; detail: string }[]
}

export interface StratPortfolio {
  months: number
  meanExcess: number
  tStat: number
  hitRate: number
  cagr: number
  benchCagr: number
  maxDrawdown: number
  benchMaxDrawdown: number
  avgHoldings: number
  equity: number[] | null
  bench: number[] | null
  dates: string[] | null
}

export interface StratGroup { n: number; excess6m: number; excess12m: number; hit12m: number; trap12m: number }

export interface BusinessVsPrice {
  symbol: string
  name: string
  cik: number
  fetchedAt: string
  comparison?: FundComparison
  quarters: FundQuarter[]
  rules: { LookbackDays: number; MinDrop: number; MinRevenue: number; MinProfit: number; MaxDebtRise: number }
  backtest?: { status: string; note: string; portfolio: StratPortfolio; groups: Record<string, StratGroup>; from: string; to: string; at: string }
}

export interface LevelStat {
  ratio: number
  fib: boolean
  touches: number
  bounces: number
  stopped: number
  bounceRate: number
  expectancy: number
  avg13w: number
  avg26w: number
}

export interface LevelEvent { date: string; entry: number; target: number; stop: number; outcome: 'bounce' | 'stopped' | 'open'; weeks: number; ret13w: number | null; ret26w: number | null }

export interface LevelsTest {
  stocks: number
  from: string
  to: string
  levels: LevelStat[]
  breakEven: number
  neighbours: LevelStat
  z: number
  status: 'special' | 'not_special' | 'worse'
  note: string
}

export interface LevelsView {
  symbol: string
  breakEven: number
  view: {
    price: number
    date: string
    swing?: { lowDate: string; low: number; highDate: string; high: number }
    retracement: number
    levels: { ratio: number; price: number; fib: boolean }[] | null
    supports: { low: number; high: number; touches: number; last: string }[] | null
    plan?: { zoneLow: number; zoneHigh: number; stop: number; stopReason: string; riskFromMid: number; state: 'above' | 'in_zone' | 'below' }
    history: LevelEvent[] | null
    summary: LevelStat
    note?: string
  }
  test?: LevelsTest
  testAt?: string
}

export interface StrategyResults {
  progress: { running: boolean; stage: string; done: number; total: number; lastRun: string; lastError?: string }
  results: null | {
    at: string
    value: {
      from: string
      to: string
      stocks: number
      months: number
      groups: Record<string, StratGroup>
      portfolio: StratPortfolio
      dipOnly: StratPortfolio
      status: string
      statusNote: string
      notes: string[] | null
    } | null
    levels: LevelsTest | null
    screen: { symbol: string; name: string; cmp: FundComparison }[] | null
    evaluated: number
    priceStocks: number
    fundStocks: number
    missing: number
  }
}

export interface ResearchFinding {
  text: string
  tone: 'positive' | 'negative' | 'neutral'
  sources: number[] | null
}

export interface ResearchReport {
  id: number
  subject: string
  kind: 'stock' | 'crypto' | 'token'
  name: string
  symbol: string
  createdAt: string
  price: number
  headline?: string
  overview: string
  fundamentals: 'strong' | 'mixed' | 'weak' | 'insufficient'
  fundamentalsReason: string
  sections: { key: string; title: string; summary: string; findings: ResearchFinding[] | null; gaps: string[] | null }[] | null
  bullCase: ResearchFinding[] | null
  bearCase: ResearchFinding[] | null
  wouldChangeView: string[] | null
  hypeCheck: string
  changes: ResearchFinding[] | null
  previousId?: number
  previousAt?: string
  sources: { id: number; url: string; title?: string; retrieved: boolean }[] | null
  facts: string
  notes: string
  cost: { model: string; searches: number; fetches: number; input: number; output: number; usd: number; seconds: number }
}

export interface ResearchJob {
  subject: string
  started: string
  stage: string
  running: boolean
  error?: string
  reportId?: number
}

export interface ResearchView {
  subject: string
  enabled: boolean
  history: { id: number; createdAt: string; fundamentals: string; price: number; costUsd: number }[]
  latest?: ResearchReport
  job?: ResearchJob | null
  spent?: number
  budget?: number
  estimate?: number
  priceNow?: number
  priceAt?: string
  identity?: string
  allowance?: Allowance
}

/** One line of the Research home list: the latest report and the price move since. */
export interface ResearchSubject {
  subject: string
  kind: string
  name: string
  symbol: string
  reports: number
  last: string
  latestId: number
  fundamentals: string
  headline?: string
  price: number
  identity?: string
  priceNow?: number
  priceAt?: string
}

export interface ResearchOverview {
  enabled: boolean
  subjects?: ResearchSubject[]
  spent?: number
  budget?: number
  model?: string
  estimate?: number
  allowance?: Allowance
}

/** A member's monthly research allowance (limit -1: admin, no limit). */
export interface Allowance {
  used: number
  limit: number
}

export interface TokenMove {
  date: string
  return: number
}

export interface TokenRisk {
  source: string
  from: string
  to: string
  days: number
  totalReturn: number
  annualVol: number
  typicalDay: number
  worstDay: TokenMove
  bestDay: TokenMove
  worstWeek: TokenMove
  worstMonth: TokenMove
  maxDrawdown: number
  peakDate: string
  troughDate: string
  fromHigh: number
  bigDayShare: number
  crashWeekRate: number
  bad1in20Day: number
  bad1in20Week: number
  sizeFor1Pct: number
  bench: { symbol: string; name: string; days: number; corr: number; beta: number; downDays: number; tokenOnDown: number; benchOnDown: number }[] | null
}

export interface TokenReport {
  risk?: TokenRisk
  listing?: TokenListing
  lookalike?: TokenListing
  address: string
  chain: string
  chainLabel: string
  name: string
  symbol: string
  fetchedAt: string
  priceUsd: number
  marketCap: number
  fdv: number
  liquidity: number
  totalLiquidity: number
  volume24h: number
  buys24h: number
  sells24h: number
  change: Record<string, number>
  pairs: number
  dexId: string
  pairAddress: string
  pairUrl: string
  launched: string
  ageDays: number
  holderCount: number
  top1: number
  top10: number
  holders: { address: string; percent: number; tag?: string; locked: boolean; isPool: boolean }[] | null
  lpLocked?: number
  history?: {
    interval: 'day' | 'hour'
    candles: { time: number; open: number; high: number; low: number; close: number; volume: number }[]
    return7d?: number
    return30d?: number
    returnAll: number
    high: number
    fromHigh: number
    maxDrawdown: number
    annualizedVol: number
    typicalDayMove: number
    windowDays: number
  }
  flags: TokenFlag[]
  riskScore: number
  verdict: string
  summary: string
  noForecast: string
  notes?: string[]
}

export interface TokenSearchResult {
  address: string
  chain: string
  name: string
  symbol: string
  liquidity: number
  marketCap: number
  volume24h: number
  ageDays: number
  listing?: TokenListing
  original: boolean
  warning?: string
}

export interface TokenBrief {
  address: string
  chain: string
  chainLabel: string
  name: string
  symbol: string
  priceUsd: number
  change24h: number
  marketCap: number
  liquidity: number
  top10: number
  verdict: string
  riskScore: number
  reds: number
  ambers: number
  identity: 'original' | 'lookalike' | 'different_project' | 'unlisted' | 'unknown'
  listing?: TokenListing
  checkedAt: string
}

export interface CoinMatch { id: string; name: string; symbol: string; rank?: number }

export interface TokenListing {
  id: string
  name: string
  symbol: string
  rank?: number
  chain: string
  address: string
  url: string
  elsewhere?: boolean
}

export interface Expiry {
  date: string
  strike: number
  straddle: number
  impliedMove: number
  impliedVol: number
}

export interface Contribution {
  key: string
  label: string
  z: number
  weight: number
  contribution: number
  probImpact: number
}

export interface CrossAsset {
  pool: string
  used: boolean
  assets: number
  inSample: boolean
  p: number
  overall: Metrics
  onAsset?: Metrics
  testFrom: string
  testTo: string
  trainedAt: string
  drivers: Contribution[] | null
}

export interface EarningsInfo {
  covered: boolean
  note: string
  next?: { date: string; time: string; barsAway: number; epsForecast?: number }
  history: { date: string; time: string; eps?: number; forecast?: number; surprisePct?: number; reaction: number; drift20?: number }[]
  reactions: number
  avgAbsMove: number
  rmsMove: number
  normalMove: number
  beatRate: number
  driftBeat?: number
  driftMiss?: number
  coveredFrom?: string
}

export interface RankDriver {
  key: string
  label: string
  value: number
  percentile: number
  contribution: number
}

export interface RankItem {
  symbol: string
  name: string
  price: number
  lastDate: string
  pOutperform: number
  rank: number
  of: number
  expectedExcess: number
  drivers: RankDriver[] | null
}

export interface SymbolRank {
  universe: string
  horizon: string
  status: string
  item: RankItem
}

export interface RankModel {
  universe: string
  label?: string
  horizon: string
  bars: number
  assets: string[]
  asOf: string
  trainedAt: string
  live: RankItem[] | null
  validation: {
    status: 'validated' | 'weak' | 'no_edge'
    statusNote: string
    testFrom: string
    testTo: string
    rows: number
    meanIc: number
    icTStat: number
    icHitRate: number
    periods: number
    hitRate: number
    topExcess: number
    botExcess: number
    deciles: { decile: number; n: number; avgExcess: number; beatRate: number }[] | null
    topAlpha?: number
    topBeta?: number
    components?: Record<string, { meanIc: number; icTStat: number }>
    strategy: {
      costPerTrade: number
      cagr: number
      benchCagr: number
      sharpe: number
      benchSharpe: number
      maxDrawdown: number
      benchMaxDrawdown: number
      turnover: number
      breakEvenCost?: number
      equity: number[] | null
      bench: number[] | null
      dates: string[] | null
    }
  }
}

export interface TrainingProgress {
  running: boolean
  stage: string
  done: number
  total: number
  lastRun: string
  lastError?: string
  failed?: string[]
}

export interface EarningsProgress {
  running: boolean
  daysDone: number
  daysTotal: number
  coveredFrom: string
  events: number
  lastError?: string
}

export interface LLMUsage {
  todayUsd: number
  last7Usd: number
  last30Usd: number
  calls30: number
  batchShare30: number
  avgPerCallUsd: number
  avgInput: number
  avgOutput: number
}

export interface DirSummary {
  pool: string
  horizon: string
  assets: number
  rows: number
  accuracy: number
  baseAccuracy: number
  brierSkill: number
  auc: number
  pValue: number
  nEffective: number
  testFrom: string
  testTo: string
  trainedAt: string
}

export interface Article {
  id: string
  title: string
  source: string
  url: string
  published: string
  summary?: string
  scope: 'asset' | 'market'
  query: string
  lexScore: number
  lexHits?: string[]
  relevance: number
  direction: number
  impact: string
  horizon: string
  reasoning?: string
  scoredBy: 'llm' | 'lexicon'
}

export interface NewsAggregate {
  score: number
  coverage: number
  articles: number
  logitShift: number
  probShift: number
}

export interface IndicatorReading {
  name: string
  group: string
  value: number
  display: string
  signal: 'bullish' | 'bearish' | 'neutral'
  note: string
}

export interface ConditionalStat {
  condition: string
  note: string
  byHorizon: Record<string, { n: number; upRate: number; avgReturn: number; allUpRate: number; allAvgReturn: number }>
}

/** One horizon read in plain words (server: internal/plain). */
export interface PlainAnswer {
  key: string
  say: string
  more: string
  tested: 'proven' | 'weak' | 'not_proven'
  testLabel: string
  tests: number
  right: number
  guess: number
  guessWord: 'up' | 'down'
  reasons: { lead: string; text: string }[]
  shown: number
}

export interface Report {
  plain?: PlainAnswer[]
  id: number
  symbol: string
  name: string
  class: string
  currency: string
  exchange: string
  provider: string
  benchmark: string
  generatedAt: string
  asOf: string
  price: number
  change1d: number
  historyFrom: string
  historyBars: number
  elapsedMs: number
  usedLlm: boolean
  llmModel?: string
  headline: string
  horizons: HorizonForecast[]
  indicators: IndicatorReading[] | null
  evidence: ConditionalStat[] | null
  news: {
    scoredBy: 'llm' | 'lexicon' | 'none'
    articles: Article[] | null
    aggregates: Record<string, NewsAggregate>
    error?: string
  }
  narrative: string
  bullCase: string[] | null
  bearCase: string[] | null
  catalysts: string[] | null
  risks: string[] | null
  warnings: string[] | null
  features: { used: string[] | null; dropped: string[] | null }
  earnings?: EarningsInfo
  ranks?: SymbolRank[] | null
  options?: Expiry[] | null
  replay?: ReplayInfo
  chart: {
    dates: string[]
    open: number[]
    high: number[]
    low: number[]
    close: number[]
    volume: number[]
    sma50: (number | null)[]
    sma200: (number | null)[]
  }
}

export interface WatchItem {
  symbol: string
  name?: string
  price?: number
  change1d?: number
  currency?: string
  asOf?: string
  generatedAt?: string
  reportId?: number
  horizons: { key: string; signal: string; pUp: number; status: string; conviction: number; available: boolean }[]
  warnings: number
}

export interface ScanItem {
  symbol: string
  name: string
  class: string
  price: number
  horizon: string
  signal: string
  status: string
  pUp: number
  conviction: number
  expectedReturn: number
  hitRate: number
  brierSkill: number
  strategyCagr: number
  buyHoldCagr: number
  score: number
  topReason: string
  pValue: number
  fdrPass: boolean
  note?: string
  error?: string
}

export interface ScanResult {
  id: number
  startedAt: string
  finishedAt: string
  universes: string[] | null
  horizon: string
  scanned: number
  buys: ScanItem[] | null
  avoid: ScanItem[] | null
  noEdge: ScanItem[] | null
  failed: ScanItem[] | null
}

export interface ScanProgress {
  running: boolean
  done: number
  total: number
  current: string
  started: string
}

export interface Status {
  ok: boolean
  admin: boolean
  llm: boolean
  llmModel: string
  alerts?: boolean
  scheduler: { running: boolean; lastRun: string; lastErr?: string; nextRun: string; times: string[] }
  scan: ScanProgress
  training?: TrainingProgress
  earnings?: EarningsProgress
}

export interface Prediction {
  id: number
  reportId: number
  symbol: string
  class: string
  horizon: string
  bars: number
  createdAt: string
  asOf: string
  targetDate: string
  price: number
  pUp: number
  pModel: number
  newsShift: number
  signal: string
  status: string
  conviction: number
  expectedReturn: number
  usedLlm: boolean
  resolved: boolean
  exitDate?: string
  realizedReturn?: number
}

export interface TrackStats {
  n: number
  hitRate: number
  brier: number
  avgReturn: number
  baseRate: number
}

export interface TrackRecord {
  total: number
  open: number
  overall: TrackStats
  byHorizon: Record<string, TrackStats>
  bySignal: Record<string, TrackStats>
  byStatus: Record<string, TrackStats>
  modelOnly: TrackStats
  withNews: TrackStats
  calibration: Bucket[]
  recent: Prediction[] | null
  pending: Prediction[] | null
  drift: (Drift & { symbol: string; horizon: string })[] | null
}

export interface SearchResult {
  symbol: string
  name: string
  exchange: string
  type: string
}


// ---------------------------------------------------------------- accounts

export interface User {
  id: number
  email: string
  name: string
  role: 'admin' | 'member'
  createdAt: string
}

export interface QuizResult {
  answers: Record<string, number>
  score: number
  total: number
  takenAt: string
}

export interface Profile {
  experience: '' | 'new' | 'some' | 'experienced'
  goals: string[]
  horizon: '' | '1d' | '1w' | '1m' | '1y'
  interests: string[]
  dropReaction: '' | 'sell_all' | 'sell_some' | 'hold' | 'buy_more'
  detail: '' | 'simple' | 'full'
  /** How things are explained when the person chose it; '' means decided from their answers. */
  wording: '' | 'beginner' | 'intermediate' | 'advanced'
  quiz?: QuizResult
  status: '' | 'skipped' | 'done'
  updatedAt: string
}

/** The profile turned into decisions the pages apply. */
export interface ProfileSettings {
  level: 'beginner' | 'intermediate' | 'advanced'
  detail: 'simple' | 'full'
  horizon: '1d' | '1w' | '1m' | '1y'
  showTokens: boolean
  risk: '' | 'low' | 'medium' | 'high'
  tips: string[]
}

export type ThemeChoice = 'auto' | 'light' | 'dark'
export type AccentName = 'teal' | 'indigo' | 'plum' | 'amber' | 'ocean' | 'graphite'
export interface Prefs {
  theme: ThemeChoice
  accent: AccentName
}

export interface Me {
  user: User
  profile: Profile
  settings: ProfileSettings
  prefs: Prefs
}

/** One line of the Today briefing. */
export interface TodayLine {
  tag: string
  tone: 'accent' | 'up' | 'down' | 'muted'
  strong?: string
  text: string
  link?: string
}

export interface TodayStep {
  id: string
  title: string
  body: string
  action: string
  link: string
  skipped: boolean
}

export interface TodayRange {
  key: '1d' | '1w' | '1m' | '1y'
  available: boolean
  reason?: string
  pUp: number
  base: number
  p10: number
  p50: number
  p90: number
  status: string
  signal: string
  verdict: string
}

export interface TodayRow {
  symbol: string
  name: string
  price: number
  change1d: number
  currency: string
  analysed: boolean
  ranges: TodayRange[]
}

export interface Today {
  title: string
  lastSeen?: string
  asOf?: string
  lines: TodayLine[]
  step: TodayStep | null
  rows: TodayRow[]
  tokens: (TokenBrief & { level: number; label: string })[]
}

export interface Effect {
  field: string
  answer: string
  effect: string
}

export interface QuizQuestion {
  id: string
  text: string
  options: string[]
  explain: string
}

export interface ProfileView {
  profile: Profile
  settings: ProfileSettings
  effects: Effect[]
  correct?: Record<string, number>
  starter?: string[]
}

export interface AuthConfig {
  signup: 'invite' | 'open'
  email: boolean
  needsSetup: boolean
}

export interface SessionInfo {
  id: number
  createdAt: string
  lastSeenAt: string
  userAgent: string
  ip: string
  current: boolean
}

export interface UserRow extends User {
  disabled: boolean
  lastLoginAt?: string
  watching: number
  positions: number
  profileStatus: string
}

export interface Invite {
  id: number
  note: string
  role: string
  maxUses: number
  uses: number
  expiresAt: string
  revoked: boolean
  createdBy: string
  createdAt: string
}

export type ProfileInput = Omit<Profile, 'quiz' | 'updatedAt'> & { quizAnswers?: Record<string, number> }

// ---- learning from what you do (step 2 of personalisation)

export interface SuggestionCard {
  say: string
  more: string
  yes: string
  no: string
  why: string[]
  done: string
  undo: string
}

export interface Suggestion {
  id: number
  kind: 'add' | 'remove' | 'horizon' | 'detail' | 'level' | 'reminder'
  subject: string
  card: SuggestionCard
  answer: '' | 'yes' | 'no' | 'undone' | 'expired' | 'noted' | 'muted'
  shownAt: string
  snoozeUntil?: string
  label?: string
}

export interface HabitFact {
  key: string
  title: string
  detail?: string
  count: string
}

export interface HabitPattern {
  key: string
  title: string
  need: string
  progress: number
  ready: boolean
  found: boolean
  text?: string
  detail?: string
  count?: string
}

export interface Habits {
  closed: number
  facts: HabitFact[]
  patterns: HabitPattern[]
  gapPct: number
  gapName?: string
  onlyClass?: string
}

export interface LearnPick {
  kind: string
  label: string
  everyone: number
  yes: number
  answered: number
  estimate: number
}

export interface Learned {
  paused: boolean
  muted: string[]
  since?: string
  events: number
  asked: Suggestion[]
  noticed: { key: string; text: string; detail: string }[]
  picks: LearnPick[]
  habits: Habits
  model: ModelRead
}

// The personal model's read (backend/internal/learn/model.go).
export interface ModelRead {
  stage: 'collecting' | 'early' | 'full'
  trades: number
  paper: number
  practice: number
  early: number
  full: number
  clear: boolean
  finding?: {
    key: string
    worse: boolean
    title: string
    detail: string
    in: number
    inWins: number
    out: number
    outWins: number
    rate: number
    lo: number
    hi: number
    other: number
    luck: number
  }
  test?: { passed: boolean; earlier: number; later: number; heldUp: boolean; text: string }
}

// Practice rounds (backend/internal/learn/practice.go).
export interface AppSaid {
  available: boolean
  text: string
  lean: 'up' | 'down' | ''
  status: string
  tested: boolean
  pUp: number
  baseRate: number
}

export interface PracticeRound {
  id: number
  set: number
  n: number
  of: number
  hint: string
  before: number[]
  hold: number
  app?: AppSaid
  working: boolean
  decided: boolean
  call?: 'buy' | 'skip'
  stopPct?: number
  targetPct?: number
  symbol?: string
  name?: string
  day?: string
  entry?: number
  after?: number[]
  result?: 'target' | 'stop' | 'time'
  exitBar?: number
  exitPrice?: number
  pnl?: number
  headline?: string
  appLine?: string
}

export interface PracticeState {
  set: number
  rounds: PracticeRound[]
  current?: PracticeRound
  summary?: {
    rounds: number
    bought: number
    target: number
    stop: number
    time: number
    pnl: number
    headline: string
    lines: string[]
    worth?: { key: string; title: string; detail?: string; count: string }
  }
  done: number
  bought: number
  stops: number[]
  targets: number[]
  stake: number
  finished: boolean
}

export interface ReminderSet {
  class: 'token' | 'crypto' | 'stock'
  classLabel: string
  muted: boolean
  items: { key: string; title: string; detail?: string; count: string }[]
  gapPct: number
  gapName?: string
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { 'X-Requested-With': 'market-oracle' } // the server refuses changes without it (forgery protection)
  if (body) headers['Content-Type'] = 'application/json'
  const res = await fetch(path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  })
  const text = await res.text()
  let data: unknown = null
  try {
    data = text ? JSON.parse(text) : null
  } catch {
    throw new Error(`Bad response (${res.status}): ${text.slice(0, 200)}`)
  }
  if (!res.ok) {
    const msg = (data as { error?: string } | null)?.error ?? res.statusText
    // A session that ended (signed out elsewhere, password changed, expired)
    // sends the person back to the sign-in page.
    if (res.status === 401 && !path.startsWith('/api/auth/')) window.dispatchEvent(new Event('auth:signed-out'))
    throw new ApiError(res.status, msg)
  }
  return data as T
}

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

const enc = encodeURIComponent

export const auth = {
  config: () => req<AuthConfig>('GET', '/api/auth/config'),
  inviteValid: (code: string) => req<{ valid: boolean }>('GET', `/api/auth/invite?code=${enc(code)}`),
  signup: (b: { email: string; name: string; password: string; invite: string }) => req<Me>('POST', '/api/auth/signup', b),
  login: (email: string, password: string) => req<Me>('POST', '/api/auth/login', { email, password }),
  logout: () => req('POST', '/api/auth/logout'),
  forgot: (email: string) => req<{ ok: boolean; email: boolean }>('POST', '/api/auth/forgot', { email }),
  reset: (token: string, password: string) => req('POST', '/api/auth/reset', { token, password }),
  me: () => req<Me>('GET', '/api/auth/me'),
  savePrefs: (p: Prefs) => req<Prefs>('PUT', '/api/account/prefs', p),
  updateAccount: (name: string, email: string, password: string) => req<Me>('PUT', '/api/account', { name, email, password }),
  changePassword: (current: string, next: string) => req('PUT', '/api/account/password', { current, new: next }),
  sessions: () => req<SessionInfo[]>('GET', '/api/account/sessions'),
  revokeSession: (id: number) => req('DELETE', `/api/account/sessions/${id}`),
  deleteAccount: (password: string) => req('POST', '/api/account/delete', { password }),
  profile: () => req<ProfileView>('GET', '/api/profile'),
  saveProfile: (p: ProfileInput) => req<ProfileView>('PUT', '/api/profile', p),
  quiz: () => req<QuizQuestion[]>('GET', '/api/profile/quiz'),
  users: () => req<UserRow[]>('GET', '/api/admin/users'),
  updateUser: (id: number, change: { role?: string; disabled?: boolean }) => req('PUT', `/api/admin/users/${id}`, change),
  deleteUser: (id: number) => req('DELETE', `/api/admin/users/${id}`),
  resetLink: (id: number) => req<{ link: string }>('POST', `/api/admin/users/${id}/reset-link`),
  invites: () => req<Invite[]>('GET', '/api/admin/invites'),
  createInvite: (b: { note: string; maxUses: number; days: number; role: string }) => req<{ id: number; code: string; link: string }>('POST', '/api/admin/invites', b),
  revokeInvite: (id: number) => req('DELETE', `/api/admin/invites/${id}`),
}

export const api = {
  status: () => req<Status>('GET', '/api/status'),
  today: () => req<Today>('GET', '/api/today'),
  todayStep: (id: string, action: 'done' | 'skip' | 'undo') => req('POST', '/api/today/step', { id, action }),
  search: (q: string) => req<SearchResult[]>('GET', `/api/search?q=${enc(q)}`),
  latestReport: (sym: string) => req<Report>('GET', `/api/report/${enc(sym)}`),
  reportById: (id: number) => req<Report>('GET', `/api/reports/${id}`),
  runReport: (sym: string, llm: boolean, force: boolean) =>
    req<Report>('POST', `/api/report/${enc(sym)}/run?llm=${llm ? 1 : 0}&force=${force ? 1 : 0}`),
  reportHistory: (sym: string) =>
    req<{ id: number; createdAt: string; asOf: string; headline: string }[] | null>('GET', `/api/report/${enc(sym)}/history`),
  watchlist: () => req<WatchItem[]>('GET', '/api/watchlist'),
  addWatch: (symbol: string) => req<{ ok: string; kind: 'asset' | 'token'; address?: string }>('POST', '/api/watchlist', { symbol }),
  watchTokens: () => req<{ address: string; chain: string; brief?: TokenBrief }[]>('GET', '/api/watchlist/tokens'),
  removeWatch: (symbol: string) => req('DELETE', `/api/watchlist/${enc(symbol)}`),
  universes: () => req<Record<string, string[]>>('GET', '/api/scan/universes'),
  startScan: (universes: string[], symbols: string[], horizon: string) =>
    req<ScanProgress>('POST', '/api/scan', { universes, symbols, horizon }),
  scanProgress: () => req<ScanProgress>('GET', '/api/scan/progress'),
  latestScan: () => req<ScanResult>('GET', '/api/scan/latest'),
  trackRecord: () => req<TrackRecord>('GET', '/api/track-record'),
  resolve: () => req<{ resolved: number }>('POST', '/api/track-record/resolve'),
  models: () => req<{ direction: DirSummary[] | null; training: TrainingProgress; earnings: EarningsProgress }>('GET', '/api/models'),
  trainModels: () => req<TrainingProgress>('POST', '/api/models/train'),
  replay: (sym: string, date: string) => req<Report>('POST', `/api/replay/${enc(sym)}?date=${date}`),
  replaySeries: (sym: string, from: string, to: string, every: number) =>
    req<ReplaySeries>('POST', `/api/replay/${enc(sym)}/series?from=${from}&to=${to}&every=${every}`),
  token: (address: string, refresh = false) => req<TokenReport>('GET', `/api/token/${enc(address)}${refresh ? '?refresh=1' : ''}`),
  coinSearch: (q: string) => req<CoinMatch[]>('GET', `/api/coin-search?q=${enc(q)}`),
  tokenRanks: (address: string) => req<SymbolRank[]>('GET', `/api/token/${enc(address)}/ranks`),
  tokenSearch: (q: string) => req<TokenSearchResult[] | null>('GET', `/api/token-search?q=${enc(q)}`),
  llmUsage: () => req<LLMUsage>('GET', '/api/llm/usage'),
  rankings: () => req<RankModel[] | null>('GET', '/api/rankings'),
  backfillEarnings: () => req<EarningsProgress>('POST', '/api/earnings/backfill'),
  journal: () => req<Journal>('GET', '/api/journal'),
  openPosition: (p: OpenPosition) => req<Position>('POST', '/api/journal', p),
  closePosition: (id: number, exit?: number) => req('POST', `/api/journal/${id}/close`, { exit: exit ?? 0 }),
  updatePlan: (id: number, stop?: number, target?: number) => req('PUT', `/api/journal/${id}/plan`, { stop, target }),
  deletePosition: (id: number) => req('DELETE', `/api/journal/${id}`),
  saveJournalSettings: (s: JournalSettings) => req<JournalSettings>('PUT', '/api/journal/settings', s),
  businessVsPrice: (sym: string) => req<BusinessVsPrice>('GET', `/api/fundamentals/${enc(sym)}`),
  chartLevels: (sym: string) => req<LevelsView>('GET', `/api/levels/${enc(sym)}`),
  strategies: () => req<StrategyResults>('GET', '/api/strategies'),
  runStrategies: () => req('POST', '/api/strategies/run'),
  researchOverview: () => req<ResearchOverview>('GET', '/api/research'),
  research: (subject: string) => req<ResearchView>('GET', `/api/research/${enc(subject)}`),
  researchReport: (id: number) => req<ResearchReport>('GET', `/api/research/report/${id}`),
  startResearch: (subject: string) => req<ResearchJob>('POST', `/api/research/${enc(subject)}`),
  quote: (symbol: string) => req<{ symbol: string; kind: string; price: number }>('GET', `/api/quote?symbol=${enc(symbol)}`),
  testAlert: () => req('POST', '/api/alerts/test'),
  runAll: (scan: boolean) => req('POST', `/api/run-all?scan=${scan ? 1 : 0}`),
  suggestion: () => req<{ suggestion: Suggestion | null }>('GET', '/api/learn/suggestion'),
  answerSuggestion: (id: number, answer: 'yes' | 'no' | 'undo' | 'again') => req<Suggestion>('POST', `/api/learn/suggestion/${id}`, { answer }),
  learned: () => req<Learned>('GET', '/api/learn'),
  learnPrefs: (paused: boolean, muted: string[]) => req('PUT', '/api/learn/prefs', { paused, muted }),
  forget: (what: string) => req('POST', '/api/learn/forget', { what }),
  reminders: (symbol: string) => req<ReminderSet>('GET', `/api/learn/reminders?symbol=${enc(symbol)}`),
  answerReminder: (cls: string, answer: 'noted' | 'muted', title: string) => req('POST', '/api/learn/reminders', { class: cls, answer, title }),
  practice: () => req<PracticeState>('GET', '/api/practice'),
  startPractice: () => req<PracticeState>('POST', '/api/practice/start'),
  decideRound: (id: number, call: 'buy' | 'skip', stop: number, target: number) => req<PracticeRound>('POST', `/api/practice/${id}`, { call, stop, target }),
}

// track records something the person did, for the app to learn from. It never
// blocks the page and ignores failures (nothing is kept while learning is paused).
export function track(kind: 'visit' | 'view' | 'detail' | 'page', subject: string) {
  req('POST', '/api/learn/event', { kind, subject }).catch(() => {})
}
