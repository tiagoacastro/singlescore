// Hand-authored TypeScript shapes mirroring the GraphQL schema. These are
// used until we wire a GraphQL codegen pipeline.

export type Provenance = 'DEMO' | 'HISTORICAL' | 'REPLAY';
export type MatchStatus =
  | 'SCHEDULED'
  | 'FINISHED'
  | 'POSTPONED'
  | 'CANCELLED';
export type Outcome = 'HOME' | 'DRAW' | 'AWAY';
export type BenchmarkHorizon = 'PRE_MATCH';
export type BenchmarkSourceType = 'ALGORITHM' | 'MARKET_ODDS' | 'MANUAL';

export interface Competition {
  id: string;
  name: string;
  country?: string | null;
  season?: string | null;
  logoUrl?: string | null;
}

export interface Team {
  id: string;
  name: string;
  shortName?: string | null;
  country?: string | null;
  eloRating?: number | null;
  logoUrl?: string | null;
}

export interface ModelPrediction {
  id: string;
  modelName: string;
  modelVersion: string;
  forecastAt: string;
  matchMinute?: number | null;
  homeScoreAtForecast?: number;
  awayScoreAtForecast?: number;
  pHome: number;
  pDraw: number;
  pAway: number;
  provenance: Provenance;
}

export interface BenchmarkPrediction {
  id: string;
  provider: string;
  sourceType: BenchmarkSourceType;
  horizon: BenchmarkHorizon;
  forecastAt: string;
  matchMinute?: number | null;
  pHome: number;
  pDraw: number;
  pAway: number;
  provenance: Provenance;
}

export interface MatchEvent {
  id: string;
  eventType: string;
  teamSide?: 'HOME' | 'AWAY' | 'NEUTRAL' | null;
  minute?: number | null;
  addedMinute?: number | null;
  description?: string | null;
  eventTime: string;
}

export interface MatchCommentary {
  sequence: number;
  minute?: number | null;
  addedMinute?: number | null;
  period?: number | null;
  kind: string;
  teamSide?: 'HOME' | 'AWAY' | 'NEUTRAL' | null;
  text: string;
}

export interface Match {
  id: string;
  competition: Competition;
  homeTeam: Team;
  awayTeam: Team;
  kickoffAt: string;
  status: MatchStatus;
  minute?: number | null;
  homeScore: number;
  awayScore: number;
  finalResult?: Outcome | null;
  provenance: Provenance;
  venue?: string | null;
  events?: MatchEvent[];
  commentary?: MatchCommentary[];
  modelPredictions?: ModelPrediction[];
  benchmarkPredictions?: BenchmarkPrediction[];
  preMatchPrediction?: ModelPrediction | null;
  stats?: MatchStats | null;
}

export interface MatchStats {
  homeShots?: number | null;
  awayShots?: number | null;
  homeShotsOnTarget?: number | null;
  awayShotsOnTarget?: number | null;
  homeCorners?: number | null;
  awayCorners?: number | null;
  homeFouls?: number | null;
  awayFouls?: number | null;
  homeYellowCards?: number | null;
  awayYellowCards?: number | null;
  homeRedCards?: number | null;
  awayRedCards?: number | null;
  homeXg?: number | null;
  awayXg?: number | null;
  referee?: string | null;
  source: string;
}

export interface Health {
  status: string;
  databaseReachable: boolean;
  demoMode: boolean;
  version: string;
  startedAt: string;
}

export interface DataSourceStatus {
  provider: string;
  jobName: string;
  lastRunAt?: string | null;
  lastStatus?: string | null;
  recordsIn: number;
  recordsOut: number;
  errorMessage?: string | null;
}

export interface EvaluationSummary {
  modelName: string;
  modelVersion: string;
  sampleSize: number;
  brierScore: number;
  logLoss: number;
  accuracy: number;
  dateRangeFrom?: string | null;
  dateRangeTo?: string | null;
}

export interface Dashboard {
  recentFinishedMatches: Match[];
  modelMetrics?: EvaluationSummary | null;
  dataSources: DataSourceStatus[];
  systemStatus: Health;
}

export interface CalibrationBin {
  binLow: number;
  binHigh: number;
  avgPredicted: number;
  observedFrequency: number;
  sampleSize: number;
}

export interface EvaluationReport {
  summary?: EvaluationSummary | null;
  calibration: CalibrationBin[];
  byMinuteBucket: { minuteBucket: string; brierScore: number; logLoss: number; sampleSize: number }[];
  byCompetition: { competition: Competition; brierScore: number; logLoss: number; sampleSize: number }[];
  benchmarks: {
    provider: string;
    horizon: BenchmarkHorizon;
    sampleSize: number;
    brierScore: number;
    logLoss: number;
    accuracy: number;
  }[];
  missingBenchmarkCoverage: number;
}
