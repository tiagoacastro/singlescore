// Package repository holds SingleStore-backed data access. Each file is
// focused on a related group of tables. Queries always take a context so
// callers can enforce deadlines, and all time values are UTC.
package repository

import (
	"time"
)

// Competition is the subset of a competitions row the API serves.
type Competition struct {
	ID      string
	Name    string
	Country *string
	Season  *string
	LogoURL *string
}

// Team is the subset of a teams row the API serves.
type Team struct {
	ID        string
	Name      string
	ShortName *string
	Country   *string
	LogoURL   *string
	EloRating float64
}

// MatchStatus is a matches.status value: SCHEDULED, FINISHED, POSTPONED
// or CANCELLED (the column also allows LIVE / HALFTIME, which nothing
// writes any more).
type MatchStatus string

// Outcome enumerates final match results.
type Outcome string

const (
	OutcomeHome Outcome = "HOME"
	OutcomeDraw Outcome = "DRAW"
	OutcomeAway Outcome = "AWAY"
)

// Provenance labels where a row came from: DEMO, HISTORICAL or REPLAY.
type Provenance string

// Match mirrors the matches table row (team ids resolved via join elsewhere).
type Match struct {
	ID            string
	CompetitionID string
	HomeTeamID    string
	AwayTeamID    string
	KickoffAt     time.Time
	Status        MatchStatus
	Minute        *int
	HomeScore     int
	AwayScore     int
	HomeRedCards  int
	AwayRedCards  int
	FinalResult   *Outcome
	Venue         *string
	Provenance    Provenance
}

// FinishedMatch is a completed match's result, for evaluation.
type FinishedMatch struct {
	ID            string
	CompetitionID string
	FinalResult   Outcome
}

// MatchEvent mirrors the match_events table row.
type MatchEvent struct {
	ID          string
	MatchID     string
	EventType   string
	TeamSide    *string
	Minute      *int
	AddedMinute *int
	Description *string
	EventTime   time.Time
}

// MatchStats mirrors the match_stats table row. One row per match
// (1:1 with matches). All counts are nullable because the upstream
// CSV may omit them for very old archive seasons.
type MatchStats struct {
	MatchID      string
	HomeShots    *int
	AwayShots    *int
	HomeShotsOnT *int
	AwayShotsOnT *int
	HomeCorners  *int
	AwayCorners  *int
	HomeFouls    *int
	AwayFouls    *int
	HomeYellow   *int
	AwayYellow   *int
	HomeRed      *int
	AwayRed      *int
	HomeXG       *float64
	AwayXG       *float64
	Referee      *string
	Source       string
}

// MatchCommentary mirrors the match_commentary table row. One row per
// commentary line scraped from the upstream provider (ESPN). The
// natural key is (match_id, sequence); there is no synthetic id
// column.
type MatchCommentary struct {
	MatchID     string
	Sequence    int
	Minute      *int
	AddedMinute *int
	Period      *int
	Kind        string
	TeamSide    *string
	Text        string
}

// ModelPrediction is the subset of a model_predictions row the API
// serves (features_json and the red-card inputs stay in the table).
type ModelPrediction struct {
	ID            string
	MatchID       string
	ModelName     string
	ModelVersion  string
	ForecastAt    time.Time
	MatchMinute   *int // nil = pre-match
	HomeScoreAtFC int
	AwayScoreAtFC int
	PHome         float64
	PDraw         float64
	PAway         float64
	Provenance    Provenance
}

// BenchmarkPrediction is the subset of a benchmark_predictions row the
// API serves (raw odds and overround stay in the table).
type BenchmarkPrediction struct {
	ID          string
	MatchID     string
	Provider    string
	SourceType  string
	Horizon     string
	ForecastAt  time.Time
	MatchMinute *int
	PHome       float64
	PDraw       float64
	PAway       float64
	Provenance  Provenance
}

// IngestionRun is the subset of a data_ingestion_runs row the
// dashboard shows.
type IngestionRun struct {
	Provider     string
	JobName      string
	Status       string // STARTED SUCCEEDED FAILED PARTIAL
	StartedAt    time.Time
	RecordsIn    int
	RecordsOut   int
	ErrorMessage *string
}
