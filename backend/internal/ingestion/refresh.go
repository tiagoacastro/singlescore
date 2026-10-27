package ingestion

import (
	"context"
	"fmt"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// RefreshMatch fetches a single match from the provider by its external
// id, upserts the row, and writes a fresh model-prediction snapshot.
// This is the hot path for the per-match live-focus refresh. The caller
// is responsible for deduping (so N concurrent viewers don't fire N
// requests); this method does the fetch unconditionally.
//
// The provider must implement MatchFetcher — if not, an error is
// returned so the caller can fall back to the broader sync.
func (s *Sync) RefreshMatch(ctx context.Context, internalMatchID string) error {
	fetcher, ok := s.provider.(MatchFetcher)
	if !ok {
		return fmt.Errorf("provider %q does not implement MatchFetcher", s.provider.Name())
	}
	existing, err := s.deps.Matches.GetByID(ctx, internalMatchID)
	if err != nil {
		return fmt.Errorf("load match %s: %w", internalMatchID, err)
	}
	if existing == nil {
		return fmt.Errorf("match %s not found", internalMatchID)
	}
	if existing.ExternalID == nil || *existing.ExternalID == "" {
		return fmt.Errorf("match %s has no external id", internalMatchID)
	}

	fx, err := fetcher.FetchMatch(ctx, *existing.ExternalID)
	if err != nil {
		return fmt.Errorf("fetch match %s: %w", *existing.ExternalID, err)
	}

	// Preserve competition_id + team_ids from the existing row — the
	// per-match endpoint sometimes omits them, and we want to avoid
	// nulling-out stable foreign keys.
	kickoff := fx.KickoffAt.UTC().Round(time.Microsecond)
	srcUpdated := fx.SourceUpdatedAt.UTC().Round(time.Microsecond)
	ext := fx.ExternalMatchID
	if err := s.deps.Matches.UpsertMatch(ctx, repository.Match{
		ID:              existing.ID,
		Provider:        s.provider.Name(),
		ExternalID:      &ext,
		CompetitionID:   existing.CompetitionID,
		HomeTeamID:      existing.HomeTeamID,
		AwayTeamID:      existing.AwayTeamID,
		KickoffAt:       kickoff,
		Status:          fx.Status,
		Minute:          fx.Minute,
		HomeScore:       fx.HomeScore,
		AwayScore:       fx.AwayScore,
		HomeRedCards:    existing.HomeRedCards,
		AwayRedCards:    existing.AwayRedCards,
		FinalResult:     fx.FinalResult,
		Venue:           fx.Venue,
		Provenance:      repository.ProvenanceLive,
		SourceUpdatedAt: &srcUpdated,
	}); err != nil {
		return fmt.Errorf("upsert match: %w", err)
	}

	// Fresh prediction snapshots using the shared policy so refresher
	// and sync produce identical rows (keyed deterministically).
	elo, err := s.eloFor(ctx, existing.HomeTeamID, existing.AwayTeamID)
	if err != nil {
		return fmt.Errorf("resolve team elo: %w", err)
	}
	rows, _ := s.buildMatchPredictions(existing.ID, *fx, elo, time.Now().UTC().Round(time.Microsecond))
	if len(rows) == 0 {
		return nil
	}
	return s.deps.Predictions.InsertModelBatch(ctx, rows)
}

// RebuildFinishedTimelineFromEvents regenerates the dense 5-minute
// probability-evolution snapshots for a FINISHED match using the real
// goal timings in match_events. Upgrades the HT/FT approximation to a
// true per-minute scoreline curve: a 24' goal creates a step change at
// minute 25 (the nearest 5-min grid point), plus an extra snapshot at
// minute 24 for pixel-accurate alignment.
//
// Safe to call repeatedly — deterministic row ids mean re-runs upsert
// in place rather than inserting duplicates. No-op (returns nil) for
// non-FINISHED matches.
func (s *Sync) RebuildFinishedTimelineFromEvents(ctx context.Context, internalMatchID string) error {
	m, err := s.deps.Matches.GetByID(ctx, internalMatchID)
	if err != nil {
		return fmt.Errorf("load match: %w", err)
	}
	if m == nil {
		return fmt.Errorf("match %s not found", internalMatchID)
	}
	if m.Status != repository.StatusFinished {
		return nil
	}
	eventsByMatch, err := s.deps.Matches.EventsForMatches(ctx, []string{internalMatchID})
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}
	elo, err := s.eloFor(ctx, m.HomeTeamID, m.AwayTeamID)
	if err != nil {
		return fmt.Errorf("resolve team elo: %w", err)
	}
	// Minimal fixture carrying only the fields buildPrediction uses.
	fx := Fixture{
		ExternalMatchID: strOrEmpty(m.ExternalID),
		KickoffAt:       m.KickoffAt,
		Status:          m.Status,
		HomeScore:       m.HomeScore,
		AwayScore:       m.AwayScore,
	}
	rows, errs := s.buildFinishedTimelineWithEvents(internalMatchID, fx, elo, eventsByMatch[internalMatchID], time.Now().UTC().Round(time.Microsecond))
	for _, e := range errs {
		s.deps.Logger.Warn("predict rebuild warning", "err", e)
	}
	if len(rows) == 0 {
		return nil
	}
	return s.deps.Predictions.InsertModelBatch(ctx, rows)
}

// eloFor loads both teams in one query and returns their Elo + the
// configured home-advantage bump. Shared by RefreshMatch and
// RebuildFinishedTimelineFromEvents so neither path has to deal with
// a two-step lookup.
func (s *Sync) eloFor(ctx context.Context, homeTeamID, awayTeamID string) (eloInputs, error) {
	teams, err := s.deps.Catalog.GetTeams(ctx, []string{homeTeamID, awayTeamID})
	if err != nil {
		return eloInputs{}, err
	}
	return eloInputs{
		home:          valueOrDefault(teams[homeTeamID].EloRating, s.opts.DefaultEloRating),
		away:          valueOrDefault(teams[awayTeamID].EloRating, s.opts.DefaultEloRating),
		homeAdvantage: s.opts.HomeAdvantageElo,
	}, nil
}
