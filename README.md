# SingleScore

Historical football prediction analytics MVP. Rebuilds each finished
match's win-probability timeline from its real events, compares the
pre-match forecast against market odds, and evaluates it against actual
outcomes. There is no live data: everything is loaded from completed
matches by hourly SingleStore notebooks.

Scope v1: top-flight only — **Primeira Liga** (Portugal), **Premier League**
(England), **Serie A** (Italy), **La Liga** (Spain), **Bundesliga**
(Germany). See `docs/data-sources.md`.

## Stack

- **Database**: SingleStoreDB (MySQL-wire, v10.x). Rowstore for per-match
  tables, reference tables for the small lookup catalog.
- **Backend**: Go 1.26 + `gqlgen` GraphQL served via `net/http` (no
  third-party HTTP routers), `go-sql-driver/mysql` with TLS, `log/slog`
  JSON logs, Elo-based baseline predictor + evaluation (Brier, log-loss,
  calibration, bucketed by minute/competition).
- **Frontend**: React 18 + TypeScript + SCSS + Apollo Client + React
  Router 6 + Recharts, bundled with Webpack 5 + SWC (explicitly **not**
  Vite). Dark sports-analytics theme.
- **Testing**: Go unit tests for evaluation + predictor; Playwright e2e
  for Dashboard / Match detail / Evaluation.
- **Dev tooling**: SingleStore MCP (hosted; see `~/.cursor/mcp.json`),
  Figma MCP.

## Repository layout

```
SingleScore/
├── backend/                 Go GraphQL API
│   ├── cmd/
│   │   └── server/          main entry point
│   ├── internal/
│   │   ├── config/          env loading
│   │   ├── db/              SingleStore pool + health
│   │   ├── logging/         slog JSON wiring
│   │   ├── graph/           gqlgen schema, generated code, resolvers
│   │   ├── repository/      read-only SingleStore repositories
│   │   ├── service/         orchestration + repo→GraphQL mapping
│   │   ├── prediction/      Elo baseline predictor (+ unit tests), the
│   │   │                    reference the notebooks' Python port mirrors
│   │   └── evaluation/      Brier / log loss / calibration (+ unit tests)
│   ├── gqlgen.yml
│   └── go.mod
├── frontend/                React + TypeScript app
│   ├── public/              static index.html
│   ├── src/
│   │   ├── apollo/          client wiring
│   │   ├── components/      Nav, MatchRow, Chip, EmptyState
│   │   ├── graphql/         queries + hand-authored TS types
│   │   ├── pages/           Dashboard / Matches / MatchDetail / Evaluation
│   │   └── styles/          SCSS tokens + global
│   └── tests/               Playwright config + e2e specs
├── migrations/              numbered .sql files
├── notebooks/               hourly SingleStore ingest notebooks
├── scripts/                 db-apply.sh, db-shell.sh, notebook bodies as scripts
├── docs/
│   ├── architecture.md
│   ├── data-sources.md      (league scope + free-tier plan)
│   └── graphql.md
├── .env.example
└── README.md
```

## Prerequisites

- Go 1.26+ (installed under `~/.local/go/bin/go` on the dev machine)
- Node.js 20+ and npm 10+
- MySQL CLI (used to apply migrations)
- Access to the SingleStoreDB cluster whose credentials are in `.env`

## Running the stack locally

```bash
# 1) apply migrations once
./scripts/db-apply.sh

# 2) load the season (see "Data" below). On a fresh database run the CSV
#    ingest again after the ESPN scrape so the probability timelines are
#    rebuilt from the real goal / red-card minutes.
.venv/bin/python scripts/ingest_pl_season.py
.venv/bin/python scripts/ingest_pl_events_espn.py
.venv/bin/python scripts/ingest_pl_season.py

# 3) start the backend (serves GraphQL on :8080)
cd backend && go run ./cmd/server
# or: go build -o /tmp/singlescore-backend ./cmd/server && /tmp/singlescore-backend

# 4) in another terminal, start the webpack dev server (proxies /graphql → :8080)
cd frontend && npm install && npm run dev
# → open http://127.0.0.1:3000
```

GraphQL endpoint: `POST http://127.0.0.1:8080/graphql`. GraphiQL /
Playground is served at `GET http://127.0.0.1:8080/` when
`BACKEND_LOG_LEVEL=debug` or `BACKEND_DEMO_MODE=true`.

### Playwright e2e

Headless Chromium smoke-tests for the Dashboard, Match detail, and
Evaluation pages:

```bash
cd frontend
npm run test:e2e:install   # first time only (~314 MB download)
npm run test:e2e           # requires backend + dev server running
```

### Running the baseline unit tests

```bash
cd backend && go test ./...
```

## Data

The app is **historical-only**: the backend is a read-only GraphQL API
and never calls a football data provider. All rows are written by the
notebooks below and tagged `provenance = 'HISTORICAL'`.
`BACKEND_DEMO_MODE` (default `true`) only shows the DEMO badge in the
header and serves the GraphQL Playground.

Match lists, the dashboard and the match page's score card show the
model's **pre-match** odds. Later snapshots of a finished match converge
on the result (100% for the winner), so they say nothing about the
forecast; the dashboard's Brier / log-loss cards score the pre-match
prediction for the same reason.

### Historical ingestion (football-data.co.uk, hourly notebook)

All match data is pulled from a single public CSV at
[football-data.co.uk](https://www.football-data.co.uk/englandm.php)
(free for personal / educational use, one HTTPS `GET` per run) by a
SingleStore notebook that runs on an hourly schedule.

Source of truth: [`notebooks/premier_league_ingest.ipynb`](notebooks/premier_league_ingest.ipynb)
(see [`notebooks/README.md`](notebooks/README.md) for how to upload and
schedule it in the SingleStore Portal). The same body ships as
[`scripts/ingest_pl_season.py`](scripts/ingest_pl_season.py) so you can
run it locally without a Jupyter kernel:

```bash
uv venv .venv --python 3.12
uv pip install --python .venv/bin/python pandas requests pymysql
.venv/bin/python scripts/ingest_pl_season.py
```

What it populates per run (fully idempotent — deterministic SHA-1 UUIDs
+ `INSERT ... ON DUPLICATE KEY UPDATE`):

- One `competitions` row (Premier League 2026/27)
- 20 `teams` rows with chronologically-updated Elo ratings
- ~80 `matches` rows
- ~1 700 `model_predictions` (retrospective timeline per played match,
  using the `elo-poisson v0.2.0` baseline shared with the Go backend): a
  5-minute grid + half-time + full-time, plus a snapshot at every goal /
  red-card minute. Scorelines and red cards come from the ESPN
  `match_events` below when their goals add up to the CSV final score
  (`features_json.score_source = "events"`); otherwise they fall back to
  the HT/FT approximation. A brand-new match picks up its event-driven
  timeline on the next hourly run after the ESPN notebook has scraped it.
- ~80 `benchmark_predictions` (market-consensus pre-match row per
  played match, derived from the Avg / Bet365 1X2 odds columns)
- One `data_ingestion_runs` row (keyed on `provider|yyyy-mm-ddTHH`)

### Per-event data + minute-by-minute commentary (ESPN, hourly notebook)

The CSV has no per-event timings, so exact goal / card / substitution
minutes, VAR delays, and the full commentary feed (shots saved, blocked,
missed, fouls, corners, offsides) are scraped from ESPN's public JSON
endpoints by
[`notebooks/premier_league_events_espn.ipynb`](notebooks/premier_league_events_espn.ipynb)
(generated from [`scripts/ingest_pl_events_espn.py`](scripts/ingest_pl_events_espn.py)).
This scraping was explicitly authorised by the project owner; it is not
a licensed feed and is throttled (300 ms between requests, incremental
re-runs only refetch the last 7 days). It writes `match_events` and
`match_commentary` (migration `0004`), which feed the match page's event
timeline, opportunity map, and minute-by-minute panel.

## Model honesty

- Predictions come from an internal, transparent baseline (`elo-baseline
  v0.1.0`): Elo expectancy with home-field advantage, then in-play
  adjustments for scoreline, minute, and red-card imbalance.
- Nothing in the UI claims this model beats any benchmark.
- Every snapshot stores `(forecast_at, match_minute, home_score_at_fc,
  away_score_at_fc, model_version, provenance)` so evaluation is
  leakage-free.
- Benchmark predictions are only captured prospectively. We never
  reconstruct a historical forecast from a current one.

## Verified vs. pending

| Area                               | Status                                                |
| ---------------------------------- | ----------------------------------------------------- |
| Go toolchain                       | installed, 1.26.0                                     |
| SingleStore connectivity           | verified (hosted MCP + direct DML)                    |
| Migrations v1                      | applied (schema v1 live on the `singlescore` db)      |
| Backend scaffolding                | complete; `go build ./...` + `go vet ./...` clean     |
| GraphQL schema + resolvers         | complete; wired to repositories via the service layer |
| Repositories                       | complete, read-only (notebooks do all writes)         |
| Baseline predictor + unit tests    | complete; all tests green                             |
| Evaluation (Brier/log-loss/calib)  | complete; all tests green                             |
| Frontend scaffolding               | complete; `npm run typecheck` + `npm run build` clean |
| Dashboard + Match detail pages     | complete, rendering real GraphQL data                 |
| Model evaluation page              | complete, calibration/minute-bucket/competition views |
| Playwright e2e                     | 3 specs green against the running stack               |
| ESPN events + commentary scrape    | complete; `notebooks/premier_league_events_espn.ipynb` + `scripts/ingest_pl_events_espn.py`, 80/80 matches matched |
| football-data.co.uk CSV ingest     | complete; `notebooks/premier_league_ingest.ipynb` + `scripts/ingest_pl_season.py`, idempotent, ~4s per run |
| Scheduled ingestion job            | complete via SingleStore Portal → Jobs on the notebook (hourly cron) |
| Figma designs                      | **pending** — using in-repo SCSS tokens for v1        |
