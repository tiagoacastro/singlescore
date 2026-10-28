// Package prediction defines the Predictor interface and a transparent
// baseline implementation.
//
// The baseline is a Poisson-race model: it estimates per-team goal
// rates from the Elo gap, then computes outcome probabilities by
// summing over all plausible (home_remaining_goals, away_remaining_goals)
// pairs weighted by independent Poisson distributions. Current scoreline
// is carried in as an offset so a 1-0 at minute 80 is "home needs the
// away team to score ≥2 in the next 10 minutes to lose", which has a
// vanishingly small Poisson tail — exactly the behaviour we want.
//
// Key properties this gives us for free:
//
//   - At minute 90 with any non-zero margin, residual Poisson is zero,
//     so the leader's probability converges to 1.0 and the trailer's
//     to 0.0 automatically. No special-casing of full-time needed.
//   - Pre-match (minute 0) reduces to a Dixon-Coles-lite forecast where
//     stronger teams score more and draws are naturally more likely for
//     evenly-matched sides.
//   - A new goal shifts the current margin by 1 and immediately moves
//     mass monotonically toward the scorer, with the size of the step
//     scaling naturally with remaining time (small early, large late).
//   - Red cards reduce the carded team's residual lambda, hurting them.
//
// This is explicitly NOT a trained model — it's a transparent, testable
// baseline with sensible calibration at the endpoints.
//
// The server doesn't call it: stored predictions are written by
// scripts/ingest_season.py, whose `predict` mirrors this v0.2.0 model
// (TestBaseline_MatchesPythonPort keeps the two in step). The stored
// v0.3.0 forecasts reuse this Poisson race and add stoppage time, a
// two-sided red-card effect and better Elo inputs.
package prediction

import "math"

// Snapshot is the subset of match state the baseline predictor needs.
type Snapshot struct {
	HomeElo       float64
	AwayElo       float64
	HomeScore     int
	AwayScore     int
	Minute        int // 0 for pre-match
	HomeRedCards  int
	AwayRedCards  int
	HomeAdvantage float64 // additive Elo bump for home side; default 60
}

// Probabilities carries the three outcome probabilities.
type Probabilities struct {
	Home float64
	Draw float64
	Away float64
}

// Predictor is implemented by anything that can produce probabilities
// for a snapshot of match state. The interface is intentionally minimal
// so a Go routine, a Python microservice, or a stored procedure could
// satisfy it later.
type Predictor interface {
	Predict(s Snapshot) (Probabilities, error)
	Name() string
	Version() string
}

// Baseline implements Predictor with a transparent Elo-driven Poisson
// race.
type Baseline struct{}

// NewBaseline returns a Baseline predictor.
func NewBaseline() Baseline { return Baseline{} }

// Name returns the model name. Kept stable across formula revisions —
// Version differentiates them.
func (Baseline) Name() string { return "elo-poisson" }

// Version returns the model version. Bump this on any formula change
// so forecasts are grouped correctly in evaluations.
func (Baseline) Version() string { return "v0.2.0" }

// Tunable constants. These are deliberate magic numbers; moving them
// into config is premature until we have a tuning harness.
const (
	// avgGoalsPerTeamFullMatch is the long-run average goals scored per
	// team per 90 minutes across top-5 European leagues. Used as the
	// "neutral" rate before Elo scaling.
	avgGoalsPerTeamFullMatch = 1.35

	// eloHalfLifeForGoalRatio scales how much the Elo gap skews the two
	// teams' scoring rates: their ratio is exp(gap / this), so an
	// 800-point gap makes the stronger side score e ≈ 2.7x as often and
	// +400 about 1.65x. A smaller value overweights Elo. (The name is
	// historical; the Python port calls it ELO_HALF_LIFE.)
	eloHalfLifeForGoalRatio = 800.0

	// redCardLambdaFactor is the multiplier applied to the carded
	// team's remaining λ per red-card imbalance step. 0.72 ≈ reflects
	// the empirical goal-rate drop for 10 vs 11 at the top level.
	redCardLambdaFactor = 0.72

	// maxExtraGoalsPerSide bounds the Poisson sum. At λ ≤ 1.5, P(X>10)
	// is on the order of 1e-7 — truncation error is dwarfed by any
	// other modelling assumption we're making.
	maxExtraGoalsPerSide = 10
)

// Predict implements the Poisson-race described in the package doc.
func (Baseline) Predict(s Snapshot) (Probabilities, error) {
	ha := s.HomeAdvantage
	if ha == 0 {
		ha = 60
	}

	// Step 1: Elo-driven per-team expected goals over a full 90 min.
	//
	// Classic Dixon-Coles-style parameterisation: multiply the shared
	// league-average rate by exp(±ratio) where ratio scales with the
	// Elo gap. Keeps the geometric mean = avgGoalsPerTeamFullMatch so
	// the overall scoring rate stays realistic.
	eloGap := (s.HomeElo + ha - s.AwayElo) / eloHalfLifeForGoalRatio
	lambdaHomeFull := avgGoalsPerTeamFullMatch * math.Exp(+eloGap/2)
	lambdaAwayFull := avgGoalsPerTeamFullMatch * math.Exp(-eloGap/2)

	// Step 2: Scale to residual time. At minute 90 → 0 residual goals
	// expected → P(residual=0)=1 → outcome determined by current
	// margin alone. This is what makes the model commit at full-time.
	minute := s.Minute
	if minute < 0 {
		minute = 0
	}
	if minute > 90 {
		minute = 90
	}
	remainingShare := float64(90-minute) / 90.0

	// Step 3: Red-card penalty on the residual (not retroactive — the
	// carded team continues at a reduced rate for the remaining time).
	redImbalance := s.HomeRedCards - s.AwayRedCards
	if redImbalance > 0 {
		// Home has more reds → home residual rate drops.
		lambdaHomeFull *= math.Pow(redCardLambdaFactor, float64(redImbalance))
	} else if redImbalance < 0 {
		lambdaAwayFull *= math.Pow(redCardLambdaFactor, float64(-redImbalance))
	}

	muHome := lambdaHomeFull * remainingShare
	muAway := lambdaAwayFull * remainingShare

	// Step 4: Poisson race. Sum P(h, a) over all plausible residual
	// goal pairs; attribute mass to outcomes based on
	//   final_margin = currentMargin + (h - a).
	currentMargin := s.HomeScore - s.AwayScore

	// Pre-compute Poisson PMFs so we don't redo the exp() in the inner
	// loop. (11 * 2) exps instead of 11 * 11 * 2 = 242 ones.
	pH := poissonPMFVector(muHome)
	pA := poissonPMFVector(muAway)

	var pHome, pDraw, pAway float64
	for h := 0; h <= maxExtraGoalsPerSide; h++ {
		for a := 0; a <= maxExtraGoalsPerSide; a++ {
			p := pH[h] * pA[a]
			margin := currentMargin + (h - a)
			switch {
			case margin > 0:
				pHome += p
			case margin < 0:
				pAway += p
			default:
				pDraw += p
			}
		}
	}

	// Renormalise to compensate for the truncated tail (negligible but
	// non-zero). Also guards against the degenerate λ=0 case for
	// non-finite inputs.
	sum := pHome + pDraw + pAway
	if sum <= 0 || math.IsNaN(sum) {
		return Probabilities{Home: 1.0 / 3, Draw: 1.0 / 3, Away: 1.0 / 3}, nil
	}
	return Probabilities{
		Home: pHome / sum,
		Draw: pDraw / sum,
		Away: pAway / sum,
	}, nil
}

// factCache is a lookup of k! for k in [0, maxExtraGoalsPerSide]. More
// precise than computing via math.Lgamma at this range.
var factCache = [maxExtraGoalsPerSide + 1]float64{
	1, 1, 2, 6, 24, 120, 720, 5040, 40320, 362880, 3628800,
}

// poissonPMFVector returns the Poisson PMF for k in [0..maxExtra] in a
// single pass, caching e^-mu. Handles mu == 0 (point mass at k=0).
func poissonPMFVector(mu float64) [maxExtraGoalsPerSide + 1]float64 {
	var out [maxExtraGoalsPerSide + 1]float64
	if mu <= 0 {
		out[0] = 1
		return out
	}
	eNegMu := math.Exp(-mu)
	muPow := 1.0 // μ^k, iteratively multiplied
	for k := 0; k <= maxExtraGoalsPerSide; k++ {
		out[k] = eNegMu * muPow / factCache[k]
		muPow *= mu
	}
	return out
}
