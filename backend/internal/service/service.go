// Package service composes repositories into GraphQL-shaped responses.
//
// The service deliberately returns gqlgen model structs so resolvers can
// stay very thin. If/when we add non-GraphQL callers we can split a
// domain layer back out, but keeping conversion in one place avoids the
// double-struct tax while the API is small.
package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/evaluation"
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
	return &Service{catalog: cat, matches: m, predictions: p, ingestion: i}
}

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

	ingestRuns, err := s.ingestion.LatestPerJob(ctx, 10)
	if err != nil {
		return nil, err
	}
	ds := make([]*model.DataSourceStatus, 0, len(ingestRuns))
	for _, r := range ingestRuns {
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

	return &model.Dashboard{
		LiveMatches:             liveModels,
		RecentFinishedMatches:   finishedModels,
		RecentPredictionChanges: changes,
		ModelMetrics:            metrics,
		DataSources:             ds,
		// SystemStatus is attached by the resolver (it needs runtime deps).
	}, nil
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
		cursor := cursorFromMatch(page.Matches[i])
		edges = append(edges, &model.MatchEdge{Node: m, Cursor: cursor})
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

// Match returns a single match, fully hydrated with events / predictions.
func (s *Service) Match(ctx context.Context, id string) (*model.Match, error) {
	raw, err := s.matches.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	// Hydrate teams + competition.
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

	m := toMatchModel(*raw, compMap, teamMap)
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
	if n := len(m.ModelPredictions); n > 0 {
		m.LatestModelPrediction = m.ModelPredictions[n-1]
	}

	return m, nil
}

// -------------------- Model evaluation --------------------

// ModelEvaluation runs the evaluation metrics against all finished matches.
// When modelName or modelVersion are nil we evaluate every model present.
func (s *Service) ModelEvaluation(ctx context.Context, modelName, modelVersion *string) (*model.EvaluationReport, error) {
	finals, err := s.predictions.AllFinishedResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(finals) == 0 {
		return emptyReport(), nil
	}
	finalByID := make(map[string]repository.Outcome, len(finals))
	matchIDs := make([]string, 0, len(finals))
	for _, f := range finals {
		finalByID[f.MatchID] = f.FinalResult
		matchIDs = append(matchIDs, f.MatchID)
	}

	modelPreds, err := s.predictions.ModelForMatches(ctx, matchIDs)
	if err != nil {
		return nil, err
	}
	benchPreds, err := s.predictions.BenchmarkForMatches(ctx, matchIDs)
	if err != nil {
		return nil, err
	}

	// Pull matches + competitions for byCompetition grouping.
	finishedMatches := make([]repository.Match, 0, len(matchIDs))
	for _, id := range matchIDs {
		m, err := s.matches.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if m != nil {
			finishedMatches = append(finishedMatches, *m)
		}
	}
	compIDs := uniqueCompetitionIDs(finishedMatches)
	compMap, err := s.catalog.GetCompetitions(ctx, compIDs)
	if err != nil {
		return nil, err
	}
	compByMatch := make(map[string]string, len(finishedMatches))
	for _, m := range finishedMatches {
		compByMatch[m.ID] = m.CompetitionID
	}

	// Build the model-prediction evaluation set.
	var labeledSet []labeledPred
	var dateFrom, dateTo *time.Time
	for matchID, preds := range modelPreds {
		actual, ok := finalByID[matchID]
		if !ok {
			continue
		}
		for _, p := range preds {
			if modelName != nil && *modelName != "" && p.ModelName != *modelName {
				continue
			}
			if modelVersion != nil && *modelVersion != "" && p.ModelVersion != *modelVersion {
				continue
			}
			ep := evaluation.Prediction{
				PHome:  p.PHome,
				PDraw:  p.PDraw,
				PAway:  p.PAway,
				Actual: outcomeFromRepo(actual),
			}
			if p.MatchMinute != nil {
				ep.Minute = *p.MatchMinute
			}
			labeledSet = append(labeledSet, labeledPred{pred: ep, rep: p})
			ts := p.ForecastAt
			if dateFrom == nil || ts.Before(*dateFrom) {
				t := ts
				dateFrom = &t
			}
			if dateTo == nil || ts.After(*dateTo) {
				t := ts
				dateTo = &t
			}
		}
	}

	if len(labeledSet) == 0 {
		return emptyReport(), nil
	}

	// Aggregate per model name/version — if the caller didn't pin them we
	// pick the most-represented one for the headline summary. In practice
	// the baseline predictor is the only one for v1.
	preds := make([]evaluation.Prediction, 0, len(labeledSet))
	for _, l := range labeledSet {
		preds = append(preds, l.pred)
	}
	sum, _ := evaluation.Compute(preds)
	headName, headVersion := pickHeadlineModel(labeledSet)
	summary := &model.EvaluationSummary{
		ModelName:     headName,
		ModelVersion:  headVersion,
		SampleSize:    sum.SampleSize,
		BrierScore:    sum.BrierScore,
		LogLoss:       sum.LogLoss,
		Accuracy:      sum.Accuracy,
		DateRangeFrom: dateFrom,
		DateRangeTo:   dateTo,
	}

	// Calibration bins (10 bins).
	bins := evaluation.Calibration(preds, 10)
	calibration := make([]*model.CalibrationBin, 0, len(bins))
	for _, b := range bins {
		b := b
		calibration = append(calibration, &model.CalibrationBin{
			BinLow:            b.BinLow,
			BinHigh:           b.BinHigh,
			AvgPredicted:      b.AvgPredicted,
			ObservedFrequency: b.ObservedFrequency,
			SampleSize:        b.SampleSize,
		})
	}

	// By minute bucket.
	byBucket := map[string][]evaluation.Prediction{}
	for _, l := range labeledSet {
		b := evaluation.MinuteBucket(l.pred.Minute)
		byBucket[b] = append(byBucket[b], l.pred)
	}
	bucketOut := make([]*model.MinuteBucketMetric, 0, len(byBucket))
	for _, name := range orderedBucketNames() {
		if ps, ok := byBucket[name]; ok {
			s2, _ := evaluation.Compute(ps)
			bucketOut = append(bucketOut, &model.MinuteBucketMetric{
				MinuteBucket: name,
				BrierScore:   s2.BrierScore,
				LogLoss:      s2.LogLoss,
				SampleSize:   s2.SampleSize,
			})
		}
	}

	// By competition.
	byComp := map[string][]evaluation.Prediction{}
	for _, l := range labeledSet {
		cid, ok := compByMatch[l.rep.MatchID]
		if !ok {
			continue
		}
		byComp[cid] = append(byComp[cid], l.pred)
	}
	compOut := make([]*model.CompetitionMetric, 0, len(byComp))
	for cid, ps := range byComp {
		comp, ok := compMap[cid]
		if !ok {
			continue
		}
		s3, _ := evaluation.Compute(ps)
		compOut = append(compOut, &model.CompetitionMetric{
			Competition: toCompetitionModel(comp),
			BrierScore:  s3.BrierScore,
			LogLoss:     s3.LogLoss,
			SampleSize:  s3.SampleSize,
		})
	}
	sort.Slice(compOut, func(i, j int) bool { return compOut[i].Competition.Name < compOut[j].Competition.Name })

	// Benchmarks evaluation grouped by (provider, horizon).
	type bkey struct {
		provider string
		horizon  string
	}
	benchGroups := map[bkey][]evaluation.Prediction{}
	matchesWithBench := map[string]struct{}{}
	for matchID, bs := range benchPreds {
		actual, ok := finalByID[matchID]
		if !ok {
			continue
		}
		if len(bs) > 0 {
			matchesWithBench[matchID] = struct{}{}
		}
		for _, b := range bs {
			ep := evaluation.Prediction{
				PHome:  b.PHome,
				PDraw:  b.PDraw,
				PAway:  b.PAway,
				Actual: outcomeFromRepo(actual),
			}
			if b.MatchMinute != nil {
				ep.Minute = *b.MatchMinute
			}
			key := bkey{provider: b.Provider, horizon: b.Horizon}
			benchGroups[key] = append(benchGroups[key], ep)
		}
	}
	benchOut := make([]*model.BenchmarkEvaluationSummary, 0, len(benchGroups))
	for k, ps := range benchGroups {
		s4, _ := evaluation.Compute(ps)
		benchOut = append(benchOut, &model.BenchmarkEvaluationSummary{
			Provider:   k.provider,
			Horizon:    model.BenchmarkHorizon(k.horizon),
			SampleSize: s4.SampleSize,
			BrierScore: s4.BrierScore,
			LogLoss:    s4.LogLoss,
			Accuracy:   s4.Accuracy,
		})
	}
	sort.Slice(benchOut, func(i, j int) bool { return benchOut[i].Provider < benchOut[j].Provider })

	// Missing benchmark coverage = fraction of finished matches with 0 benchmark forecasts.
	missing := 0.0
	if len(finals) > 0 {
		missing = 1.0 - float64(len(matchesWithBench))/float64(len(finals))
		if missing < 0 {
			missing = 0
		}
		if missing > 1 {
			missing = 1
		}
	}

	return &model.EvaluationReport{
		Summary:                  summary,
		Calibration:              calibration,
		ByMinuteBucket:           bucketOut,
		ByCompetition:            compOut,
		Benchmarks:               benchOut,
		MissingBenchmarkCoverage: missing,
	}, nil
}

// -------------------- helpers --------------------

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

func (s *Service) recentPredictionChanges(ctx context.Context, limit int) ([]*model.PredictionChange, error) {
	// Pull the newest N snapshots overall and, for each match, compute the
	// delta vs the previous snapshot for that match.
	recents, err := s.predictions.RecentModelPredictions(ctx, 60)
	if err != nil {
		return nil, err
	}
	if len(recents) == 0 {
		return []*model.PredictionChange{}, nil
	}
	// Group by match id, sorted by forecast_at desc inside each group.
	byMatch := map[string][]repository.ModelPrediction{}
	for _, p := range recents {
		byMatch[p.MatchID] = append(byMatch[p.MatchID], p)
	}
	type candidate struct {
		matchID string
		latest  repository.ModelPrediction
		prev    repository.ModelPrediction
		l1      float64
	}
	candidates := make([]candidate, 0, len(byMatch))
	for mid, ps := range byMatch {
		sort.Slice(ps, func(i, j int) bool { return ps[i].ForecastAt.After(ps[j].ForecastAt) })
		if len(ps) < 2 {
			continue
		}
		latest, prev := ps[0], ps[1]
		dH := latest.PHome - prev.PHome
		dD := latest.PDraw - prev.PDraw
		dA := latest.PAway - prev.PAway
		l1 := math.Abs(dH) + math.Abs(dD) + math.Abs(dA)
		if l1 < 0.001 {
			continue
		}
		candidates = append(candidates, candidate{matchID: mid, latest: latest, prev: prev, l1: l1})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].l1 > candidates[j].l1 })
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		return []*model.PredictionChange{}, nil
	}

	// Hydrate match contexts for the chosen candidates.
	matchIDs := make([]string, 0, len(candidates))
	for _, c := range candidates {
		matchIDs = append(matchIDs, c.matchID)
	}
	raws := make([]repository.Match, 0, len(matchIDs))
	for _, id := range matchIDs {
		m, err := s.matches.GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if m != nil {
			raws = append(raws, *m)
		}
	}
	hyd, err := s.hydrateMatches(ctx, raws, false)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.Match{}
	for _, m := range hyd {
		byID[m.ID] = m
	}

	out := make([]*model.PredictionChange, 0, len(candidates))
	for _, c := range candidates {
		m, ok := byID[c.matchID]
		if !ok {
			continue
		}
		out = append(out, &model.PredictionChange{
			Match:      m,
			Prediction: toModelPredictionModel(c.latest),
			DeltaHome:  c.latest.PHome - c.prev.PHome,
			DeltaDraw:  c.latest.PDraw - c.prev.PDraw,
			DeltaAway:  c.latest.PAway - c.prev.PAway,
		})
	}
	return out, nil
}

func (s *Service) liveModelMetrics(ctx context.Context) (*model.EvaluationSummary, error) {
	finals, err := s.predictions.AllFinishedResults(ctx)
	if err != nil {
		return nil, err
	}
	if len(finals) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(finals))
	finalBy := make(map[string]repository.Outcome, len(finals))
	for _, f := range finals {
		ids = append(ids, f.MatchID)
		finalBy[f.MatchID] = f.FinalResult
	}
	latestByMatch, err := s.predictions.LatestModelForMatches(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(latestByMatch) == 0 {
		return nil, nil
	}
	var preds []evaluation.Prediction
	var modelName, modelVersion string
	var dateFrom, dateTo *time.Time
	for mid, p := range latestByMatch {
		actual, ok := finalBy[mid]
		if !ok {
			continue
		}
		preds = append(preds, evaluation.Prediction{
			PHome: p.PHome, PDraw: p.PDraw, PAway: p.PAway,
			Actual: outcomeFromRepo(actual),
		})
		if modelName == "" {
			modelName = p.ModelName
			modelVersion = p.ModelVersion
		}
		if dateFrom == nil || p.ForecastAt.Before(*dateFrom) {
			t := p.ForecastAt
			dateFrom = &t
		}
		if dateTo == nil || p.ForecastAt.After(*dateTo) {
			t := p.ForecastAt
			dateTo = &t
		}
	}
	if len(preds) == 0 {
		return nil, nil
	}
	sum, _ := evaluation.Compute(preds)
	return &model.EvaluationSummary{
		ModelName:     modelName,
		ModelVersion:  modelVersion,
		SampleSize:    sum.SampleSize,
		BrierScore:    sum.BrierScore,
		LogLoss:       sum.LogLoss,
		Accuracy:      sum.Accuracy,
		DateRangeFrom: dateFrom,
		DateRangeTo:   dateTo,
	}, nil
}

// labeledPred pairs an evaluation prediction with its raw repository row so
// grouping by model name/version or competition can be done after Compute.
type labeledPred struct {
	pred evaluation.Prediction
	rep  repository.ModelPrediction
}

func pickHeadlineModel(labeled []labeledPred) (name, version string) {
	type key struct{ n, v string }
	counts := map[key]int{}
	for _, l := range labeled {
		counts[key{n: l.rep.ModelName, v: l.rep.ModelVersion}]++
	}
	var best key
	bestN := -1
	for k, n := range counts {
		if n > bestN {
			best, bestN = k, n
		}
	}
	return best.n, best.v
}

func orderedBucketNames() []string {
	return []string{"pre-match", "0-14", "15-29", "30-44", "45-59", "60-74", "75+"}
}

func uniqueCompetitionIDs(ms []repository.Match) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if _, ok := seen[m.CompetitionID]; ok {
			continue
		}
		seen[m.CompetitionID] = struct{}{}
		out = append(out, m.CompetitionID)
	}
	return out
}

func dedupStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func outcomeFromRepo(o repository.Outcome) evaluation.Outcome {
	switch o {
	case repository.OutcomeHome:
		return evaluation.OutcomeHome
	case repository.OutcomeDraw:
		return evaluation.OutcomeDraw
	case repository.OutcomeAway:
		return evaluation.OutcomeAway
	}
	return evaluation.OutcomeDraw
}

func emptyReport() *model.EvaluationReport {
	return &model.EvaluationReport{
		Summary:                  nil,
		Calibration:              []*model.CalibrationBin{},
		ByMinuteBucket:           []*model.MinuteBucketMetric{},
		ByCompetition:            []*model.CompetitionMetric{},
		Benchmarks:               []*model.BenchmarkEvaluationSummary{},
		MissingBenchmarkCoverage: 0,
	}
}

// cursorFromMatch rebuilds the opaque cursor for a match row. We reuse
// the repository's cursor shape (base64 of RFC3339Nano|id).
func cursorFromMatch(m repository.Match) string {
	raw := m.KickoffAt.UTC().Format(time.RFC3339Nano) + "|" + m.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// ErrServiceNotReady is returned when a required collaborator is missing.
var ErrServiceNotReady = errors.New("service: not initialised")

// -------------------- model mapping --------------------

func toCompetitionModel(c repository.Competition) *model.Competition {
	return &model.Competition{
		ID:      c.ID,
		Name:    c.Name,
		Country: c.Country,
		Season:  c.Season,
		LogoURL: c.LogoURL,
	}
}

func toTeamModel(t repository.Team) *model.Team {
	elo := t.EloRating
	return &model.Team{
		ID:        t.ID,
		Name:      t.Name,
		ShortName: t.ShortName,
		Country:   t.Country,
		EloRating: &elo,
		LogoURL:   t.LogoURL,
	}
}

func toMatchModel(r repository.Match, comps map[string]repository.Competition, teams map[string]repository.Team) *model.Match {
	comp := comps[r.CompetitionID]
	home := teams[r.HomeTeamID]
	away := teams[r.AwayTeamID]
	var finalResult *model.Outcome
	if r.FinalResult != nil {
		fr := model.Outcome(string(*r.FinalResult))
		finalResult = &fr
	}
	return &model.Match{
		ID:                   r.ID,
		Competition:          toCompetitionModel(comp),
		HomeTeam:             toTeamModel(home),
		AwayTeam:             toTeamModel(away),
		KickoffAt:            r.KickoffAt,
		Status:               model.MatchStatus(string(r.Status)),
		Minute:               r.Minute,
		HomeScore:            r.HomeScore,
		AwayScore:            r.AwayScore,
		FinalResult:          finalResult,
		Provenance:           model.Provenance(string(r.Provenance)),
		Venue:                r.Venue,
		Events:               []*model.MatchEvent{},
		ModelPredictions:     []*model.ModelPrediction{},
		BenchmarkPredictions: []*model.BenchmarkPrediction{},
	}
}

func toEventModel(e repository.MatchEvent) *model.MatchEvent {
	return &model.MatchEvent{
		ID:          e.ID,
		EventType:   e.EventType,
		TeamSide:    e.TeamSide,
		Minute:      e.Minute,
		AddedMinute: e.AddedMinute,
		Description: e.Description,
		EventTime:   e.EventTime,
	}
}

func toModelPredictionModel(p repository.ModelPrediction) *model.ModelPrediction {
	return &model.ModelPrediction{
		ID:                  p.ID,
		ModelName:           p.ModelName,
		ModelVersion:        p.ModelVersion,
		ForecastAt:          p.ForecastAt,
		MatchMinute:         p.MatchMinute,
		HomeScoreAtForecast: p.HomeScoreAtFC,
		AwayScoreAtForecast: p.AwayScoreAtFC,
		PHome:               p.PHome,
		PDraw:               p.PDraw,
		PAway:               p.PAway,
		Provenance:          model.Provenance(string(p.Provenance)),
	}
}

func toBenchmarkModel(b repository.BenchmarkPrediction) *model.BenchmarkPrediction {
	return &model.BenchmarkPrediction{
		ID:          b.ID,
		Provider:    b.Provider,
		SourceType:  model.BenchmarkSourceType(strings.ToUpper(b.SourceType)),
		Horizon:     model.BenchmarkHorizon(strings.ToUpper(b.Horizon)),
		ForecastAt:  b.ForecastAt,
		MatchMinute: b.MatchMinute,
		PHome:       b.PHome,
		PDraw:       b.PDraw,
		PAway:       b.PAway,
		Provenance:  model.Provenance(string(b.Provenance)),
	}
}
