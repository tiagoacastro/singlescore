// Package footballdata is a thin, free-tier-aware client for the
// football-data.org v4 REST API.
//
// Rate limit for the free tier is 10 requests per minute per API token.
// This client enforces that locally so a tight polling loop can't
// accidentally burn the quota.
//
// Only the subset of endpoints we actually need is implemented:
//
//	GET /v4/competitions/{code}/matches
//
// Everything else is deliberately omitted — add endpoints when a
// consuming job needs them, not before.
package footballdata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the production football-data.org v4 base URL.
const DefaultBaseURL = "https://api.football-data.org/v4"

// Client is a free-tier-safe football-data.org v4 client.
//
// Rate limiting is layered:
//
//  1. A local token-bucket enforces at most N requests per minute
//     (default 10, the free-tier limit at time of writing). This is a
//     floor — the client will never fire faster than this even if the
//     server would allow it.
//
//  2. After every response the client reads the server's own
//     `x-requests-available-minute` and `X-RequestCounter-Reset` headers.
//     If the server reports zero available, the client sleeps for the
//     reset window before releasing the next request. This is the
//     authoritative source of truth and copes gracefully if the account
//     tier changes or the API tightens the quota.
//
//  3. On HTTP 429 the client honours `Retry-After` (or falls back to the
//     reset window) and the caller sees an APIError they can retry.
type Client struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
	limiter    *minuteLimiter
	gate       *serverGate
}

// Options configures a Client. BaseURL defaults to DefaultBaseURL and
// HTTPClient defaults to a client with a 15 s timeout. RequestsPerMinute
// defaults to the free-tier limit of 10.
type Options struct {
	BaseURL           string
	HTTPClient        *http.Client
	RequestsPerMinute int
}

// New returns a Client. apiToken must not be empty.
func New(apiToken string, opts Options) (*Client, error) {
	if strings.TrimSpace(apiToken) == "" {
		return nil, fmt.Errorf("footballdata: apiToken is required")
	}
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	rpm := opts.RequestsPerMinute
	if rpm <= 0 {
		rpm = 10
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiToken:   apiToken,
		httpClient: httpClient,
		limiter:    newMinuteLimiter(rpm),
		gate:       newServerGate(),
	}, nil
}

// APIError is returned when the server responds with a non-2xx status.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	return fmt.Sprintf("footballdata: HTTP %d: %s", e.StatusCode, msg)
}

// MatchesResponse mirrors the subset of GET /v4/competitions/{code}/matches
// we actually read. Fields the application doesn't use are left out so
// the parser is cheap and resilient to upstream additions.
type MatchesResponse struct {
	Competition struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Code   string `json:"code"`
		Area   struct {
			Name string `json:"name"`
		} `json:"area"`
		Emblem string `json:"emblem"`
	} `json:"competition"`
	Filters struct {
		Season string `json:"season"`
	} `json:"filters"`
	ResultSet struct {
		Count int `json:"count"`
	} `json:"resultSet"`
	Matches []APIMatch `json:"matches"`
}

// APIMatch is a single match row from the matches endpoint.
type APIMatch struct {
	ID          int    `json:"id"`
	UTCDate     string `json:"utcDate"`
	Status      string `json:"status"`
	Minute      *int   `json:"minute,omitempty"`
	Matchday    int    `json:"matchday"`
	Stage       string `json:"stage"`
	LastUpdated string `json:"lastUpdated"`
	HomeTeam    APITeam `json:"homeTeam"`
	AwayTeam    APITeam `json:"awayTeam"`
	Score       struct {
		Winner   *string `json:"winner,omitempty"` // HOME_TEAM, AWAY_TEAM, DRAW, null
		Duration string  `json:"duration"`
		FullTime APIScore `json:"fullTime"`
		HalfTime APIScore `json:"halfTime"`
	} `json:"score"`
	Season struct {
		ID        int    `json:"id"`
		StartDate string `json:"startDate"`
	} `json:"season"`
}

// APITeam is a nested team reference on a match.
type APITeam struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"shortName"`
	Tla       string `json:"tla"`
	Crest     string `json:"crest"`
}

// APIScore mirrors the full-time / half-time score.
type APIScore struct {
	Home *int `json:"home,omitempty"`
	Away *int `json:"away,omitempty"`
}

// MatchesQuery narrows a /competitions/{code}/matches request. Zero
// values mean "unfiltered". DateFrom and DateTo are inclusive ISO-8601
// dates (YYYY-MM-DD, UTC).
type MatchesQuery struct {
	DateFrom time.Time
	DateTo   time.Time
	Status   []string // e.g. ["SCHEDULED","LIVE","IN_PLAY","PAUSED"]
}

// GetCompetitionMatches fetches matches for the given competition code
// (e.g. "PL", "PD", "SA", "BL1", "PPL"). An empty MatchesQuery returns
// the full current season (equivalent to the old no-arg API). Use a
// narrow query for live-scoreboard polling to spend one request per
// league instead of refetching the archive on every tick.
func (c *Client) GetCompetitionMatches(ctx context.Context, competitionCode string, q MatchesQuery) (*MatchesResponse, error) {
	if competitionCode == "" {
		return nil, fmt.Errorf("footballdata: competitionCode is required")
	}
	u := fmt.Sprintf("%s/competitions/%s/matches", c.baseURL, url.PathEscape(competitionCode))
	if qs := q.encode(); qs != "" {
		u += "?" + qs
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.apiToken)
	req.Header.Set("Accept", "application/json")

	if err := c.limiter.wait(ctx); err != nil {
		return nil, err
	}
	if err := c.gate.wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("footballdata: GET %s: %w", u, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("footballdata: read body: %w", err)
	}
	// Record quota + reset even on error responses so a 429 teaches the
	// gate the correct back-off window.
	c.gate.observe(resp.Header)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    extractErrorMessage(body),
			Body:       truncate(string(body), 500),
		}
	}
	var out MatchesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("footballdata: decode response: %w", err)
	}
	return &out, nil
}

// GetMatch fetches a single match by its football-data.org match id.
// Used by the per-match live-focus refresh path, which polls more
// frequently than the broad fixtures sweep.
func (c *Client) GetMatch(ctx context.Context, matchID int) (*APIMatch, error) {
	if matchID <= 0 {
		return nil, fmt.Errorf("footballdata: matchID must be > 0")
	}
	u := fmt.Sprintf("%s/matches/%d", c.baseURL, matchID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.apiToken)
	req.Header.Set("Accept", "application/json")

	if err := c.limiter.wait(ctx); err != nil {
		return nil, err
	}
	if err := c.gate.wait(ctx); err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("footballdata: GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("footballdata: read body: %w", err)
	}
	c.gate.observe(resp.Header)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Message:    extractErrorMessage(body),
			Body:       truncate(string(body), 500),
		}
	}
	// GET /v4/matches/{id} returns the match wrapped as either a top-
	// level APIMatch or inside {"match": {...}} depending on API version.
	// Try the raw shape first, then fall back to the wrapper.
	var raw APIMatch
	if err := json.Unmarshal(body, &raw); err == nil && raw.ID != 0 {
		return &raw, nil
	}
	var wrapper struct {
		Match APIMatch `json:"match"`
	}
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, fmt.Errorf("footballdata: decode match: %w", err)
	}
	if wrapper.Match.ID == 0 {
		return nil, fmt.Errorf("footballdata: match response has no id")
	}
	return &wrapper.Match, nil
}

func (q MatchesQuery) encode() string {
	parts := []string{}
	if !q.DateFrom.IsZero() {
		parts = append(parts, "dateFrom="+q.DateFrom.UTC().Format("2006-01-02"))
	}
	if !q.DateTo.IsZero() {
		parts = append(parts, "dateTo="+q.DateTo.UTC().Format("2006-01-02"))
	}
	if len(q.Status) > 0 {
		parts = append(parts, "status="+strings.Join(q.Status, ","))
	}
	return strings.Join(parts, "&")
}

func extractErrorMessage(body []byte) string {
	var payload struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		if payload.Message != "" {
			return payload.Message
		}
		if payload.Error != "" {
			return payload.Error
		}
	}
	return ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// -------- server-reported quota gate --------

// serverGate tracks the server-reported quota (x-requests-available-minute
// + X-RequestCounter-Reset, and Retry-After on 429) and blocks the next
// request when the account's bucket is empty. It is strictly in addition
// to minuteLimiter: the local bucket is a floor, the server gate is the
// authoritative ceiling.
type serverGate struct {
	mu         sync.Mutex
	notBefore  time.Time
	available  int // -1 = unknown
	resetAt    time.Time
}

func newServerGate() *serverGate {
	return &serverGate{available: -1}
}

func (g *serverGate) wait(ctx context.Context) error {
	for {
		g.mu.Lock()
		now := time.Now()
		if !g.notBefore.IsZero() && g.notBefore.After(now) {
			sleep := g.notBefore.Sub(now)
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
			continue
		}
		g.mu.Unlock()
		return nil
	}
}

// observe reads rate-limit headers from a response and updates the gate.
// Called on every response (success or error) so a 429 teaches us the
// right back-off window.
func (g *serverGate) observe(h http.Header) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := time.Now()

	// X-RequestCounter-Reset is in whole seconds until the current window
	// resets; it is present on virtually every football-data.org response.
	resetSec := atoiHeader(h.Get("X-RequestCounter-Reset"))
	if resetSec > 0 {
		g.resetAt = now.Add(time.Duration(resetSec) * time.Second)
	}
	// x-requests-available-minute reports what the account has left in
	// the current minute window. 0 means the next request will 429.
	avail := atoiHeader(h.Get("x-requests-available-minute"))
	if avail >= 0 {
		g.available = avail
		if avail == 0 && !g.resetAt.IsZero() {
			g.notBefore = g.resetAt
		}
	}
	// Retry-After is server-authoritative; honour it over our own math.
	if ra := atoiHeader(h.Get("Retry-After")); ra > 0 {
		g.notBefore = now.Add(time.Duration(ra) * time.Second)
	}
}

// Available returns the last-observed quota (or -1 if we haven't talked
// to the server yet). Primarily useful for observability / tests.
func (c *Client) Available() int {
	c.gate.mu.Lock()
	defer c.gate.mu.Unlock()
	return c.gate.available
}

func atoiHeader(s string) int {
	if s == "" {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return -1
	}
	return n
}

// -------- local token-bucket --------

type minuteLimiter struct {
	mu       sync.Mutex
	window   time.Duration
	capacity int
	events   []time.Time
}

func newMinuteLimiter(rpm int) *minuteLimiter {
	return &minuteLimiter{window: time.Minute, capacity: rpm}
}

// wait blocks until a request slot is available, respecting ctx cancel.
func (l *minuteLimiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-l.window)
		// Drop expired events.
		kept := l.events[:0]
		for _, t := range l.events {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		l.events = kept
		if len(l.events) < l.capacity {
			l.events = append(l.events, now)
			l.mu.Unlock()
			return nil
		}
		// Oldest event + window = next free slot.
		next := l.events[0].Add(l.window).Sub(now)
		l.mu.Unlock()
		if next <= 0 {
			next = 10 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(next):
		}
	}
}
