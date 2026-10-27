// Package ingestion defines the pluggable Provider abstraction used to
// pull fixtures, events, predictions and odds from external sources.
//
// The core principle: provider-specific response types stay inside this
// package. Each adapter normalizes external data into the application's
// domain structs before it leaves the package.
package ingestion

import (
	"context"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// Capability describes a feature a Provider supports.
type Capability string

const (
	CapFixtures    Capability = "FIXTURES"
	CapLiveState   Capability = "LIVE_STATE"
	CapEvents      Capability = "EVENTS"
	CapPredictions Capability = "PREDICTIONS"
	CapOdds        Capability = "ODDS"
)

// Fixture normalizes external match metadata the provider knows about.
type Fixture struct {
	ExternalMatchID string
	ExternalCompID  string
	CompetitionName string
	Country         string
	Season          string
	HomeTeamExtID   string
	HomeTeamName    string
	AwayTeamExtID   string
	AwayTeamName    string
	KickoffAt       time.Time
	Venue           *string
	Status          repository.MatchStatus
	HomeScore       int
	AwayScore       int
	Minute          *int
	FinalResult     *repository.Outcome
	SourceUpdatedAt time.Time
}

// Event is a normalized match event from a provider.
type Event struct {
	ExternalEventID string
	ExternalMatchID string
	EventType       string
	TeamSide        *string
	Minute          *int
	AddedMinute     *int
	Description     *string
	EventTime       time.Time
	SourceTimestamp time.Time
}

// BenchmarkForecast is a captured external prediction.
type BenchmarkForecast struct {
	ExternalMatchID string
	Horizon         string // PRE_MATCH or LIVE
	SourceType      string // ALGORITHM, MARKET_ODDS, MANUAL
	ForecastAt      time.Time
	SourceTimestamp *time.Time
	Minute          *int
	HomeScoreAtFC   int
	AwayScoreAtFC   int
	PHome           float64
	PDraw           float64
	PAway           float64
	RawOddsJSON     *string
	Overround       *float64
}

// Provider is implemented by anything that can supply football data.
type Provider interface {
	Name() string
	Capabilities() []Capability
	FetchFixtures(ctx context.Context) ([]Fixture, error)
	FetchEvents(ctx context.Context, extMatchIDs []string) ([]Event, error)
	FetchBenchmarkForecasts(ctx context.Context, extMatchIDs []string) ([]BenchmarkForecast, error)
}
