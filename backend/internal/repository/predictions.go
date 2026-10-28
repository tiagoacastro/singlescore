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

// MinuteAccuracy aggregates the model's snapshots at one match minute.
type MinuteAccuracy struct {
	Minute     int
	SampleSize int
	Correct    int // most likely outcome == final result
	AsItStands int // scoreline at the minute == final result
	BrierSum   float64
}

// AccuracyByMinute scores every pre-match and 5-minute-grid snapshot (45
// is half-time) of finished matches whose timeline was built from real
// event minutes, grouped by minute. Event-minute and full-time snapshots
// are left out so every minute covers the same matches.
func (p *Predictions) AccuracyByMinute(ctx context.Context) ([]MinuteAccuracy, error) {
	const q = `
		SELECT minute, COUNT(*),
		       SUM(pick = final_result),
		       SUM(standing = final_result),
		       SUM(POW(p_home - (final_result = 'HOME'), 2) + POW(p_draw - (final_result = 'DRAW'), 2)
		           + POW(p_away - (final_result = 'AWAY'), 2))
		FROM (
		  SELECT COALESCE(mp.match_minute, 0) AS minute, m.final_result,
		         mp.p_home, mp.p_draw, mp.p_away,
		         -- Same tie-break as evaluation.Argmax: home, then draw, then away.
		         CASE WHEN mp.p_home >= mp.p_draw AND mp.p_home >= mp.p_away THEN 'HOME'
		              WHEN mp.p_draw >= mp.p_away THEN 'DRAW' ELSE 'AWAY' END AS pick,
		         CASE WHEN mp.home_score_at_fc > mp.away_score_at_fc THEN 'HOME'
		              WHEN mp.home_score_at_fc < mp.away_score_at_fc THEN 'AWAY' ELSE 'DRAW' END AS standing
		  FROM model_predictions mp
		  JOIN matches m ON m.id = mp.match_id
		  WHERE m.status = 'FINISHED' AND m.final_result IS NOT NULL
		    AND COALESCE(mp.match_minute, 0) % 5 = 0 AND COALESCE(mp.match_minute, 0) < 90
		    AND JSON_EXTRACT_STRING(mp.features_json, 'score_source') = 'events'
		) t
		GROUP BY minute
		ORDER BY minute`
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("accuracy by minute: %w", err)
	}
	defer rows.Close()
	var out []MinuteAccuracy
	for rows.Next() {
		var r MinuteAccuracy
		if err := rows.Scan(&r.Minute, &r.SampleSize, &r.Correct, &r.AsItStands, &r.BrierSum); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
