package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Ingestion exposes read access to the data_ingestion_runs table.
type Ingestion struct {
	pool *sql.DB
}

// NewIngestion returns an Ingestion repo.
func NewIngestion(pool *sql.DB) *Ingestion { return &Ingestion{pool: pool} }

// LatestPerJob returns the most recent run of each (provider, job_name),
// newest first.
func (i *Ingestion) LatestPerJob(ctx context.Context, limit int) ([]IngestionRun, error) {
	if limit <= 0 {
		limit = 20
	}
	const q = `
		SELECT r.provider, r.job_name, r.status, r.started_at,
		       r.records_in, r.records_out, r.error_message
		FROM data_ingestion_runs r
		JOIN (
		  SELECT provider, job_name, MAX(started_at) AS started_at
		  FROM data_ingestion_runs
		  GROUP BY provider, job_name
		) latest ON latest.provider = r.provider AND latest.job_name = r.job_name
		         AND latest.started_at = r.started_at
		ORDER BY r.started_at DESC
		LIMIT ?`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := i.pool.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("latest ingestion runs: %w", err)
	}
	defer rows.Close()
	var out []IngestionRun
	for rows.Next() {
		var r IngestionRun
		if err := rows.Scan(&r.Provider, &r.JobName, &r.Status, &r.StartedAt,
			&r.RecordsIn, &r.RecordsOut, &r.ErrorMessage); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
