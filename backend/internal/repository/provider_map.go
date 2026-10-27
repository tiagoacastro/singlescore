package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ProviderMatchMap is one row in provider_match_map — a mapping between
// our internal match id and some secondary provider's fixture id. Used
// by the api-football.com adapter so per-event lookups don't require a
// separate "find the fixture" API call.
type ProviderMatchMap struct {
	InternalMatchID   string
	Provider          string
	ExternalFixtureID string
	MappedAt          time.Time
}

// ProviderMap exposes read/write access to provider_match_map.
type ProviderMap struct {
	pool *sql.DB
}

// NewProviderMap returns a ProviderMap bound to the given pool.
func NewProviderMap(pool *sql.DB) *ProviderMap {
	return &ProviderMap{pool: pool}
}

// Get returns the mapping for (internalMatchID, provider), or nil if
// none is stored. Returns an error only for driver-level failures;
// missing rows are modelled as (nil, nil).
func (p *ProviderMap) Get(ctx context.Context, internalMatchID, provider string) (*ProviderMatchMap, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	row := p.pool.QueryRowContext(ctx, `
		SELECT internal_match_id, provider, external_fixture_id, mapped_at
		FROM provider_match_map
		WHERE internal_match_id = ? AND provider = ?
	`, internalMatchID, provider)
	var m ProviderMatchMap
	if err := row.Scan(&m.InternalMatchID, &m.Provider, &m.ExternalFixtureID, &m.MappedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// UpsertBatch inserts or updates many mappings in one round-trip. Safe
// to re-run (ON DUPLICATE KEY UPDATE overwrites the external id if a
// later mapping run disagrees with the previous one — this is the
// desired behaviour: the latest mapping wins).
func (p *ProviderMap) UpsertBatch(ctx context.Context, rows []ProviderMatchMap) error {
	if len(rows) == 0 {
		return nil
	}
	const cols = 3
	tuple := "(" + strings.Repeat("?, ", cols-1) + "?),"
	placeholders := strings.Repeat(tuple, len(rows))
	placeholders = placeholders[:len(placeholders)-1]
	q := `INSERT INTO provider_match_map
		  (internal_match_id, provider, external_fixture_id)
		VALUES ` + placeholders + `
		ON DUPLICATE KEY UPDATE
		  external_fixture_id = VALUES(external_fixture_id),
		  mapped_at = CURRENT_TIMESTAMP(6)`
	args := make([]any, 0, len(rows)*cols)
	for _, r := range rows {
		args = append(args, r.InternalMatchID, r.Provider, r.ExternalFixtureID)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := p.pool.ExecContext(ctx, q, args...); err != nil {
		return fmt.Errorf("upsert provider_match_map batch: %w", err)
	}
	return nil
}

// UnmappedFinishedMatchDates returns distinct UTC calendar dates
// (YYYY-MM-DD) on which a FINISHED match exists in our DB but no
// mapping row exists for the given provider. Newest dates first.
// Used to drive the date-based api-football mapping sweep: one API
// call per returned date.
func (p *ProviderMap) UnmappedFinishedMatchDates(ctx context.Context, provider string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, `
		SELECT DISTINCT DATE(m.kickoff_at) AS d
		FROM matches m
		LEFT JOIN provider_match_map pm
		  ON pm.internal_match_id = m.id AND pm.provider = ?
		WHERE m.status = 'FINISHED'
		  AND pm.internal_match_id IS NULL
		ORDER BY d DESC
		LIMIT ?
	`, provider, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d time.Time
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d.UTC().Format("2006-01-02"))
	}
	return out, rows.Err()
}

// MappedWithoutEvents returns the ids of FINISHED matches that have a
// mapping row for the given provider but ZERO events stored for that
// provider yet. This is the backfill queue — exactly the matches we
// should spend our daily API budget on, newest kickoff first.
func (p *ProviderMap) MappedWithoutEvents(ctx context.Context, provider string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Left-anti join: match is mapped, no event rows exist from the
	// same provider. Restricted to FINISHED so we don't waste quota
	// on scheduled matches that have no events to fetch yet.
	rows, err := p.pool.QueryContext(ctx, `
		SELECT m.id
		FROM matches m
		INNER JOIN provider_match_map pm
		  ON pm.internal_match_id = m.id AND pm.provider = ?
		LEFT JOIN match_events me
		  ON me.match_id = m.id AND me.provider = ?
		WHERE m.status = 'FINISHED'
		  AND me.id IS NULL
		ORDER BY m.kickoff_at DESC
		LIMIT ?
	`, provider, provider, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UnmappedForProvider returns the ids of matches that have no mapping
// row for the given provider yet. Useful for driving a backfill loop:
// "give me every match I still need to look up".
func (p *ProviderMap) UnmappedForProvider(ctx context.Context, provider string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, `
		SELECT m.id
		FROM matches m
		LEFT JOIN provider_match_map pm
		  ON pm.internal_match_id = m.id AND pm.provider = ?
		WHERE pm.internal_match_id IS NULL
		ORDER BY m.kickoff_at DESC
		LIMIT ?
	`, provider, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
