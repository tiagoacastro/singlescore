// Package main — the SingleScore demo seeder.
//
// Populates competitions, teams, matches, events, model predictions,
// benchmark predictions and ingestion runs with transparent demo data.
// All rows are tagged provenance = DEMO so the UI can label them clearly.
//
// Scope (v1): Primeira Liga (Portugal), Premier League (England),
// Serie A (Italy), La Liga (Spain), Bundesliga (Germany).
//
// Idempotency: competitions and teams are UPSERTed by stable UUIDs derived
// from (provider, external_id). Matches are UPSERTed by stable UUIDs too.
// Related rows (events / predictions / benchmarks) are wiped per match and
// re-inserted so re-running the seeder produces consistent state.
package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"database/sql"

	"github.com/tiagocastro/singlescore/backend/internal/config"
	"github.com/tiagocastro/singlescore/backend/internal/db"
	"github.com/tiagocastro/singlescore/backend/internal/logging"
	"github.com/tiagocastro/singlescore/backend/internal/prediction"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

const (
	demoProvider = "DEMO"
	season       = "2026/27"
)

type teamSeed struct {
	name     string
	short    string
	country  string
	elo      float64
	external string // provider external id
}

type compSeed struct {
	name     string
	country  string
	external string
	teams    []teamSeed
}

// bigFive — Portugal plus the Big Four + Bundesliga.
var bigFive = []compSeed{
	{
		name:     "Primeira Liga",
		country:  "Portugal",
		external: "PPL",
		teams: []teamSeed{
			{name: "SL Benfica", short: "SLB", country: "Portugal", elo: 1810, external: "PPL-SLB"},
			{name: "FC Porto", short: "FCP", country: "Portugal", elo: 1795, external: "PPL-FCP"},
			{name: "Sporting CP", short: "SCP", country: "Portugal", elo: 1820, external: "PPL-SCP"},
			{name: "SC Braga", short: "SCB", country: "Portugal", elo: 1690, external: "PPL-SCB"},
			{name: "Vitória SC", short: "VSC", country: "Portugal", elo: 1560, external: "PPL-VSC"},
			{name: "Rio Ave FC", short: "RAV", country: "Portugal", elo: 1500, external: "PPL-RAV"},
		},
	},
	{
		name:     "Premier League",
		country:  "England",
		external: "PL",
		teams: []teamSeed{
			{name: "Manchester City", short: "MCI", country: "England", elo: 1980, external: "PL-MCI"},
			{name: "Arsenal", short: "ARS", country: "England", elo: 1935, external: "PL-ARS"},
			{name: "Liverpool", short: "LIV", country: "England", elo: 1945, external: "PL-LIV"},
			{name: "Manchester United", short: "MUN", country: "England", elo: 1810, external: "PL-MUN"},
			{name: "Chelsea", short: "CHE", country: "England", elo: 1795, external: "PL-CHE"},
			{name: "Tottenham Hotspur", short: "TOT", country: "England", elo: 1810, external: "PL-TOT"},
		},
	},
	{
		name:     "Serie A",
		country:  "Italy",
		external: "SA",
		teams: []teamSeed{
			{name: "Inter", short: "INT", country: "Italy", elo: 1915, external: "SA-INT"},
			{name: "AC Milan", short: "MIL", country: "Italy", elo: 1860, external: "SA-MIL"},
			{name: "Juventus", short: "JUV", country: "Italy", elo: 1850, external: "SA-JUV"},
			{name: "Napoli", short: "NAP", country: "Italy", elo: 1875, external: "SA-NAP"},
			{name: "AS Roma", short: "ROM", country: "Italy", elo: 1780, external: "SA-ROM"},
			{name: "Lazio", short: "LAZ", country: "Italy", elo: 1760, external: "SA-LAZ"},
		},
	},
	{
		name:     "La Liga",
		country:  "Spain",
		external: "PD",
		teams: []teamSeed{
			{name: "Real Madrid", short: "RMA", country: "Spain", elo: 1990, external: "PD-RMA"},
			{name: "FC Barcelona", short: "FCB", country: "Spain", elo: 1960, external: "PD-FCB"},
			{name: "Atlético de Madrid", short: "ATM", country: "Spain", elo: 1870, external: "PD-ATM"},
			{name: "Real Sociedad", short: "RSO", country: "Spain", elo: 1740, external: "PD-RSO"},
			{name: "Athletic Club", short: "ATH", country: "Spain", elo: 1735, external: "PD-ATH"},
			{name: "Villarreal", short: "VIL", country: "Spain", elo: 1725, external: "PD-VIL"},
		},
	},
	{
		name:     "Bundesliga",
		country:  "Germany",
		external: "BL1",
		teams: []teamSeed{
			{name: "Bayern München", short: "BAY", country: "Germany", elo: 1965, external: "BL1-BAY"},
			{name: "Borussia Dortmund", short: "BVB", country: "Germany", elo: 1830, external: "BL1-BVB"},
			{name: "RB Leipzig", short: "RBL", country: "Germany", elo: 1800, external: "BL1-RBL"},
			{name: "Bayer Leverkusen", short: "B04", country: "Germany", elo: 1875, external: "BL1-B04"},
			{name: "Eintracht Frankfurt", short: "SGE", country: "Germany", elo: 1700, external: "BL1-SGE"},
			{name: "VfB Stuttgart", short: "VFB", country: "Germany", elo: 1710, external: "BL1-VFB"},
		},
	},
}

func main() {
	resetFlag := flag.Bool("reset", true, "wipe events/predictions for seeded matches before inserting")
	wipeDemo := flag.Bool("wipe-demo", false, "delete ALL rows with provenance=DEMO before seeding (clean slate)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	logger := logging.New(cfg.LogLevel)
	logger.Info("seed starting",
		"host", cfg.DB.Host,
		"db", cfg.DB.Database,
		"reset", *resetFlag,
		"wipe_demo", *wipeDemo,
		"leagues", len(bigFive),
	)

	pool, err := db.Open(cfg.DB)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := db.Ping(ctx, pool); err != nil {
		logger.Error("db ping failed; aborting seed", "err", err)
		os.Exit(1)
	}

	catalog := repository.NewCatalog(pool)
	matches := repository.NewMatches(pool)
	preds := repository.NewPredictions(pool)
	ingest := repository.NewIngestion(pool)
	baseline := prediction.NewBaseline()

	if *wipeDemo {
		if err := wipeDemoData(ctx, pool, logger); err != nil {
			logger.Error("wipe-demo failed", "err", err)
			os.Exit(1)
		}
	}

	runID := uuid.NewString()
	// SingleStore DATETIME(6) is picky about non-canonical fractional
	// representations from go-sql-driver's interpolation, so we round to
	// microseconds explicitly.
	start := time.Now().UTC().Round(time.Microsecond)
	if err := ingest.Insert(ctx, repository.IngestionRun{
		ID: runID, Provider: demoProvider, JobName: "seed", Status: "STARTED", StartedAt: start,
	}); err != nil {
		logger.Warn("ingest start insert failed", "err", err)
	}

	stats, err := run(ctx, logger, catalog, matches, preds, ingest, baseline, *resetFlag)
	finished := time.Now().UTC().Round(time.Microsecond)
	if err != nil {
		msg := err.Error()
		_ = ingest.Insert(ctx, repository.IngestionRun{
			ID: uuid.NewString(), Provider: demoProvider, JobName: "seed",
			Status: "FAILED", StartedAt: start, FinishedAt: &finished,
			RecordsIn: stats.recordsIn, RecordsOut: stats.recordsOut, ErrorMessage: &msg,
		})
		logger.Error("seed failed", "err", err, "records_in", stats.recordsIn, "records_out", stats.recordsOut)
		os.Exit(1)
	}
	_ = ingest.Insert(ctx, repository.IngestionRun{
		ID: uuid.NewString(), Provider: demoProvider, JobName: "seed",
		Status: "SUCCEEDED", StartedAt: start, FinishedAt: &finished,
		RecordsIn: stats.recordsIn, RecordsOut: stats.recordsOut,
	})
	logger.Info("seed complete",
		"competitions", stats.competitions,
		"teams", stats.teams,
		"matches", stats.matches,
		"events", stats.events,
		"model_preds", stats.modelPreds,
		"benchmark_preds", stats.benchPreds,
		"duration_ms", time.Since(start).Milliseconds(),
	)
}

type seedStats struct {
	competitions int
	teams        int
	matches      int
	events       int
	modelPreds   int
	benchPreds   int
	recordsIn    int
	recordsOut   int
}

func run(ctx context.Context, logger loggerLike,
	catalog *repository.Catalog, matches *repository.Matches,
	preds *repository.Predictions, ingest *repository.Ingestion,
	baseline prediction.Baseline, reset bool) (seedStats, error) {

	var s seedStats
	rng := rand.New(rand.NewSource(42)) // deterministic

	compByID := map[string]compSeed{}
	teamsByID := map[string]teamSeed{}
	teamIDByExternal := map[string]string{}
	compIDByExternal := map[string]string{}

	for _, c := range bigFive {
		cid := deterministicID("competition", c.external)
		country := c.country
		seasonStr := season
		if err := catalog.UpsertCompetition(ctx, repository.Competition{
			ID: cid, Provider: demoProvider, ExternalID: strPtr(c.external),
			Name: c.name, Country: &country, Season: &seasonStr,
		}); err != nil {
			return s, fmt.Errorf("upsert competition %s: %w", c.name, err)
		}
		compByID[cid] = c
		compIDByExternal[c.external] = cid
		s.competitions++
		s.recordsIn++
		s.recordsOut++
		for _, t := range c.teams {
			tid := deterministicID("team", t.external)
			short := t.short
			country := t.country
			if err := catalog.UpsertTeam(ctx, repository.Team{
				ID: tid, Provider: demoProvider, ExternalID: strPtr(t.external),
				Name: t.name, ShortName: &short, Country: &country,
				EloRating: t.elo,
			}); err != nil {
				return s, fmt.Errorf("upsert team %s: %w", t.name, err)
			}
			teamsByID[tid] = t
			teamIDByExternal[t.external] = tid
			s.teams++
			s.recordsIn++
			s.recordsOut++
		}
	}
	logger.Info("catalog seeded",
		"competitions", s.competitions, "teams", s.teams)

	// Build the fixture plan. For each competition we want:
	//   2 finished matches (yesterday)
	//   1 live match (today, in progress)
	//   2 scheduled matches (tomorrow / next week)
	now := time.Now().UTC()
	type fixturePlan struct {
		compExt   string
		homeExt   string
		awayExt   string
		kickoff   time.Time
		status    repository.MatchStatus
		homeScore int
		awayScore int
		minute    *int
		final     *repository.Outcome
	}

	plans := []fixturePlan{}

	for _, c := range bigFive {
		if len(c.teams) < 4 {
			continue
		}
		// Pair up teams deterministically.
		pairs := [][2]int{{0, 3}, {1, 4}, {2, 5}, {0, 2}, {3, 1}}
		for i, pair := range pairs {
			home := c.teams[pair[0]]
			away := c.teams[pair[1]]
			var status repository.MatchStatus
			var kickoff time.Time
			var homeScore, awayScore int
			var minute *int
			var final *repository.Outcome
			switch i {
			case 0, 1:
				// Finished — yesterday at various times.
				status = repository.StatusFinished
				kickoff = truncateHour(now.AddDate(0, 0, -1).Add(time.Duration(14+2*i) * time.Hour))
				homeScore, awayScore = deterministicFinalScore(home.elo, away.elo, rng.Int63())
				fr := finalOutcome(homeScore, awayScore)
				final = &fr
			case 2:
				// Live — kicked off ~60-70 minutes ago.
				status = repository.StatusLive
				min := 42 + i*10 // 62
				minute = &min
				kickoff = now.Add(-time.Duration(min) * time.Minute).Truncate(time.Minute)
				homeScore, awayScore = inPlayScore(home.elo, away.elo, min, rng.Int63())
			default:
				// Scheduled — upcoming.
				status = repository.StatusScheduled
				daysAhead := i - 2 // 1, 2
				kickoff = truncateHour(now.AddDate(0, 0, daysAhead).Add(19 * time.Hour))
			}
			plans = append(plans, fixturePlan{
				compExt: c.external, homeExt: home.external, awayExt: away.external,
				kickoff: kickoff, status: status,
				homeScore: homeScore, awayScore: awayScore,
				minute: minute, final: final,
			})
		}
	}

	for _, p := range plans {
		cid := compIDByExternal[p.compExt]
		htID := teamIDByExternal[p.homeExt]
		atID := teamIDByExternal[p.awayExt]
		// Match id derived from (comp, home, away, kickoff-date). We deliberately
		// avoid the full kickoff timestamp so re-running the seeder within the
		// same day produces the same id and upserts in place.
		mid := deterministicID("match", p.compExt+"|"+p.homeExt+"|"+p.awayExt+"|"+p.kickoff.UTC().Format("2006-01-02"))

		srcUpdated := p.kickoff.Round(time.Microsecond)
		if p.minute != nil {
			t := p.kickoff.Add(time.Duration(*p.minute) * time.Minute).Round(time.Microsecond)
			srcUpdated = t
		}
		kickoffMicro := p.kickoff.Round(time.Microsecond)

		ext := p.compExt + "-" + p.homeExt + "-" + p.awayExt
		if err := matches.UpsertMatch(ctx, repository.Match{
			ID: mid, Provider: demoProvider, ExternalID: &ext,
			CompetitionID: cid, HomeTeamID: htID, AwayTeamID: atID,
			KickoffAt: kickoffMicro, Status: p.status,
			Minute: p.minute, HomeScore: p.homeScore, AwayScore: p.awayScore,
			FinalResult: p.final, Provenance: repository.ProvenanceDemo,
			SourceUpdatedAt: &srcUpdated,
		}); err != nil {
			return s, fmt.Errorf("upsert match %s vs %s: %w", p.homeExt, p.awayExt, err)
		}
		s.matches++
		s.recordsIn++
		s.recordsOut++

		if reset {
			if err := preds.DeleteEventsForMatch(ctx, mid); err != nil {
				return s, err
			}
			if err := preds.DeleteModelForMatch(ctx, mid); err != nil {
				return s, err
			}
			if err := preds.DeleteBenchmarkForMatch(ctx, mid); err != nil {
				return s, err
			}
		}

		home := teamsByID[htID]
		away := teamsByID[atID]

		// Events: synthesise plausible goal + card events for finished and live.
		events := synthesizeEvents(mid, p.status, p.minute, p.kickoff, p.homeScore, p.awayScore, rng)
		if err := matches.InsertEventsBatch(ctx, events); err != nil {
			return s, fmt.Errorf("insert events batch: %w", err)
		}
		s.events += len(events)
		s.recordsOut += len(events)

		// Model predictions: pre-match + minute snapshots.
		snaps := predictionTimeline(baseline, home.elo, away.elo, p.status, p.minute, p.homeScore, p.awayScore, p.kickoff)
		modelRows := make([]repository.ModelPrediction, 0, len(snaps))
		for _, snap := range snaps {
			modelRows = append(modelRows, repository.ModelPrediction{
				ID: uuid.NewString(), MatchID: mid,
				ModelName: baseline.Name(), ModelVersion: baseline.Version(),
				ForecastAt:    snap.at.Round(time.Microsecond),
				MatchMinute:   snap.minute,
				HomeScoreAtFC: snap.homeAt, AwayScoreAtFC: snap.awayAt,
				PHome: snap.probs.Home, PDraw: snap.probs.Draw, PAway: snap.probs.Away,
				Provenance: repository.ProvenanceDemo,
			})
		}
		if err := preds.InsertModelBatch(ctx, modelRows); err != nil {
			return s, fmt.Errorf("insert model batch: %w", err)
		}
		s.modelPreds += len(modelRows)
		s.recordsOut += len(modelRows)

		// Benchmark predictions: 1 pre-match from demo-market, plus 1 live
		// snapshot for live/finished matches.
		benchs := benchmarkTimeline(home.elo, away.elo, p.status, p.minute, p.homeScore, p.awayScore, p.kickoff, rng)
		for i := range benchs {
			benchs[i].ID = uuid.NewString()
			benchs[i].MatchID = mid
			benchs[i].Provider = "demo-market"
			benchs[i].Provenance = repository.ProvenanceDemo
			benchs[i].ForecastAt = benchs[i].ForecastAt.Round(time.Microsecond)
		}
		if err := preds.InsertBenchmarkBatch(ctx, benchs); err != nil {
			return s, fmt.Errorf("insert benchmark batch: %w", err)
		}
		s.benchPreds += len(benchs)
		s.recordsOut += len(benchs)
	}

	// Record a couple of synthetic ingestion runs so the dashboard has
	// something to show.
	runs := []repository.IngestionRun{
		{
			ID: uuid.NewString(), Provider: "demo-market", JobName: "benchmarks",
			Status: "SUCCEEDED", StartedAt: now.Add(-45 * time.Minute).Round(time.Microsecond),
			FinishedAt: timePtr(now.Add(-44 * time.Minute).Round(time.Microsecond)),
			RecordsIn:  s.benchPreds, RecordsOut: s.benchPreds,
		},
		{
			ID: uuid.NewString(), Provider: demoProvider, JobName: "replay",
			Status: "SUCCEEDED", StartedAt: now.Add(-10 * time.Minute).Round(time.Microsecond),
			FinishedAt: timePtr(now.Add(-9 * time.Minute).Round(time.Microsecond)),
			RecordsIn:  s.modelPreds, RecordsOut: s.modelPreds,
		},
	}
	for _, r := range runs {
		_ = ingest.Insert(ctx, r)
	}

	return s, nil
}

// ------------------------- helpers --------------------------

type loggerLike interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// wipeDemoData removes all DEMO-provenance rows from matches + related
// tables, plus demo ingestion-run rows. Catalog rows (competitions, teams)
// are left in place because they're small and upserted again anyway.
func wipeDemoData(ctx context.Context, pool *sql.DB, logger loggerLike) error {
	wipeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	stmts := []string{
		`DELETE FROM match_events WHERE match_id IN (SELECT id FROM matches WHERE provenance='DEMO')`,
		`DELETE FROM model_predictions WHERE match_id IN (SELECT id FROM matches WHERE provenance='DEMO')`,
		`DELETE FROM benchmark_predictions WHERE match_id IN (SELECT id FROM matches WHERE provenance='DEMO')`,
		`DELETE FROM matches WHERE provenance='DEMO'`,
		`DELETE FROM data_ingestion_runs WHERE provider IN ('DEMO','demo-market')`,
	}
	for _, q := range stmts {
		if _, err := pool.ExecContext(wipeCtx, q); err != nil {
			return fmt.Errorf("wipe (%s): %w", q, err)
		}
	}
	logger.Info("wiped demo data")
	return nil
}

type modelSnap struct {
	at     time.Time
	minute *int
	homeAt int
	awayAt int
	probs  prediction.Probabilities
}

func predictionTimeline(baseline prediction.Baseline,
	homeElo, awayElo float64,
	status repository.MatchStatus, liveMinute *int,
	finalHome, finalAway int, kickoff time.Time,
) []modelSnap {

	snaps := []modelSnap{}

	// Pre-match snapshot: 1 hour before kickoff.
	preAt := kickoff.Add(-1 * time.Hour)
	preMin := 0
	preProbs, _ := baseline.Predict(prediction.Snapshot{
		HomeElo: homeElo, AwayElo: awayElo,
		HomeScore: 0, AwayScore: 0, Minute: 0,
	})
	snaps = append(snaps, modelSnap{at: preAt, minute: &preMin, probs: preProbs})

	switch status {
	case repository.StatusFinished:
		// Snapshots every 15 minutes, with simulated scoreline progression.
		scores := interpolateScoreProgression(finalHome, finalAway)
		mins := []int{5, 15, 30, 45, 60, 75, 90}
		for _, m := range mins {
			idx := int(float64(m) / 90.0 * float64(len(scores)-1))
			if idx >= len(scores) {
				idx = len(scores) - 1
			}
			hs, as := scores[idx][0], scores[idx][1]
			probs, _ := baseline.Predict(prediction.Snapshot{
				HomeElo: homeElo, AwayElo: awayElo,
				HomeScore: hs, AwayScore: as, Minute: m,
			})
			mm := m
			snaps = append(snaps, modelSnap{
				at: kickoff.Add(time.Duration(m) * time.Minute), minute: &mm,
				homeAt: hs, awayAt: as, probs: probs,
			})
		}
	case repository.StatusLive:
		cur := 0
		if liveMinute != nil {
			cur = *liveMinute
		}
		// Pre-match plus a few in-play snapshots up to the current minute.
		mins := []int{5, 15, 30}
		if cur >= 45 {
			mins = append(mins, 45)
		}
		if cur >= 60 {
			mins = append(mins, 60)
		}
		if cur > 0 {
			mins = append(mins, cur)
		}
		scores := scoreTrajectory(finalHome, finalAway, cur)
		for _, m := range mins {
			idx := int(float64(m) / float64(max(1, cur)) * float64(len(scores)-1))
			if idx >= len(scores) {
				idx = len(scores) - 1
			}
			hs, as := scores[idx][0], scores[idx][1]
			probs, _ := baseline.Predict(prediction.Snapshot{
				HomeElo: homeElo, AwayElo: awayElo,
				HomeScore: hs, AwayScore: as, Minute: m,
			})
			mm := m
			snaps = append(snaps, modelSnap{
				at: kickoff.Add(time.Duration(m) * time.Minute), minute: &mm,
				homeAt: hs, awayAt: as, probs: probs,
			})
		}
	case repository.StatusScheduled:
		// Pre-match only — already added above.
	}
	return snaps
}

func benchmarkTimeline(homeElo, awayElo float64,
	status repository.MatchStatus, liveMinute *int,
	finalHome, finalAway int, kickoff time.Time, rng *rand.Rand,
) []repository.BenchmarkPrediction {

	// Pre-match "market" benchmark: perturb the Elo-implied prior slightly.
	base := eloWinExpectancy(homeElo, awayElo, 60)
	drawShare := 0.26 - 0.3*math.Abs(base-0.5)
	drawShare = clamp(drawShare, 0.14, 0.3)
	pre := repository.BenchmarkPrediction{
		SourceType: "MARKET_ODDS", Horizon: "PRE_MATCH",
		ForecastAt:    kickoff.Add(-30 * time.Minute),
		MatchMinute:   nil,
		HomeScoreAtFC: 0, AwayScoreAtFC: 0,
		PHome: clamp(base*(1-drawShare)+(rng.Float64()-0.5)*0.03, 0.02, 0.98),
		PDraw: clamp(drawShare+(rng.Float64()-0.5)*0.02, 0.05, 0.5),
		PAway: 0,
		MatchStatus: strPtr(string(repository.StatusScheduled)),
	}
	pre.PAway = clamp(1-pre.PHome-pre.PDraw, 0.02, 0.98)
	// Renormalise.
	sum := pre.PHome + pre.PDraw + pre.PAway
	pre.PHome /= sum
	pre.PDraw /= sum
	pre.PAway /= sum
	out := []repository.BenchmarkPrediction{pre}

	if status == repository.StatusLive && liveMinute != nil {
		m := *liveMinute
		live := repository.BenchmarkPrediction{
			SourceType: "MARKET_ODDS", Horizon: "LIVE",
			ForecastAt:    kickoff.Add(time.Duration(m) * time.Minute),
			MatchMinute:   &m,
			HomeScoreAtFC: finalHome, AwayScoreAtFC: finalAway,
			MatchStatus: strPtr(string(repository.StatusLive)),
		}
		// Simple live model: boost leader a bit, shrink draw a bit, add noise.
		leader := 0
		if finalHome > finalAway {
			leader = 1
		} else if finalAway > finalHome {
			leader = -1
		}
		pH := base
		pD := drawShare
		if leader == 1 {
			pH = clamp(pH+0.2, 0.01, 0.99)
			pD = clamp(pD-0.1, 0.05, 0.5)
		} else if leader == -1 {
			pH = clamp(pH-0.2, 0.01, 0.99)
			pD = clamp(pD-0.1, 0.05, 0.5)
		} else {
			pD = clamp(pD+0.1, 0.05, 0.5)
		}
		pA := clamp(1-pH-pD, 0.01, 0.99)
		live.PHome = pH
		live.PDraw = pD
		live.PAway = pA
		// Renormalise.
		sum := live.PHome + live.PDraw + live.PAway
		live.PHome /= sum
		live.PDraw /= sum
		live.PAway /= sum
		out = append(out, live)
	}

	if status == repository.StatusFinished {
		// Snapshot at ~75' for the record.
		m := 75
		fin := repository.BenchmarkPrediction{
			SourceType: "MARKET_ODDS", Horizon: "LIVE",
			ForecastAt:    kickoff.Add(time.Duration(m) * time.Minute),
			MatchMinute:   &m,
			HomeScoreAtFC: finalHome, AwayScoreAtFC: finalAway,
			MatchStatus:   strPtr(string(repository.StatusLive)),
			PHome:         clamp(base+0.1, 0.01, 0.99),
			PDraw:         clamp(drawShare*0.8, 0.05, 0.5),
		}
		fin.PAway = clamp(1-fin.PHome-fin.PDraw, 0.02, 0.98)
		sum := fin.PHome + fin.PDraw + fin.PAway
		fin.PHome /= sum
		fin.PDraw /= sum
		fin.PAway /= sum
		out = append(out, fin)
	}
	return out
}

// synthesizeEvents produces a plausible set of goals/cards given a final
// scoreline. For live matches we only emit events up to the current minute.
func synthesizeEvents(matchID string, status repository.MatchStatus, liveMinute *int,
	kickoff time.Time, homeScore, awayScore int, rng *rand.Rand) []repository.MatchEvent {

	cap := 0
	switch status {
	case repository.StatusFinished:
		cap = homeScore + awayScore + 2
	case repository.StatusLive:
		cap = homeScore + awayScore + 1
	}
	if cap == 0 {
		return nil
	}

	events := make([]repository.MatchEvent, 0, cap)

	// Spread goal minutes across the first 90 (or up to current for live).
	ceiling := 90
	if status == repository.StatusLive && liveMinute != nil {
		ceiling = *liveMinute
	}
	if ceiling <= 0 {
		return nil
	}

	// Deterministic sequence derived from matchID so events don't move
	// between seed runs for the same match.
	seed := sha1Int(matchID)
	local := rand.New(rand.NewSource(seed))
	seq := 0
	emitGoal := func(side string, idx int) {
		min := 5 + local.Intn(max(1, ceiling-5))
		at := kickoff.Add(time.Duration(min) * time.Minute).Round(time.Microsecond)
		desc := fmt.Sprintf("%s goal", strings.ToLower(side))
		src := at
		seq++
		events = append(events, repository.MatchEvent{
			ID: deterministicID("event", fmt.Sprintf("%s|goal|%s|%d|%d", matchID, side, idx, seq)),
			MatchID: matchID, Provider: demoProvider,
			EventType: "GOAL", TeamSide: strPtr(side),
			Minute: &min, Description: &desc,
			EventTime: at, SourceTimestamp: &src,
		})
	}
	for i := 0; i < homeScore; i++ {
		emitGoal("HOME", i)
	}
	for i := 0; i < awayScore; i++ {
		emitGoal("AWAY", i)
	}
	// A yellow card or two for colour.
	if ceiling > 20 {
		for i := 0; i < 2; i++ {
			min := 20 + local.Intn(max(1, ceiling-20))
			side := "HOME"
			if local.Intn(2) == 0 {
				side = "AWAY"
			}
			at := kickoff.Add(time.Duration(min) * time.Minute).Round(time.Microsecond)
			desc := "yellow card"
			src := at
			seq++
			events = append(events, repository.MatchEvent{
				ID: deterministicID("event", fmt.Sprintf("%s|card|%s|%d|%d", matchID, side, i, seq)),
				MatchID: matchID, Provider: demoProvider,
				EventType: "YELLOW_CARD", TeamSide: strPtr(side),
				Minute: &min, Description: &desc,
				EventTime: at, SourceTimestamp: &src,
			})
		}
	}
	// Sort by minute to keep timeline tidy.
	for i := 0; i < len(events); i++ {
		for j := i + 1; j < len(events); j++ {
			if events[j].EventTime.Before(events[i].EventTime) {
				events[i], events[j] = events[j], events[i]
			}
		}
	}
	return events
}

func interpolateScoreProgression(finalHome, finalAway int) [][2]int {
	// Simple linear interpolation of goals over 10 steps.
	steps := 10
	out := make([][2]int, steps+1)
	for i := 0; i <= steps; i++ {
		fh := float64(finalHome) * float64(i) / float64(steps)
		fa := float64(finalAway) * float64(i) / float64(steps)
		out[i] = [2]int{int(math.Floor(fh)), int(math.Floor(fa))}
	}
	out[steps] = [2]int{finalHome, finalAway}
	return out
}

func scoreTrajectory(curHome, curAway, curMinute int) [][2]int {
	// Same as interpolate but up to current scoreline / current minute.
	if curMinute <= 0 {
		return [][2]int{{0, 0}}
	}
	steps := 6
	out := make([][2]int, steps+1)
	for i := 0; i <= steps; i++ {
		fh := float64(curHome) * float64(i) / float64(steps)
		fa := float64(curAway) * float64(i) / float64(steps)
		out[i] = [2]int{int(math.Floor(fh)), int(math.Floor(fa))}
	}
	out[steps] = [2]int{curHome, curAway}
	return out
}

func deterministicFinalScore(homeElo, awayElo float64, salt int64) (int, int) {
	// Elo-biased score sampler bounded 0-3, with periodic "upsets" so the
	// demo doesn't look suspiciously perfect against our baseline predictor.
	e := eloWinExpectancy(homeElo, awayElo, 60)
	// Use the salt as a cheap offset so different pairs look different.
	bump := float64(salt%7) / 10.0
	// Every 3rd match flips the Elo lean to inject upsets.
	if (salt%3) == 0 {
		e = 1 - e
	}
	h := int(math.Round(e*3 + bump*0.3))
	a := int(math.Round((1-e)*3 - bump*0.1))
	if h < 0 {
		h = 0
	}
	if a < 0 {
		a = 0
	}
	if h > 4 {
		h = 4
	}
	if a > 4 {
		a = 4
	}
	return h, a
}

func inPlayScore(homeElo, awayElo float64, minute int, salt int64) (int, int) {
	h, a := deterministicFinalScore(homeElo, awayElo, salt)
	// Scale down proportionally to minutes played.
	frac := float64(minute) / 90.0
	return int(math.Round(float64(h) * frac)), int(math.Round(float64(a) * frac))
}

func eloWinExpectancy(homeElo, awayElo, homeAdvantage float64) float64 {
	return 1.0 / (1.0 + math.Pow(10, (awayElo-(homeElo+homeAdvantage))/400.0))
}

func finalOutcome(home, away int) repository.Outcome {
	switch {
	case home > away:
		return repository.OutcomeHome
	case home < away:
		return repository.OutcomeAway
	default:
		return repository.OutcomeDraw
	}
}

func deterministicID(prefix, key string) string {
	h := sha1.Sum([]byte(prefix + "|" + key))
	// Build a UUID v5-ish from the first 16 bytes.
	id, err := uuid.FromBytes(h[:16])
	if err != nil {
		return uuid.NewString()
	}
	return id.String()
}

func sha1Int(s string) int64 {
	h := sha1.Sum([]byte(s))
	hex := hex.EncodeToString(h[:4])
	var out int64
	fmt.Sscanf(hex, "%x", &out)
	if out < 0 {
		out = -out
	}
	return out
}

func truncateHour(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
}

func strPtr(s string) *string {
	s2 := s
	return &s2
}

func timePtr(t time.Time) *time.Time { return &t }

func clamp(x, lo, hi float64) float64 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}
