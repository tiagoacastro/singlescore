// Package apifootball is a thin, free-tier-aware client for the
// api-football.com v3 REST API, used specifically for per-event data
// (goals, cards, substitutions) that the football-data.org free tier
// does not expose.
//
// Free tier: 100 requests per day per API token. We also enforce a
// local 10 req/min floor so a buggy loop can't DoS the account.
//
// Only the subset of endpoints we actually need is implemented:
//
//	GET /v3/fixtures?date=YYYY-MM-DD   — list fixtures on a given day
//	GET /v3/fixtures/events?fixture=ID — list events for one fixture
//
// Add endpoints when a consuming job needs them, not before.
package apifootball

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

// DefaultBaseURL is the production api-sports direct base URL (not the
// RapidAPI gateway, which uses a different host and auth header).
const DefaultBaseURL = "https://v3.football.api-sports.io"

// DefaultDailyRequestLimit is the api-football free-tier quota. We
// treat it as a hard local ceiling — once hit, further calls block
// until the daily counter resets at UTC midnight.
const DefaultDailyRequestLimit = 100

// Client is a free-tier-safe api-football v3 client.
//
// Rate limiting is layered just like the football-data.org client:
//
//  1. A local token-bucket enforces at most RequestsPerMinute (default
//     10) — a floor the client never breaches, regardless of the
//     server's willingness.
//  2. A daily counter stops firing once DefaultDailyRequestLimit calls
//     have been consumed in a UTC day, so unattended loops can't blow
//     the 100 req/day free-tier budget.
//  3. On 429 the client honours Retry-After.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
	limiter    *minuteLimiter
	daily      *dailyLimiter
}

// Options configures a Client. BaseURL defaults to DefaultBaseURL.
// HTTPClient defaults to a 15s-timeout client. RequestsPerMinute
// defaults to 10. DailyLimit defaults to DefaultDailyRequestLimit.
type Options struct {
	BaseURL           string
	HTTPClient        *http.Client
	RequestsPerMinute int
	DailyLimit        int
}

// New returns a Client. apiKey must not be empty.
func New(apiKey string, opts Options) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("apifootball: apiKey is required")
	}
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	rpm := opts.RequestsPerMinute
	if rpm <= 0 {
		rpm = 10
	}
	daily := opts.DailyLimit
	if daily <= 0 {
		daily = DefaultDailyRequestLimit
	}
	return &Client{
		baseURL:    strings.TrimRight(base, "/"),
		apiKey:     apiKey,
		httpClient: hc,
		limiter:    newMinuteLimiter(rpm),
		daily:      newDailyLimiter(daily),
	}, nil
}

// APIError is returned when the server responds with a non-2xx status.
type APIError struct {
	StatusCode int
	Body       string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("apifootball: HTTP %d: %s", e.StatusCode, e.Body)
}

// RequestsRemainingToday returns how many requests are left in today's
// local budget. Useful for logging / dashboards.
func (c *Client) RequestsRemainingToday() int {
	return c.daily.remaining()
}

// APIFixture is one entry in /fixtures response.
type APIFixture struct {
	Fixture struct {
		ID        int    `json:"id"`
		Date      string `json:"date"`
		Timestamp int64  `json:"timestamp"`
		Status    struct {
			Short string `json:"short"`
			Long  string `json:"long"`
		} `json:"status"`
	} `json:"fixture"`
	League struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"league"`
	Teams struct {
		Home struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"home"`
		Away struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"away"`
	} `json:"teams"`
	Goals struct {
		Home *int `json:"home"`
		Away *int `json:"away"`
	} `json:"goals"`
}

// APIEvent is one entry in /fixtures/events response.
type APIEvent struct {
	Time struct {
		Elapsed int  `json:"elapsed"`
		Extra   *int `json:"extra"`
	} `json:"time"`
	Team struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"team"`
	Player struct {
		ID   *int    `json:"id"`
		Name *string `json:"name"`
	} `json:"player"`
	Assist struct {
		ID   *int    `json:"id"`
		Name *string `json:"name"`
	} `json:"assist"`
	Type     string  `json:"type"`
	Detail   string  `json:"detail"`
	Comments *string `json:"comments"`
}

// apiEnvelope is the outer wrapper api-football wraps every response in.
type apiEnvelope struct {
	Get      string          `json:"get"`
	Errors   json.RawMessage `json:"errors"`
	Results  int             `json:"results"`
	Response json.RawMessage `json:"response"`
}

// ListFixturesByDate returns every api-football fixture scheduled on
// the given UTC date. Can be filtered to a specific league/season.
func (c *Client) ListFixturesByDate(ctx context.Context, date string, league, season int) ([]APIFixture, error) {
	q := url.Values{}
	q.Set("date", date)
	if league > 0 {
		q.Set("league", strconv.Itoa(league))
	}
	if season > 0 {
		q.Set("season", strconv.Itoa(season))
	}
	var out []APIFixture
	if err := c.getJSON(ctx, "/fixtures?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// listFixturesByLeagueSeason returns every fixture in a league/season.
// Costs exactly one API call. Used by the fixture-mapping sweep.
func (c *Client) listFixturesByLeagueSeason(ctx context.Context, league, season int) ([]APIFixture, error) {
	q := url.Values{}
	q.Set("league", strconv.Itoa(league))
	q.Set("season", strconv.Itoa(season))
	var out []APIFixture
	if err := c.getJSON(ctx, "/fixtures?"+q.Encode(), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListEventsForFixture returns all goals/cards/subs/etc. for a fixture.
func (c *Client) ListEventsForFixture(ctx context.Context, fixtureID int) ([]APIEvent, error) {
	var out []APIEvent
	if err := c.getJSON(ctx, "/fixtures/events?fixture="+strconv.Itoa(fixtureID), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// getJSON is the single HTTP entry point; everything flows through it so
// the rate limiting + error handling is consistent.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	if err := c.limiter.wait(ctx); err != nil {
		return err
	}
	if err := c.daily.wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-apisports-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		ra := parseRetryAfter(resp.Header.Get("Retry-After"))
		return &APIError{StatusCode: resp.StatusCode, Body: string(body), RetryAfter: ra}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	var env apiEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	// api-football's `errors` field is a quirky polymorphic shape:
	// `[]` when empty, `{"token": "..."}` when there's a problem.
	// We treat anything non-empty-array as a hard error.
	if len(env.Errors) > 0 && string(env.Errors) != "[]" && string(env.Errors) != "{}" {
		return fmt.Errorf("apifootball error payload: %s", string(env.Errors))
	}
	// Decode `response` into the caller's target. Response is always an
	// array; the caller supplies a pointer-to-slice.
	if len(env.Response) == 0 {
		return nil
	}
	return json.Unmarshal(env.Response, out)
}

// -----------------------------------------------------------------------
// rate limiter: minute token bucket
// -----------------------------------------------------------------------

type minuteLimiter struct {
	mu       sync.Mutex
	capacity int
	windows  []time.Time
}

func newMinuteLimiter(rpm int) *minuteLimiter {
	return &minuteLimiter{capacity: rpm}
}

func (l *minuteLimiter) wait(ctx context.Context) error {
	for {
		l.mu.Lock()
		now := time.Now()
		cutoff := now.Add(-time.Minute)
		// Drop timestamps older than one minute.
		kept := l.windows[:0]
		for _, t := range l.windows {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		l.windows = kept
		if len(l.windows) < l.capacity {
			l.windows = append(l.windows, now)
			l.mu.Unlock()
			return nil
		}
		// Would exceed capacity; sleep until oldest timestamp expires.
		wait := time.Until(l.windows[0].Add(time.Minute)) + 50*time.Millisecond
		l.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// -----------------------------------------------------------------------
// rate limiter: daily counter
// -----------------------------------------------------------------------

type dailyLimiter struct {
	mu       sync.Mutex
	capacity int
	day      string
	consumed int
}

func newDailyLimiter(cap int) *dailyLimiter {
	return &dailyLimiter{capacity: cap, day: time.Now().UTC().Format("2006-01-02")}
}

func (d *dailyLimiter) remaining() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.rollIfNeeded()
	left := d.capacity - d.consumed
	if left < 0 {
		return 0
	}
	return left
}

func (d *dailyLimiter) wait(ctx context.Context) error {
	for {
		d.mu.Lock()
		d.rollIfNeeded()
		if d.consumed < d.capacity {
			d.consumed++
			d.mu.Unlock()
			return nil
		}
		// Already blew the daily budget. Sleep until next UTC midnight.
		next := nextUTCMidnight(time.Now().UTC())
		wait := time.Until(next) + 500*time.Millisecond
		d.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (d *dailyLimiter) rollIfNeeded() {
	today := time.Now().UTC().Format("2006-01-02")
	if today != d.day {
		d.day = today
		d.consumed = 0
	}
}

func nextUTCMidnight(t time.Time) time.Time {
	y, m, day := t.Date()
	return time.Date(y, m, day+1, 0, 0, 0, 0, time.UTC)
}

// parseRetryAfter accepts seconds-as-int or HTTP-date; returns 0 for
// anything unparseable (caller should fall back to its own backoff).
func parseRetryAfter(s string) time.Duration {
	if s == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(s); err == nil {
		d := time.Until(t)
		if d > 0 {
			return d
		}
	}
	return 0
}
