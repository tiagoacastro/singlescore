package ingestion

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/prediction"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// finishedTimelineStepMin controls the density of the retrospective
// probability-evolution snapshots we generate for FINISHED matches.
// Every 5 minutes yields 19 points per match (0, 5, …, 85, 90) which
// renders as a smooth curve without an absurd row count.
const finishedTimelineStepMin = 5

// Prediction origin labels — short tokens stored in features_json so
// downstream consumers can tell what intent produced each snapshot.
const (
	originPreMatch         = "pre-match"
	originHalfTime         = "half-time"
	originFullTime         = "full-time"
	originInPlay           = "in-play"
	originReconstructed5m  = "reconstructed-5min"
	originGoalStep         = "goal-step"
	forecastAtKickoffShift = -1 * time.Hour // pre-match forecasts are back-dated so they sort before in-play rows
)

// eloInputs bundles per-team Elo + home advantage so buildPrediction
// doesn't take six parameters.
type eloInputs struct {
	home          float64
	away          float64
	homeAdvantage float64
}

// buildPrediction runs the baseline predictor at (minute, scoreline) and
// returns a ModelPrediction row keyed deterministically so repeat syncs
// upsert in place.
//
// NOTE: we deliberately do NOT short-circuit the predictor at full-time
// to force 1.0 on the actual winner. The chart's job is to show the
// MODEL's predictions over time — overriding with ground truth would
// hide the model's actual confidence and inflate its Brier score. The
// elo-baseline's inability to commit to 100% at minute 90 is a known
// limitation to be fixed by a better model, not by hard-coding.
func (s *Sync) buildPrediction(matchID string, fx Fixture, elo eloInputs, minute, homeScore, awayScore int, forecastAt time.Time, origin string) (repository.ModelPrediction, error) {
	snap := prediction.Snapshot{
		HomeElo:        elo.home,
		AwayElo:        elo.away,
		HomeScore:      homeScore,
		AwayScore:      awayScore,
		Minute:         minute,
		HomeAdvantage:  elo.homeAdvantage,
		MatchStartedAt: fx.KickoffAt,
	}
	probs, err := s.deps.Predictor.Predict(snap)
	if err != nil {
		return repository.ModelPrediction{}, err
	}
	featBytes, _ := json.Marshal(map[string]any{
		"home_elo":        elo.home,
		"away_elo":        elo.away,
		"minute":          minute,
		"home_score":      homeScore,
		"away_score":      awayScore,
		"home_advantage":  elo.homeAdvantage,
		"model":           s.deps.Predictor.Name(),
		"model_version":   s.deps.Predictor.Version(),
		"source_provider": s.provider.Name(),
		"origin":          origin,
	})
	feats := string(featBytes)
	var minutePtr *int
	if minute > 0 {
		m := minute
		minutePtr = &m
	}
	// Deterministic id so re-syncs replace rather than duplicate: one
	// row per (match, model, model_version, minute, origin). Pre-match
	// (minute=0, origin=pre-match) is idempotent across the match's
	// lifetime; in-play rows are keyed on their minute so each unique
	// in-play minute gets its own row.
	predID := deterministicID("prediction",
		fmt.Sprintf("%s|%s|%s|%d|%s", matchID, s.deps.Predictor.Name(), s.deps.Predictor.Version(), minute, origin))
	// Pre-match forecasts are back-dated to kickoff - 1h so the
	// evaluation layer can order them ahead of in-play snapshots
	// regardless of when we actually generated them.
	fa := forecastAt
	if origin == originPreMatch {
		fa = fx.KickoffAt.Add(forecastAtKickoffShift).UTC().Round(time.Microsecond)
	}
	return repository.ModelPrediction{
		ID:            predID,
		MatchID:       matchID,
		ModelName:     s.deps.Predictor.Name(),
		ModelVersion:  s.deps.Predictor.Version(),
		ForecastAt:    fa,
		MatchMinute:   minutePtr,
		HomeScoreAtFC: homeScore,
		AwayScoreAtFC: awayScore,
		PHome:         probs.Home,
		PDraw:         probs.Draw,
		PAway:         probs.Away,
		FeaturesJSON:  &feats,
		Provenance:    repository.ProvenanceLive,
	}, nil
}

// buildMatchPredictions returns all prediction rows for a single match
// according to the current policy:
//
//   - SCHEDULED / POSTPONED / CANCELLED: one pre-match snapshot.
//   - LIVE / HALFTIME: pre-match + optional HT (if HT reached) + an
//     in-play snapshot at the current minute+score.
//   - FINISHED: a dense 5-minute retrospective timeline from kickoff
//     to full-time (19 points), using "last known scoreline" between
//     observations because the free-tier feed doesn't give per-goal
//     timings. Hard-observation minutes (0, 45, 90) keep their
//     specific origin labels so repeated syncs upsert in place.
func (s *Sync) buildMatchPredictions(matchID string, fx Fixture, elo eloInputs, forecastAt time.Time) ([]repository.ModelPrediction, []string) {
	ctx := newPredictionCtx(s, matchID, fx, elo, forecastAt)
	if fx.Status == repository.StatusFinished {
		ctx.appendFinishedApproxTimeline()
		return ctx.rows, ctx.errs
	}
	// Live / scheduled: pre-match, optional HT, optional in-play.
	ctx.add(0, 0, 0, originPreMatch)
	if fx.HalfTimeHome != nil && fx.HalfTimeAway != nil {
		ctx.add(45, *fx.HalfTimeHome, *fx.HalfTimeAway, originHalfTime)
	}
	if fx.Status == repository.StatusLive || fx.Status == repository.StatusHalftime {
		ctx.add(minuteOrZero(fx.Minute), fx.HomeScore, fx.AwayScore, originInPlay)
	}
	return ctx.rows, ctx.errs
}

// buildFinishedTimelineWithEvents produces the same shape as the
// FINISHED-match branch of buildMatchPredictions but uses actual goal
// timings instead of the HT/FT approximation.
//
// Sampling strategy:
//   - Pre-match (minute 0, 0-0, origin=pre-match)
//   - 5-minute grid at 5, 10, ..., 85 (scoreline derived from events)
//   - One extra snapshot at each goal minute so the chart shows a
//     sharp step at the real event time, not only at the next grid
//     boundary.
//   - Full-time (minute 90, FT scoreline, origin=full-time)
func (s *Sync) buildFinishedTimelineWithEvents(matchID string, fx Fixture, elo eloInputs, events []repository.MatchEvent, forecastAt time.Time) ([]repository.ModelPrediction, []string) {
	ctx := newPredictionCtx(s, matchID, fx, elo, forecastAt)
	scoreAt := func(upToMinute int) (home, away int) {
		for _, ev := range events {
			if ev.EventType != "GOAL" || ev.Minute == nil || *ev.Minute > upToMinute {
				continue
			}
			if ev.TeamSide != nil {
				switch *ev.TeamSide {
				case "HOME":
					home++
				case "AWAY":
					away++
				}
			}
		}
		return home, away
	}

	// 5-min grid.
	for minute := 0; minute <= 90; minute += finishedTimelineStepMin {
		switch minute {
		case 0:
			ctx.add(0, 0, 0, originPreMatch)
		case 90:
			ctx.add(90, fx.HomeScore, fx.AwayScore, originFullTime)
		default:
			hs, as := scoreAt(minute)
			origin := originReconstructed5m
			if minute == 45 {
				origin = originHalfTime
			}
			ctx.add(minute, hs, as, origin)
		}
	}

	// Extra snapshots at each goal minute for sharp step alignment.
	seen := map[int]bool{}
	for _, ev := range events {
		if ev.EventType != "GOAL" || ev.Minute == nil {
			continue
		}
		m := *ev.Minute
		if m <= 0 || m >= 90 || m%finishedTimelineStepMin == 0 || seen[m] {
			continue
		}
		seen[m] = true
		hs, as := scoreAt(m)
		ctx.add(m, hs, as, originGoalStep)
	}
	return ctx.rows, ctx.errs
}

// predictionCtx is a tiny accumulator shared by the predictions-build
// helpers so each sample point is just one call to .add().
type predictionCtx struct {
	s          *Sync
	matchID    string
	fx         Fixture
	elo        eloInputs
	forecastAt time.Time
	rows       []repository.ModelPrediction
	errs       []string
}

func newPredictionCtx(s *Sync, matchID string, fx Fixture, elo eloInputs, forecastAt time.Time) *predictionCtx {
	return &predictionCtx{
		s:          s,
		matchID:    matchID,
		fx:         fx,
		elo:        elo,
		forecastAt: forecastAt,
	}
}

func (c *predictionCtx) add(minute, hs, as int, origin string) {
	row, err := c.s.buildPrediction(c.matchID, c.fx, c.elo, minute, hs, as, c.forecastAt, origin)
	if err != nil {
		c.errs = append(c.errs, fmt.Sprintf("predict %s %s@%d: %v", c.fx.ExternalMatchID, origin, minute, err))
		return
	}
	c.rows = append(c.rows, row)
}

// appendFinishedApproxTimeline writes the 5-minute retrospective grid
// for a FINISHED match using the HT/FT approximation (goal timings not
// yet known). Overwritten later by RebuildFinishedTimelineFromEvents
// once api-football per-event data is in.
func (c *predictionCtx) appendFinishedApproxTimeline() {
	fx := c.fx
	htKnown := fx.HalfTimeHome != nil && fx.HalfTimeAway != nil
	for minute := 0; minute <= 90; minute += finishedTimelineStepMin {
		var hs, as int
		var origin string
		switch {
		case minute == 0:
			hs, as, origin = 0, 0, originPreMatch
		case minute == 90:
			hs, as, origin = fx.HomeScore, fx.AwayScore, originFullTime
		case minute == 45 && htKnown:
			hs, as, origin = *fx.HalfTimeHome, *fx.HalfTimeAway, originHalfTime
		case minute < 45 || !htKnown:
			hs, as, origin = 0, 0, originReconstructed5m
		default:
			hs, as, origin = *fx.HalfTimeHome, *fx.HalfTimeAway, originReconstructed5m
		}
		c.add(minute, hs, as, origin)
	}
}
