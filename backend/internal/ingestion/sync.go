package ingestion

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/tiagocastro/singlescore/backend/internal/prediction"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

// Logger is the subset of our logger the sync needs.
type Logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// SyncResult summarises a sync run — populated even on failure so the
// caller can write a diagnostic row to data_ingestion_runs.
type SyncResult struct {
	Provider         string
	JobName          string
	StartedAt        time.Time
	FinishedAt       time.Time
	Status           string // STARTED | SUCCEEDED | FAILED | PARTIAL
	FixturesIn       int
	Competitions     int
	Teams            int
	MatchesUpserted  int
	ModelPredictions int
	Errors           []string
}

// SyncOptions tunes a sync.
type SyncOptions struct {
	// DefaultEloRating is used the first time we see a team. Repeated
	// syncs leave the team row's elo_rating untouched (upsert writes
	// elo on both insert and update, so we always pass this value;
	// good-enough until Elo is maintained by a dedicated job).
	DefaultEloRating float64
	// HomeAdvantageElo is passed to the predictor.
	HomeAdvantageElo float64
	// RecordRun controls whether a data_ingestion_runs row is written.
	// Defaults to true; set false in tests.
	RecordRun bool
	// SkipFinishedWithFullTime, when true, drops any FINISHED match
	// that already has a stored full-time prediction snapshot from the
	// per-sync prediction pass. These matches' model inputs are frozen
	// so regenerating them produces identical rows — a pure cost. Set
	// false (default) to keep the belt-and-braces behaviour.
	SkipFinishedWithFullTime bool
}

// Deps are the dependencies a Sync needs.
type Deps struct {
	Catalog     *repository.Catalog
	Matches     *repository.Matches
	Predictions *repository.Predictions
	Ingestion   *repository.Ingestion
	Predictor   prediction.Predictor
	Logger      Logger
}

// Sync pulls fixtures from a Provider and upserts them into
// SingleStore, writing a fresh model prediction snapshot per match.
type Sync struct {
	deps     Deps
	provider Provider
	opts     SyncOptions
}

// NewSync returns a Sync with sensible defaults filled in.
func NewSync(deps Deps, provider Provider, opts SyncOptions) *Sync {
	if opts.DefaultEloRating == 0 {
		opts.DefaultEloRating = 1500
	}
	if opts.HomeAdvantageElo == 0 {
		opts.HomeAdvantageElo = 60
	}
	return &Sync{deps: deps, provider: provider, opts: opts}
}

// Run executes one sync pass.
func (s *Sync) Run(ctx context.Context) (SyncResult, error) {
	res := SyncResult{
		Provider:  s.provider.Name(),
		JobName:   "fixtures",
		StartedAt: time.Now().UTC().Round(time.Microsecond),
		Status:    "STARTED",
	}
	runID := uuid.NewString()
	if s.opts.RecordRun {
		_ = s.deps.Ingestion.Insert(ctx, repository.IngestionRun{
			ID: runID, Provider: res.Provider, JobName: res.JobName,
			Status: "STARTED", StartedAt: res.StartedAt,
		})
	}

	fixtures, fetchErr := s.provider.FetchFixtures(ctx)
	// A hard failure returns no fixtures at all; a partial failure
	// returns some fixtures AND an error we should surface but not
	// die on.
	if fetchErr != nil && len(fixtures) == 0 {
		res.Status = "FAILED"
		res.FinishedAt = time.Now().UTC().Round(time.Microsecond)
		res.Errors = append(res.Errors, fmt.Sprintf("fetch fixtures: %v", fetchErr))
		s.recordRun(ctx, res, fetchErr)
		return res, fetchErr
	}
	if fetchErr != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("fetch fixtures (partial): %v", fetchErr))
		s.deps.Logger.Warn("sync partial fetch", "provider", res.Provider, "err", fetchErr.Error())
	}
	res.FixturesIn = len(fixtures)
	s.deps.Logger.Info("sync fetched fixtures", "provider", res.Provider, "count", res.FixturesIn)

	// 1) Collect unique competitions + teams across all fixtures, then
	//    flush each in a single batch. Previous behaviour was one
	//    round-trip per row, which can't finish a whole-season import
	//    inside sensible CLI timeouts on Oregon-US-West.
	competitionIDs, teamIDs, compRows, teamRows := s.collateCatalog(fixtures)
	if err := s.deps.Catalog.UpsertCompetitionBatch(ctx, compRows); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("upsert competitions batch: %v", err))
	} else {
		res.Competitions = len(compRows)
	}
	if err := s.deps.Catalog.UpsertTeamBatch(ctx, teamRows); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("upsert teams batch: %v", err))
	} else {
		res.Teams = len(teamRows)
	}

	// 2) Build + batch-upsert matches.
	pending, matchRows, missingRefs := s.collateMatches(fixtures, competitionIDs, teamIDs)
	res.Errors = append(res.Errors, missingRefs...)
	if err := s.deps.Matches.UpsertMatchBatch(ctx, matchRows); err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("upsert matches batch: %v", err))
	} else {
		res.MatchesUpserted = len(matchRows)
	}

	// 3) Refresh team Elo map in one query so predictor uses whatever
	//    the repo currently has.
	ids := make([]string, 0, len(teamIDs))
	for _, id := range teamIDs {
		ids = append(ids, id)
	}
	teamsByID, err := s.deps.Catalog.GetTeams(ctx, ids)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("read teams: %v", err))
	}

	// 3a) For FINISHED matches, decide which to skip:
	//   - skipDone     : already has a full-time snapshot → regenerating
	//                    the HT/FT-approximation grid would produce
	//                    byte-identical rows. Pure cost.
	//   - skipOwnedByEvents : has per-event data in match_events (goals /
	//                    red cards). Events-sync owns the per-minute
	//                    timeline for these matches; sync must NOT run
	//                    the approximation path or it will clobber the
	//                    higher-fidelity goal-step rebuild.
	// Both lookups are shared sync-wide to avoid per-match round-trips.
	skipDone := map[string]bool{}
	skipOwnedByEvents := map[string]bool{}
	finishedIDs := make([]string, 0)
	for _, pm := range pending {
		if pm.fx.Status == repository.StatusFinished {
			finishedIDs = append(finishedIDs, pm.matchID)
		}
	}
	if len(finishedIDs) > 0 {
		if s.opts.SkipFinishedWithFullTime {
			done, err := s.deps.Predictions.MatchesWithFullTimeSnapshot(ctx, finishedIDs,
				s.deps.Predictor.Name(), s.deps.Predictor.Version())
			if err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("full-time snapshot lookup: %v", err))
			} else {
				skipDone = done
			}
		}
		eventsByMatch, err := s.deps.Matches.EventsForMatches(ctx, finishedIDs)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("events lookup: %v", err))
		} else {
			for mid, evs := range eventsByMatch {
				if len(evs) > 0 {
					skipOwnedByEvents[mid] = true
				}
			}
		}
	}

	// 4) Prediction snapshots. See predictions.go for the policy.
	rows := make([]repository.ModelPrediction, 0, len(pending)*2)
	forecastAt := time.Now().UTC().Round(time.Microsecond)
	for _, pm := range pending {
		if skipOwnedByEvents[pm.matchID] {
			continue
		}
		if skipDone[pm.matchID] {
			continue
		}
		elo := eloInputs{
			home:          valueOrDefault(teamsByID[pm.homeID].EloRating, s.opts.DefaultEloRating),
			away:          valueOrDefault(teamsByID[pm.awayID].EloRating, s.opts.DefaultEloRating),
			homeAdvantage: s.opts.HomeAdvantageElo,
		}
		matchRows, errs := s.buildMatchPredictions(pm.matchID, pm.fx, elo, forecastAt)
		rows = append(rows, matchRows...)
		res.Errors = append(res.Errors, errs...)
	}
	if len(rows) > 0 {
		if err := s.deps.Predictions.InsertModelBatch(ctx, rows); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("insert model batch: %v", err))
		} else {
			res.ModelPredictions = len(rows)
		}
	}

	res.FinishedAt = time.Now().UTC().Round(time.Microsecond)
	switch {
	case len(res.Errors) == 0:
		res.Status = "SUCCEEDED"
	case res.MatchesUpserted > 0:
		res.Status = "PARTIAL"
	default:
		res.Status = "FAILED"
	}
	s.recordRun(ctx, res, nil)
	return res, nil
}

// pendingMatch pairs a fixture with the resolved internal ids we need
// to build its match + prediction rows.
type pendingMatch struct {
	fx      Fixture
	matchID string
	homeID  string
	awayID  string
}

// collateCatalog walks the fixtures and returns the unique
// (competition, team) rows to upsert, plus ext-id→internal-id maps
// used by collateMatches below.
func (s *Sync) collateCatalog(fixtures []Fixture) (
	competitionIDs map[string]string,
	teamIDs map[string]string,
	compRows []repository.Competition,
	teamRows []repository.Team,
) {
	competitionIDs = map[string]string{}
	teamIDs = map[string]string{}
	for _, fx := range fixtures {
		if _, ok := competitionIDs[fx.ExternalCompID]; !ok {
			cid := deterministicID("competition", s.provider.Name()+"|"+fx.ExternalCompID)
			competitionIDs[fx.ExternalCompID] = cid
			compRows = append(compRows, repository.Competition{
				ID: cid, Provider: s.provider.Name(), ExternalID: strPtr(fx.ExternalCompID),
				Name: fx.CompetitionName, Country: strOrNil(fx.Country), Season: strOrNil(fx.Season),
			})
		}
		for _, team := range []struct{ ext, name string }{
			{fx.HomeTeamExtID, fx.HomeTeamName},
			{fx.AwayTeamExtID, fx.AwayTeamName},
		} {
			if _, ok := teamIDs[team.ext]; ok {
				continue
			}
			tid := deterministicID("team", s.provider.Name()+"|"+team.ext)
			teamIDs[team.ext] = tid
			teamRows = append(teamRows, repository.Team{
				ID: tid, Provider: s.provider.Name(), ExternalID: strPtr(team.ext),
				Name: team.name, EloRating: s.opts.DefaultEloRating,
			})
		}
	}
	return
}

// collateMatches resolves each fixture's foreign keys and emits a
// (pendingMatch, matchRow) pair for every well-formed fixture. Any
// fixture missing its competition or a team is reported in missingRefs.
func (s *Sync) collateMatches(
	fixtures []Fixture,
	competitionIDs map[string]string,
	teamIDs map[string]string,
) (pending []pendingMatch, matchRows []repository.Match, missingRefs []string) {
	matchRows = make([]repository.Match, 0, len(fixtures))
	pending = make([]pendingMatch, 0, len(fixtures))
	for _, fx := range fixtures {
		cid, cok := competitionIDs[fx.ExternalCompID]
		hid, hok := teamIDs[fx.HomeTeamExtID]
		aid, aok := teamIDs[fx.AwayTeamExtID]
		if !cok || !hok || !aok {
			missingRefs = append(missingRefs, fmt.Sprintf("missing refs for match %s", fx.ExternalMatchID))
			continue
		}
		mid := deterministicID("match", s.provider.Name()+"|"+fx.ExternalMatchID)
		kickoff := fx.KickoffAt.UTC().Round(time.Microsecond)
		srcUpdated := fx.SourceUpdatedAt.UTC().Round(time.Microsecond)
		ext := fx.ExternalMatchID
		matchRows = append(matchRows, repository.Match{
			ID: mid, Provider: s.provider.Name(), ExternalID: &ext,
			CompetitionID: cid, HomeTeamID: hid, AwayTeamID: aid,
			KickoffAt: kickoff, Status: fx.Status,
			Minute: fx.Minute, HomeScore: fx.HomeScore, AwayScore: fx.AwayScore,
			FinalResult: fx.FinalResult, Venue: fx.Venue,
			Provenance:      repository.ProvenanceLive,
			SourceUpdatedAt: &srcUpdated,
		})
		pending = append(pending, pendingMatch{fx: fx, matchID: mid, homeID: hid, awayID: aid})
	}
	return
}

// recordRun writes a diagnostic row to data_ingestion_runs summarising
// the pass. Best-effort — write failures are logged, not returned.
func (s *Sync) recordRun(ctx context.Context, res SyncResult, fetchErr error) {
	if !s.opts.RecordRun {
		return
	}
	var errMsg *string
	switch {
	case len(res.Errors) > 0:
		m := strings.Join(res.Errors, "; ")
		if len(m) > 2000 {
			m = m[:2000] + "…"
		}
		errMsg = &m
	case fetchErr != nil:
		m := fetchErr.Error()
		errMsg = &m
	}
	detBytes, _ := json.Marshal(map[string]any{
		"fixtures_in":       res.FixturesIn,
		"competitions":      res.Competitions,
		"teams":             res.Teams,
		"matches_upserted":  res.MatchesUpserted,
		"model_predictions": res.ModelPredictions,
	})
	dj := string(detBytes)
	finished := res.FinishedAt
	run := repository.IngestionRun{
		ID: uuid.NewString(), Provider: res.Provider, JobName: res.JobName,
		Status: res.Status, StartedAt: res.StartedAt, FinishedAt: &finished,
		RecordsIn: res.FixturesIn, RecordsOut: res.MatchesUpserted + res.ModelPredictions,
		ErrorMessage: errMsg, DetailsJSON: &dj,
	}
	if err := s.deps.Ingestion.Insert(ctx, run); err != nil {
		s.deps.Logger.Warn("record ingestion run failed", "err", err)
	}
}
