package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Predictions exposes read access to model_predictions,
// benchmark_predictions and prediction_evaluations.
type Predictions struct {
	pool *sql.DB
}

// NewPredictions returns a Predictions repo.
func NewPredictions(pool *sql.DB) *Predictions { return &Predictions{pool: pool} }

// PreMatchModelForMatches returns the pre-kickoff model prediction per
// match id (match_minute IS NULL), i.e. the odds before anything
// happened on the pitch. When several model versions wrote one, the
// newest forecast wins.
func (p *Predictions) PreMatchModelForMatches(ctx context.Context, matchIDs []string) (map[string]ModelPrediction, error) {
	if len(matchIDs) == 0 {
		return map[string]ModelPrediction{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	// Correlated subquery pattern: pick the pre-match row with max
	// forecast_at per match.
	q := fmt.Sprintf(`
		SELECT mp.id, mp.match_id, mp.model_name, mp.model_version, mp.forecast_at,
		       mp.match_minute, mp.home_score_at_fc, mp.away_score_at_fc,
		       mp.home_red_cards_fc, mp.away_red_cards_fc,
		       mp.p_home, mp.p_draw, mp.p_away, mp.features_json, mp.provenance,
		       mp.source_timestamp
		FROM model_predictions mp
		JOIN (
		  SELECT match_id, MAX(forecast_at) AS mx
		  FROM model_predictions
		  WHERE match_id IN (%s) AND match_minute IS NULL
		  GROUP BY match_id
		) latest ON latest.match_id = mp.match_id AND latest.mx = mp.forecast_at
		WHERE mp.match_minute IS NULL
	`, placeholders)
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]ModelPrediction, len(matchIDs))
	for rows.Next() {
		var r ModelPrediction
		var provenance string
		if err := rows.Scan(&r.ID, &r.MatchID, &r.ModelName, &r.ModelVersion, &r.ForecastAt,
			&r.MatchMinute, &r.HomeScoreAtFC, &r.AwayScoreAtFC,
			&r.HomeRedCardsFC, &r.AwayRedCardsFC,
			&r.PHome, &r.PDraw, &r.PAway, &r.FeaturesJSON, &provenance,
			&r.SourceTimestamp); err != nil {
			return nil, err
		}
		r.Provenance = Provenance(provenance)
		// Keep the one with the latest ForecastAt in case of ties.
		if existing, ok := out[r.MatchID]; !ok || r.ForecastAt.After(existing.ForecastAt) {
			out[r.MatchID] = r
		}
	}
	return out, rows.Err()
}

// ModelForMatches returns all model predictions for a set of match ids,
// grouped by match id and ordered by forecast_at ASC.
func (p *Predictions) ModelForMatches(ctx context.Context, matchIDs []string) (map[string][]ModelPrediction, error) {
	if len(matchIDs) == 0 {
		return map[string][]ModelPrediction{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT id, match_id, model_name, model_version, forecast_at,
		       match_minute, home_score_at_fc, away_score_at_fc,
		       home_red_cards_fc, away_red_cards_fc,
		       p_home, p_draw, p_away, features_json, provenance, source_timestamp
		FROM model_predictions
		WHERE match_id IN (%s)
		ORDER BY match_id ASC, COALESCE(match_minute, 0) ASC, forecast_at ASC
	`, placeholders)
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]ModelPrediction, len(matchIDs))
	for rows.Next() {
		var r ModelPrediction
		var provenance string
		if err := rows.Scan(&r.ID, &r.MatchID, &r.ModelName, &r.ModelVersion, &r.ForecastAt,
			&r.MatchMinute, &r.HomeScoreAtFC, &r.AwayScoreAtFC,
			&r.HomeRedCardsFC, &r.AwayRedCardsFC,
			&r.PHome, &r.PDraw, &r.PAway, &r.FeaturesJSON, &provenance,
			&r.SourceTimestamp); err != nil {
			return nil, err
		}
		r.Provenance = Provenance(provenance)
		out[r.MatchID] = append(out[r.MatchID], r)
	}
	return out, rows.Err()
}

// BenchmarkForMatches returns benchmark predictions for a set of match ids.
func (p *Predictions) BenchmarkForMatches(ctx context.Context, matchIDs []string) (map[string][]BenchmarkPrediction, error) {
	if len(matchIDs) == 0 {
		return map[string][]BenchmarkPrediction{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT id, match_id, provider, provider_version, source_type, horizon,
		       forecast_at, source_timestamp, match_minute, match_status,
		       home_score_at_fc, away_score_at_fc, home_red_cards_fc, away_red_cards_fc,
		       p_home, p_draw, p_away, raw_odds_json, overround, provenance
		FROM benchmark_predictions
		WHERE match_id IN (%s)
		ORDER BY forecast_at ASC
	`, placeholders)
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]BenchmarkPrediction, len(matchIDs))
	for rows.Next() {
		var r BenchmarkPrediction
		var provenance string
		if err := rows.Scan(&r.ID, &r.MatchID, &r.Provider, &r.ProviderVersion, &r.SourceType, &r.Horizon,
			&r.ForecastAt, &r.SourceTimestamp, &r.MatchMinute, &r.MatchStatus,
			&r.HomeScoreAtFC, &r.AwayScoreAtFC, &r.HomeRedCardsFC, &r.AwayRedCardsFC,
			&r.PHome, &r.PDraw, &r.PAway, &r.RawOddsJSON, &r.Overround, &provenance); err != nil {
			return nil, err
		}
		r.Provenance = Provenance(provenance)
		out[r.MatchID] = append(out[r.MatchID], r)
	}
	return out, rows.Err()
}

// --- evaluations -----------------------------------------------------

// EvaluatedModelSummary aggregates model predictions against actuals.
type EvaluatedModelSummary struct {
	ModelName    string
	ModelVersion string
	SampleSize   int
	BrierScore   float64
	LogLoss      float64
	Accuracy     float64
	DateFrom     *time.Time
	DateTo       *time.Time
}

// FinishedMatchFinalResult pairs match id with its final result for evaluation.
type FinishedMatchFinalResult struct {
	MatchID     string
	FinalResult Outcome
}

// AllFinishedResults returns final results for completed matches.
func (p *Predictions) AllFinishedResults(ctx context.Context) ([]FinishedMatchFinalResult, error) {
	const q = `SELECT id, final_result FROM matches WHERE status='FINISHED' AND final_result IS NOT NULL`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FinishedMatchFinalResult
	for rows.Next() {
		var r FinishedMatchFinalResult
		var fr string
		if err := rows.Scan(&r.MatchID, &fr); err != nil {
			return nil, err
		}
		r.FinalResult = Outcome(fr)
		out = append(out, r)
	}
	return out, rows.Err()
}
