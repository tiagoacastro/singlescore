// Package prediction defines the Predictor interface and a transparent
// baseline implementation that combines a pre-match Elo rating with simple
// in-play adjustments for scoreline, elapsed time, and red-card imbalance.
//
// This is explicitly NOT a validated statistical model. It exists so the
// rest of the system has something coherent to score, replay, and
// visualise. Swap it for a trained model later by implementing Predictor.
package prediction

import (
	"math"
	"time"
)

// Snapshot is the subset of match state the baseline predictor needs.
type Snapshot struct {
	HomeElo        float64
	AwayElo        float64
	HomeScore      int
	AwayScore      int
	Minute         int // 0 for pre-match
	HomeRedCards   int
	AwayRedCards   int
	HomeAdvantage  float64 // additive Elo bump for home side; default 60
	MatchStartedAt time.Time
}

// Probabilities carries the three outcome probabilities.
type Probabilities struct {
	Home float64
	Draw float64
	Away float64
}

// Predictor is implemented by anything that can produce probabilities for
// a snapshot of match state. The interface is intentionally minimal so a
// Go routine, a Python microservice, or a stored procedure could satisfy
// it later.
type Predictor interface {
	Predict(s Snapshot) (Probabilities, error)
	Name() string
	Version() string
}

// Baseline implements Predictor with a transparent Elo-based model.
type Baseline struct{}

// NewBaseline returns a Baseline predictor.
func NewBaseline() Baseline { return Baseline{} }

// Name returns the model name.
func (Baseline) Name() string { return "elo-baseline" }

// Version returns the model version. Bump this when the formula changes
// so forecasts are grouped correctly in evaluations.
func (Baseline) Version() string { return "v0.1.0" }

// Predict combines pre-match Elo probabilities with crude in-play
// adjustments. Deliberately simple and easy to reason about.
func (Baseline) Predict(s Snapshot) (Probabilities, error) {
	ha := s.HomeAdvantage
	if ha == 0 {
		ha = 60
	}

	// Classic Elo win expectancy with home-field advantage.
	//   E_home = 1 / (1 + 10^((AwayElo - (HomeElo + ha)) / 400))
	eHome := 1.0 / (1.0 + math.Pow(10, (s.AwayElo-(s.HomeElo+ha))/400.0))
	eHome = clamp(eHome, 0.02, 0.98)

	// Pre-match draw baseline: football is draw-prone. Allocate ~26%
	// to draws when teams are evenly matched; shrink as asymmetry grows.
	asymmetry := math.Abs(eHome - 0.5) // 0 when even, 0.5 when extreme
	drawShare := 0.30 - 0.35*asymmetry
	drawShare = clamp(drawShare, 0.08, 0.32)

	// Split the remaining 1 - draw between home and away in Elo ratio.
	remaining := 1.0 - drawShare
	pHome := remaining * eHome
	pAway := remaining * (1 - eHome)
	pDraw := drawShare

	// In-play adjustments. Only apply after kick-off.
	if s.Minute > 0 {
		// Time-decay: as minutes pass, the current scoreline becomes
		// harder to overturn. We model this as the weight on the
		// current scoreline grows with minute.
		timeWeight := math.Min(1.0, float64(s.Minute)/90.0)

		// Score margin for the home team.
		margin := s.HomeScore - s.AwayScore

		// Red-card imbalance (positive = home has one more red).
		redImbalance := s.HomeRedCards - s.AwayRedCards

		// Lead swing per goal shrinks the losing side's win prob and
		// transfers mass to the leader. Draw shrinks as margin grows
		// unless the score is level.
		if margin != 0 {
			swing := 0.18 * float64(margin) * (0.4 + 0.6*timeWeight)
			pHome = clamp(pHome+swing, 0.005, 0.995)
			pAway = clamp(pAway-swing, 0.005, 0.995)
			// Shrink draw as margin grows.
			shrink := 0.05 * math.Abs(float64(margin)) * timeWeight
			pDraw = clamp(pDraw-shrink, 0.01, 0.5)
		} else {
			// Level score: boost draw mass as time elapses.
			pDraw = clamp(pDraw+0.08*timeWeight, 0.01, 0.6)
		}

		// Red cards hurt the team that received them.
		if redImbalance != 0 {
			rc := 0.08 * float64(redImbalance) * (0.5 + 0.5*timeWeight)
			pHome = clamp(pHome-rc, 0.005, 0.995)
			pAway = clamp(pAway+rc, 0.005, 0.995)
		}
	}

	// Renormalise so the three probabilities sum to 1.
	sum := pHome + pDraw + pAway
	if sum <= 0 {
		return Probabilities{Home: 1.0 / 3, Draw: 1.0 / 3, Away: 1.0 / 3}, nil
	}
	return Probabilities{
		Home: pHome / sum,
		Draw: pDraw / sum,
		Away: pAway / sum,
	}, nil
}

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
