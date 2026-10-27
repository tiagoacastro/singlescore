package ingestion

import (
	"context"
	"crypto/sha1"
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

// SyncResult summarises a sync run.
type SyncResult struct {
	Provider         string
	JobName          string
	StartedAt        time.Time
	FinishedAt       time.Time
	Status           string // STARTED SUCCEEDED FAILED PARTIAL
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
	// syncs leave the team row's elo_rating untouched (upsert uses the
	// first-seen value — see Catalog.UpsertTeam which writes elo on both
	// insert and update, so we always pass this value; good-enough until
	// Elo is maintained by a dedicated job).
	DefaultEloRating float64
	// HomeAdvantageElo is passed to the predictor.
	HomeAdvantageElo float64
	// RecordRun controls whether a data_ingestion_runs row is written.
	// Defaults to true; set false in tests.
	RecordRun bool
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

// Sync pulls fixtures from a Provider and upserts them into SingleStore,
// also writing a fresh model prediction snapshot per match.
type Sync struct {
	deps     Deps
	provider Provider
	opts     SyncOptions
}

// NewSync returns a Sync.
func NewSync(deps Deps, provider Provider, opts SyncOptions) *Sync {
	if opts.DefaultEloRating == 0 {
		opts.DefaultEloRating = 1500
	}
	if opts.HomeAdvantageElo == 0 {
		opts.HomeAdvantageElo = 60
	}
	return &Sync{deps: deps, provider: provider, opts: opts}
}

// Run executes one sync pass. The returned SyncResult is always
// populated, even if the run partially or fully fails, so callers can
// write a diagnostic row to data_ingestion_runs.
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
	if fetchErr != nil {
		res.Status = "FAILED"
		res.FinishedAt = time.Now().UTC().Round(time.Microsecond)
		res.Errors = append(res.Errors, fmt.Sprintf("fetch fixtures: %v", fetchErr))
		s.recordRun(ctx, res, fetchErr)
		return res, fetchErr
	}
	res.FixturesIn = len(fixtures)
	s.deps.Logger.Info("sync fetched fixtures", "provider", res.Provider, "count", res.FixturesIn)

	// 1) competitions + teams
	type teamKey struct{ ext string }
	competitionIDs := map[string]string{} // extCompID -> internal UUID
	teamIDs := map[string]string{}        // extTeamID -> internal UUID

	for _, fx := range fixtures {
		if _, ok := competitionIDs[fx.ExternalCompID]; !ok {
			cid := deterministicID("competition", s.provider.Name()+"|"+fx.ExternalCompID)
			country := fx.Country
			season := fx.Season
			if err := s.deps.Catalog.UpsertCompetition(ctx, repository.Competition{
				ID: cid, Provider: s.provider.Name(), ExternalID: strPtr(fx.ExternalCompID),
				Name: fx.CompetitionName, Country: strOrNil(country), Season: strOrNil(season),
			}); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("upsert competition %s: %v", fx.ExternalCompID, err))
				continue
			}
			competitionIDs[fx.ExternalCompID] = cid
			res.Competitions++
		}
		for _, team := range []struct{ ext, name string }{
			{fx.HomeTeamExtID, fx.HomeTeamName},
			{fx.AwayTeamExtID, fx.AwayTeamName},
		} {
			if _, ok := teamIDs[team.ext]; ok {
				continue
			}
			tid := deterministicID("team", s.provider.Name()+"|"+team.ext)
			if err := s.deps.Catalog.UpsertTeam(ctx, repository.Team{
				ID: tid, Provider: s.provider.Name(), ExternalID: strPtr(team.ext),
				Name: team.name, EloRating: s.opts.DefaultEloRating,
			}); err != nil {
				res.Errors = append(res.Errors, fmt.Sprintf("upsert team %s: %v", team.ext, err))
				continue
			}
			teamIDs[team.ext] = tid
			res.Teams++
		}
	}

	// 2) matches
	type pendingMatch struct {
		fx       Fixture
		matchID  string
		homeID   string
		awayID   string
		homeTeam repository.Team
		awayTeam repository.Team
	}
	var pending []pendingMatch

	for _, fx := range fixtures {
		cid, cok := competitionIDs[fx.ExternalCompID]
		hid, hok := teamIDs[fx.HomeTeamExtID]
		aid, aok := teamIDs[fx.AwayTeamExtID]
		if !cok || !hok || !aok {
			res.Errors = append(res.Errors, fmt.Sprintf("missing refs for match %s", fx.ExternalMatchID))
			continue
		}
		mid := deterministicID("match", s.provider.Name()+"|"+fx.ExternalMatchID)
		kickoff := fx.KickoffAt.UTC().Round(time.Microsecond)
		srcUpdated := fx.SourceUpdatedAt.UTC().Round(time.Microsecond)
		ext := fx.ExternalMatchID
		if err := s.deps.Matches.UpsertMatch(ctx, repository.Match{
			ID: mid, Provider: s.provider.Name(), ExternalID: &ext,
			CompetitionID: cid, HomeTeamID: hid, AwayTeamID: aid,
			KickoffAt: kickoff, Status: fx.Status,
			Minute: fx.Minute, HomeScore: fx.HomeScore, AwayScore: fx.AwayScore,
			FinalResult: fx.FinalResult, Venue: fx.Venue,
			Provenance:      repository.ProvenanceLive,
			SourceUpdatedAt: &srcUpdated,
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("upsert match %s: %v", fx.ExternalMatchID, err))
			continue
		}
		res.MatchesUpserted++
		pending = append(pending, pendingMatch{fx: fx, matchID: mid, homeID: hid, awayID: aid})
	}

	// 3) refresh team Elo map (so predictor uses whatever Elo repo has).
	ids := make([]string, 0, len(teamIDs))
	for _, id := range teamIDs {
		ids = append(ids, id)
	}
	teamsByID, err := s.deps.Catalog.GetTeams(ctx, ids)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("read teams: %v", err))
	}
	for i := range pending {
		pending[i].homeTeam = teamsByID[pending[i].homeID]
		pending[i].awayTeam = teamsByID[pending[i].awayID]
	}

	// 4) write a fresh model prediction per upserted match. Scheduled
	// matches get a pre-match snapshot; live matches get an in-play one;
	// finished matches are not re-forecast (they're historical).
	rows := make([]repository.ModelPrediction, 0, len(pending))
	forecastAt := time.Now().UTC().Round(time.Microsecond)
	for _, pm := range pending {
		if pm.fx.Status == repository.StatusFinished || pm.fx.Status == repository.StatusCancelled || pm.fx.Status == repository.StatusPostponed {
			continue
		}
		snap := prediction.Snapshot{
			HomeElo:        valueOrDefault(pm.homeTeam.EloRating, s.opts.DefaultEloRating),
			AwayElo:        valueOrDefault(pm.awayTeam.EloRating, s.opts.DefaultEloRating),
			HomeScore:      pm.fx.HomeScore,
			AwayScore:      pm.fx.AwayScore,
			Minute:         minuteOrZero(pm.fx.Minute),
			HomeAdvantage:  s.opts.HomeAdvantageElo,
			MatchStartedAt: pm.fx.KickoffAt,
		}
		probs, perr := s.deps.Predictor.Predict(snap)
		if perr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("predict %s: %v", pm.fx.ExternalMatchID, perr))
			continue
		}
		feat := map[string]any{
			"home_elo":        snap.HomeElo,
			"away_elo":        snap.AwayElo,
			"minute":          snap.Minute,
			"home_score":      snap.HomeScore,
			"away_score":      snap.AwayScore,
			"home_advantage":  snap.HomeAdvantage,
			"model":           s.deps.Predictor.Name(),
			"model_version":   s.deps.Predictor.Version(),
			"source_provider": s.provider.Name(),
		}
		featBytes, _ := json.Marshal(feat)
		feats := string(featBytes)
		m := snap.Minute
		var minutePtr *int
		if snap.Minute > 0 {
			mm := m
			minutePtr = &mm
		}
		rows = append(rows, repository.ModelPrediction{
			ID:            uuid.NewString(),
			MatchID:       pm.matchID,
			ModelName:     s.deps.Predictor.Name(),
			ModelVersion:  s.deps.Predictor.Version(),
			ForecastAt:    forecastAt,
			MatchMinute:   minutePtr,
			HomeScoreAtFC: pm.fx.HomeScore,
			AwayScoreAtFC: pm.fx.AwayScore,
			PHome:         probs.Home,
			PDraw:         probs.Draw,
			PAway:         probs.Away,
			FeaturesJSON:  &feats,
			Provenance:    repository.ProvenanceLive,
		})
	}
	if len(rows) > 0 {
		if err := s.deps.Predictions.InsertModelBatch(ctx, rows); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("insert model batch: %v", err))
		} else {
			res.ModelPredictions = len(rows)
		}
	}

	// Final status
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

func (s *Sync) recordRun(ctx context.Context, res SyncResult, fetchErr error) {
	if !s.opts.RecordRun {
		return
	}
	var errMsg *string
	if len(res.Errors) > 0 {
		m := strings.Join(res.Errors, "; ")
		if len(m) > 2000 {
			m = m[:2000] + "…"
		}
		errMsg = &m
	} else if fetchErr != nil {
		m := fetchErr.Error()
		errMsg = &m
	}
	details := map[string]any{
		"fixtures_in":       res.FixturesIn,
		"competitions":      res.Competitions,
		"teams":             res.Teams,
		"matches_upserted":  res.MatchesUpserted,
		"model_predictions": res.ModelPredictions,
	}
	detBytes, _ := json.Marshal(details)
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

// ---------- helpers ----------

func deterministicID(prefix, key string) string {
	h := sha1.Sum([]byte(prefix + "|" + key))
	id, err := uuid.FromBytes(h[:16])
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func strPtr(s string) *string { return &s }

func strOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func minuteOrZero(m *int) int {
	if m == nil {
		return 0
	}
	return *m
}

func valueOrDefault(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
