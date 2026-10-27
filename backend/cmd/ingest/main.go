// Package main — the SingleScore live ingestion CLI.
//
// Usage:
//
//	FOOTBALL_DATA_API_TOKEN=xxx ./ingest
//	./ingest -competitions=PL,PPL
//	./ingest -once=false -interval=5m   # (future) loop mode
//
// For v1 we only run once and exit. A systemd timer / Kubernetes CronJob
// is responsible for scheduling. The provider is currently hard-wired to
// football-data.org (free tier, five supported competitions). Add a
// -provider flag when a second adapter lands.
//
// Secrets: FOOTBALL_DATA_API_TOKEN MUST come from the environment. It is
// never logged, written to disk, or returned over the API. Do not pass
// it on the command line in shared shells.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/tiagocastro/singlescore/backend/internal/config"
	"github.com/tiagocastro/singlescore/backend/internal/db"
	"github.com/tiagocastro/singlescore/backend/internal/ingestion"
	"github.com/tiagocastro/singlescore/backend/internal/ingestion/footballdata"
	"github.com/tiagocastro/singlescore/backend/internal/logging"
	"github.com/tiagocastro/singlescore/backend/internal/prediction"
	"github.com/tiagocastro/singlescore/backend/internal/repository"
)

func main() {
	comps := flag.String("competitions", strings.Join(footballdata.SupportedCompetitions, ","),
		"comma-separated football-data.org competition codes (free tier: PPL,PL,SA,PD,BL1)")
	dryRun := flag.Bool("dry-run", false, "fetch and log fixtures without upserting")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall deadline for the sync run")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	logger := logging.New(cfg.LogLevel)

	token := strings.TrimSpace(os.Getenv("FOOTBALL_DATA_API_TOKEN"))
	if token == "" {
		logger.Error("FOOTBALL_DATA_API_TOKEN is not set",
			"hint", "export FOOTBALL_DATA_API_TOKEN=<your-free-tier-token> and re-run")
		os.Exit(2)
	}

	compList := splitCSV(*comps)
	logger.Info("ingest starting",
		"provider", footballdata.ProviderName,
		"competitions", compList,
		"dry_run", *dryRun,
	)

	client, err := footballdata.New(token, footballdata.Options{})
	if err != nil {
		logger.Error("footballdata client", "err", err)
		os.Exit(1)
	}
	provider := footballdata.NewProvider(client, compList)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *dryRun {
		fixtures, err := provider.FetchFixtures(ctx)
		if err != nil {
			logger.Error("fetch fixtures", "err", err)
			os.Exit(1)
		}
		logger.Info("dry-run: fetched fixtures", "count", len(fixtures))
		for _, fx := range fixtures {
			logger.Info("fixture",
				"comp", fx.ExternalCompID,
				"home", fx.HomeTeamName, "away", fx.AwayTeamName,
				"kickoff", fx.KickoffAt.Format(time.RFC3339),
				"status", fx.Status,
				"score", fmt.Sprintf("%d-%d", fx.HomeScore, fx.AwayScore),
			)
		}
		return
	}

	pool, err := db.Open(cfg.DB)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Ping(ctx, pool); err != nil {
		logger.Error("db ping failed; aborting ingest", "err", err)
		os.Exit(1)
	}

	deps := ingestion.Deps{
		Catalog:     repository.NewCatalog(pool),
		Matches:     repository.NewMatches(pool),
		Predictions: repository.NewPredictions(pool),
		Ingestion:   repository.NewIngestion(pool),
		Predictor:   prediction.NewBaseline(),
		Logger:      logger,
	}
	sync := ingestion.NewSync(deps, provider, ingestion.SyncOptions{RecordRun: true})
	res, err := sync.Run(ctx)
	if err != nil {
		logger.Error("sync failed",
			"err", err,
			"status", res.Status,
			"fixtures_in", res.FixturesIn,
			"matches_upserted", res.MatchesUpserted,
		)
		os.Exit(1)
	}
	logger.Info("ingest complete",
		"status", res.Status,
		"fixtures_in", res.FixturesIn,
		"competitions", res.Competitions,
		"teams", res.Teams,
		"matches_upserted", res.MatchesUpserted,
		"model_predictions", res.ModelPredictions,
		"errors", len(res.Errors),
		"duration_ms", res.FinishedAt.Sub(res.StartedAt).Milliseconds(),
	)
	for _, e := range res.Errors {
		logger.Warn("ingest error", "detail", e)
	}
	if res.Status == "FAILED" {
		os.Exit(1)
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
