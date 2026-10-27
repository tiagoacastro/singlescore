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

// UpsertCompetition inserts a competition by id or updates it if it exists.
func (c *Catalog) UpsertCompetition(ctx context.Context, comp Competition) error {
	const q = `
		INSERT INTO competitions
		  (id, provider, external_id, name, country, season, logo_url)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		  provider=VALUES(provider),
		  external_id=VALUES(external_id),
		  name=VALUES(name),
		  country=VALUES(country),
		  season=VALUES(season),
		  logo_url=VALUES(logo_url),
		  updated_at=CURRENT_TIMESTAMP(6)
	`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := c.pool.ExecContext(ctx, q, comp.ID, comp.Provider, comp.ExternalID, comp.Name, comp.Country, comp.Season, comp.LogoURL)
	return err
}

// UpsertTeam inserts a team by id or updates it if it exists.
func (c *Catalog) UpsertTeam(ctx context.Context, t Team) error {
	const q = `
		INSERT INTO teams
		  (id, provider, external_id, name, short_name, country, logo_url, elo_rating)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
		  provider=VALUES(provider),
		  external_id=VALUES(external_id),
		  name=VALUES(name),
		  short_name=VALUES(short_name),
		  country=VALUES(country),
		  logo_url=VALUES(logo_url),
		  elo_rating=VALUES(elo_rating),
		  updated_at=CURRENT_TIMESTAMP(6)
	`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := c.pool.ExecContext(ctx, q, t.ID, t.Provider, t.ExternalID, t.Name, t.ShortName, t.Country, t.LogoURL, t.EloRating)
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
