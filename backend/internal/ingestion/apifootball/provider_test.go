package apifootball

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// All tests in this package mock the api-football.com HTTP server with
// httptest. We never fire real API calls from tests — the free tier is
// 100 req/day and tests must not consume that budget.

func newTestServer(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c, err := New("test-key", Options{
		BaseURL:           srv.URL,
		RequestsPerMinute: 100, // relax for tests
		DailyLimit:        1000,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestFetchFixtureMappingForDates_FiltersToConfiguredLeagues(t *testing.T) {
	// Response includes two matches we DO care about (league 140 = La
	// Liga) and one we don't (league 9999 = some other competition).
	payload := `{
	  "get": "fixtures",
	  "errors": [],
	  "results": 3,
	  "response": [
	    {
	      "fixture": {"id": 999001, "date": "2026-10-26T19:00:00+00:00",
	                  "timestamp": 0, "status": {"short": "FT", "long": "Match Finished"}},
	      "league":  {"id": 140, "name": "La Liga"},
	      "teams":   {"home": {"id": 536, "name": "Sevilla"},
	                  "away": {"id": 727, "name": "Osasuna"}},
	      "goals":   {"home": 2, "away": 0}
	    },
	    {
	      "fixture": {"id": 999002, "date": "2026-10-26T20:00:00+00:00",
	                  "timestamp": 0, "status": {"short": "FT", "long": "Match Finished"}},
	      "league":  {"id": 9999, "name": "Not Our League"},
	      "teams":   {"home": {"id": 1, "name": "X"},
	                  "away": {"id": 2, "name": "Y"}},
	      "goals":   {"home": 0, "away": 0}
	    },
	    {
	      "fixture": {"id": 999003, "date": "2026-10-26T21:00:00+00:00",
	                  "timestamp": 0, "status": {"short": "FT", "long": "Match Finished"}},
	      "league":  {"id": 140, "name": "La Liga"},
	      "teams":   {"home": {"id": 530, "name": "Atletico Madrid"},
	                  "away": {"id": 548, "name": "Real Madrid"}},
	      "goals":   {"home": 1, "away": 1}
	    }
	  ]
	}`
	calls := 0
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("x-apisports-key") != "test-key" {
			t.Fatalf("missing/wrong auth header: %q", r.Header.Get("x-apisports-key"))
		}
		if !strings.Contains(r.URL.RawQuery, "date=2026-10-26") {
			t.Fatalf("expected date query: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	})
	p := NewProvider(c, []LeagueMapping{{InternalCompCode: "PD", APIFootballID: 140, Season: 2025}})
	cands, err := p.FetchFixtureMappingForDates(context.Background(), []string{"2026-10-26"})
	if err != nil {
		t.Fatalf("FetchFixtureMappingForDates: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 api call, got %d", calls)
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates (league 9999 filtered out), got %d", len(cands))
	}
	if cands[0].APIFootballFixtureID != 999001 || cands[0].CompetitionCode != "PD" {
		t.Fatalf("candidate[0] mismatch: %+v", cands[0])
	}
	if cands[1].APIFootballFixtureID != 999003 {
		t.Fatalf("candidate[1] mismatch: %+v", cands[1])
	}
}

func TestFetchEventsForFixture_FiltersToGoalsAndRedCards(t *testing.T) {
	payload := `{
	  "get": "fixtures/events",
	  "errors": [],
	  "results": 7,
	  "response": [
	    {"time": {"elapsed": 23, "extra": null},
	     "team": {"id": 536, "name": "Sevilla"},
	     "player": {"id": 100, "name": "Isco"}, "assist": {"id": null, "name": null},
	     "type": "Goal", "detail": "Normal Goal", "comments": null},
	    {"time": {"elapsed": 40, "extra": null},
	     "team": {"id": 727, "name": "Osasuna"},
	     "player": {"id": 200, "name": "Garcia"}, "assist": {"id": null, "name": null},
	     "type": "Card", "detail": "Yellow Card", "comments": "Foul"},
	    {"time": {"elapsed": 55, "extra": 2},
	     "team": {"id": 727, "name": "Osasuna"},
	     "player": {"id": 200, "name": "Garcia"}, "assist": {"id": null, "name": null},
	     "type": "Card", "detail": "Second Yellow card", "comments": "Dissent"},
	    {"time": {"elapsed": 60, "extra": null},
	     "team": {"id": 727, "name": "Osasuna"},
	     "player": {"id": 300, "name": "Lopez"}, "assist": {"id": 301, "name": "Benzema"},
	     "type": "subst", "detail": "Substitution 1", "comments": null},
	    {"time": {"elapsed": 72, "extra": null},
	     "team": {"id": 536, "name": "Sevilla"},
	     "player": {"id": 400, "name": "Suso"}, "assist": {"id": null, "name": null},
	     "type": "Goal", "detail": "Penalty", "comments": null},
	    {"time": {"elapsed": 80, "extra": null},
	     "team": {"id": 536, "name": "Sevilla"},
	     "player": {"id": 500, "name": "Rakitic"}, "assist": {"id": null, "name": null},
	     "type": "Goal", "detail": "Missed Penalty", "comments": null},
	    {"time": {"elapsed": 85, "extra": null},
	     "team": {"id": 727, "name": "Osasuna"},
	     "player": {"id": 600, "name": "Moi"}, "assist": {"id": null, "name": null},
	     "type": "Var", "detail": "Goal cancelled", "comments": null}
	  ]
	}`
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	})
	p := NewProvider(c, nil)
	events, err := p.FetchEventsForFixture(context.Background(), 999001, time.Now())
	if err != nil {
		t.Fatalf("FetchEventsForFixture: %v", err)
	}
	// Expect: Isco goal (23'), 2nd yellow red (55'+2), Suso penalty (72').
	// Dropped: yellow card, substitution, missed penalty, VAR.
	if len(events) != 3 {
		t.Fatalf("expected 3 kept events, got %d: %+v", len(events), events)
	}
	if events[0].EventType != "GOAL" || events[0].Minute != 23 {
		t.Fatalf("event[0] mismatch: %+v", events[0])
	}
	if events[1].EventType != "RED_CARD" || events[1].Minute != 55 || events[1].AddedMinute == nil || *events[1].AddedMinute != 2 {
		t.Fatalf("event[1] (2nd-yellow red) mismatch: %+v", events[1])
	}
	if events[2].EventType != "GOAL" || !strings.Contains(events[2].Description, "Penalty") {
		t.Fatalf("event[2] (penalty) mismatch: %+v", events[2])
	}
}

func TestClient_RejectsEmptyAPIKey(t *testing.T) {
	if _, err := New("", Options{}); err == nil {
		t.Fatal("expected error for empty api key")
	}
}

func TestClient_SurfacesErrorEnvelope(t *testing.T) {
	c, _ := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"get":"fixtures","errors":{"token":"invalid"},"results":0,"response":[]}`))
	})
	p := NewProvider(c, []LeagueMapping{{InternalCompCode: "PD", APIFootballID: 140, Season: 2025}})
	_, err := p.FetchFixtureMappingForDates(context.Background(), []string{"2026-10-26"})
	if err == nil {
		t.Fatal("expected error from invalid-token envelope")
	}
}

func TestClient_DailyLimitBlocks(t *testing.T) {
	// Only 2 requests allowed; third blocks (we cancel context to prove it).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"get":"fixtures","errors":[],"results":0,"response":[]}`))
	}))
	defer srv.Close()
	c, err := New("k", Options{BaseURL: srv.URL, RequestsPerMinute: 100, DailyLimit: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.ListEventsForFixture(context.Background(), 1); err != nil {
		t.Fatalf("call 1: %v", err)
	}
	if _, err := c.ListEventsForFixture(context.Background(), 2); err != nil {
		t.Fatalf("call 2: %v", err)
	}
	if left := c.RequestsRemainingToday(); left != 0 {
		t.Fatalf("expected 0 remaining, got %d", left)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.ListEventsForFixture(ctx, 3); err == nil {
		t.Fatal("expected ctx cancellation after daily limit")
	}
}
