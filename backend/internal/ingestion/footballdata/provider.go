package footballdata

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/ingestion"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)


// ProviderName is the identifier we store in `provider` columns.
const ProviderName = "football-data.org"

// Provider implements ingestion.Provider against football-data.org.
//
// Scope v1 (free tier): PPL, PL, SA, PD, BL1 — the five leagues the
// product currently supports. Anything outside this list is skipped at
// the adapter boundary.
//
// Window is an optional date filter applied to every per-competition
// request. The zero value means "whole current season" and costs one
// request per league regardless of window size, so prefer a narrow
// window for high-frequency live polling.
type Provider struct {
	client     *Client
	comps      []string
	capability []ingestion.Capability
	window     MatchesQuery
}

// SupportedCompetitions is the whitelist of free-tier competitions we
// pull. The order is used for round-robin fetching.
var SupportedCompetitions = []string{"PPL", "PL", "SA", "PD", "BL1"}

// NewProvider returns a Provider using the given client and the
// restricted set of free-tier competitions we care about. Pass a custom
// `comps` to scope further.
func NewProvider(client *Client, comps []string) *Provider {
	return NewProviderWithWindow(client, comps, MatchesQuery{})
}

// NewProviderWithWindow is NewProvider plus a date-window filter applied
// to every per-competition request.
func NewProviderWithWindow(client *Client, comps []string, window MatchesQuery) *Provider {
	if len(comps) == 0 {
		comps = append(comps, SupportedCompetitions...)
	}
	return &Provider{
		client: client,
		comps:  comps,
		window: window,
		capability: []ingestion.Capability{
			ingestion.CapFixtures,
			ingestion.CapLiveState,
		},
	}
}

// Name reports the provider id.
func (*Provider) Name() string { return ProviderName }

// Capabilities reports what the provider can produce. Free tier does not
// expose a goal-level event stream or predictions.
func (p *Provider) Capabilities() []ingestion.Capability {
	out := make([]ingestion.Capability, len(p.capability))
	copy(out, p.capability)
	return out
}

// FetchFixtures returns every scheduled, live, and finished match across
// the configured competitions.
//
// Error semantics:
//   - All competitions failed → returns (nil, non-nil error) with the
//     joined per-competition failures. Caller treats as a hard failure.
//   - At least one succeeded + at least one failed → returns (fixtures,
//     *PartialError). Caller SHOULD surface the error (so the operator
//     knows some leagues were skipped) but can keep the partial fixtures.
//   - All competitions succeeded → returns (fixtures, nil).
func (p *Provider) FetchFixtures(ctx context.Context) ([]ingestion.Fixture, error) {
	out := make([]ingestion.Fixture, 0, 64)
	var errMsgs []string
	okAny := false
	for _, code := range p.comps {
		resp, err := p.client.GetCompetitionMatches(ctx, code, p.window)
		if err != nil {
			errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", code, err))
			continue
		}
		okAny = true
		compName := resp.Competition.Name
		country := resp.Competition.Area.Name
		season := normaliseSeason(resp.Filters.Season)
		for _, m := range resp.Matches {
			fx, err := mapMatch(m, code, compName, country, season)
			if err != nil {
				errMsgs = append(errMsgs, fmt.Sprintf("%s match %d: %v", code, m.ID, err))
				continue
			}
			out = append(out, fx)
		}
	}
	if !okAny && len(errMsgs) > 0 {
		return nil, fmt.Errorf("all competitions failed: %s", strings.Join(errMsgs, "; "))
	}
	if len(errMsgs) > 0 {
		return out, &PartialError{Messages: errMsgs}
	}
	return out, nil
}

// PartialError is returned by FetchFixtures when some (but not all)
// competitions failed. The caller receives the successful fixtures and
// this error so it can decide whether to proceed and how to log.
type PartialError struct {
	Messages []string
}

func (e *PartialError) Error() string {
	return "partial fetch: " + strings.Join(e.Messages, "; ")
}

// FetchMatch fetches a single match by its external id (the stringified
// football-data.org integer id). Used by the per-match live-focus
// refresh — poll a specific match more often than the broad sweep.
func (p *Provider) FetchMatch(ctx context.Context, externalMatchID string) (*ingestion.Fixture, error) {
	id, err := strconv.Atoi(externalMatchID)
	if err != nil {
		return nil, fmt.Errorf("external match id %q is not a football-data.org integer id", externalMatchID)
	}
	m, err := p.client.GetMatch(ctx, id)
	if err != nil {
		return nil, err
	}
	// Competition metadata on single-match responses is thinner than on
	// the per-competition sweep; fall back to the ExternalCompID we
	// already have in catalog. Season from match response if available.
	compCode := ""
	// football-data.org nests competition on the match response. Our
	// APIMatch currently doesn't model that explicitly, so leave the
	// code empty; the sync's upsert path keys on external match id so
	// the match will still find its existing catalog rows.
	fx, err := mapMatch(*m, compCode, "", "", "")
	if err != nil {
		return nil, err
	}
	return &fx, nil
}

// FetchEvents is not supported on the free tier.
func (*Provider) FetchEvents(_ context.Context, _ []string) ([]ingestion.Event, error) {
	return nil, nil
}

// FetchBenchmarkForecasts is not supported on the free tier.
func (*Provider) FetchBenchmarkForecasts(_ context.Context, _ []string) ([]ingestion.BenchmarkForecast, error) {
	return nil, nil
}

// -------- mapping --------

func mapMatch(m APIMatch, compCode, compName, country, season string) (ingestion.Fixture, error) {
	kickoff, err := time.Parse(time.RFC3339, m.UTCDate)
	if err != nil {
		return ingestion.Fixture{}, fmt.Errorf("parse utcDate %q: %w", m.UTCDate, err)
	}
	status := mapStatus(m.Status)
	home, away := 0, 0
	if m.Score.FullTime.Home != nil {
		home = *m.Score.FullTime.Home
	}
	if m.Score.FullTime.Away != nil {
		away = *m.Score.FullTime.Away
	}
	// If the match is still in play / half-time, prefer the live score
	// which football-data.org returns under FullTime for in-play matches
	// as "current" (yes, the field name is misleading; it tracks running
	// score and is finalised only when status becomes FINISHED).
	var finalResult *repository.Outcome
	if status == repository.StatusFinished && m.Score.Winner != nil {
		fr := mapWinner(*m.Score.Winner)
		if fr != "" {
			finalResult = &fr
		}
	}

	fx := ingestion.Fixture{
		ExternalMatchID: strconv.Itoa(m.ID),
		ExternalCompID:  compCode,
		CompetitionName: compName,
		Country:         country,
		Season:          season,
		HomeTeamExtID:   strconv.Itoa(m.HomeTeam.ID),
		HomeTeamName:    m.HomeTeam.Name,
		AwayTeamExtID:   strconv.Itoa(m.AwayTeam.ID),
		AwayTeamName:    m.AwayTeam.Name,
		KickoffAt:       kickoff.UTC(),
		Status:          status,
		HomeScore:       home,
		AwayScore:       away,
		Minute:          m.Minute,
		FinalResult:     finalResult,
		// HalfTime is populated once the match has reached half-time or
		// later. Before HT both pointers in the payload are nil and we
		// carry that nil through so the sync layer knows no HT snapshot
		// should be generated yet.
		HalfTimeHome:    m.Score.HalfTime.Home,
		HalfTimeAway:    m.Score.HalfTime.Away,
		SourceUpdatedAt: parseLastUpdated(m.LastUpdated, kickoff),
	}
	return fx, nil
}

func mapStatus(s string) repository.MatchStatus {
	switch strings.ToUpper(s) {
	case "SCHEDULED", "TIMED":
		return repository.StatusScheduled
	case "LIVE", "IN_PLAY":
		return repository.StatusLive
	case "PAUSED":
		return repository.StatusHalftime
	case "FINISHED", "AWARDED":
		return repository.StatusFinished
	case "POSTPONED", "SUSPENDED":
		return repository.StatusPostponed
	case "CANCELED", "CANCELLED":
		return repository.StatusCancelled
	default:
		return repository.StatusScheduled
	}
}

func mapWinner(w string) repository.Outcome {
	switch strings.ToUpper(w) {
	case "HOME_TEAM":
		return repository.OutcomeHome
	case "AWAY_TEAM":
		return repository.OutcomeAway
	case "DRAW":
		return repository.OutcomeDraw
	}
	return ""
}

func parseLastUpdated(s string, fallback time.Time) time.Time {
	if s == "" {
		return fallback.UTC()
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fallback.UTC()
	}
	return t.UTC()
}

// normaliseSeason converts a filter like "2026" into "2026/27" so the
// value we store looks like the rest of the codebase. If the input
// already contains a slash or is empty, it's returned as-is.
func normaliseSeason(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "/") {
		return s
	}
	year, err := strconv.Atoi(s)
	if err != nil {
		return s
	}
	next := (year + 1) % 100
	return fmt.Sprintf("%d/%02d", year, next)
}
