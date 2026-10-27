// Package service composes repositories into GraphQL-shaped responses.
//
// The service deliberately returns gqlgen model structs so resolvers can
// stay very thin. If/when we add non-GraphQL callers we can split a
// domain layer back out, but keeping conversion in one place avoids the
// double-struct tax while the API is small.
//
// Files in this package:
//
//   - service.go    — Service type, lifecycle, Dashboard, Competitions,
//                     Matches list, single Match hydration + refresh.
//   - evaluation.go — ModelEvaluation + metrics helpers.
//   - mappers.go    — repository → gqlgen model converters.
package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/graph/model"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// Service exposes query-shaped reads for the GraphQL resolvers.
type Service struct {
	catalog     *repository.Catalog
	matches     *repository.Matches
	predictions *repository.Predictions
	ingestion   *repository.Ingestion

	refresher MatchRefresher // optional
	logger    serviceLogger  // optional

	// Per-match last-refresh timestamps + mutex, used to dedupe
	// concurrent refresh calls (multiple viewers of the same live
	// match shouldn't each hit the upstream API every 12 s).
	refreshMu       sync.Mutex
	lastRefreshedAt map[string]time.Time
}

// MatchRefresher triggers a one-match live refresh. Implemented by
// ingestion.Sync; abstracted here so the service doesn't take a direct
// dependency on the ingestion package.
type MatchRefresher interface {
	RefreshMatch(ctx context.Context, internalMatchID string) error
}

type serviceLogger interface {
	Warn(msg string, args ...any)
	Info(msg string, args ...any)
}

// RefreshWindow is how long a per-match refresh is cached before
// another one is allowed. 10 s pairs well with a 12 s client poll
// (one upstream request per unique viewer per poll, no duplicates on
// concurrent calls).
const RefreshWindow = 10 * time.Second

// scheduledRefreshWindow is how far ahead/behind kickoff a SCHEDULED
// match is still eligible for an upstream refresh (so clicking a
// match 15 min before kickoff pulls fresh line-ups, but clicking one
// next week does not).
const scheduledRefreshWindow = 3 * time.Hour

// New builds a Service from its collaborator repositories. The returned
// service has no refresher wired; call WithRefresher to enable per-match
// live-focus polling.
func New(cat *repository.Catalog, m *repository.Matches, p *repository.Predictions, i *repository.Ingestion) *Service {
	return &Service{
		catalog:         cat,
		matches:         m,
		predictions:     p,
		ingestion:       i,
		lastRefreshedAt: map[string]time.Time{},
	}
}

// WithRefresher attaches a MatchRefresher so Match(id) transparently
// re-fetches upstream when the stored match is stale and still in a
// status that benefits from live data. Pass nil to disable (default).
func (s *Service) WithRefresher(r MatchRefresher, logger serviceLogger) *Service {
	s.refresher = r
	s.logger = logger
	return s
}

// ErrServiceNotReady is returned when a required collaborator is missing.
var ErrServiceNotReady = errors.New("service: not initialised")

// -------------------- Dashboard --------------------

// Dashboard assembles the home-screen payload.
func (s *Service) Dashboard(ctx context.Context) (*model.Dashboard, error) {
	live, err := s.matches.Live(ctx)
	if err != nil {
		return nil, fmt.Errorf("live matches: %w", err)
	}
	recentFinished, err := s.matches.RecentFinished(ctx, 10)
	if err != nil {
		return nil, fmt.Errorf("recent finished: %w", err)
	}

	liveModels, err := s.hydrateMatches(ctx, live, true)
	if err != nil {
		return nil, err
	}
	finishedModels, err := s.hydrateMatches(ctx, recentFinished, false)
	if err != nil {
		return nil, err
	}

	changes, err := s.recentPredictionChanges(ctx, 5)
	if err != nil {
		return nil, err
	}
	metrics, err := s.liveModelMetrics(ctx)
	if err != nil {
		return nil, err
	}

	ds, err := s.loadDataSourceStatus(ctx)
	if err != nil {
		return nil, err
	}

	return &model.Dashboard{
		LiveMatches:             liveModels,
		RecentFinishedMatches:   finishedModels,
		RecentPredictionChanges: changes,
		ModelMetrics:            metrics,
		DataSources:             ds,
		// SystemStatus is attached by the resolver (it needs runtime deps).
	}, nil
}

// loadDataSourceStatus pulls the most-recent ingestion run per job and
// shapes it into the gqlgen DataSourceStatus list.
func (s *Service) loadDataSourceStatus(ctx context.Context) ([]*model.DataSourceStatus, error) {
	runs, err := s.ingestion.LatestPerJob(ctx, 10)
	if err != nil {
		return nil, err
	}
	ds := make([]*model.DataSourceStatus, 0, len(runs))
	for _, r := range runs {
		r := r
		var lastRun *time.Time
		if !r.StartedAt.IsZero() {
			t := r.StartedAt
			lastRun = &t
		}
		status := r.Status
		ds = append(ds, &model.DataSourceStatus{
			Provider:     r.Provider,
			JobName:      r.JobName,
			LastRunAt:    lastRun,
			LastStatus:   &status,
			RecordsIn:    r.RecordsIn,
			RecordsOut:   r.RecordsOut,
			ErrorMessage: r.ErrorMessage,
		})
	}
	return ds, nil
}

// -------------------- Competitions --------------------

// Competitions returns all competitions sorted by name.
func (s *Service) Competitions(ctx context.Context) ([]*model.Competition, error) {
	comps, err := s.catalog.ListCompetitions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Competition, 0, len(comps))
	for _, c := range comps {
		c := c
		out = append(out, toCompetitionModel(c))
	}
	return out, nil
}

// -------------------- Matches (list + detail) --------------------

// Matches runs a paged match query and hydrates the results.
func (s *Service) Matches(ctx context.Context, status *model.MatchStatus, first *int, after *string) (*model.MatchConnection, error) {
	var filter repository.MatchFilter
	if status != nil {
		rs := repository.MatchStatus(*status)
		filter.Status = &rs
	}
	if first != nil {
		filter.First = *first
	}
	if after != nil {
		filter.After = *after
	}
	page, err := s.matches.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	models, err := s.hydrateMatches(ctx, page.Matches, true)
	if err != nil {
		return nil, err
	}

	edges := make([]*model.MatchEdge, 0, len(models))
	for i, m := range models {
		edges = append(edges, &model.MatchEdge{Node: m, Cursor: cursorFromMatch(page.Matches[i])})
	}
	var endCursor *string
	if page.EndCursor != "" {
		ec := page.EndCursor
		endCursor = &ec
	}
	return &model.MatchConnection{
		Edges:      edges,
		PageInfo:   &model.PageInfo{EndCursor: endCursor, HasNextPage: page.HasNext},
		TotalCount: page.TotalCount,
	}, nil
}

// Match returns a single match fully hydrated with events + predictions.
//
// If a refresher is attached and the match is in a status that benefits
// from live data (LIVE, HALFTIME, or SCHEDULED within ±3 h of kickoff),
// we trigger a one-match upstream refresh — deduped per match by
// RefreshWindow — so a client polling every 12 s sees fresh scores
// without each poll firing a separate API request.
func (s *Service) Match(ctx context.Context, id string) (*model.Match, error) {
	raw, err := s.matches.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	if s.refresher != nil && s.shouldRefresh(*raw) {
		s.triggerRefresh(ctx, raw)
		// Reload the row so events + predictions pick up new values.
		if reloaded, err := s.matches.GetByID(ctx, id); err == nil && reloaded != nil {
			raw = reloaded
		}
	}

	return s.hydrateMatchDetail(ctx, *raw)
}

// hydrateMatchDetail loads all per-match child collections (teams,
// competition, events, model + benchmark predictions) and builds the
// gqlgen Match. Separated from Match() so refresh orchestration stays
// readable in the hot path.
func (s *Service) hydrateMatchDetail(ctx context.Context, raw repository.Match) (*model.Match, error) {
	teamMap, err := s.catalog.GetTeams(ctx, []string{raw.HomeTeamID, raw.AwayTeamID})
	if err != nil {
		return nil, fmt.Errorf("teams: %w", err)
	}
	compMap, err := s.catalog.GetCompetitions(ctx, []string{raw.CompetitionID})
	if err != nil {
		return nil, fmt.Errorf("competitions: %w", err)
	}
	events, err := s.matches.EventsForMatches(ctx, []string{raw.ID})
	if err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	modelPreds, err := s.predictions.ModelForMatches(ctx, []string{raw.ID})
	if err != nil {
		return nil, fmt.Errorf("model preds: %w", err)
	}
	benchPreds, err := s.predictions.BenchmarkForMatches(ctx, []string{raw.ID})
	if err != nil {
		return nil, fmt.Errorf("bench preds: %w", err)
	}
	stats, err := s.matches.StatsForMatches(ctx, []string{raw.ID})
	if err != nil {
		return nil, fmt.Errorf("stats: %w", err)
	}

	m := toMatchModel(raw, compMap, teamMap)
	for _, e := range events[raw.ID] {
		e := e
		m.Events = append(m.Events, toEventModel(e))
	}
	for _, p := range modelPreds[raw.ID] {
		p := p
		m.ModelPredictions = append(m.ModelPredictions, toModelPredictionModel(p))
	}
	for _, b := range benchPreds[raw.ID] {
		b := b
		m.BenchmarkPredictions = append(m.BenchmarkPredictions, toBenchmarkModel(b))
	}
	if s, ok := stats[raw.ID]; ok {
		m.Stats = toMatchStatsModel(s)
	}
	if n := len(m.ModelPredictions); n > 0 {
		m.LatestModelPrediction = m.ModelPredictions[n-1]
	}
	return m, nil
}

// triggerRefresh calls the attached refresher and logs the outcome. A
// failed refresh must never fail the query — stale data is still useful.
func (s *Service) triggerRefresh(ctx context.Context, raw *repository.Match) {
	started := time.Now()
	if err := s.refresher.RefreshMatch(ctx, raw.ID); err != nil {
		if s.logger != nil {
			s.logger.Warn("match refresh failed",
				"match_id", raw.ID, "err", err.Error(),
				"duration_ms", time.Since(started).Milliseconds())
		}
		return
	}
	if s.logger != nil {
		s.logger.Info("match refreshed",
			"match_id", raw.ID, "status", string(raw.Status),
			"duration_ms", time.Since(started).Milliseconds())
	}
}

// shouldRefresh decides whether a match query should trigger an
// upstream refresh. Gates on:
//
//  1. Match status (LIVE/HALFTIME always; SCHEDULED only within
//     ±scheduledRefreshWindow of kickoff).
//  2. Dedupe window (claims the slot before returning true so
//     parallel callers only trigger one refresh).
func (s *Service) shouldRefresh(m repository.Match) bool {
	switch m.Status {
	case repository.StatusLive, repository.StatusHalftime:
		// always eligible
	case repository.StatusScheduled:
		if time.Until(m.KickoffAt).Abs() > scheduledRefreshWindow {
			return false
		}
	default:
		return false
	}
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	now := time.Now()
	if last, ok := s.lastRefreshedAt[m.ID]; ok && now.Sub(last) < RefreshWindow {
		return false
	}
	s.lastRefreshedAt[m.ID] = now
	return true
}

// -------------------- shared batch hydration --------------------

// hydrateMatches turns a slice of raw match rows into gqlgen models,
// batching team / competition lookups and (optionally) latest-prediction
// fetches so N raws costs at most 3 round-trips total.
func (s *Service) hydrateMatches(ctx context.Context, raws []repository.Match, withLatest bool) ([]*model.Match, error) {
	if len(raws) == 0 {
		return []*model.Match{}, nil
	}
	teamIDs := make([]string, 0, len(raws)*2)
	compIDs := make([]string, 0, len(raws))
	matchIDs := make([]string, 0, len(raws))
	for _, r := range raws {
		teamIDs = append(teamIDs, r.HomeTeamID, r.AwayTeamID)
		compIDs = append(compIDs, r.CompetitionID)
		matchIDs = append(matchIDs, r.ID)
	}
	teams, err := s.catalog.GetTeams(ctx, dedupStrings(teamIDs))
	if err != nil {
		return nil, fmt.Errorf("teams: %w", err)
	}
	comps, err := s.catalog.GetCompetitions(ctx, dedupStrings(compIDs))
	if err != nil {
		return nil, fmt.Errorf("competitions: %w", err)
	}
	var latest map[string]repository.ModelPrediction
	if withLatest {
		latest, err = s.predictions.LatestModelForMatches(ctx, matchIDs)
		if err != nil {
			return nil, fmt.Errorf("latest preds: %w", err)
		}
	}
	out := make([]*model.Match, 0, len(raws))
	for _, r := range raws {
		r := r
		m := toMatchModel(r, comps, teams)
		if lp, ok := latest[r.ID]; ok {
			m.LatestModelPrediction = toModelPredictionModel(lp)
		}
		out = append(out, m)
	}
	return out, nil
}

// cursorFromMatch rebuilds the opaque cursor for a match row. We reuse
// the repository's cursor shape (base64 of RFC3339Nano|id).
func cursorFromMatch(m repository.Match) string {
	raw := m.KickoffAt.UTC().Format(time.RFC3339Nano) + "|" + m.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}
