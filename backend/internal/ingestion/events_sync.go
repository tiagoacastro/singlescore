package ingestion

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/ingestion/apifootball"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// EventsSync orchestrates the api-football.com event-ingestion pipeline.
// It is intentionally separate from the main Sync (which runs on
// football-data.org) because:
//
//   - the two data sources have independent rate limits
//   - event ingestion is strictly per-match-on-demand (1 API call each),
//     not a batch sweep
//   - a run can be invoked lazily from the service layer when a user
//     views a match detail page, or in bulk from cmd/ingest-events
type EventsSync struct {
	provider *apifootball.Provider
	matches  *repository.Matches
	catalog  *repository.Catalog
	pmap     *repository.ProviderMap
	// sync is optional. When set, successful per-match event fetches
	// trigger a probability-timeline rebuild for that match using the
	// real goal timings. Allows RefreshMatchEvents to upgrade the
	// coarse HT/FT approximation to a per-minute step curve.
	sync   *Sync
	logger Logger
}

// NewEventsSync wires an EventsSync. All dependencies are required
// except `sync` which may be nil (events-only mode: no prediction
// timeline rebuild on each event refresh).
func NewEventsSync(p *apifootball.Provider, m *repository.Matches, c *repository.Catalog, pm *repository.ProviderMap, sync *Sync, log Logger) *EventsSync {
	return &EventsSync{provider: p, matches: m, catalog: c, pmap: pm, sync: sync, logger: log}
}

// MappingResult summarises a RunMapping call.
type MappingResult struct {
	DatesQueried      int
	CandidatesFetched int
	MappingsWritten   int
	Unmatched         int
	Errors            []string
}

// RunMapping sweeps api-football for fixtures on the UTC calendar
// dates where we have FINISHED matches that haven't been mapped yet.
// Each unique date costs exactly one API call.
//
// Important free-tier caveat: api-football only serves data for a
// 3-day rolling window (yesterday / today / tomorrow). The sweep
// pre-filters its date list to that window so we don't waste calls
// on dates the API will reject outright. For a long-running project
// this means mapping is an ongoing drip — a match played today
// becomes reachable today and tomorrow, then disappears from the
// free tier the day after.
//
// `maxCalls` caps how many dates to query in this run so a single
// invocation can't blow the whole 100-req daily budget. The sweep
// never queries more than 3 dates on the free tier regardless of
// maxCalls, because that's the whole addressable window.
//
// Mapping heuristic: two matches are the same if their home team name
// AND away team name AND kickoff date all agree (names normalised to
// lower-case and stripped of common suffixes like "FC" to tolerate
// mild spelling drift between providers).
//
// Safe to re-run — upsert overwrites existing rows and the query only
// returns dates still unmapped, so each run makes incremental progress.
func (e *EventsSync) RunMapping(ctx context.Context, maxCalls int) (MappingResult, error) {
	if maxCalls <= 0 {
		maxCalls = 10
	}
	res := MappingResult{}
	// Candidate dates: all unmapped FINISHED match-days, newest first.
	wantDates, err := e.pmap.UnmappedFinishedMatchDates(ctx, apifootball.ProviderName, maxCalls*4)
	if err != nil {
		return res, fmt.Errorf("find unmapped dates: %w", err)
	}
	// Intersect with the free-tier 3-day window.
	windowSet := make(map[string]struct{}, 3)
	for _, d := range apifootball.FreeTierWindowDates(time.Now()) {
		windowSet[d] = struct{}{}
	}
	var dates []string
	for _, d := range wantDates {
		if _, ok := windowSet[d]; ok {
			dates = append(dates, d)
			if len(dates) >= maxCalls {
				break
			}
		}
	}
	res.DatesQueried = len(dates)
	if len(dates) == 0 {
		if e.logger != nil {
			e.logger.Info("mapping sweep: no in-window unmapped dates",
				"provider", apifootball.ProviderName,
				"free_tier_window", apifootball.FreeTierWindowDates(time.Now()),
				"unmapped_total", len(wantDates),
			)
		}
		return res, nil
	}
	candidates, perDateErrs := e.provider.FetchFixtureMappingForDates(ctx, dates)
	for _, err := range perDateErrs {
		res.Errors = append(res.Errors, err.Error())
		if e.logger != nil {
			e.logger.Warn("mapping date skipped", "err", err.Error())
		}
	}
	res.CandidatesFetched = len(candidates)
	if len(candidates) == 0 {
		return res, nil
	}
	// Pull every match row once; cheaper than per-candidate lookups.
	rows, err := e.matches.ListAll(ctx)
	if err != nil {
		return res, fmt.Errorf("list matches: %w", err)
	}
	// Index our rows by (date, home_norm, away_norm). Date is the UTC
	// calendar date of kickoff — not an exact-timestamp match because
	// the two providers' reported kickoffs sometimes drift by minutes.
	type key struct{ date, home, away string }
	byKey := make(map[key]string, len(rows))
	teams, _ := e.catalog.ListTeams(ctx)
	teamName := make(map[string]string, len(teams))
	for _, t := range teams {
		teamName[t.ID] = normaliseTeam(t.Name)
	}
	for _, r := range rows {
		byKey[key{
			date: r.KickoffAt.UTC().Format("2006-01-02"),
			home: teamName[r.HomeTeamID],
			away: teamName[r.AwayTeamID],
		}] = r.ID
	}
	// Match candidates against our index.
	var mappings []repository.ProviderMatchMap
	for _, cand := range candidates {
		internalID, ok := byKey[key{
			date: cand.KickoffUTC.Format("2006-01-02"),
			home: normaliseTeam(cand.HomeTeamName),
			away: normaliseTeam(cand.AwayTeamName),
		}]
		if !ok {
			res.Unmatched++
			continue
		}
		mappings = append(mappings, repository.ProviderMatchMap{
			InternalMatchID:   internalID,
			Provider:          apifootball.ProviderName,
			ExternalFixtureID: fmt.Sprintf("%d", cand.APIFootballFixtureID),
		})
	}
	if err := e.pmap.UpsertBatch(ctx, mappings); err != nil {
		return res, fmt.Errorf("upsert mappings: %w", err)
	}
	res.MappingsWritten = len(mappings)
	if e.logger != nil {
		e.logger.Info("fixture mapping synced",
			"provider", apifootball.ProviderName,
			"dates_queried", res.DatesQueried,
			"candidates", res.CandidatesFetched,
			"written", res.MappingsWritten,
			"unmatched", res.Unmatched,
			"remaining_quota", e.provider.RequestsRemainingToday(),
		)
	}
	return res, nil
}

// ErrNoMapping surfaces up when a per-match event fetch is attempted
// but RunMapping hasn't been run (or didn't find a match for this
// fixture). The caller should run RunMapping and retry.
var ErrNoMapping = errors.New("ingestion: no api-football mapping for this match")

// RefreshMatchEvents fetches goals + red cards for ONE match (1 API
// call) and upserts them to match_events. Returns the number of events
// written. Safe to call concurrently — deterministic event ids mean
// repeated upserts of the same event collapse to no-ops.
func (e *EventsSync) RefreshMatchEvents(ctx context.Context, internalMatchID string) (int, error) {
	m, err := e.matches.GetByID(ctx, internalMatchID)
	if err != nil {
		return 0, fmt.Errorf("load match: %w", err)
	}
	if m == nil {
		return 0, fmt.Errorf("match %s not found", internalMatchID)
	}
	mapping, err := e.pmap.Get(ctx, internalMatchID, apifootball.ProviderName)
	if err != nil {
		return 0, fmt.Errorf("load mapping: %w", err)
	}
	if mapping == nil {
		return 0, ErrNoMapping
	}
	afFixtureID, err := atoiStrict(mapping.ExternalFixtureID)
	if err != nil {
		return 0, fmt.Errorf("bad mapping fixture id %q: %w", mapping.ExternalFixtureID, err)
	}
	// Round observed-at to microseconds so SingleStore's DATETIME(6)
	// column accepts it without "Invalid DATE/TIME" errors on insert.
	observed := time.Now().UTC().Round(time.Microsecond)
	events, err := e.provider.FetchEventsForFixture(ctx, afFixtureID, observed)
	if err != nil {
		return 0, fmt.Errorf("fetch events: %w", err)
	}
	if len(events) == 0 {
		return 0, nil
	}
	// Resolve HOME/AWAY by comparing api-football's reported team
	// name to our match's known home/away team names (same
	// normalisation rules as the fixture mapping sweep).
	teams, err := e.catalog.GetTeams(ctx, []string{m.HomeTeamID, m.AwayTeamID})
	if err != nil {
		return 0, fmt.Errorf("load teams: %w", err)
	}
	homeNorm := normaliseTeam(teams[m.HomeTeamID].Name)
	awayNorm := normaliseTeam(teams[m.AwayTeamID].Name)

	rows := make([]repository.MatchEvent, 0, len(events))
	for _, ev := range events {
		var sidePtr *string
		switch normaliseTeam(ev.TeamName) {
		case homeNorm:
			s := "HOME"
			sidePtr = &s
		case awayNorm:
			s := "AWAY"
			sidePtr = &s
		}
		min := ev.Minute
		desc := ev.Description
		rows = append(rows, repository.MatchEvent{
			ID:              deterministicID("event", apifootball.ProviderName+"|"+ev.ExternalID),
			MatchID:         internalMatchID,
			Provider:        apifootball.ProviderName,
			ExternalID:      &ev.ExternalID,
			EventType:       ev.EventType,
			TeamSide:        sidePtr,
			Minute:          &min,
			AddedMinute:     ev.AddedMinute,
			Description:     &desc,
			EventTime:       observed,
			SourceTimestamp: &observed,
		})
	}
	if err := e.matches.InsertEventsBatch(ctx, rows); err != nil {
		return 0, fmt.Errorf("insert events: %w", err)
	}
	// Rebuild the probability timeline with the real goal timings. We
	// treat this as best-effort: if the rebuild fails the events are
	// still written and the next regular sync will fall back to the
	// HT/FT approximation.
	rebuilt := false
	if e.sync != nil && m.Status == repository.StatusFinished {
		if err := e.sync.RebuildFinishedTimelineFromEvents(ctx, internalMatchID); err != nil {
			if e.logger != nil {
				e.logger.Warn("timeline rebuild failed", "match_id", internalMatchID, "err", err)
			}
		} else {
			rebuilt = true
		}
	}
	if e.logger != nil {
		e.logger.Info("events refreshed",
			"match_id", internalMatchID,
			"provider", apifootball.ProviderName,
			"events", len(rows),
			"timeline_rebuilt", rebuilt,
			"remaining_quota", e.provider.RequestsRemainingToday(),
		)
	}
	return len(rows), nil
}

// normaliseTeam strips common suffixes and casing so "Sevilla FC" and
// "Sevilla" and "SEVILLA FC" all collapse to the same key.
func normaliseTeam(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, suf := range []string{" fc", " cf", " sc", " ac", " afc", " cfc"} {
		n = strings.TrimSuffix(n, suf)
	}
	for _, pre := range []string{"fc ", "ac ", "sc ", "cd ", "ca "} {
		n = strings.TrimPrefix(n, pre)
	}
	// Collapse remaining punctuation + whitespace.
	n = strings.NewReplacer(".", "", "'", "", "-", " ").Replace(n)
	n = strings.Join(strings.Fields(n), " ")
	return n
}


func atoiStrict(s string) (int, error) {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("non-numeric character %q", c)
		}
		n = n*10 + int(c-'0')
	}
	if len(s) == 0 {
		return 0, fmt.Errorf("empty string")
	}
	return n, nil
}
