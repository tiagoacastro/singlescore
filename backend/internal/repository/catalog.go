package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Catalog exposes read/write access to the competitions and teams tables.
type Catalog struct {
	pool *sql.DB
}

// NewCatalog returns a Catalog bound to the given pool.
func NewCatalog(pool *sql.DB) *Catalog { return &Catalog{pool: pool} }

// UpsertCompetition inserts a single competition. Delegates to the batch
// implementation for consistency.
func (c *Catalog) UpsertCompetition(ctx context.Context, comp Competition) error {
	return c.UpsertCompetitionBatch(ctx, []Competition{comp})
}

// UpsertCompetitionBatch upserts many competitions in one round-trip.
func (c *Catalog) UpsertCompetitionBatch(ctx context.Context, rows []Competition) error {
	if len(rows) == 0 {
		return nil
	}
	const cols = 7
	tuple := "(" + strings.Repeat("?, ", cols-1) + "?),"
	placeholders := strings.Repeat(tuple, len(rows))
	placeholders = placeholders[:len(placeholders)-1]
	q := `INSERT INTO competitions
		  (id, provider, external_id, name, country, season, logo_url)
		VALUES ` + placeholders + `
		ON DUPLICATE KEY UPDATE
		  provider=VALUES(provider),
		  external_id=VALUES(external_id),
		  name=VALUES(name),
		  country=VALUES(country),
		  season=VALUES(season),
		  logo_url=VALUES(logo_url),
		  updated_at=CURRENT_TIMESTAMP(6)`
	args := make([]any, 0, len(rows)*cols)
	for _, r := range rows {
		args = append(args, r.ID, r.Provider, r.ExternalID, r.Name, r.Country, r.Season, r.LogoURL)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := c.pool.ExecContext(ctx, q, args...)
	return err
}

// UpsertTeam inserts a single team. Delegates to the batch implementation.
func (c *Catalog) UpsertTeam(ctx context.Context, t Team) error {
	return c.UpsertTeamBatch(ctx, []Team{t})
}

// UpsertTeamBatch upserts many teams in one round-trip.
func (c *Catalog) UpsertTeamBatch(ctx context.Context, rows []Team) error {
	if len(rows) == 0 {
		return nil
	}
	const cols = 8
	tuple := "(" + strings.Repeat("?, ", cols-1) + "?),"
	placeholders := strings.Repeat(tuple, len(rows))
	placeholders = placeholders[:len(placeholders)-1]
	q := `INSERT INTO teams
		  (id, provider, external_id, name, short_name, country, logo_url, elo_rating)
		VALUES ` + placeholders + `
		ON DUPLICATE KEY UPDATE
		  provider=VALUES(provider),
		  external_id=VALUES(external_id),
		  name=VALUES(name),
		  short_name=VALUES(short_name),
		  country=VALUES(country),
		  logo_url=VALUES(logo_url),
		  elo_rating=VALUES(elo_rating),
		  updated_at=CURRENT_TIMESTAMP(6)`
	args := make([]any, 0, len(rows)*cols)
	for _, r := range rows {
		args = append(args, r.ID, r.Provider, r.ExternalID, r.Name, r.ShortName, r.Country, r.LogoURL, r.EloRating)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := c.pool.ExecContext(ctx, q, args...)
	return err
}

// ListCompetitions returns competitions ordered by name.
func (c *Catalog) ListCompetitions(ctx context.Context) ([]Competition, error) {
	const q = `
		SELECT id, provider, external_id, name, country, season, logo_url, created_at, updated_at
		FROM competitions
		ORDER BY name ASC
	`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := c.pool.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("query competitions: %w", err)
	}
	defer rows.Close()

	var out []Competition
	for rows.Next() {
		var r Competition
		if err := rows.Scan(&r.ID, &r.Provider, &r.ExternalID, &r.Name, &r.Country, &r.Season, &r.LogoURL, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetTeams returns the teams whose ids are in the provided set, keyed by id.
// ListTeams returns every team row. Used by jobs that need to resolve
// team-id → name in bulk (e.g. the api-football fixture-mapping sweep,
// which keys on team names). Cheap at MVP scale (hundreds of teams).
func (c *Catalog) ListTeams(ctx context.Context) ([]Team, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := c.pool.QueryContext(ctx, `
		SELECT id, provider, external_id, name, short_name, country, logo_url,
		       elo_rating, created_at, updated_at
		FROM teams
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Team
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Provider, &t.ExternalID, &t.Name, &t.ShortName,
			&t.Country, &t.LogoURL, &t.EloRating, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (c *Catalog) GetTeams(ctx context.Context, ids []string) (map[string]Team, error) {
	if len(ids) == 0 {
		return map[string]Team{}, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT id, provider, external_id, name, short_name, country, logo_url, elo_rating, created_at, updated_at
		FROM teams WHERE id IN (%s)
	`, placeholders)

	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := c.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query teams: %w", err)
	}
	defer rows.Close()

	out := make(map[string]Team, len(ids))
	for rows.Next() {
		var r Team
		if err := rows.Scan(&r.ID, &r.Provider, &r.ExternalID, &r.Name, &r.ShortName, &r.Country, &r.LogoURL, &r.EloRating, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

// GetCompetitions returns competitions keyed by id for a given id set.
func (c *Catalog) GetCompetitions(ctx context.Context, ids []string) (map[string]Competition, error) {
	if len(ids) == 0 {
		return map[string]Competition{}, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT id, provider, external_id, name, country, season, logo_url, created_at, updated_at
		FROM competitions WHERE id IN (%s)
	`, placeholders)
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := c.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query competitions by id: %w", err)
	}
	defer rows.Close()
	out := make(map[string]Competition, len(ids))
	for rows.Next() {
		var r Competition
		if err := rows.Scan(&r.ID, &r.Provider, &r.ExternalID, &r.Name, &r.Country, &r.Season, &r.LogoURL, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}
