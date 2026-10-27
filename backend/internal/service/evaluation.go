package service

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/evaluation"
	"github.com/tiagocastro/singlescore/backend/internal/graph/model"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// ModelEvaluation runs the evaluation metrics against all finished
// matches. When modelName or modelVersion are nil we evaluate every
// model present and pick the most-represented one for the headline.
func (s *Service) ModelEvaluation(ctx context.Context, modelName, modelVersion *string) (*model.EvaluationReport, error) {
	finals, finalByID, matchIDs, err := s.loadFinishedMatches(ctx)
	if err != nil {
		return nil, err
	}
	if len(finals) == 0 {
		return emptyReport(), nil
	}

	modelPreds, err := s.predictions.ModelForMatches(ctx, matchIDs)
	if err != nil {
		return nil, err
	}
	benchPreds, err := s.predictions.BenchmarkForMatches(ctx, matchIDs)
	if err != nil {
		return nil, err
	}

	compByMatch, compMap, err := s.loadCompetitionsForMatches(ctx, matchIDs)
	if err != nil {
		return nil, err
	}

	// 1) Model predictions set, optionally filtered by name/version.
	labeledSet, dateFrom, dateTo := labelModelPredictions(modelPreds, finalByID, modelName, modelVersion)
	if len(labeledSet) == 0 {
		return emptyReport(), nil
	}

	// 2) Overall summary.
	summary := computeSummary(labeledSet, dateFrom, dateTo)

	// 3) Calibration bins (10 bins).
	calibration := computeCalibrationBins(labeledSet)

	// 4) By minute bucket (pre-match, 0-14, …, 75+).
	bucketOut := computeMinuteBuckets(labeledSet)

	// 5) By competition (sorted by name for stable output).
	compOut := computeByCompetition(labeledSet, compByMatch, compMap)

	// 6) Benchmarks grouped by (provider, horizon).
	benchOut, matchesWithBench := computeBenchmarkGroups(benchPreds, finalByID)
	sort.Slice(benchOut, func(i, j int) bool { return benchOut[i].Provider < benchOut[j].Provider })

	// Missing benchmark coverage = fraction of finished matches with
	// zero benchmark forecasts. Clamped to [0, 1] defensively.
	missing := 1.0 - float64(len(matchesWithBench))/float64(len(finals))
	if missing < 0 {
		missing = 0
	}
	if missing > 1 {
		missing = 1
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

// loadFinishedMatches returns the final-result list, a (match_id →
// outcome) index for O(1) lookup, and the slice of match ids.
func (s *Service) loadFinishedMatches(ctx context.Context) (
	finals []repository.FinishedMatchFinalResult,
	finalByID map[string]repository.Outcome,
	matchIDs []string,
	err error,
) {
	finals, err = s.predictions.AllFinishedResults(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	finalByID = make(map[string]repository.Outcome, len(finals))
	matchIDs = make([]string, 0, len(finals))
	for _, f := range finals {
		finalByID[f.MatchID] = f.FinalResult
		matchIDs = append(matchIDs, f.MatchID)
	}
	return
}

// loadCompetitionsForMatches resolves competition rows for a batch of
// match ids and returns both a (match_id → comp_id) index and the raw
// (comp_id → competition) map.
func (s *Service) loadCompetitionsForMatches(ctx context.Context, matchIDs []string) (
	compByMatch map[string]string,
	compMap map[string]repository.Competition,
	err error,
) {
	finishedMatches := make([]repository.Match, 0, len(matchIDs))
	for _, id := range matchIDs {
		m, mErr := s.matches.GetByID(ctx, id)
		if mErr != nil {
			return nil, nil, mErr
		}
		if m != nil {
			finishedMatches = append(finishedMatches, *m)
		}
	}
	compMap, err = s.catalog.GetCompetitions(ctx, uniqueCompetitionIDs(finishedMatches))
	if err != nil {
		return nil, nil, err
	}
	compByMatch = make(map[string]string, len(finishedMatches))
	for _, m := range finishedMatches {
		compByMatch[m.ID] = m.CompetitionID
	}
	return
}

// labelModelPredictions walks every model prediction for every finished
// match and converts matching rows into evaluation.Prediction records,
// also tracking the min/max forecast_at seen (for the summary's date
// range). Optional modelName/modelVersion restrict the set.
func labelModelPredictions(
	modelPreds map[string][]repository.ModelPrediction,
	finalByID map[string]repository.Outcome,
	modelName, modelVersion *string,
) (labeled []labeledPred, dateFrom, dateTo *time.Time) {
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
				PHome: p.PHome, PDraw: p.PDraw, PAway: p.PAway,
				Actual: outcomeFromRepo(actual),
			}
			if p.MatchMinute != nil {
				ep.Minute = *p.MatchMinute
			}
			labeled = append(labeled, labeledPred{pred: ep, rep: p})
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
	return
}

func computeSummary(labeled []labeledPred, dateFrom, dateTo *time.Time) *model.EvaluationSummary {
	preds := make([]evaluation.Prediction, 0, len(labeled))
	for _, l := range labeled {
		preds = append(preds, l.pred)
	}
	sum, _ := evaluation.Compute(preds)
	headName, headVersion := pickHeadlineModel(labeled)
	return &model.EvaluationSummary{
		ModelName:     headName,
		ModelVersion:  headVersion,
		SampleSize:    sum.SampleSize,
		BrierScore:    sum.BrierScore,
		LogLoss:       sum.LogLoss,
		Accuracy:      sum.Accuracy,
		DateRangeFrom: dateFrom,
		DateRangeTo:   dateTo,
	}
}

func computeCalibrationBins(labeled []labeledPred) []*model.CalibrationBin {
	preds := make([]evaluation.Prediction, 0, len(labeled))
	for _, l := range labeled {
		preds = append(preds, l.pred)
	}
	bins := evaluation.Calibration(preds, 10)
	out := make([]*model.CalibrationBin, 0, len(bins))
	for _, b := range bins {
		b := b
		out = append(out, &model.CalibrationBin{
			BinLow: b.BinLow, BinHigh: b.BinHigh,
			AvgPredicted:      b.AvgPredicted,
			ObservedFrequency: b.ObservedFrequency,
			SampleSize:        b.SampleSize,
		})
	}
	return out
}

func computeMinuteBuckets(labeled []labeledPred) []*model.MinuteBucketMetric {
	byBucket := map[string][]evaluation.Prediction{}
	for _, l := range labeled {
		byBucket[evaluation.MinuteBucket(l.pred.Minute)] = append(byBucket[evaluation.MinuteBucket(l.pred.Minute)], l.pred)
	}
	out := make([]*model.MinuteBucketMetric, 0, len(byBucket))
	for _, name := range orderedBucketNames() {
		ps, ok := byBucket[name]
		if !ok {
			continue
		}
		s, _ := evaluation.Compute(ps)
		out = append(out, &model.MinuteBucketMetric{
			MinuteBucket: name,
			BrierScore:   s.BrierScore,
			LogLoss:      s.LogLoss,
			SampleSize:   s.SampleSize,
		})
	}
	return out
}

func computeByCompetition(
	labeled []labeledPred,
	compByMatch map[string]string,
	compMap map[string]repository.Competition,
) []*model.CompetitionMetric {
	byComp := map[string][]evaluation.Prediction{}
	for _, l := range labeled {
		cid, ok := compByMatch[l.rep.MatchID]
		if !ok {
			continue
		}
		byComp[cid] = append(byComp[cid], l.pred)
	}
	out := make([]*model.CompetitionMetric, 0, len(byComp))
	for cid, ps := range byComp {
		comp, ok := compMap[cid]
		if !ok {
			continue
		}
		s, _ := evaluation.Compute(ps)
		out = append(out, &model.CompetitionMetric{
			Competition: toCompetitionModel(comp),
			BrierScore:  s.BrierScore,
			LogLoss:     s.LogLoss,
			SampleSize:  s.SampleSize,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Competition.Name < out[j].Competition.Name })
	return out
}

// computeBenchmarkGroups returns the grouped (provider, horizon)
// summaries plus the set of match ids that have at least one benchmark
// prediction (used by the caller to compute MissingBenchmarkCoverage).
func computeBenchmarkGroups(
	benchPreds map[string][]repository.BenchmarkPrediction,
	finalByID map[string]repository.Outcome,
) ([]*model.BenchmarkEvaluationSummary, map[string]struct{}) {
	type bkey struct{ provider, horizon string }
	groups := map[bkey][]evaluation.Prediction{}
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
				PHome: b.PHome, PDraw: b.PDraw, PAway: b.PAway,
				Actual: outcomeFromRepo(actual),
			}
			if b.MatchMinute != nil {
				ep.Minute = *b.MatchMinute
			}
			groups[bkey{b.Provider, b.Horizon}] = append(groups[bkey{b.Provider, b.Horizon}], ep)
		}
	}
	out := make([]*model.BenchmarkEvaluationSummary, 0, len(groups))
	for k, ps := range groups {
		s, _ := evaluation.Compute(ps)
		out = append(out, &model.BenchmarkEvaluationSummary{
			Provider:   k.provider,
			Horizon:    model.BenchmarkHorizon(k.horizon),
			SampleSize: s.SampleSize,
			BrierScore: s.BrierScore,
			LogLoss:    s.LogLoss,
			Accuracy:   s.Accuracy,
		})
	}
	return out, matchesWithBench
}

// -------------------- recent-changes + live metrics --------------------

// recentPredictionChanges returns the N matches whose latest prediction
// differs most (L1 norm) from the prior one. Powers the "biggest moves"
// section of the dashboard.
func (s *Service) recentPredictionChanges(ctx context.Context, limit int) ([]*model.PredictionChange, error) {
	recents, err := s.predictions.RecentModelPredictions(ctx, 60)
	if err != nil {
		return nil, err
	}
	if len(recents) == 0 {
		return []*model.PredictionChange{}, nil
	}

	// Group by match, newest first.
	byMatch := map[string][]repository.ModelPrediction{}
	for _, p := range recents {
		byMatch[p.MatchID] = append(byMatch[p.MatchID], p)
	}
	type candidate struct {
		matchID       string
		latest, prev  repository.ModelPrediction
		l1            float64
	}
	candidates := make([]candidate, 0, len(byMatch))
	for mid, ps := range byMatch {
		sort.Slice(ps, func(i, j int) bool { return ps[i].ForecastAt.After(ps[j].ForecastAt) })
		if len(ps) < 2 {
			continue
		}
		latest, prev := ps[0], ps[1]
		l1 := math.Abs(latest.PHome-prev.PHome) +
			math.Abs(latest.PDraw-prev.PDraw) +
			math.Abs(latest.PAway-prev.PAway)
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
	raws := make([]repository.Match, 0, len(candidates))
	for _, c := range candidates {
		m, err := s.matches.GetByID(ctx, c.matchID)
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

// liveModelMetrics computes a Brier/log-loss summary over the LATEST
// prediction per finished match, used by the dashboard mini-metrics
// panel. Returns nil when there's nothing to score.
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

// -------------------- evaluation helpers --------------------

// labeledPred pairs an evaluation.Prediction with its raw repository
// row so grouping by model name/version or competition can be done
// after Compute.
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
