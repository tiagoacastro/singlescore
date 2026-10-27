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

// Matches exposes read access to matches and their per-match child
// tables (match_events, match_stats, match_commentary).
type Matches struct {
	pool *sql.DB
}

// NewMatches returns a Matches repo bound to the pool.
func NewMatches(pool *sql.DB) *Matches { return &Matches{pool: pool} }

const matchColumns = `id, competition_id, home_team_id, away_team_id, kickoff_at, status,
	minute, home_score, away_score, home_red_cards, away_red_cards, final_result, venue, provenance`

type rowScanner interface{ Scan(dest ...any) error }

func scanMatch(s rowScanner) (Match, error) {
	var r Match
	err := s.Scan(&r.ID, &r.CompetitionID, &r.HomeTeamID, &r.AwayTeamID, &r.KickoffAt, &r.Status,
		&r.Minute, &r.HomeScore, &r.AwayScore, &r.HomeRedCards, &r.AwayRedCards,
		&r.FinalResult, &r.Venue, &r.Provenance)
	return r, err
}

func (m *Matches) queryMatches(ctx context.Context, q string, args ...any) ([]Match, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Match
	for rows.Next() {
		r, err := scanMatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetByID returns one match, or nil when it doesn't exist.
func (m *Matches) GetByID(ctx context.Context, id string) (*Match, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	r, err := scanMatch(m.pool.QueryRowContext(ctx,
		"SELECT "+matchColumns+" FROM matches WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get match: %w", err)
	}
	return &r, nil
}

// MatchFilter narrows the match list query.
type MatchFilter struct {
	Status        *MatchStatus
	CompetitionID string // exact match when non-empty
	Team          string // case-insensitive substring of either team's name
	First         int
	After         string // opaque cursor
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

// likeEscaper makes user input literal inside a LIKE pattern.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

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
	if f.CompetitionID != "" {
		where = append(where, "competition_id = ?")
		args = append(args, f.CompetitionID)
	}
	if team := strings.TrimSpace(f.Team); team != "" {
		// teams.name is utf8mb4_bin (case-sensitive) and SingleStore has
		// no per-expression COLLATE, so compare lower-cased.
		pattern := "%" + likeEscaper.Replace(strings.ToLower(team)) + "%"
		where = append(where, `(home_team_id IN (SELECT id FROM teams WHERE LOWER(name) LIKE ?)
			OR away_team_id IN (SELECT id FROM teams WHERE LOWER(name) LIKE ?))`)
		args = append(args, pattern, pattern)
	}

	// The total covers the whole filtered list, so it's counted before
	// the cursor narrows the page.
	ctxCount, cancelCount := context.WithTimeout(ctx, 15*time.Second)
	defer cancelCount()
	var total int
	countQ := "SELECT COUNT(*) FROM matches WHERE " + strings.Join(where, " AND ")
	if err := m.pool.QueryRowContext(ctxCount, countQ, args...).Scan(&total); err != nil {
		return PageResult{}, fmt.Errorf("count matches: %w", err)
	}

	if f.After != "" {
		kickoff, id, err := decodeCursor(f.After)
		if err != nil {
			return PageResult{}, fmt.Errorf("invalid cursor: %w", err)
		}
		// Keyset pagination. SingleStore has no row-value comparison, so
		// (kickoff_at, id) < (?, ?) is spelled out.
		where = append(where, "(kickoff_at < ? OR (kickoff_at = ? AND id < ?))")
		args = append(args, kickoff, kickoff, id)
	}
	// Fetch one extra row to learn whether there's a next page.
	out, err := m.queryMatches(ctx, fmt.Sprintf(
		"SELECT %s FROM matches WHERE %s ORDER BY kickoff_at DESC, id DESC LIMIT %d",
		matchColumns, strings.Join(where, " AND "), f.First+1), args...)
	if err != nil {
		return PageResult{}, fmt.Errorf("list matches: %w", err)
	}

	hasNext := len(out) > f.First
	if hasNext {
		out = out[:f.First]
	}
	var endCursor string
	if len(out) > 0 {
		endCursor = EncodeCursor(out[len(out)-1])
	}
	return PageResult{Matches: out, TotalCount: total, EndCursor: endCursor, HasNext: hasNext}, nil
}

// RecentFinished returns the most recently finished matches.
func (m *Matches) RecentFinished(ctx context.Context, limit int) ([]Match, error) {
	if limit <= 0 {
		limit = 10
	}
	out, err := m.queryMatches(ctx, fmt.Sprintf(
		"SELECT %s FROM matches WHERE status = 'FINISHED' ORDER BY kickoff_at DESC, id DESC LIMIT %d",
		matchColumns, limit))
	if err != nil {
		return nil, fmt.Errorf("recent finished matches: %w", err)
	}
	return out, nil
}

// Finished returns every finished match with a final result.
func (m *Matches) Finished(ctx context.Context) ([]FinishedMatch, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, `SELECT id, competition_id, final_result FROM matches
		WHERE status = 'FINISHED' AND final_result IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("finished matches: %w", err)
	}
	defer rows.Close()
	var out []FinishedMatch
	for rows.Next() {
		var r FinishedMatch
		if err := rows.Scan(&r.ID, &r.CompetitionID, &r.FinalResult); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// EventsForMatches returns events keyed by match id in on-pitch order
// (minute, then stoppage time, so 45'+3 sorts before 46').
func (m *Matches) EventsForMatches(ctx context.Context, matchIDs []string) (map[string][]MatchEvent, error) {
	out := make(map[string][]MatchEvent, len(matchIDs))
	if len(matchIDs) == 0 {
		return out, nil
	}
	in, args := inClause(matchIDs)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, match_id, event_type, team_side, minute, added_minute, description, event_time
		FROM match_events
		WHERE match_id IN (%s)
		ORDER BY match_id, COALESCE(minute, 0), COALESCE(added_minute, 0), event_time`, in), args...)
	if err != nil {
		return nil, fmt.Errorf("match events: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e MatchEvent
		if err := rows.Scan(&e.ID, &e.MatchID, &e.EventType, &e.TeamSide,
			&e.Minute, &e.AddedMinute, &e.Description, &e.EventTime); err != nil {
			return nil, err
		}
		out[e.MatchID] = append(out[e.MatchID], e)
	}
	return out, rows.Err()
}

// StatsForMatches returns match_stats rows keyed by match id. Matches
// without stats are absent from the map.
func (m *Matches) StatsForMatches(ctx context.Context, matchIDs []string) (map[string]MatchStats, error) {
	out := make(map[string]MatchStats, len(matchIDs))
	if len(matchIDs) == 0 {
		return out, nil
	}
	in, args := inClause(matchIDs)
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, fmt.Sprintf(`
		SELECT match_id, home_shots, away_shots, home_shots_on_t, away_shots_on_t,
		       home_corners, away_corners, home_fouls, away_fouls,
		       home_yellow, away_yellow, home_red, away_red,
		       home_xg, away_xg, referee, source
		FROM match_stats
		WHERE match_id IN (%s)`, in), args...)
	if err != nil {
		return nil, fmt.Errorf("match stats: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var s MatchStats
		if err := rows.Scan(&s.MatchID, &s.HomeShots, &s.AwayShots, &s.HomeShotsOnT, &s.AwayShotsOnT,
			&s.HomeCorners, &s.AwayCorners, &s.HomeFouls, &s.AwayFouls,
			&s.HomeYellow, &s.AwayYellow, &s.HomeRed, &s.AwayRed,
			&s.HomeXG, &s.AwayXG, &s.Referee, &s.Source); err != nil {
			return nil, err
		}
		out[s.MatchID] = s
	}
	return out, rows.Err()
}

// CommentaryForMatches returns match_commentary rows keyed by match id,
// ordered by ESPN's sequence number. The frontend sorts for display.
func (m *Matches) CommentaryForMatches(ctx context.Context, matchIDs []string) (map[string][]MatchCommentary, error) {
	out := make(map[string][]MatchCommentary, len(matchIDs))
	if len(matchIDs) == 0 {
		return out, nil
	}
	in, args := inClause(matchIDs)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	rows, err := m.pool.QueryContext(ctx, fmt.Sprintf(`
		SELECT match_id, sequence, minute, added_minute, period, kind, team_side, text
		FROM match_commentary
		WHERE match_id IN (%s)
		ORDER BY match_id, sequence`, in), args...)
	if err != nil {
		return nil, fmt.Errorf("match commentary: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c MatchCommentary
		if err := rows.Scan(&c.MatchID, &c.Sequence, &c.Minute, &c.AddedMinute, &c.Period,
			&c.Kind, &c.TeamSide, &c.Text); err != nil {
			return nil, err
		}
		out[c.MatchID] = append(out[c.MatchID], c)
	}
	return out, rows.Err()
}

// --- cursors ---------------------------------------------------------

// EncodeCursor is the opaque pagination cursor for a match row
// (base64 of "kickoff RFC3339Nano|id").
func EncodeCursor(m Match) string {
	raw := m.KickoffAt.UTC().Format(time.RFC3339Nano) + "|" + m.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(s string) (time.Time, string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, "", err
	}
	kickoff, id, ok := strings.Cut(string(b), "|")
	if !ok {
		return time.Time{}, "", errors.New("bad cursor payload")
	}
	t, err := time.Parse(time.RFC3339Nano, kickoff)
	if err != nil {
		return time.Time{}, "", err
	}
	return t, id, nil
}
