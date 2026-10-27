package footballdata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

const (
	ppl2026Response = `{
  "filters": {"season": "2026"},
  "resultSet": {"count": 2},
  "competition": {
    "id": 2017,
    "name": "Primeira Liga",
    "code": "PPL",
    "area": {"name": "Portugal"},
    "emblem": "https://crests.football-data.org/PPL.png"
  },
  "matches": [
    {
      "id": 547123,
      "utcDate": "2026-10-27T19:00:00Z",
      "status": "FINISHED",
      "matchday": 10,
      "stage": "REGULAR_SEASON",
      "lastUpdated": "2026-10-27T20:50:00Z",
      "homeTeam": {"id": 503, "name": "SL Benfica", "shortName": "Benfica", "tla": "BEN", "crest": ""},
      "awayTeam": {"id": 498, "name": "FC Porto", "shortName": "Porto", "tla": "FCP", "crest": ""},
      "score": {
        "winner": "HOME_TEAM",
        "duration": "REGULAR",
        "fullTime": {"home": 2, "away": 1},
        "halfTime": {"home": 1, "away": 0}
      },
      "season": {"id": 2026, "startDate": "2026-08-10"}
    },
    {
      "id": 547124,
      "utcDate": "2026-10-28T20:15:00Z",
      "status": "IN_PLAY",
      "minute": 42,
      "matchday": 10,
      "stage": "REGULAR_SEASON",
      "lastUpdated": "2026-10-28T20:57:30Z",
      "homeTeam": {"id": 1062, "name": "Sporting CP", "shortName": "Sporting", "tla": "SPO", "crest": ""},
      "awayTeam": {"id": 1082, "name": "Rio Ave FC", "shortName": "Rio Ave", "tla": "RAV", "crest": ""},
      "score": {
        "winner": null,
        "duration": "REGULAR",
        "fullTime": {"home": 1, "away": 0},
        "halfTime": {"home": 1, "away": 0}
      },
      "season": {"id": 2026, "startDate": "2026-08-10"}
    }
  ]
}`

	plErrorResponse = `{"message":"Resource not found.","errorCode":404}`
)

func newTestServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New("test-token", Options{BaseURL: srv.URL, RequestsPerMinute: 1000})
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return srv, c
}

func TestClient_GetCompetitionMatches_Success(t *testing.T) {
	calls := 0
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Auth-Token") != "test-token" {
			t.Errorf("missing or wrong X-Auth-Token: %q", r.Header.Get("X-Auth-Token"))
		}
		if r.URL.Path != "/v4/competitions/PPL/matches" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ppl2026Response))
	})
	c.baseURL = c.baseURL + "/v4" // the test handler expects /v4 prefix

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.GetCompetitionMatches(ctx, "PPL")
	if err != nil {
		t.Fatalf("GetCompetitionMatches: %v", err)
	}
	if calls != 1 {
		t.Fatalf("want 1 HTTP call, got %d", calls)
	}
	if resp.Competition.Code != "PPL" {
		t.Fatalf("want code PPL, got %q", resp.Competition.Code)
	}
	if len(resp.Matches) != 2 {
		t.Fatalf("want 2 matches, got %d", len(resp.Matches))
	}
	if resp.Matches[0].HomeTeam.Name != "SL Benfica" {
		t.Fatalf("home team parse: %+v", resp.Matches[0].HomeTeam)
	}
}

func TestClient_GetCompetitionMatches_ErrorStatus(t *testing.T) {
	_, c := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(plErrorResponse))
	})
	c.baseURL = c.baseURL + "/v4"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.GetCompetitionMatches(ctx, "ZZZ")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", apiErr.StatusCode)
	}
	if apiErr.Message != "Resource not found." {
		t.Fatalf("want parsed message, got %q", apiErr.Message)
	}
}

func TestClient_RateLimiter_SpacesRequests(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"competition":{"code":"PL"},"matches":[]}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New("test-token", Options{BaseURL: srv.URL + "/v4", RequestsPerMinute: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// Two requests must fit instantly; the third must be blocked by the
	// limiter and the short context should cancel it with ctx error.
	for i := 0; i < 2; i++ {
		if _, err := c.GetCompetitionMatches(ctx, "PL"); err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}
	if _, err := c.GetCompetitionMatches(ctx, "PL"); err == nil {
		t.Fatal("third request should be blocked by limiter + ctx timeout")
	}
	if calls != 2 {
		t.Fatalf("want 2 HTTP calls, got %d", calls)
	}
}

func TestProvider_FetchFixtures_MapsCorrectly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(ppl2026Response))
	}))
	t.Cleanup(srv.Close)
	client, err := New("test-token", Options{BaseURL: srv.URL + "/v4", RequestsPerMinute: 1000})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	provider := NewProvider(client, []string{"PPL"})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	fx, err := provider.FetchFixtures(ctx)
	if err != nil {
		t.Fatalf("FetchFixtures: %v", err)
	}
	if len(fx) != 2 {
		t.Fatalf("want 2 fixtures, got %d", len(fx))
	}

	finished := fx[0]
	if finished.Status != repository.StatusFinished {
		t.Fatalf("status: want FINISHED got %s", finished.Status)
	}
	if finished.HomeScore != 2 || finished.AwayScore != 1 {
		t.Fatalf("score: want 2-1 got %d-%d", finished.HomeScore, finished.AwayScore)
	}
	if finished.FinalResult == nil || *finished.FinalResult != repository.OutcomeHome {
		t.Fatalf("final result: want HOME got %v", finished.FinalResult)
	}
	if finished.Season != "2026/27" {
		t.Fatalf("season: want 2026/27 got %q", finished.Season)
	}
	if finished.CompetitionName != "Primeira Liga" || finished.Country != "Portugal" {
		t.Fatalf("competition metadata: %+v", finished)
	}
	if finished.ExternalCompID != "PPL" {
		t.Fatalf("external comp id: want PPL got %q", finished.ExternalCompID)
	}
	if finished.ExternalMatchID != "547123" {
		t.Fatalf("external match id: %q", finished.ExternalMatchID)
	}
	if !strings.EqualFold(finished.HomeTeamName, "SL Benfica") {
		t.Fatalf("home team name: %q", finished.HomeTeamName)
	}

	live := fx[1]
	if live.Status != repository.StatusLive {
		t.Fatalf("live status: want LIVE got %s", live.Status)
	}
	if live.Minute == nil || *live.Minute != 42 {
		t.Fatalf("live minute: %v", live.Minute)
	}
	if live.FinalResult != nil {
		t.Fatalf("live final result should be nil, got %v", live.FinalResult)
	}
}

func TestProvider_FetchFixtures_FailsOnlyIfAllCompsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"blocked"}`))
	}))
	t.Cleanup(srv.Close)
	client, err := New("test-token", Options{BaseURL: srv.URL + "/v4", RequestsPerMinute: 1000})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	provider := NewProvider(client, []string{"ZZZ", "YYY"})
	_, err = provider.FetchFixtures(context.Background())
	if err == nil {
		t.Fatal("expected error when all competitions fail")
	}
	if !strings.Contains(err.Error(), "all competitions failed") {
		t.Fatalf("expected joined error, got %q", err.Error())
	}
}

func TestMapStatus(t *testing.T) {
	cases := map[string]repository.MatchStatus{
		"SCHEDULED": repository.StatusScheduled,
		"TIMED":     repository.StatusScheduled,
		"IN_PLAY":   repository.StatusLive,
		"LIVE":      repository.StatusLive,
		"PAUSED":    repository.StatusHalftime,
		"FINISHED":  repository.StatusFinished,
		"AWARDED":   repository.StatusFinished,
		"POSTPONED": repository.StatusPostponed,
		"CANCELED":  repository.StatusCancelled,
		"GARBAGE":   repository.StatusScheduled,
	}
	for in, want := range cases {
		if got := mapStatus(in); got != want {
			t.Errorf("mapStatus(%q) = %s want %s", in, got, want)
		}
	}
}

func TestMapWinner(t *testing.T) {
	cases := map[string]repository.Outcome{
		"HOME_TEAM": repository.OutcomeHome,
		"AWAY_TEAM": repository.OutcomeAway,
		"DRAW":      repository.OutcomeDraw,
		"":          "",
		"OTHER":     "",
	}
	for in, want := range cases {
		if got := mapWinner(in); got != want {
			t.Errorf("mapWinner(%q) = %q want %q", in, got, want)
		}
	}
}

func TestNormaliseSeason(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"2026":    "2026/27",
		"2099":    "2099/00",
		"2026/27": "2026/27",
		"garbage": "garbage",
	}
	for in, want := range cases {
		if got := normaliseSeason(in); got != want {
			t.Errorf("normaliseSeason(%q) = %q want %q", in, got, want)
		}
	}
}
