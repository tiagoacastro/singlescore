// Package main — the SingleScore per-event ingestion CLI (api-football).
//
// Scope: fetches goals and red cards for FINISHED matches that already
// exist in our DB (ingested from football-data.org). Everything else
// api-football reports (yellow cards, substitutions, VAR, etc.) is
// filtered out at the provider layer so this binary never writes types
// the UI doesn't plot.
//
// Modes:
//
//	./ingest-events -map
//	    One-time sweep per season. Hits /v3/fixtures?league=X&season=Y
//	    once per configured league (5 calls total for our current
//	    coverage) and persists the (internal_match_id → api-football
//	    fixture_id) mapping to provider_match_map.
//
//	./ingest-events -match <internal-match-uuid>
//	    Fetch events for ONE match. Exactly 1 API call. Fails with a
//	    helpful message if the mapping is missing — run -map first.
//
//	./ingest-events -backfill -limit 50
//	    Walk recent FINISHED matches that have a mapping but no events
//	    stored yet. Fires at most -limit API calls. Combined with the
//	    client's 100-req/day ceiling, this will never blow the budget.
//
// Secrets: API_FOOTBALL_KEY MUST come from the environment. Never
// logged, written to disk, or returned over the API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/config"
	"github.com/tiagocastro/singlescore/backend/internal/db"
	"github.com/tiagocastro/singlescore/backend/internal/ingestion"
	"github.com/tiagocastro/singlescore/backend/internal/ingestion/apifootball"
	"github.com/tiagocastro/singlescore/backend/internal/ingestion/footballdata"
	"github.com/tiagocastro/singlescore/backend/internal/logging"
	"github.com/tiagocastro/singlescore/backend/internal/prediction"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

func main() {
	doMap := flag.Bool("map", false, "run the fixture mapping sweep (1 API call per unique match-day with FINISHED matches lacking a mapping)")
	mapBudget := flag.Int("map-budget", 10, "max API calls to spend on mapping in this run; stops early if the DB has fewer unmapped dates")
	matchID := flag.String("match", "", "fetch events for ONE internal match id (1 API call)")
	backfill := flag.Bool("backfill", false, "walk mapped matches that have no events yet and fetch each (1 API call per match, capped by -limit)")
	rebuildOnly := flag.String("rebuild-only", "", "rebuild the probability timeline for ONE finished match from already-stored events (0 API calls). Use to repair data without burning quota.")
	limit := flag.Int("limit", 10, "max matches to backfill in one run (hard cap by daily quota too)")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall deadline")
	flag.Parse()

	if !*doMap && *matchID == "" && !*backfill && *rebuildOnly == "" {
		fmt.Fprintln(os.Stderr, "nothing to do: pass -map, -match=<id>, -backfill, or -rebuild-only=<id>")
		os.Exit(2)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	logger := logging.New(cfg.LogLevel)

	key := strings.TrimSpace(os.Getenv("API_FOOTBALL_KEY"))
	if key == "" {
		logger.Error("API_FOOTBALL_KEY is not set",
			"hint", "export API_FOOTBALL_KEY=<your-free-tier-key> and re-run")
		os.Exit(2)
	}

	client, err := apifootball.New(key, apifootball.Options{})
	if err != nil {
		logger.Error("apifootball client", "err", err)
		os.Exit(1)
	}
	provider := apifootball.NewProvider(client, nil)

	pool, err := db.Open(cfg.DB)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := db.Ping(pingCtx, pool); err != nil {
		pingCancel()
		logger.Error("db ping failed", "err", err)
		os.Exit(1)
	}
	pingCancel()

	// Also stand up a football-data.org Sync — only used so EventsSync
	// can rebuild the probability timeline (not for fetching fixtures
	// here; the FOOTBALL_DATA_API_TOKEN is optional and the Sync is
	// never Run()). If the token is missing we skip the wire-up and
	// the timeline rebuild step becomes a no-op.
	var sync *ingestion.Sync
	if fdToken := strings.TrimSpace(os.Getenv("FOOTBALL_DATA_API_TOKEN")); fdToken != "" {
		fdClient, err := footballdata.New(fdToken, footballdata.Options{})
		if err == nil {
			fdProv := footballdata.NewProviderWithWindow(fdClient, footballdata.SupportedCompetitions, footballdata.MatchesQuery{})
			sync = ingestion.NewSync(ingestion.Deps{
				Catalog:     repository.NewCatalog(pool),
				Matches:     repository.NewMatches(pool),
				Predictions: repository.NewPredictions(pool),
				Ingestion:   repository.NewIngestion(pool),
				Predictor:   prediction.NewBaseline(),
				Logger:      logger,
			}, fdProv, ingestion.SyncOptions{})
		}
	}
	es := ingestion.NewEventsSync(
		provider,
		repository.NewMatches(pool),
		repository.NewCatalog(pool),
		repository.NewProviderMap(pool),
		sync,
		logger,
	)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// --- fixture mapping sweep ------------------------------------------
	if *doMap {
		logger.Info("fixture mapping sweep starting",
			"provider", apifootball.ProviderName,
			"budget", *mapBudget,
			"quota_before", client.RequestsRemainingToday(),
		)
		res, err := es.RunMapping(ctx, *mapBudget)
		if err != nil {
			logger.Error("mapping failed", "err", err,
				"dates_queried", res.DatesQueried,
				"candidates", res.CandidatesFetched,
				"written", res.MappingsWritten,
			)
			os.Exit(1)
		}
	}

	// --- single-match fetch ---------------------------------------------
	if *matchID != "" {
		n, err := es.RefreshMatchEvents(ctx, *matchID)
		if err != nil {
			if errors.Is(err, ingestion.ErrNoMapping) {
				logger.Error("no mapping for this match; run -map first",
					"match_id", *matchID)
				os.Exit(1)
			}
			logger.Error("event fetch failed", "match_id", *matchID, "err", err)
			os.Exit(1)
		}
		logger.Info("event fetch done",
			"match_id", *matchID, "events", n,
			"quota_remaining", client.RequestsRemainingToday(),
		)
	}

	// --- rebuild-only (no api-football call) ----------------------------
	// Uses events already stored in match_events + the baseline predictor
	// to overwrite the (match, model, model_version, minute, origin)
	// prediction rows in place. Idempotent; costs zero api-football
	// quota. Use after fixing a sync-time clobbering bug or when
	// swapping predictors.
	if *rebuildOnly != "" {
		if sync == nil {
			logger.Error("rebuild-only needs FOOTBALL_DATA_API_TOKEN in env to wire the predictor Sync")
			os.Exit(2)
		}
		if err := sync.RebuildFinishedTimelineFromEvents(ctx, *rebuildOnly); err != nil {
			logger.Error("rebuild failed", "match_id", *rebuildOnly, "err", err)
			os.Exit(1)
		}
		logger.Info("rebuild done", "match_id", *rebuildOnly)
	}

	// --- backfill walk --------------------------------------------------
	if *backfill {
		pmap := repository.NewProviderMap(pool)
		// We look for matches that HAVE a mapping (so we can call
		// /fixtures/events?fixture=X directly without any lookup) and
		// still have zero stored events, newest kickoff first.
		candidates, err := pmap.MappedWithoutEvents(ctx, apifootball.ProviderName, *limit)
		if err != nil {
			logger.Error("find backfill candidates failed", "err", err)
			os.Exit(1)
		}
		if len(candidates) == 0 {
			logger.Info("backfill: nothing to do")
			return
		}
		logger.Info("backfill starting",
			"candidates", len(candidates),
			"quota_remaining", client.RequestsRemainingToday(),
		)
		success := 0
		failed := 0
		for _, id := range candidates {
			if client.RequestsRemainingToday() <= 0 {
				logger.Warn("daily quota exhausted; stopping backfill",
					"processed", success+failed,
					"remaining_candidates", len(candidates)-(success+failed),
				)
				break
			}
			n, err := es.RefreshMatchEvents(ctx, id)
			if err != nil {
				logger.Warn("backfill match failed", "match_id", id, "err", err)
				failed++
				continue
			}
			success++
			logger.Info("backfill ok", "match_id", id, "events", n,
				"quota_remaining", client.RequestsRemainingToday())
		}
		logger.Info("backfill done",
			"success", success, "failed", failed,
			"quota_remaining", client.RequestsRemainingToday())
	}
}
