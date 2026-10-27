# SingleScore

Live football intelligence and prediction analytics MVP. Tracks match
predictions over time, compares them against benchmark providers, and
evaluates them against actual match outcomes.

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
│   │   ├── server/          main entry point
│   │   ├── seed/            demo seeder (5 leagues × 5 matches each)
│   │   └── ingest/          live ingestion CLI (football-data.org)
│   ├── internal/
│   │   ├── config/          env loading
│   │   ├── db/              SingleStore pool + health
│   │   ├── logging/         slog JSON wiring
│   │   ├── graph/           gqlgen schema, generated code, resolvers
│   │   ├── repository/      SingleStore repositories (batched writes)
│   │   ├── service/         orchestration + repo→GraphQL mapping
│   │   ├── prediction/      Elo baseline predictor (+ unit tests)
│   │   ├── evaluation/      Brier / log loss / calibration (+ unit tests)
│   │   └── ingestion/       provider interface + sync orchestrator
│   │       └── footballdata/ football-data.org v4 adapter (free tier)
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
├── scripts/                 db-apply.sh, db-shell.sh
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

# 2) seed demo data (idempotent; add --wipe-demo for a clean slate)
cd backend && go run ./cmd/seed --wipe-demo && cd ..

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

## Demo mode vs. real data

- `BACKEND_DEMO_MODE=true` (the current default) serves data populated by
  `cmd/seed`. Every row is tagged `provenance = 'DEMO'`, and the UI
  surfaces that chip wherever the data is shown.
- `BACKEND_DEMO_MODE=false` runs the normal ingestion loop. **No live
  provider is called without its API token being set.** See
  `docs/data-sources.md` for the free-tier plan.

### Historical ingestion (football-data.co.uk, hourly notebook)

The project is **API-free in day-to-day operation**: all live data is
pulled from a single public CSV at
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
- ~1 520 `model_predictions` (19-point retrospective timeline per played
  match, using the `elo-poisson v0.2.0` baseline shared with the Go backend)
- ~80 `benchmark_predictions` (market-consensus pre-match row per
  played match, derived from the Avg / Bet365 1X2 odds columns)
- One `data_ingestion_runs` row (keyed on `provider|yyyy-mm-ddTHH`)

Legacy per-match live-focus refresher (gated on
`FOOTBALL_DATA_API_TOKEN`): still present in the server for compatibility
but **off by default**. Leave the env var unset to keep the server
API-free.

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
| Repositories                       | complete, with batched insert paths                   |
| Baseline predictor + unit tests    | complete; all tests green                             |
| Evaluation (Brier/log-loss/calib)  | complete; all tests green                             |
| Demo seeder (5 leagues × 5)        | complete, idempotent, `--wipe-demo` supported         |
| Frontend scaffolding               | complete; `npm run typecheck` + `npm run build` clean |
| Dashboard + Match detail pages     | complete, rendering real GraphQL data                 |
| Model evaluation page              | complete, calibration/minute-bucket/competition views |
| Playwright e2e                     | 3 specs green against the running stack               |
| football-data.co.uk CSV ingest     | complete; `notebooks/premier_league_ingest.ipynb` + `scripts/ingest_pl_season.py`, idempotent, ~4s per run |
| football-data.org adapter          | kept for compatibility; off by default (no `FOOTBALL_DATA_API_TOKEN`) |
| Scheduled ingestion job            | complete via SingleStore Portal → Jobs on the notebook (hourly cron) |
| Figma designs                      | **pending** — using in-repo SCSS tokens for v1        |
