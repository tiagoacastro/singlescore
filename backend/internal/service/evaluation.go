package service

import (
	"context"
	"sort"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/evaluation"
	"github.com/tiagocastro/singlescore/backend/internal/graph/model"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// ModelEvaluation scores the model against every finished match.
//
// Full-time snapshots are excluded everywhere: they're written after the
// final whistle and put ~100% on the known result. The headline,
// calibration and per-competition rows score the pre-match prediction
// (comparable with the pre-match market benchmarks and the dashboard);
// the minute buckets cover the in-play snapshots. modelName and
// modelVersion optionally restrict which predictions are scored.
func (s *Service) ModelEvaluation(ctx context.Context, modelName, modelVersion *string) (*model.EvaluationReport, error) {
	key := deref(modelName) + "|" + deref(modelVersion)
	s.evalMu.Lock()
	cached, ok := s.evalCache[key]
	s.evalMu.Unlock()
	if ok && time.Since(cached.at) < evaluationCacheTTL {
		return cached.report, nil
	}
	report, err := s.computeEvaluation(ctx, modelName, modelVersion)
	if err != nil {
		return nil, err
	}
	s.evalMu.Lock()
	s.evalCache[key] = cachedReport{report: report, at: time.Now()}
	s.evalMu.Unlock()
	return report, nil
}

func (s *Service) computeEvaluation(ctx context.Context, modelName, modelVersion *string) (*model.EvaluationReport, error) {
	finished, err := s.loadFinished(ctx)
	if err != nil {
		return nil, err
	}
	if len(finished.ids) == 0 {
		return emptyReport(), nil
	}
	var (
		modelPreds map[string][]repository.ModelPrediction
		benchPreds map[string][]repository.BenchmarkPrediction
		comps      map[string]repository.Competition
		byMinute   []repository.MinuteAccuracy
	)
	err = parallel(ctx,
		func(ctx context.Context) (err error) {
			byMinute, err = s.predictions.AccuracyByMinute(ctx)
			return err
		},
		func(ctx context.Context) (err error) {
			modelPreds, err = s.predictions.ModelForMatches(ctx, finished.ids)
			return err
		},
		func(ctx context.Context) (err error) {
			benchPreds, err = s.predictions.BenchmarkForMatches(ctx, finished.ids)
			return err
		},
		func(ctx context.Context) (err error) {
			comps, err = s.catalog.GetCompetitions(ctx, dedupStrings(finished.competitionIDs()))
			return err
		},
	)
	if err != nil {
		return nil, err
	}

	scored := scoreModelPredictions(modelPreds, finished.outcome, deref(modelName), deref(modelVersion))
	if len(scored) == 0 {
		return emptyReport(), nil
	}
	var preMatch []scoredPred
	for _, p := range scored {
		if p.row.MatchMinute == nil {
			preMatch = append(preMatch, p)
		}
	}

	benchmarks, covered := computeBenchmarkGroups(benchPreds, finished.outcome)
	return &model.EvaluationReport{
		Summary:                  computeSummary(preMatch),
		Calibration:              computeCalibrationBins(preMatch),
		ByMinuteBucket:           computeMinuteBuckets(scored),
		ByMinute:                 toMinuteAccuracy(byMinute),
		ByCompetition:            computeByCompetition(preMatch, finished, comps, benchPreds),
		Benchmarks:               benchmarks,
		MissingBenchmarkCoverage: 1 - float64(covered)/float64(len(finished.ids)),
	}, nil
}

// preMatchModelMetrics is the dashboard's headline: the evaluation
// summary over the pre-match prediction of each finished match. Nil when
// there's nothing to score.
func (s *Service) preMatchModelMetrics(ctx context.Context) (*model.EvaluationSummary, error) {
	finished, err := s.loadFinished(ctx)
	if err != nil || len(finished.ids) == 0 {
		return nil, err
	}
	byMatch, err := s.predictions.PreMatchModelForMatches(ctx, finished.ids)
	if err != nil {
		return nil, err
	}
	scored := make([]scoredPred, 0, len(byMatch))
	for id, p := range byMatch {
		if actual, ok := finished.outcome[id]; ok {
			scored = append(scored, newScoredPred(p, actual))
		}
	}
	return computeSummary(scored), nil
}

// finishedSet indexes the finished matches being evaluated.
type finishedSet struct {
	ids         []string
	outcome     map[string]repository.Outcome // match id -> result
	competition map[string]string             // match id -> competition id
}

func (f finishedSet) competitionIDs() []string {
	out := make([]string, 0, len(f.competition))
	for _, cid := range f.competition {
		out = append(out, cid)
	}
	return out
}

func (s *Service) loadFinished(ctx context.Context) (finishedSet, error) {
	rows, err := s.matches.Finished(ctx)
	if err != nil {
		return finishedSet{}, err
	}
	f := finishedSet{
		ids:         make([]string, 0, len(rows)),
		outcome:     make(map[string]repository.Outcome, len(rows)),
		competition: make(map[string]string, len(rows)),
	}
	for _, r := range rows {
		f.ids = append(f.ids, r.ID)
		f.outcome[r.ID] = r.FinalResult
		f.competition[r.ID] = r.CompetitionID
	}
	return f, nil
}

// -------------------- scoring --------------------

// scoredPred pairs an evaluation.Prediction with the row it came from,
// so results can be grouped by model, minute or competition.
type scoredPred struct {
	pred evaluation.Prediction
	row  repository.ModelPrediction
}

func newScoredPred(p repository.ModelPrediction, actual repository.Outcome) scoredPred {
	ep := evaluation.Prediction{PHome: p.PHome, PDraw: p.PDraw, PAway: p.PAway, Actual: outcomeFromRepo(actual)}
	if p.MatchMinute != nil {
		ep.Minute = *p.MatchMinute
	}
	return scoredPred{pred: ep, row: p}
}

// scoreModelPredictions labels every forecast of a finished match with
// its result, skipping full-time snapshots (minute >= 90) and, when set,
// other model names / versions.
func scoreModelPredictions(
	byMatch map[string][]repository.ModelPrediction,
	outcome map[string]repository.Outcome,
	modelName, modelVersion string,
) []scoredPred {
	var out []scoredPred
	for matchID, preds := range byMatch {
		actual, ok := outcome[matchID]
		if !ok {
			continue
		}
		for _, p := range preds {
			if (p.MatchMinute != nil && *p.MatchMinute >= 90) ||
				(modelName != "" && p.ModelName != modelName) ||
				(modelVersion != "" && p.ModelVersion != modelVersion) {
				continue
			}
			out = append(out, newScoredPred(p, actual))
		}
	}
	return out
}

func predictionsOf(scored []scoredPred) []evaluation.Prediction {
	out := make([]evaluation.Prediction, len(scored))
	for i, s := range scored {
		out[i] = s.pred
	}
	return out
}

// computeSummary is the headline metrics block, labelled with the most
// represented model and the forecast date range. Nil when empty.
func computeSummary(scored []scoredPred) *model.EvaluationSummary {
	if len(scored) == 0 {
		return nil
	}
	type modelKey struct{ name, version string }
	counts := map[modelKey]int{}
	var head modelKey
	var from, to time.Time
	for i, s := range scored {
		k := modelKey{s.row.ModelName, s.row.ModelVersion}
		counts[k]++
		if counts[k] > counts[head] {
			head = k
		}
		if t := s.row.ForecastAt; i == 0 || t.Before(from) {
			from = t
		}
		if t := s.row.ForecastAt; i == 0 || t.After(to) {
			to = t
		}
	}
	sum, _ := evaluation.Compute(predictionsOf(scored))
	return &model.EvaluationSummary{
		ModelName:     head.name,
		ModelVersion:  head.version,
		SampleSize:    sum.SampleSize,
		BrierScore:    sum.BrierScore,
		LogLoss:       sum.LogLoss,
		Accuracy:      sum.Accuracy,
		DateRangeFrom: &from,
		DateRangeTo:   &to,
	}
}

func computeCalibrationBins(scored []scoredPred) []*model.CalibrationBin {
	bins := evaluation.Calibration(predictionsOf(scored), 10)
	out := make([]*model.CalibrationBin, 0, len(bins))
	for _, b := range bins {
		out = append(out, &model.CalibrationBin{
			BinLow:            b.BinLow,
			BinHigh:           b.BinHigh,
			AvgPredicted:      b.AvgPredicted,
			ObservedFrequency: b.ObservedFrequency,
			SampleSize:        b.SampleSize,
		})
	}
	return out
}

func computeMinuteBuckets(scored []scoredPred) []*model.MinuteBucketMetric {
	byBucket := map[string][]evaluation.Prediction{}
	for _, s := range scored {
		b := evaluation.MinuteBucket(s.pred.Minute)
		byBucket[b] = append(byBucket[b], s.pred)
	}
	out := make([]*model.MinuteBucketMetric, 0, len(byBucket))
	for _, name := range evaluation.MinuteBuckets {
		ps, ok := byBucket[name]
		if !ok {
			continue
		}
		sum, _ := evaluation.Compute(ps)
		out = append(out, &model.MinuteBucketMetric{
			MinuteBucket: name,
			BrierScore:   sum.BrierScore,
			LogLoss:      sum.LogLoss,
			SampleSize:   sum.SampleSize,
		})
	}
	return out
}

// computeByCompetition scores the (pre-match) predictions per
// competition, next to the market's pre-match odds on the same matches.
func computeByCompetition(
	scored []scoredPred, finished finishedSet, comps map[string]repository.Competition,
	benchByMatch map[string][]repository.BenchmarkPrediction,
) []*model.CompetitionMetric {
	modelBy := map[string][]evaluation.Prediction{}
	marketBy := map[string][]evaluation.Prediction{}
	for _, s := range scored {
		cid, ok := finished.competition[s.row.MatchID]
		if !ok {
			continue
		}
		modelBy[cid] = append(modelBy[cid], s.pred)
		if b, ok := preMatchMarket(benchByMatch[s.row.MatchID]); ok {
			marketBy[cid] = append(marketBy[cid], evaluation.Prediction{
				PHome: b.PHome, PDraw: b.PDraw, PAway: b.PAway, Actual: s.pred.Actual,
			})
		}
	}
	out := make([]*model.CompetitionMetric, 0, len(modelBy))
	for cid, ps := range modelBy {
		comp, ok := comps[cid]
		if !ok {
			continue
		}
		sum, _ := evaluation.Compute(ps)
		m := &model.CompetitionMetric{
			Competition: toCompetitionModel(comp),
			BrierScore:  sum.BrierScore,
			LogLoss:     sum.LogLoss,
			Accuracy:    sum.Accuracy,
			SampleSize:  sum.SampleSize,
		}
		if mk := marketBy[cid]; len(mk) > 0 {
			ms, _ := evaluation.Compute(mk)
			m.MarketBrierScore, m.MarketLogLoss = &ms.BrierScore, &ms.LogLoss
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Competition, out[j].Competition
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return deref(a.Season) > deref(b.Season) // newest season first
	})
	return out
}

// preMatchMarket picks a match's pre-match market-odds benchmark.
func preMatchMarket(bs []repository.BenchmarkPrediction) (repository.BenchmarkPrediction, bool) {
	for _, b := range bs {
		if b.SourceType == "MARKET_ODDS" && b.Horizon == "PRE_MATCH" {
			return b, true
		}
	}
	return repository.BenchmarkPrediction{}, false
}

// computeBenchmarkGroups scores benchmark forecasts grouped by
// (provider, horizon), and counts the finished matches that have at
// least one benchmark.
func computeBenchmarkGroups(
	byMatch map[string][]repository.BenchmarkPrediction, outcome map[string]repository.Outcome,
) (out []*model.BenchmarkEvaluationSummary, covered int) {
	type groupKey struct{ provider, horizon string }
	groups := map[groupKey][]evaluation.Prediction{}
	for matchID, bs := range byMatch {
		actual, ok := outcome[matchID]
		if !ok || len(bs) == 0 {
			continue
		}
		covered++
		for _, b := range bs {
			ep := evaluation.Prediction{PHome: b.PHome, PDraw: b.PDraw, PAway: b.PAway, Actual: outcomeFromRepo(actual)}
			if b.MatchMinute != nil {
				ep.Minute = *b.MatchMinute
			}
			k := groupKey{b.Provider, b.Horizon}
			groups[k] = append(groups[k], ep)
		}
	}
	out = make([]*model.BenchmarkEvaluationSummary, 0, len(groups))
	for k, ps := range groups {
		sum, _ := evaluation.Compute(ps)
		out = append(out, &model.BenchmarkEvaluationSummary{
			Provider:   k.provider,
			Horizon:    model.BenchmarkHorizon(k.horizon),
			SampleSize: sum.SampleSize,
			BrierScore: sum.BrierScore,
			LogLoss:    sum.LogLoss,
			Accuracy:   sum.Accuracy,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Horizon < out[j].Horizon
	})
	return out, covered
}

// -------------------- helpers --------------------

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func dedupStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; s == "" || ok {
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
	case repository.OutcomeAway:
		return evaluation.OutcomeAway
	}
	return evaluation.OutcomeDraw
}

func toMinuteAccuracy(rows []repository.MinuteAccuracy) []*model.MinuteAccuracy {
	out := make([]*model.MinuteAccuracy, 0, len(rows))
	for _, r := range rows {
		n := float64(r.SampleSize)
		out = append(out, &model.MinuteAccuracy{
			Minute:             r.Minute,
			SampleSize:         r.SampleSize,
			Accuracy:           float64(r.Correct) / n,
			AsItStandsAccuracy: float64(r.AsItStands) / n,
			BrierScore:         r.BrierSum / n,
		})
	}
	return out
}

func emptyReport() *model.EvaluationReport {
	return &model.EvaluationReport{
		Calibration:    []*model.CalibrationBin{},
		ByMinuteBucket: []*model.MinuteBucketMetric{},
		ByMinute:       []*model.MinuteAccuracy{},
		ByCompetition:  []*model.CompetitionMetric{},
		Benchmarks:     []*model.BenchmarkEvaluationSummary{},
	}
}
