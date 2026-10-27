package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Predictions exposes read/write access to model_predictions,
// benchmark_predictions and prediction_evaluations.
type Predictions struct {
	pool *sql.DB
}

// NewPredictions returns a Predictions repo.
func NewPredictions(pool *sql.DB) *Predictions { return &Predictions{pool: pool} }

// InsertModel writes a single model prediction snapshot.
func (p *Predictions) InsertModel(ctx context.Context, r ModelPrediction) error {
	return p.InsertModelBatch(ctx, []ModelPrediction{r})
}

// InsertModelBatch writes many model prediction snapshots in one round-trip.
func (p *Predictions) InsertModelBatch(ctx context.Context, rows []ModelPrediction) error {
	if len(rows) == 0 {
		return nil
	}
	const cols = 16
	placeholders := strings.Repeat("(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),", len(rows))
	placeholders = placeholders[:len(placeholders)-1]
	q := `INSERT INTO model_predictions
		  (id, match_id, model_name, model_version, forecast_at, match_minute,
		   home_score_at_fc, away_score_at_fc, home_red_cards_fc, away_red_cards_fc,
		   p_home, p_draw, p_away, features_json, provenance, source_timestamp)
		VALUES ` + placeholders
	args := make([]any, 0, len(rows)*cols)
	for _, r := range rows {
		args = append(args,
			r.ID, r.MatchID, r.ModelName, r.ModelVersion, r.ForecastAt, r.MatchMinute,
			r.HomeScoreAtFC, r.AwayScoreAtFC, r.HomeRedCardsFC, r.AwayRedCardsFC,
			r.PHome, r.PDraw, r.PAway, r.FeaturesJSON, string(r.Provenance), r.SourceTimestamp,
		)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := p.pool.ExecContext(ctx, q, args...)
	return err
}

// InsertBenchmark writes a single benchmark prediction snapshot.
func (p *Predictions) InsertBenchmark(ctx context.Context, r BenchmarkPrediction) error {
	return p.InsertBenchmarkBatch(ctx, []BenchmarkPrediction{r})
}

// InsertBenchmarkBatch writes many benchmark prediction snapshots in one round-trip.
func (p *Predictions) InsertBenchmarkBatch(ctx context.Context, rows []BenchmarkPrediction) error {
	if len(rows) == 0 {
		return nil
	}
	const cols = 20
	tuple := "(" + strings.Repeat("?, ", cols-1) + "?),"
	placeholders := strings.Repeat(tuple, len(rows))
	placeholders = placeholders[:len(placeholders)-1]
	q := `INSERT INTO benchmark_predictions
		  (id, match_id, provider, provider_version, source_type, horizon,
		   forecast_at, source_timestamp, match_minute, match_status,
		   home_score_at_fc, away_score_at_fc, home_red_cards_fc, away_red_cards_fc,
		   p_home, p_draw, p_away, raw_odds_json, overround, provenance)
		VALUES ` + placeholders
	args := make([]any, 0, len(rows)*cols)
	for _, r := range rows {
		args = append(args,
			r.ID, r.MatchID, r.Provider, r.ProviderVersion, r.SourceType, r.Horizon,
			r.ForecastAt, r.SourceTimestamp, r.MatchMinute, r.MatchStatus,
			r.HomeScoreAtFC, r.AwayScoreAtFC, r.HomeRedCardsFC, r.AwayRedCardsFC,
			r.PHome, r.PDraw, r.PAway, r.RawOddsJSON, r.Overround, string(r.Provenance),
		)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := p.pool.ExecContext(ctx, q, args...)
	return err
}

// LatestModelForMatches returns the newest model prediction per match id.
func (p *Predictions) LatestModelForMatches(ctx context.Context, matchIDs []string) (map[string]ModelPrediction, error) {
	if len(matchIDs) == 0 {
		return map[string]ModelPrediction{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	// Correlated subquery pattern: pick row with max forecast_at per match.
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
		  WHERE match_id IN (%s)
		  GROUP BY match_id
		) latest ON latest.match_id = mp.match_id AND latest.mx = mp.forecast_at
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

// RecentModelPredictions returns the newest N predictions overall.
func (p *Predictions) RecentModelPredictions(ctx context.Context, limit int) ([]ModelPrediction, error) {
	if limit <= 0 {
		limit = 20
	}
	q := fmt.Sprintf(`
		SELECT id, match_id, model_name, model_version, forecast_at,
		       match_minute, home_score_at_fc, away_score_at_fc,
		       home_red_cards_fc, away_red_cards_fc,
		       p_home, p_draw, p_away, features_json, provenance, source_timestamp
		FROM model_predictions
		ORDER BY forecast_at DESC
		LIMIT %d
	`, limit)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := p.pool.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelPrediction
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
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteModelForMatch wipes all model predictions for a match. Used by
// re-seeding; not exposed via GraphQL.
func (p *Predictions) DeleteModelForMatch(ctx context.Context, matchID string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := p.pool.ExecContext(ctx, `DELETE FROM model_predictions WHERE match_id = ?`, matchID)
	return err
}

// DeleteBenchmarkForMatch wipes all benchmark predictions for a match.
func (p *Predictions) DeleteBenchmarkForMatch(ctx context.Context, matchID string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := p.pool.ExecContext(ctx, `DELETE FROM benchmark_predictions WHERE match_id = ?`, matchID)
	return err
}

// DeleteEventsForMatch wipes all events for a match.
func (p *Predictions) DeleteEventsForMatch(ctx context.Context, matchID string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	_, err := p.pool.ExecContext(ctx, `DELETE FROM match_events WHERE match_id = ?`, matchID)
	return err
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
