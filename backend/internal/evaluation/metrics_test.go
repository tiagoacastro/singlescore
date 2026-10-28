package evaluation

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		p       Prediction
		wantErr bool
	}{
		{"ok", Prediction{PHome: 0.5, PDraw: 0.3, PAway: 0.2, Actual: OutcomeHome}, false},
		{"nan", Prediction{PHome: math.NaN(), PDraw: 0.5, PAway: 0.5, Actual: OutcomeDraw}, true},
		{"negative", Prediction{PHome: -0.1, PDraw: 0.5, PAway: 0.6, Actual: OutcomeHome}, true},
		{"sum off", Prediction{PHome: 0.3, PDraw: 0.3, PAway: 0.3, Actual: OutcomeHome}, true},
		{"invalid outcome", Prediction{PHome: 0.4, PDraw: 0.3, PAway: 0.3, Actual: 99}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestBrier_PerfectAndWorst(t *testing.T) {
	// Perfect call: probability 1 on actual class → Brier 0.
	perfect := Prediction{PHome: 1, PDraw: 0, PAway: 0, Actual: OutcomeHome}
	if b := Brier(perfect); !approx(b, 0, 1e-9) {
		t.Fatalf("perfect Brier = %v, want 0", b)
	}
	// Worst call: probability 1 on wrong class → Brier 2.
	worst := Prediction{PHome: 0, PDraw: 0, PAway: 1, Actual: OutcomeHome}
	if b := Brier(worst); !approx(b, 2, 1e-9) {
		t.Fatalf("worst Brier = %v, want 2", b)
	}
	// Uniform (1/3, 1/3, 1/3): Brier = (2/3)^2 + (1/3)^2 + (1/3)^2 = 6/9.
	uniform := Prediction{PHome: 1.0 / 3, PDraw: 1.0 / 3, PAway: 1.0 / 3, Actual: OutcomeDraw}
	if b := Brier(uniform); !approx(b, 6.0/9.0, 1e-9) {
		t.Fatalf("uniform Brier = %v, want 6/9", b)
	}
}

func TestLogLoss_ClampAndUniform(t *testing.T) {
	// Uniform → -log(1/3) ≈ 1.0986.
	uniform := Prediction{PHome: 1.0 / 3, PDraw: 1.0 / 3, PAway: 1.0 / 3, Actual: OutcomeDraw}
	if l := LogLoss(uniform); !approx(l, -math.Log(1.0/3), 1e-9) {
		t.Fatalf("uniform log loss = %v, want %v", l, -math.Log(1.0/3))
	}
	// Catastrophic: probability 0 on actual class should be clamped and finite.
	cat := Prediction{PHome: 1, PDraw: 0, PAway: 0, Actual: OutcomeAway}
	if l := LogLoss(cat); math.IsInf(l, 0) || math.IsNaN(l) {
		t.Fatalf("log loss not clamped: %v", l)
	}
}

func TestArgmax(t *testing.T) {
	if g := Argmax(Prediction{PHome: 0.4, PDraw: 0.35, PAway: 0.25, Actual: OutcomeHome}); g != OutcomeHome {
		t.Fatalf("argmax want HOME got %d", g)
	}
	if g := Argmax(Prediction{PHome: 0.2, PDraw: 0.5, PAway: 0.3, Actual: OutcomeDraw}); g != OutcomeDraw {
		t.Fatalf("argmax want DRAW got %d", g)
	}
	// Ties -> HOME wins the tiebreak.
	if g := Argmax(Prediction{PHome: 0.5, PDraw: 0.5, PAway: 0, Actual: OutcomeHome}); g != OutcomeHome {
		t.Fatalf("tie argmax want HOME got %d", g)
	}
}

func TestCompute_SkipsInvalid(t *testing.T) {
	preds := []Prediction{
		{PHome: 0.5, PDraw: 0.3, PAway: 0.2, Actual: OutcomeHome}, // valid
		{PHome: 0.1, PDraw: 0.6, PAway: 0.3, Actual: OutcomeDraw}, // valid
		{PHome: -0.5, PDraw: 1, PAway: 0.5, Actual: OutcomeHome},  // invalid (negative)
	}
	s, skipped := Compute(preds)
	if skipped != 1 {
		t.Fatalf("skipped = %d want 1", skipped)
	}
	if s.SampleSize != 2 {
		t.Fatalf("sample size = %d want 2", s.SampleSize)
	}
	if s.Correct != 2 {
		t.Fatalf("correct = %d want 2", s.Correct)
	}
	if !approx(s.Accuracy, 1.0, 1e-9) {
		t.Fatalf("accuracy = %v want 1", s.Accuracy)
	}
	if s.BrierScore <= 0 || s.BrierScore >= 2 {
		t.Fatalf("brier out of range: %v", s.BrierScore)
	}
	if s.LogLoss <= 0 {
		t.Fatalf("log loss should be positive, got %v", s.LogLoss)
	}
}

func TestCalibration_Buckets(t *testing.T) {
	// Every outcome probability is binned: three predictions give nine
	// (probability, happened?) pairs.
	preds := []Prediction{
		{PHome: 0.9, PDraw: 0.05, PAway: 0.05, Actual: OutcomeHome},
		{PHome: 0.9, PDraw: 0.05, PAway: 0.05, Actual: OutcomeHome},
		{PHome: 0.1, PDraw: 0.1, PAway: 0.8, Actual: OutcomeAway},
	}
	bins := Calibration(preds, 10)
	total := 0
	for _, b := range bins {
		total += b.SampleSize
	}
	if total != 9 {
		t.Fatalf("total samples = %d want 9", total)
	}
	// [0.9, 1.0): the two 0.9 home forecasts, both came true.
	if b := bins[9]; b.SampleSize != 2 || !approx(b.ObservedFrequency, 1, 1e-9) || !approx(b.AvgPredicted, 0.9, 1e-9) {
		t.Fatalf("bin[9] = %+v", b)
	}
	// [0.8, 0.9): the away forecast, came true.
	if b := bins[8]; b.SampleSize != 1 || !approx(b.ObservedFrequency, 1, 1e-9) {
		t.Fatalf("bin[8] = %+v", b)
	}
	// [0.0, 0.1): four 0.05 forecasts (draw, away x2) — none happened.
	if b := bins[0]; b.SampleSize != 4 || b.ObservedFrequency != 0 {
		t.Fatalf("bin[0] = %+v", b)
	}
	// [0.1, 0.2): home 0.1 and draw 0.1 of the third match, neither happened.
	if b := bins[1]; b.SampleSize != 2 || b.ObservedFrequency != 0 {
		t.Fatalf("bin[1] = %+v", b)
	}
}

func TestMinuteBucket(t *testing.T) {
	cases := map[int]string{0: "pre-match", -1: "pre-match", 5: "0-14", 15: "15-29", 44: "30-44", 60: "60-74", 90: "75+"}
	for m, want := range cases {
		if got := MinuteBucket(m); got != want {
			t.Fatalf("MinuteBucket(%d) = %q want %q", m, got, want)
		}
	}
}
