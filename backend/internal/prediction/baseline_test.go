package prediction

import (
	"math"
	"testing"
)

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
