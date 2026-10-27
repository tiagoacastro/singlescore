// Package evaluation implements multiclass metrics used to score football
// match predictions: Brier score, log loss, accuracy, and calibration bins.
//
// The three predicted probabilities (home, draw, away) are expected to be
// in [0, 1] and sum to approximately 1. Callers are expected to normalize
// before scoring; this package guards against pathological inputs with
// clamps and returns an error when inputs are invalid.
package evaluation

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// Outcome enumerates the three mutually-exclusive match outcomes.
type Outcome int

const (
	OutcomeHome Outcome = iota
	OutcomeDraw
	OutcomeAway
)

// Prediction is a single forecast for one match.
type Prediction struct {
	PHome float64
	PDraw float64
	PAway float64
	// Actual is the realised outcome for this match.
	Actual Outcome
	// Minute is the match minute at which the forecast was produced
	// (optional; used for bucketed reports).
	Minute int
}

// Summary is an aggregate evaluation of many predictions.
type Summary struct {
	SampleSize int
	BrierScore float64 // mean multiclass Brier (lower is better)
	LogLoss    float64 // mean multiclass log loss (lower is better)
	Accuracy   float64 // argmax == actual
	Correct    int
}

const (
	// logLossEps clamps probabilities away from 0/1 to avoid Inf log loss.
	logLossEps = 1e-15
	// probSumTolerance is how far the three probabilities may drift from 1.
	probSumTolerance = 0.02
)

// ErrInvalidPrediction is returned when a prediction fails basic validation.
var ErrInvalidPrediction = errors.New("evaluation: invalid prediction")

// Validate checks that the three probabilities are in [0, 1] and sum to ~1.
func (p Prediction) Validate() error {
	for name, v := range map[string]float64{"p_home": p.PHome, "p_draw": p.PDraw, "p_away": p.PAway} {
		if math.IsNaN(v) || v < 0 || v > 1 {
			return fmt.Errorf("%w: %s=%v out of [0,1]", ErrInvalidPrediction, name, v)
		}
	}
	sum := p.PHome + p.PDraw + p.PAway
	if math.Abs(sum-1.0) > probSumTolerance {
		return fmt.Errorf("%w: probabilities sum to %v, expected ~1", ErrInvalidPrediction, sum)
	}
	switch p.Actual {
	case OutcomeHome, OutcomeDraw, OutcomeAway:
	default:
		return fmt.Errorf("%w: actual outcome %d is not a valid Outcome", ErrInvalidPrediction, p.Actual)
	}
	return nil
}

// probsNormalized returns the probabilities normalized to sum to 1.
func (p Prediction) probsNormalized() (float64, float64, float64) {
	sum := p.PHome + p.PDraw + p.PAway
	if sum <= 0 {
		return 1.0 / 3, 1.0 / 3, 1.0 / 3
	}
	return p.PHome / sum, p.PDraw / sum, p.PAway / sum
}

// Brier returns the single-prediction multiclass Brier score, equal to the
// sum over the three one-hot encoded classes of (p - y)^2. Range: [0, 2].
func Brier(p Prediction) float64 {
	ph, pd, pa := p.probsNormalized()
	yh, yd, ya := 0.0, 0.0, 0.0
	switch p.Actual {
	case OutcomeHome:
		yh = 1
	case OutcomeDraw:
		yd = 1
	case OutcomeAway:
		ya = 1
	}
	return sq(ph-yh) + sq(pd-yd) + sq(pa-ya)
}

// LogLoss returns -log(prob_of_actual) with clamping to avoid Inf.
func LogLoss(p Prediction) float64 {
	ph, pd, pa := p.probsNormalized()
	var pActual float64
	switch p.Actual {
	case OutcomeHome:
		pActual = ph
	case OutcomeDraw:
		pActual = pd
	case OutcomeAway:
		pActual = pa
	}
	pActual = math.Max(logLossEps, math.Min(1-logLossEps, pActual))
	return -math.Log(pActual)
}

// Argmax returns the predicted class, breaking ties in favour of HOME,
// then DRAW, then AWAY (deterministic).
func Argmax(p Prediction) Outcome {
	ph, pd, pa := p.probsNormalized()
	best := OutcomeHome
	bestV := ph
	if pd > bestV {
		best = OutcomeDraw
		bestV = pd
	}
	if pa > bestV {
		best = OutcomeAway
	}
	return best
}

// Compute returns an aggregate Summary. Invalid predictions are skipped
// and counted via the returned skipped count.
func Compute(preds []Prediction) (Summary, int) {
	var s Summary
	var totalBrier, totalLog float64
	skipped := 0

	for _, p := range preds {
		if err := p.Validate(); err != nil {
			skipped++
			continue
		}
		totalBrier += Brier(p)
		totalLog += LogLoss(p)
		if Argmax(p) == p.Actual {
			s.Correct++
		}
		s.SampleSize++
	}
	if s.SampleSize == 0 {
		return s, skipped
	}
	s.BrierScore = totalBrier / float64(s.SampleSize)
	s.LogLoss = totalLog / float64(s.SampleSize)
	s.Accuracy = float64(s.Correct) / float64(s.SampleSize)
	return s, skipped
}

// CalibrationBin reports empirical calibration for predictions whose
// probability for the actual class falls in the bin [BinLow, BinHigh).
type CalibrationBin struct {
	BinLow            float64
	BinHigh           float64
	AvgPredicted      float64
	ObservedFrequency float64
	SampleSize        int
}

// Calibration returns a reliability diagram: predictions are grouped by
// the probability assigned to the predicted class (argmax), and the
// observed frequency is the fraction of those predictions where the
// predicted class matched the actual outcome. Perfect calibration lies
// on the diagonal (avgPredicted == observedFrequency).
//
// Default is 10 equal-width bins over [0, 1].
func Calibration(preds []Prediction, nBins int) []CalibrationBin {
	if nBins <= 0 {
		nBins = 10
	}
	bins := make([]CalibrationBin, nBins)
	for i := range bins {
		lo := float64(i) / float64(nBins)
		hi := float64(i+1) / float64(nBins)
		bins[i] = CalibrationBin{BinLow: lo, BinHigh: hi}
	}

	type agg struct {
		sumPred, sumHit float64
		n               int
	}
	a := make([]agg, nBins)

	for _, p := range preds {
		if err := p.Validate(); err != nil {
			continue
		}
		ph, pd, pa := p.probsNormalized()
		// Predicted class = argmax; its probability drives the bin.
		pPred := ph
		predClass := OutcomeHome
		if pd > pPred {
			pPred, predClass = pd, OutcomeDraw
		}
		if pa > pPred {
			pPred, predClass = pa, OutcomeAway
		}
		hit := 0.0
		if predClass == p.Actual {
			hit = 1.0
		}
		// Bin index: clamp pPred == 1.0 into the last bin.
		idx := int(pPred * float64(nBins))
		if idx >= nBins {
			idx = nBins - 1
		}
		a[idx].sumPred += pPred
		a[idx].sumHit += hit
		a[idx].n++
	}
	for i := range bins {
		if a[i].n > 0 {
			bins[i].AvgPredicted = a[i].sumPred / float64(a[i].n)
			bins[i].ObservedFrequency = a[i].sumHit / float64(a[i].n)
			bins[i].SampleSize = a[i].n
		}
	}
	// Sorted by bin low ascending (already true, but be defensive).
	sort.Slice(bins, func(i, j int) bool { return bins[i].BinLow < bins[j].BinLow })
	return bins
}

// MinuteBuckets lists MinuteBucket's labels in match order.
var MinuteBuckets = []string{"pre-match", "0-14", "15-29", "30-44", "45-59", "60-74", "75+"}

// MinuteBucket returns a coarse bucket name for a forecast minute.
func MinuteBucket(minute int) string {
	switch {
	case minute <= 0:
		return "pre-match"
	case minute < 15:
		return "0-14"
	case minute < 30:
		return "15-29"
	case minute < 45:
		return "30-44"
	case minute < 60:
		return "45-59"
	case minute < 75:
		return "60-74"
	default:
		return "75+"
	}
}

func sq(x float64) float64 { return x * x }
