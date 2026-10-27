package repository

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Matches exposes read access to the matches and match_events tables.
type Matches struct {
	pool *sql.DB
}

// NewMatches returns a Matches repo bound to the pool.
func NewMatches(pool *sql.DB) *Matches { return &Matches{pool: pool} }

func (m *Matches) GetByID(ctx context.Context, id string) (*Match, error) {
	const q = `
		SELECT id, provider, external_id, competition_id, home_team_id, away_team_id,
		       kickoff_at, status, minute, home_score, away_score,
		       home_red_cards, away_red_cards, final_result, venue, provenance,
		       source_updated_at, created_at, updated_at
		FROM matches WHERE id = ?
	`
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	row := m.pool.QueryRowContext(ctx, q, id)
	var r Match
	var status, provenance string
	var finalResult *string
	if err := row.Scan(&r.ID, &r.Provider, &r.ExternalID, &r.CompetitionID, &r.HomeTeamID, &r.AwayTeamID,
		&r.KickoffAt, &status, &r.Minute, &r.HomeScore, &r.AwayScore,
		&r.HomeRedCards, &r.AwayRedCards, &finalResult, &r.Venue, &provenance,
		&r.SourceUpdatedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.Status = MatchStatus(status)
	r.Provenance = Provenance(provenance)
	if finalResult != nil {
		o := Outcome(*finalResult)
		r.FinalResult = &o
	}
	return &r, nil
}

// MatchFilter narrows the match list query.
type MatchFilter struct {
	Status *MatchStatus
	First  int
	After  string // opaque cursor
}

// PageResult carries the paged matches, total count and next cursor.
type PageResult struct {
	Matches    []Match
	TotalCount int
	EndCursor  string
	HasNext    bool
}

const defaultPageSize = 20
const maxPageSize = 100

// List returns matches ordered by kickoff_at DESC then id DESC for stable
// cursor-based pagination.
func (m *Matches) List(ctx context.Context, f MatchFilter) (PageResult, error) {
	if f.First <= 0 {
		f.First = defaultPageSize
	}
	if f.First > maxPageSize {
		f.First = maxPageSize
	}

	where := []string{"1=1"}
	args := []any{}
	if f.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*f.Status))
	}
	if f.After != "" {
		kickoff, id, err := decodeCursor(f.After)
		if err != nil {
			return PageResult{}, fmt.Errorf("invalid cursor: %w", err)
		}
		where = append(where, "(kickoff_at, id) < (?, ?)")
		args = append(args, kickoff, id)
	}

	whereSQL := strings.Join(where, " AND ")

	// Count query.
	countQ := "SELECT COUNT(*) FROM matches WHERE " + whereSQL
	ctxCount, cancelCount := context.WithTimeout(ctx, 15*time.Second)
	defer cancelCount()
	var total int
	if err := m.pool.QueryRowContext(ctxCount, countQ, args...).Scan(&total); err != nil {
		return PageResult{}, fmt.Errorf("count matches: %w", err)
	}

	// Page query: fetch First+1 to detect next page.
	listQ := fmt.Sprintf(`
		SELECT id, provider, external_id, competition_id, home_team_id, away_team_id,
		       kickoff_at, status, minute, home_score, away_score,
		       home_red_cards, away_red_cards, final_result, venue, provenance,
		       source_updated_at, created_at, updated_at
		FROM matches
		WHERE %s
		ORDER BY kickoff_at DESC, id DESC
		LIMIT %d
	`, whereSQL, f.First+1)

	ctxList, cancelList := context.WithTimeout(ctx, 20*time.Second)
	defer cancelList()
	rows, err := m.pool.QueryContext(ctxList, listQ, args...)
	if err != nil {
		return PageResult{}, fmt.Errorf("list matches: %w", err)
	}
	defer rows.Close()

	out := make([]Match, 0, f.First+1)
	for rows.Next() {
		var r Match
		var status, provenance string
		var finalResult *string
		if err := rows.Scan(&r.ID, &r.Provider, &r.ExternalID, &r.CompetitionID, &r.HomeTeamID, &r.AwayTeamID,
			&r.KickoffAt, &status, &r.Minute, &r.HomeScore, &r.AwayScore,
			&r.HomeRedCards, &r.AwayRedCards, &finalResult, &r.Venue, &provenance,
			&r.SourceUpdatedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return PageResult{}, err
		}
		r.Status = MatchStatus(status)
		r.Provenance = Provenance(provenance)
		if finalResult != nil {
			o := Outcome(*finalResult)
			r.FinalResult = &o
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return PageResult{}, err
	}

	hasNext := len(out) > f.First
	if hasNext {
		out = out[:f.First]
	}
	var endCursor string
	if len(out) > 0 {
		last := out[len(out)-1]
		endCursor = encodeCursor(last.KickoffAt, last.ID)
	}
	return PageResult{Matches: out, TotalCount: total, EndCursor: endCursor, HasNext: hasNext}, nil
}

// RecentFinished returns the most recently finished matches.
func (m *Matches) RecentFinished(ctx context.Context, limit int) ([]Match, error) {
	if limit <= 0 {
		limit = 10
	}
	q := fmt.Sprintf(`
		SELECT id, provider, external_id, competition_id, home_team_id, away_team_id,
		       kickoff_at, status, minute, home_score, away_score,
		       home_red_cards, away_red_cards, final_result, venue, provenance,
		       source_updated_at, created_at, updated_at
		FROM matches
		WHERE status = 'FINISHED'
		ORDER BY kickoff_at DESC
		LIMIT %d
	`, limit)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		var r Match
		var status, provenance string
		var finalResult *string
		if err := rows.Scan(&r.ID, &r.Provider, &r.ExternalID, &r.CompetitionID, &r.HomeTeamID, &r.AwayTeamID,
			&r.KickoffAt, &status, &r.Minute, &r.HomeScore, &r.AwayScore,
			&r.HomeRedCards, &r.AwayRedCards, &finalResult, &r.Venue, &provenance,
			&r.SourceUpdatedAt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Status = MatchStatus(status)
		r.Provenance = Provenance(provenance)
		if finalResult != nil {
			o := Outcome(*finalResult)
			r.FinalResult = &o
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EventsForMatches returns events keyed by match id, ordered by event_time.
func (m *Matches) EventsForMatches(ctx context.Context, matchIDs []string) (map[string][]MatchEvent, error) {
	if len(matchIDs) == 0 {
		return map[string][]MatchEvent{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT id, match_id, provider, external_id, event_type, team_side,
		       minute, added_minute, description, event_time, source_timestamp
		FROM match_events
		WHERE match_id IN (%s)
		ORDER BY event_time ASC
	`, placeholders)
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]MatchEvent, len(matchIDs))
	for rows.Next() {
		var e MatchEvent
		if err := rows.Scan(&e.ID, &e.MatchID, &e.Provider, &e.ExternalID, &e.EventType, &e.TeamSide,
			&e.Minute, &e.AddedMinute, &e.Description, &e.EventTime, &e.SourceTimestamp); err != nil {
			return nil, err
		}
		out[e.MatchID] = append(out[e.MatchID], e)
	}
	return out, rows.Err()
}

// StatsForMatches returns match_stats rows keyed by match id. Missing
// match ids are simply absent from the result map (0:1 relationship —
// not every match has stats).
func (m *Matches) StatsForMatches(ctx context.Context, matchIDs []string) (map[string]MatchStats, error) {
	if len(matchIDs) == 0 {
		return map[string]MatchStats{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT match_id,
		       home_shots, away_shots,
		       home_shots_on_t, away_shots_on_t,
		       home_corners, away_corners,
		       home_fouls, away_fouls,
		       home_yellow, away_yellow,
		       home_red, away_red,
		       home_xg, away_xg,
		       referee, source, updated_at
		FROM match_stats
		WHERE match_id IN (%s)
	`, placeholders)
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]MatchStats, len(matchIDs))
	for rows.Next() {
		var s MatchStats
		if err := rows.Scan(
			&s.MatchID,
			&s.HomeShots, &s.AwayShots,
			&s.HomeShotsOnT, &s.AwayShotsOnT,
			&s.HomeCorners, &s.AwayCorners,
			&s.HomeFouls, &s.AwayFouls,
			&s.HomeYellow, &s.AwayYellow,
			&s.HomeRed, &s.AwayRed,
			&s.HomeXG, &s.AwayXG,
			&s.Referee, &s.Source, &s.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out[s.MatchID] = s
	}
	return out, rows.Err()
}

// CommentaryForMatches returns match_commentary rows keyed by match
// id, ordered by sequence (which already reflects on-pitch chronology
// in reverse — oldest minute = highest sequence coming out of ESPN).
// The service layer re-orders to minute-ascending for display.
func (m *Matches) CommentaryForMatches(ctx context.Context, matchIDs []string) (map[string][]MatchCommentary, error) {
	if len(matchIDs) == 0 {
		return map[string][]MatchCommentary{}, nil
	}
	placeholders := strings.Repeat("?,", len(matchIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`
		SELECT match_id, sequence, minute, added_minute, period,
		       kind, team_side, text, source
		FROM match_commentary
		WHERE match_id IN (%s)
		ORDER BY match_id, sequence ASC
	`, placeholders)
	args := make([]any, len(matchIDs))
	for i, id := range matchIDs {
		args[i] = id
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]MatchCommentary, len(matchIDs))
	for rows.Next() {
		var c MatchCommentary
		if err := rows.Scan(
			&c.MatchID, &c.Sequence, &c.Minute, &c.AddedMinute, &c.Period,
			&c.Kind, &c.TeamSide, &c.Text, &c.Source,
		); err != nil {
			return nil, err
		}
		out[c.MatchID] = append(out[c.MatchID], c)
	}
	return out, rows.Err()
}

// --- cursor helpers --------------------------------------------------

func encodeCursor(t time.Time, id string) string {
	raw := t.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("bad cursor payload")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", err
	}
	return t, parts[1], nil
}
