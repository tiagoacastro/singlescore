// Package service composes repositories into GraphQL-shaped responses.
//
// The service deliberately returns gqlgen model structs so resolvers can
// stay very thin. If/when we add non-GraphQL callers we can split a
// domain layer back out, but keeping conversion in one place avoids the
// double-struct tax while the API is small.
//
// Files in this package:
//
//   - service.go    — Service type, Dashboard, Competitions,
//     Matches list, single Match hydration.
//   - evaluation.go — ModelEvaluation + dashboard metrics.
//   - mappers.go    — repository → gqlgen model converters.
package service

import (
	"context"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/tiagocastro/singlescore/backend/internal/graph/model"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// Service exposes query-shaped reads for the GraphQL resolvers.
type Service struct {
	catalog     *repository.Catalog
	matches     *repository.Matches
	predictions *repository.Predictions
	ingestion   *repository.Ingestion
}

// New builds a Service from its collaborator repositories.
func New(cat *repository.Catalog, m *repository.Matches, p *repository.Predictions, i *repository.Ingestion) *Service {
	return &Service{
		catalog:     cat,
		matches:     m,
		predictions: p,
		ingestion:   i,
	}
}

// -------------------- Dashboard --------------------

// Dashboard assembles the home-screen payload.
func (s *Service) Dashboard(ctx context.Context) (*model.Dashboard, error) {
	var dash model.Dashboard // SystemStatus is attached by the resolver.
	err := parallel(ctx,
		func(ctx context.Context) error {
			recent, err := s.matches.RecentFinished(ctx, 10)
			if err != nil {
				return err
			}
			dash.RecentFinishedMatches, err = s.hydrateMatches(ctx, recent)
			return err
		},
		func(ctx context.Context) (err error) {
			dash.ModelMetrics, err = s.preMatchModelMetrics(ctx)
			return err
		},
		func(ctx context.Context) (err error) { dash.DataSources, err = s.loadDataSourceStatus(ctx); return err },
	)
	if err != nil {
		return nil, err
	}
	return &dash, nil
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
		out = append(out, toCompetitionModel(c))
	}
	return out, nil
}

// -------------------- Matches (list + detail) --------------------

// Matches runs a paged, filtered match query and hydrates the results.
func (s *Service) Matches(ctx context.Context, status *model.MatchStatus, competitionID, team *string, first *int, after *string) (*model.MatchConnection, error) {
	var filter repository.MatchFilter
	if status != nil {
		rs := repository.MatchStatus(*status)
		filter.Status = &rs
	}
	if competitionID != nil {
		filter.CompetitionID = *competitionID
	}
	if team != nil {
		filter.Team = *team
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
	models, err := s.hydrateMatches(ctx, page.Matches)
	if err != nil {
		return nil, err
	}

	edges := make([]*model.MatchEdge, 0, len(models))
	for i, m := range models {
		edges = append(edges, &model.MatchEdge{Node: m, Cursor: repository.EncodeCursor(page.Matches[i])})
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
func (s *Service) Match(ctx context.Context, id string) (*model.Match, error) {
	raw, err := s.matches.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}
	return s.hydrateMatchDetail(ctx, *raw)
}

// hydrateMatchDetail loads all per-match child collections (teams,
// competition, events, model + benchmark predictions) and builds the
// gqlgen Match.
func (s *Service) hydrateMatchDetail(ctx context.Context, raw repository.Match) (*model.Match, error) {
	ids := []string{raw.ID}
	var (
		teams      map[string]repository.Team
		comps      map[string]repository.Competition
		events     map[string][]repository.MatchEvent
		commentary map[string][]repository.MatchCommentary
		stats      map[string]repository.MatchStats
		modelPreds map[string][]repository.ModelPrediction
		benchPreds map[string][]repository.BenchmarkPrediction
	)
	err := parallel(ctx,
		func(ctx context.Context) (err error) {
			teams, err = s.catalog.GetTeams(ctx, []string{raw.HomeTeamID, raw.AwayTeamID})
			return err
		},
		func(ctx context.Context) (err error) {
			comps, err = s.catalog.GetCompetitions(ctx, []string{raw.CompetitionID})
			return err
		},
		func(ctx context.Context) (err error) { events, err = s.matches.EventsForMatches(ctx, ids); return err },
		func(ctx context.Context) (err error) {
			commentary, err = s.matches.CommentaryForMatches(ctx, ids)
			return err
		},
		func(ctx context.Context) (err error) { stats, err = s.matches.StatsForMatches(ctx, ids); return err },
		func(ctx context.Context) (err error) {
			modelPreds, err = s.predictions.ModelForMatches(ctx, ids)
			return err
		},
		func(ctx context.Context) (err error) {
			benchPreds, err = s.predictions.BenchmarkForMatches(ctx, ids)
			return err
		},
	)
	if err != nil {
		return nil, err
	}

	m := toMatchModel(raw, comps, teams)
	for _, e := range events[raw.ID] {
		m.Events = append(m.Events, toEventModel(e))
	}
	for _, c := range commentary[raw.ID] {
		m.Commentary = append(m.Commentary, toMatchCommentaryModel(c))
	}
	for _, p := range modelPreds[raw.ID] {
		m.ModelPredictions = append(m.ModelPredictions, toModelPredictionModel(p))
	}
	for _, b := range benchPreds[raw.ID] {
		m.BenchmarkPredictions = append(m.BenchmarkPredictions, toBenchmarkModel(b))
	}
	if st, ok := stats[raw.ID]; ok {
		m.Stats = toMatchStatsModel(st)
	}
	m.PreMatchPrediction = preMatchPrediction(m.ModelPredictions)
	return m, nil
}

// preMatchPrediction picks the pre-kickoff snapshot (no match minute)
// with the newest forecast, mirroring PreMatchModelForMatches so the
// list and detail views agree.
func preMatchPrediction(preds []*model.ModelPrediction) *model.ModelPrediction {
	var best *model.ModelPrediction
	for _, p := range preds {
		if p.MatchMinute == nil && (best == nil || p.ForecastAt.After(best.ForecastAt)) {
			best = p
		}
	}
	return best
}

// -------------------- shared batch hydration --------------------

// hydrateMatches turns a slice of raw match rows into gqlgen models,
// batching team / competition / pre-match-prediction lookups so N raws
// cost 3 concurrent round-trips.
func (s *Service) hydrateMatches(ctx context.Context, raws []repository.Match) ([]*model.Match, error) {
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
	var (
		teams    map[string]repository.Team
		comps    map[string]repository.Competition
		preMatch map[string]repository.ModelPrediction
	)
	err := parallel(ctx,
		func(ctx context.Context) (err error) {
			teams, err = s.catalog.GetTeams(ctx, dedupStrings(teamIDs))
			return err
		},
		func(ctx context.Context) (err error) {
			comps, err = s.catalog.GetCompetitions(ctx, dedupStrings(compIDs))
			return err
		},
		func(ctx context.Context) (err error) {
			preMatch, err = s.predictions.PreMatchModelForMatches(ctx, matchIDs)
			return err
		},
	)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Match, 0, len(raws))
	for _, r := range raws {
		m := toMatchModel(r, comps, teams)
		if pp, ok := preMatch[r.ID]; ok {
			m.PreMatchPrediction = toModelPredictionModel(pp)
		}
		out = append(out, m)
	}
	return out, nil
}

// parallel runs fns concurrently and returns the first error, cancelling
// the others' context. Used for independent lookups, each of which is a
// round-trip to the cluster.
func parallel(ctx context.Context, fns ...func(context.Context) error) error {
	g, gctx := errgroup.WithContext(ctx)
	for _, f := range fns {
		g.Go(func() error { return f(gctx) })
	}
	return g.Wait()
}
