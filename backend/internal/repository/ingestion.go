package repository

import (
	"context"
	"database/sql"
	"time"
)

// Ingestion exposes CRUD on the data_ingestion_runs table.
type Ingestion struct {
	pool *sql.DB
}

// NewIngestion returns an Ingestion repo.
func NewIngestion(pool *sql.DB) *Ingestion { return &Ingestion{pool: pool} }

// Insert writes a new ingestion run row.
func (i *Ingestion) Insert(ctx context.Context, r IngestionRun) error {
	const q = `
		INSERT INTO data_ingestion_runs
		  (id, provider, job_name, status, started_at, finished_at,
		   records_in, records_out, error_message, details_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := i.pool.ExecContext(ctx, q,
		r.ID, r.Provider, r.JobName, r.Status, r.StartedAt, r.FinishedAt,
		r.RecordsIn, r.RecordsOut, r.ErrorMessage, r.DetailsJSON,
	)
	return err
}

// LatestPerJob returns the most recent run per (provider, job_name).
func (i *Ingestion) LatestPerJob(ctx context.Context, limit int) ([]IngestionRun, error) {
	if limit <= 0 {
		limit = 20
	}
	// Lightweight approach: pull recent rows, then dedup in Go. Fine for MVP.
	const q = `
		SELECT id, provider, job_name, status, started_at, finished_at,
		       records_in, records_out, error_message, details_json
		FROM data_ingestion_runs
		ORDER BY started_at DESC
		LIMIT 200
	`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := i.pool.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[string]struct{})
	out := make([]IngestionRun, 0, limit)
	for rows.Next() {
		var r IngestionRun
		if err := rows.Scan(&r.ID, &r.Provider, &r.JobName, &r.Status, &r.StartedAt, &r.FinishedAt,
			&r.RecordsIn, &r.RecordsOut, &r.ErrorMessage, &r.DetailsJSON); err != nil {
			return nil, err
		}
		key := r.Provider + "|" + r.JobName
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}
