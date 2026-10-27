// Package repository holds SingleStore-backed data access. Each file is
// focused on a related group of tables. Queries always take a context so
// callers can enforce deadlines, and all time values are UTC.
package repository

import (
	"time"
)

// Competition mirrors the competitions table row.
type Competition struct {
	ID         string
	Provider   string
	ExternalID *string
	Name       string
	Country    *string
	Season     *string
	LogoURL    *string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Team mirrors the teams table row.
type Team struct {
	ID         string
	Provider   string
	ExternalID *string
	Name       string
	ShortName  *string
	Country    *string
	LogoURL    *string
	EloRating  float64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// MatchStatus enumerates match states.
type MatchStatus string

const (
	StatusScheduled MatchStatus = "SCHEDULED"
	StatusLive      MatchStatus = "LIVE"
	StatusHalftime  MatchStatus = "HALFTIME"
	StatusFinished  MatchStatus = "FINISHED"
	StatusPostponed MatchStatus = "POSTPONED"
	StatusCancelled MatchStatus = "CANCELLED"
)

// Outcome enumerates final match results.
type Outcome string

const (
	OutcomeHome Outcome = "HOME"
	OutcomeDraw Outcome = "DRAW"
	OutcomeAway Outcome = "AWAY"
)

// Provenance labels where a row came from.
type Provenance string

const (
	ProvenanceDemo       Provenance = "DEMO"
	ProvenanceHistorical Provenance = "HISTORICAL"
	ProvenanceReplay     Provenance = "REPLAY"
	ProvenanceLive       Provenance = "LIVE"
)

// Match mirrors the matches table row (team ids resolved via join elsewhere).
type Match struct {
	ID              string
	Provider        string
	ExternalID      *string
	CompetitionID   string
	HomeTeamID      string
	AwayTeamID      string
	KickoffAt       time.Time
	Status          MatchStatus
	Minute          *int
	HomeScore       int
	AwayScore       int
	HomeRedCards    int
	AwayRedCards    int
	FinalResult     *Outcome
	Venue           *string
	Provenance      Provenance
	SourceUpdatedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MatchEvent mirrors the match_events table row.
type MatchEvent struct {
	ID              string
	MatchID         string
	Provider        string
	ExternalID      *string
	EventType       string
	TeamSide        *string
	Minute          *int
	AddedMinute     *int
	Description     *string
	EventTime       time.Time
	SourceTimestamp *time.Time
}

// MatchStats mirrors the match_stats table row. One row per match
// (1:1 with matches). All counts are nullable because the upstream
// CSV may omit them for very old archive seasons.
type MatchStats struct {
	MatchID       string
	HomeShots     *int
	AwayShots     *int
	HomeShotsOnT  *int
	AwayShotsOnT  *int
	HomeCorners   *int
	AwayCorners   *int
	HomeFouls     *int
	AwayFouls     *int
	HomeYellow    *int
	AwayYellow    *int
	HomeRed       *int
	AwayRed       *int
	HomeXG        *float64
	AwayXG        *float64
	Referee       *string
	Source        string
	UpdatedAt     time.Time
}

// ModelPrediction mirrors the model_predictions table row.
type ModelPrediction struct {
	ID               string
	MatchID          string
	ModelName        string
	ModelVersion     string
	ForecastAt       time.Time
	MatchMinute      *int
	HomeScoreAtFC    int
	AwayScoreAtFC    int
	HomeRedCardsFC   int
	AwayRedCardsFC   int
	PHome            float64
	PDraw            float64
	PAway            float64
	FeaturesJSON     *string
	Provenance       Provenance
	SourceTimestamp  *time.Time
}

// BenchmarkPrediction mirrors the benchmark_predictions table row.
type BenchmarkPrediction struct {
	ID              string
	MatchID         string
	Provider        string
	ProviderVersion *string
	SourceType      string
	Horizon         string
	ForecastAt      time.Time
	SourceTimestamp *time.Time
	MatchMinute     *int
	MatchStatus     *string
	HomeScoreAtFC   int
	AwayScoreAtFC   int
	HomeRedCardsFC  int
	AwayRedCardsFC  int
	PHome           float64
	PDraw           float64
	PAway           float64
	RawOddsJSON     *string
	Overround       *float64
	Provenance      Provenance
}

// IngestionRun mirrors the data_ingestion_runs table row.
type IngestionRun struct {
	ID           string
	Provider     string
	JobName      string
	Status       string // STARTED SUCCEEDED FAILED PARTIAL
	StartedAt    time.Time
	FinishedAt   *time.Time
	RecordsIn    int
	RecordsOut   int
	ErrorMessage *string
	DetailsJSON  *string
}
