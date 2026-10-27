// Package main — the SingleScore live ingestion CLI.
//
// Usage:
//
//	FOOTBALL_DATA_API_TOKEN=xxx ./ingest
//	./ingest -competitions=PL,PPL
//	./ingest -days-back=1 -days-forward=1          # cheap today-only refresh
//	./ingest -loop=30s -days-back=1 -days-forward=1  # long-running live tick
//
// One-shot by default; -loop=<interval> turns it into a long-running
// polling process that exits cleanly on SIGINT/SIGTERM. The provider is
// currently hard-wired to football-data.org (free tier, five supported
// competitions). Add a -provider flag when a second adapter lands.
//
// Secrets: FOOTBALL_DATA_API_TOKEN MUST come from the environment. It is
// never logged, written to disk, or returned over the API. Do not pass
// it on the command line in shared shells.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
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
	timeout := flag.Duration("timeout", 15*time.Minute, "overall deadline for the sync run")
	daysBack := flag.Int("days-back", 0, "only fetch matches with kickoff >= today - N (0 = full season)")
	daysFwd := flag.Int("days-forward", 0, "only fetch matches with kickoff <= today + N (0 = full season). "+
		"-days-back=1 -days-forward=1 is the cheap live-scoreboard refresh (5 requests total)")
	loop := flag.Duration("loop", 0, "if > 0, run the sync repeatedly on this interval until SIGINT/SIGTERM. "+
		"Each tick runs with the given -timeout as its deadline. Example: -loop=30s")
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
	windowDesc := "full-season"
	if *daysBack > 0 || *daysFwd > 0 {
		windowDesc = fmt.Sprintf("-%dd..+%dd", *daysBack, *daysFwd)
	}
	logger.Info("ingest starting",
		"provider", footballdata.ProviderName,
		"competitions", compList,
		"window", windowDesc,
		"dry_run", *dryRun,
	)

	client, err := footballdata.New(token, footballdata.Options{})
	if err != nil {
		logger.Error("footballdata client", "err", err)
		os.Exit(1)
	}
	window := footballdata.MatchesQuery{}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	if *daysBack > 0 {
		window.DateFrom = today.AddDate(0, 0, -*daysBack)
	}
	if *daysFwd > 0 {
		window.DateTo = today.AddDate(0, 0, *daysFwd)
	}
	provider := footballdata.NewProviderWithWindow(client, compList, window)

	// --- dry-run exits before any DB work ---
	if *dryRun {
		drCtx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		fixtures, err := provider.FetchFixtures(drCtx)
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

	// --- DB pool: one for the whole process (one-shot and loop both). ---
	pool, err := db.Open(cfg.DB)
	if err != nil {
		logger.Error("db open", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := db.Ping(pingCtx, pool); err != nil {
		pingCancel()
		logger.Error("db ping failed; aborting ingest", "err", err)
		os.Exit(1)
	}
	pingCancel()

	deps := ingestion.Deps{
		Catalog:     repository.NewCatalog(pool),
		Matches:     repository.NewMatches(pool),
		Predictions: repository.NewPredictions(pool),
		Ingestion:   repository.NewIngestion(pool),
		Predictor:   prediction.NewBaseline(),
		Logger:      logger,
	}
	sync := ingestion.NewSync(deps, provider, ingestion.SyncOptions{
		RecordRun:                true,
		SkipFinishedWithFullTime: true,
	})

	// --- one-shot (no -loop) ---
	if *loop <= 0 {
		res, err := runOnce(context.Background(), *timeout, logger, sync, client, compList)
		if err != nil || res.Status == "FAILED" {
			os.Exit(1)
		}
		return
	}

	// --- long-running loop ---
	expectedReqPerMin := float64(len(compList)) * (60.0 / loop.Seconds())
	logger.Info("ingest loop starting",
		"interval", loop.String(),
		"competitions", len(compList),
		"expected_requests_per_min", fmt.Sprintf("%.1f", expectedReqPerMin),
	)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(*loop)
	defer ticker.Stop()

	// Run the first tick immediately so operators see output within
	// seconds instead of waiting a full interval.
	tick := 0
	runTick := func() {
		tick++
		tickCtx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		start := time.Now()
		res, err := sync.Run(tickCtx)
		dur := time.Since(start)
		if err != nil {
			logger.Warn("tick failed", "tick", tick, "err", err, "duration_ms", dur.Milliseconds())
			return
		}
		logger.Info("tick",
			"tick", tick,
			"status", res.Status,
			"fixtures_in", res.FixturesIn,
			"matches_upserted", res.MatchesUpserted,
			"model_predictions", res.ModelPredictions,
			"errors", len(res.Errors),
			"duration_ms", dur.Milliseconds(),
			"api_requests_available", client.Available(),
		)
		// Warn if a tick runs longer than its interval — this means the
		// next tick will fire the moment this one finishes and the
		// cadence has effectively doubled.
		if dur >= *loop {
			logger.Warn("tick overran interval; cadence will slip",
				"tick", tick, "duration_ms", dur.Milliseconds(), "interval_ms", loop.Milliseconds())
		}
	}
	runTick()

	for {
		select {
		case <-stop:
			logger.Info("ingest loop stopping", "signal_received", true, "total_ticks", tick)
			return
		case <-ticker.C:
			runTick()
		}
	}
}

// runOnce executes a single sync pass and prints the summary. Returns
// the SyncResult (always populated) and the sync error, if any.
func runOnce(parent context.Context, timeout time.Duration, logger interface {
	Info(string, ...any)
	Warn(string, ...any)
	Error(string, ...any)
}, sync *ingestion.Sync, client *footballdata.Client, compList []string) (ingestion.SyncResult, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	res, err := sync.Run(ctx)
	if err != nil {
		logger.Error("sync failed",
			"err", err,
			"status", res.Status,
			"fixtures_in", res.FixturesIn,
			"matches_upserted", res.MatchesUpserted,
		)
		return res, err
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
		"api_requests_available", client.Available(),
	)
	for _, e := range res.Errors {
		logger.Warn("ingest error", "detail", e)
	}
	if res.Status == "FAILED" {
		return res, errors.New("ingest status FAILED")
	}
	return res, nil
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
