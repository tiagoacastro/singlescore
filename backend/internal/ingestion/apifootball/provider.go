package apifootball

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event is the subset of an api-football event we care about. The
// orchestrator in package ingestion converts this to its own Event
// type (and ultimately a repository.MatchEvent row). We don't import
// package ingestion here to avoid an import cycle.
type Event struct {
	ExternalID  string
	EventType   string // "GOAL" or "RED_CARD"
	TeamAFID    int    // raw api-football team id
	TeamName    string // raw api-football team name — orchestrator uses
	// this to map to our internal HOME/AWAY by comparing against the
	// match's known team names (api-football and football-data.org
	// agree on the common team-name spelling after normalisation).
	Minute      int
	AddedMinute *int
	Description string
	ObservedAt  time.Time
}

// Provider name as persisted in provider_match_map.provider and in
// match_events.provider.
const ProviderName = "api-football.com"

// LeagueMapping is one of our competitions mapped to its api-football
// league id. api-football uses stable integer ids for every league;
// the mapping is static per deployment.
type LeagueMapping struct {
	InternalCompCode string // e.g. "PD"
	APIFootballID    int    // e.g. 140 (La Liga)
	Season           int    // e.g. 2025 (season start year)
}

// DefaultLeagues covers the five competitions we ingest from
// football-data.org. Season defaults to the current northern-hemisphere
// season start year — pass the right value per environment.
//
// Reference ids come straight from
// https://dashboard.api-football.com/soccer/ids/leagues.
var DefaultLeagues = []LeagueMapping{
	{InternalCompCode: "PL", APIFootballID: 39, Season: 2025},  // Premier League
	{InternalCompCode: "PD", APIFootballID: 140, Season: 2025}, // La Liga
	{InternalCompCode: "SA", APIFootballID: 135, Season: 2025}, // Serie A
	{InternalCompCode: "BL1", APIFootballID: 78, Season: 2025}, // Bundesliga
	{InternalCompCode: "PPL", APIFootballID: 94, Season: 2025}, // Primeira Liga
}

// Provider is the ingestion-side façade over the api-football client.
// It is scoped narrowly to the two things we actually need from this
// data source: a fixture mapping (one call per unique match-day we
// care about) and a per-match events lookup (1 API call per match).
type Provider struct {
	client  *Client
	leagues []LeagueMapping
}

// NewProvider wraps a Client with the default league configuration.
// Pass a non-nil leagues slice to override (e.g. for tests or a
// different season).
func NewProvider(c *Client, leagues []LeagueMapping) *Provider {
	l := leagues
	if l == nil {
		l = DefaultLeagues
	}
	return &Provider{client: c, leagues: l}
}

// RequestsRemainingToday surfaces the client's local daily-quota
// counter so orchestrators + CLIs can log remaining budget.
func (p *Provider) RequestsRemainingToday() int {
	return p.client.RequestsRemainingToday()
}

// MappingCandidate is one fixture returned by the mapping sweep. The
// caller is responsible for matching it to an internal match row.
type MappingCandidate struct {
	APIFootballFixtureID int
	HomeTeamName         string
	AwayTeamName         string
	KickoffUTC           time.Time
	CompetitionCode      string // internal code (PL/PD/SA/BL1/PPL)
}

// LeagueAFIDs is the set of api-football league ids we care about.
// Used to filter the global /fixtures?date=X responses (which return
// 100+ matches/day worldwide) down to just our 5 competitions.
func (p *Provider) LeagueAFIDs() map[int]string {
	m := make(map[int]string, len(p.leagues))
	for _, lg := range p.leagues {
		m[lg.APIFootballID] = lg.InternalCompCode
	}
	return m
}

// FetchFixtureMappingForDates fetches fixtures for a specific set of
// UTC calendar dates (YYYY-MM-DD). Each date costs exactly one API
// call. Fixtures outside our configured leagues are filtered client-
// side (api-football returns all leagues worldwide on /fixtures?date;
// we drop everything that isn't ours).
//
// Free-tier compatibility: the api-football free plan blocks
// /fixtures?league=X&season=Y for seasons after 2024, AND it only
// serves data for a rolling 3-day window (yesterday / today /
// tomorrow). Out-of-window dates are reported as per-date errors
// (collected in the returned slice) instead of aborting the whole
// sweep, so a mixed set of dates still returns whatever we COULD
// fetch.
//
// Returns a flat list of candidates. The second return value contains
// non-fatal per-date errors (nil if everything succeeded).
func (p *Provider) FetchFixtureMappingForDates(ctx context.Context, dates []string) ([]MappingCandidate, []error) {
	leagueIDs := p.LeagueAFIDs()
	var out []MappingCandidate
	var errs []error
	for _, date := range dates {
		fixtures, err := p.client.ListFixturesByDate(ctx, date, 0, 0)
		if err != nil {
			errs = append(errs, fmt.Errorf("fixtures for date %s: %w", date, err))
			continue
		}
		for _, f := range fixtures {
			code, ok := leagueIDs[f.League.ID]
			if !ok {
				continue // Not one of our leagues.
			}
			kickoff, _ := time.Parse(time.RFC3339, f.Fixture.Date)
			out = append(out, MappingCandidate{
				APIFootballFixtureID: f.Fixture.ID,
				HomeTeamName:         f.Teams.Home.Name,
				AwayTeamName:         f.Teams.Away.Name,
				KickoffUTC:           kickoff.UTC(),
				CompetitionCode:      code,
			})
		}
	}
	return out, errs
}

// FreeTierWindowDates returns the 3 UTC calendar dates currently
// accessible on the api-football free plan: yesterday, today,
// tomorrow. The plan rejects queries for any other date with a
// specific error message. Callers that know they're on the free tier
// should pre-filter their desired-date list through this helper so the
// mapping sweep doesn't waste API calls on dates it can't reach.
func FreeTierWindowDates(now time.Time) []string {
	today := now.UTC().Truncate(24 * time.Hour)
	return []string{
		today.AddDate(0, 0, -1).Format("2006-01-02"),
		today.Format("2006-01-02"),
		today.AddDate(0, 0, 1).Format("2006-01-02"),
	}
}

// ErrNoMapping is returned by FetchEventsForFixture when the caller
// should backfill the mapping before retrying.
var ErrNoMapping = errors.New("apifootball: no fixture mapping")

// FetchEventsForFixture fetches goals and red cards for one fixture
// (1 API call). Everything else that api-football reports — yellow
// cards, subs, VAR, missed penalties — is filtered out at this layer
// so downstream code gets exactly the two event types the UI plots.
//
// Returns events keyed by raw api-football team.id; the orchestrator
// converts to HOME/AWAY once it knows which internal match it belongs
// to.
func (p *Provider) FetchEventsForFixture(ctx context.Context, afFixtureID int, sourceObservedAt time.Time) ([]Event, error) {
	if afFixtureID <= 0 {
		return nil, ErrNoMapping
	}
	raw, err := p.client.ListEventsForFixture(ctx, afFixtureID)
	if err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(raw))
	for i, ev := range raw {
		kind, keep := classifyEvent(ev)
		if !keep {
			continue
		}
		minute := ev.Time.Elapsed
		var addedMinutePtr *int
		if ev.Time.Extra != nil {
			x := *ev.Time.Extra
			addedMinutePtr = &x
		}
		// Deterministic external id so repeated fetches upsert the
		// same event row rather than inserting duplicates. Index `i`
		// disambiguates the rare case of two otherwise-identical
		// events (e.g. two straight reds at the same minute).
		playerKey := "nil"
		if ev.Player.ID != nil {
			playerKey = strconv.Itoa(*ev.Player.ID)
		}
		extraMin := 0
		if ev.Time.Extra != nil {
			extraMin = *ev.Time.Extra
		}
		extID := fmt.Sprintf("af-%d-%d-%d-%d-%s-%s-%d",
			afFixtureID, minute, extraMin, ev.Team.ID, playerKey, kind, i)
		out = append(out, Event{
			ExternalID:  extID,
			EventType:   kind,
			TeamAFID:    ev.Team.ID,
			TeamName:    ev.Team.Name,
			Minute:      minute,
			AddedMinute: addedMinutePtr,
			Description: buildDescription(ev),
			ObservedAt:  sourceObservedAt,
		})
	}
	return out, nil
}

// classifyEvent maps api-football's (type, detail) pair to one of our
// supported event types. Returns keep=false for anything the UI
// doesn't plot (plain yellows, subs, VAR, missed penalties, …).
//
// Red-card detection matches both the straight-red case ("Red Card")
// and the second-yellow-becomes-red case ("Second Yellow card"),
// because api-football does NOT emit a separate Red Card event for
// the second yellow — the second-yellow event IS the dismissal.
func classifyEvent(ev APIEvent) (kind string, keep bool) {
	t := strings.ToLower(ev.Type)
	d := strings.ToLower(ev.Detail)
	switch {
	case t == "goal":
		// All goal flavours count: "Normal Goal", "Own Goal",
		// "Penalty". Missed penalties are typed "Goal" with detail
		// "Missed Penalty" — exclude those.
		if strings.Contains(d, "missed") {
			return "", false
		}
		return "GOAL", true
	case t == "card" && (strings.Contains(d, "red") || strings.Contains(d, "second yellow")):
		return "RED_CARD", true
	default:
		return "", false
	}
}

// buildDescription is a short human-readable one-liner stored on the
// MatchEvent row. Kept small so it fits in list UIs.
func buildDescription(ev APIEvent) string {
	var sb strings.Builder
	sb.WriteString(ev.Detail)
	if ev.Player.Name != nil && *ev.Player.Name != "" {
		sb.WriteString(" — ")
		sb.WriteString(*ev.Player.Name)
	}
	if ev.Assist.Name != nil && *ev.Assist.Name != "" {
		sb.WriteString(" (assist: ")
		sb.WriteString(*ev.Assist.Name)
		sb.WriteString(")")
	}
	return sb.String()
}

