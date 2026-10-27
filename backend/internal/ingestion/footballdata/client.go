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
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the production football-data.org v4 base URL.
const DefaultBaseURL = "https://api.football-data.org/v4"

// Client is a free-tier-safe football-data.org v4 client.
type Client struct {
	baseURL    string
	apiToken   string
	httpClient *http.Client
	limiter    *minuteLimiter
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

// GetCompetitionMatches fetches every match for the given competition
// code (e.g. "PL", "PD", "SA", "BL1", "PPL"). The free tier returns the
// current season by default.
func (c *Client) GetCompetitionMatches(ctx context.Context, competitionCode string) (*MatchesResponse, error) {
	if competitionCode == "" {
		return nil, fmt.Errorf("footballdata: competitionCode is required")
	}
	u := fmt.Sprintf("%s/competitions/%s/matches", c.baseURL, url.PathEscape(competitionCode))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Auth-Token", c.apiToken)
	req.Header.Set("Accept", "application/json")

	if err := c.limiter.wait(ctx); err != nil {
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

// -------- rate limiter --------

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
