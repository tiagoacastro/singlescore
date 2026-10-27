package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Predictions exposes read access to model_predictions and
// benchmark_predictions.
type Predictions struct {
	pool *sql.DB
}

// NewPredictions returns a Predictions repo.
func NewPredictions(pool *sql.DB) *Predictions { return &Predictions{pool: pool} }

const modelPredictionColumns = `id, match_id, model_name, model_version, forecast_at,
	match_minute, home_score_at_fc, away_score_at_fc, p_home, p_draw, p_away, provenance`

func scanModelPrediction(rows *sql.Rows) (ModelPrediction, error) {
	var r ModelPrediction
	err := rows.Scan(&r.ID, &r.MatchID, &r.ModelName, &r.ModelVersion, &r.ForecastAt,
		&r.MatchMinute, &r.HomeScoreAtFC, &r.AwayScoreAtFC,
		&r.PHome, &r.PDraw, &r.PAway, &r.Provenance)
	return r, err
}

// PreMatchModelForMatches returns the pre-kickoff model prediction per
// match id (match_minute IS NULL), i.e. the odds before anything
// happened on the pitch. When several rows exist (e.g. two model
// versions), the newest forecast wins, then the highest version.
func (p *Predictions) PreMatchModelForMatches(ctx context.Context, matchIDs []string) (map[string]ModelPrediction, error) {
	out := make(map[string]ModelPrediction, len(matchIDs))
	if len(matchIDs) == 0 {
		return out, nil
	}
	in, args := inClause(matchIDs)
	q := fmt.Sprintf(`SELECT %s FROM model_predictions
		WHERE match_id IN (%s) AND match_minute IS NULL
		ORDER BY match_id, forecast_at DESC, model_version DESC, id DESC`,
		modelPredictionColumns, in)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pre-match predictions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanModelPrediction(rows)
		if err != nil {
			return nil, err
		}
		if _, seen := out[r.MatchID]; !seen {
			out[r.MatchID] = r
		}
	}
	return out, rows.Err()
}

// ModelForMatches returns all model predictions for a set of match ids,
// grouped by match id and ordered by match minute (pre-match first).
func (p *Predictions) ModelForMatches(ctx context.Context, matchIDs []string) (map[string][]ModelPrediction, error) {
	out := make(map[string][]ModelPrediction, len(matchIDs))
	if len(matchIDs) == 0 {
		return out, nil
	}
	in, args := inClause(matchIDs)
	q := fmt.Sprintf(`SELECT %s FROM model_predictions
		WHERE match_id IN (%s)
		ORDER BY match_id, COALESCE(match_minute, 0), forecast_at`,
		modelPredictionColumns, in)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("model predictions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanModelPrediction(rows)
		if err != nil {
			return nil, err
		}
		out[r.MatchID] = append(out[r.MatchID], r)
	}
	return out, rows.Err()
}

// BenchmarkForMatches returns benchmark predictions for a set of match
// ids, grouped by match id and ordered by forecast time.
func (p *Predictions) BenchmarkForMatches(ctx context.Context, matchIDs []string) (map[string][]BenchmarkPrediction, error) {
	out := make(map[string][]BenchmarkPrediction, len(matchIDs))
	if len(matchIDs) == 0 {
		return out, nil
	}
	in, args := inClause(matchIDs)
	q := fmt.Sprintf(`SELECT id, match_id, provider, source_type, horizon, forecast_at,
		       match_minute, p_home, p_draw, p_away, provenance
		FROM benchmark_predictions
		WHERE match_id IN (%s)
		ORDER BY forecast_at`, in)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("benchmark predictions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var r BenchmarkPrediction
		if err := rows.Scan(&r.ID, &r.MatchID, &r.Provider, &r.SourceType, &r.Horizon,
			&r.ForecastAt, &r.MatchMinute, &r.PHome, &r.PDraw, &r.PAway, &r.Provenance); err != nil {
			return nil, err
		}
		out[r.MatchID] = append(out[r.MatchID], r)
	}
	return out, rows.Err()
}
