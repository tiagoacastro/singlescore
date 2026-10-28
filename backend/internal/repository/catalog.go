package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Catalog exposes read access to the competitions and teams tables.
type Catalog struct {
	pool *sql.DB
}

// NewCatalog returns a Catalog bound to the given pool.
func NewCatalog(pool *sql.DB) *Catalog { return &Catalog{pool: pool} }

const competitionColumns = "id, name, country, season, logo_url"

func (c *Catalog) queryCompetitions(ctx context.Context, q string, args ...any) ([]Competition, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := c.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Competition
	for rows.Next() {
		var r Competition
		if err := rows.Scan(&r.ID, &r.Name, &r.Country, &r.Season, &r.LogoURL); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListCompetitions returns competitions ordered by name.
func (c *Catalog) ListCompetitions(ctx context.Context) ([]Competition, error) {
	out, err := c.queryCompetitions(ctx, "SELECT "+competitionColumns+" FROM competitions ORDER BY name, season DESC")
	if err != nil {
		return nil, fmt.Errorf("list competitions: %w", err)
	}
	return out, nil
}

// GetCompetitions returns competitions keyed by id for a given id set.
func (c *Catalog) GetCompetitions(ctx context.Context, ids []string) (map[string]Competition, error) {
	out := make(map[string]Competition, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	in, args := inClause(ids)
	rows, err := c.queryCompetitions(ctx,
		fmt.Sprintf("SELECT %s FROM competitions WHERE id IN (%s)", competitionColumns, in), args...)
	if err != nil {
		return nil, fmt.Errorf("get competitions: %w", err)
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// GetTeams returns teams keyed by id for a given id set.
func (c *Catalog) GetTeams(ctx context.Context, ids []string) (map[string]Team, error) {
	out := make(map[string]Team, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	in, args := inClause(ids)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := c.pool.QueryContext(ctx, fmt.Sprintf(
		"SELECT id, name, short_name, country, logo_url, elo_rating FROM teams WHERE id IN (%s)", in), args...)
	if err != nil {
		return nil, fmt.Errorf("get teams: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r Team
		if err := rows.Scan(&r.ID, &r.Name, &r.ShortName, &r.Country, &r.LogoURL, &r.EloRating); err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}
