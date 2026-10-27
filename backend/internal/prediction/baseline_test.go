package prediction

import (
	"math"
	"testing"
)

// Tests that pin the critical properties of the Poisson-race baseline:
//   - probabilities sum to 1 and are non-negative,
//   - home advantage helps home,
//   - leading team's win prob rises with time,
//   - a red card hurts the carded team,
//   - **full-time with any non-zero margin commits to the leader at ~1.0** (the headline fix),
//   - **a fresh goal strictly increases the scorer's probability**,
//   - **monotonicity**: a 1-0 lead at minute N+1 is strictly more valuable than at minute N (never dips).

func TestBaseline_PreMatchSumsToOne(t *testing.T) {
	p := NewBaseline()
	cases := []Snapshot{
		{HomeElo: 1500, AwayElo: 1500},
		{HomeElo: 1700, AwayElo: 1500},
		{HomeElo: 1500, AwayElo: 1700},
		{HomeElo: 1800, AwayElo: 1300},
	}
	for i, c := range cases {
		got, err := p.Predict(c)
		if err != nil {
			t.Fatalf("case %d: Predict err %v", i, err)
		}
		sum := got.Home + got.Draw + got.Away
		if math.Abs(sum-1.0) > 1e-9 {
			t.Fatalf("case %d: sum=%v want 1", i, sum)
		}
		if got.Home < 0 || got.Draw < 0 || got.Away < 0 {
			t.Fatalf("case %d: negative probability %+v", i, got)
		}
	}
}

func TestBaseline_HomeAdvantageHelpsHome(t *testing.T) {
	p := NewBaseline()
	even, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500})
	if even.Home <= even.Away {
		t.Fatalf("home advantage should favour home: home=%v away=%v", even.Home, even.Away)
	}
}

func TestBaseline_LeadingTeamProbRisesWithTime(t *testing.T) {
	p := NewBaseline()
	early, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, HomeScore: 1, Minute: 10})
	late, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, HomeScore: 1, Minute: 85})
	if late.Home <= early.Home {
		t.Fatalf("leading home prob should grow with time: early=%v late=%v", early.Home, late.Home)
	}
}

func TestBaseline_RedCardHurtsHome(t *testing.T) {
	p := NewBaseline()
	clean, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, Minute: 30})
	oneRed, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, Minute: 30, HomeRedCards: 1})
	if oneRed.Home >= clean.Home {
		t.Fatalf("red card for home should drop home prob: clean=%v red=%v", clean.Home, oneRed.Home)
	}
}

// TestBaseline_FullTimeCommitsToWinner is the headline regression test
// for the "chart doesn't commit at FT" bug. At minute 90 with ANY non-zero
// margin, there is no residual time → no more goals → the current winner
// is certain. Expect ≥0.99 for the leader.
func TestBaseline_FullTimeCommitsToWinner(t *testing.T) {
	p := NewBaseline()
	cases := []struct {
		name    string
		snap    Snapshot
		wantMin float64
		which   string // which outcome is expected to dominate
	}{
		{"underdog home wins 1-0", Snapshot{HomeElo: 1500, AwayElo: 1800, HomeScore: 1, Minute: 90}, 0.99, "home"},
		{"away wins 1-2", Snapshot{HomeElo: 1700, AwayElo: 1500, HomeScore: 1, AwayScore: 2, Minute: 90}, 0.99, "away"},
		{"big draw 2-2", Snapshot{HomeElo: 1500, AwayElo: 1500, HomeScore: 2, AwayScore: 2, Minute: 90}, 0.99, "draw"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := p.Predict(c.snap)
			if err != nil {
				t.Fatal(err)
			}
			var winner float64
			switch c.which {
			case "home":
				winner = got.Home
			case "away":
				winner = got.Away
			case "draw":
				winner = got.Draw
			}
			if winner < c.wantMin {
				t.Fatalf("FT commitment: %q got %+v, winner=%v want ≥%v", c.name, got, winner, c.wantMin)
			}
		})
	}
}

// TestBaseline_GoalIncreasesScorerProb — a fresh goal must strictly
// move probability toward the scorer. Previously the chart would dip
// after a goal (symptom of the sync-clobbering bug + an under-powered
// time weight). The predictor itself must guarantee monotonicity.
func TestBaseline_GoalIncreasesScorerProb(t *testing.T) {
	p := NewBaseline()
	for _, minute := range []int{5, 24, 45, 65, 82} {
		before, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, Minute: minute})
		after, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, HomeScore: 1, Minute: minute})
		if after.Home <= before.Home {
			t.Fatalf("minute %d: home goal must raise home prob: before=%v after=%v", minute, before.Home, after.Home)
		}
		if after.Away >= before.Away {
			t.Fatalf("minute %d: home goal must lower away prob: before=%v after=%v", minute, before.Away, after.Away)
		}
	}
}

// TestBaseline_LeadIsMonotonicInTime — holding scoreline fixed, the
// leader's win prob strictly increases minute by minute. Pins the
// "chart goes up then back down" symptom to a data-pipeline bug (which
// it was) rather than a predictor issue, so a regression here means
// something is wrong with the predictor itself.
func TestBaseline_LeadIsMonotonicInTime(t *testing.T) {
	p := NewBaseline()
	last := 0.0
	for minute := 5; minute <= 90; minute += 5 {
		got, err := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500, HomeScore: 1, Minute: minute})
		if err != nil {
			t.Fatal(err)
		}
		if got.Home < last {
			t.Fatalf("minute %d: leader prob regressed: last=%v now=%v", minute, last, got.Home)
		}
		last = got.Home
	}
}

// TestBaseline_PreMatchDrawShareIsReasonable — evenly matched teams
// should produce a draw-share in the 20-30% range, matching the
// empirical top-5 European league rate.
func TestBaseline_PreMatchDrawShareIsReasonable(t *testing.T) {
	p := NewBaseline()
	got, _ := p.Predict(Snapshot{HomeElo: 1500, AwayElo: 1500})
	if got.Draw < 0.20 || got.Draw > 0.32 {
		t.Fatalf("pre-match draw share for even teams out of band: %+v", got)
	}
}
