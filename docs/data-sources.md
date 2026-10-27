# Data sources

Budget for external services and football data is **$0**. SingleStoreDB is
exempt because we already have cluster access. Every provider below is
tracked for license, free-tier capability and permitted use before it is
wired into the Go `ingestion.Provider` interface.

> This file reflects what we currently *plan* to use. Nothing is integrated
> live until it has a working, tested Go adapter and a `data_ingestion_runs`
> entry proving it ran.

## Competition scope (v1)

The MVP intentionally covers only the following top-flight leagues:

| Country  | Competition      | football-data.org code |
| -------- | ---------------- | ---------------------- |
| Portugal | Primeira Liga    | `PPL`                  |
| England  | Premier League   | `PL`                   |
| Italy    | Serie A          | `SA`                   |
| Spain    | La Liga          | `PD`                   |
| Germany  | Bundesliga       | `BL1`                  |

Any ingestion adapter (`internal/ingestion`) and the demo seeder
(`cmd/seed`) must restrict their output to these competitions. Fixtures
from other competitions are ignored at the adapter boundary so no out-of-
scope data reaches `matches` or `model_predictions`.

## Picked (free-tier only)

### football-data.org
- Docs: https://www.football-data.org/documentation/api
- Role: **primary** source of fixtures, completed results and basic
  competition metadata.
- Auth: free tier requires an API token (register at
  https://www.football-data.org/client/register).
- Known free-tier limits (verified at integration time): the five
  supported competitions above are covered; limit is **10 requests/minute**
  per token; no server-push, poll-only; the schema returns an `IN_PLAY`
  status with a running score during a match (we treat that as `LIVE`).
- Does **not** publish prediction probabilities; we use fixtures,
  statuses, scores and competition metadata only.
- Env: `FOOTBALL_DATA_API_TOKEN` (backend-only; never shipped to the React
  app).
- Adapter: [`backend/internal/ingestion/footballdata`](../backend/internal/ingestion/footballdata).
  Orchestrated by `backend/internal/ingestion.Sync` and invoked by the
  `backend/cmd/ingest` CLI; see the README for the exact command.

### StatsBomb Open Data
- Repo: https://github.com/statsbomb/open-data
- License: see `LICENSE.pdf` in the StatsBomb repo (non-commercial,
  attribution required). Verify before import.
- Role: historical event-level data for replay, model experimentation and
  demo content.
- Not a live feed; always flag `provenance = 'HISTORICAL'` or `'REPLAY'`.

## Observed but **not** integrated (need confirmed free access)

### Sportmonks (Football + Predictions API)
- https://www.sportmonks.com/football-api/
- Predictions endpoint is a paid add-on. Deferred.

### API-Football
- https://www.api-football.com/documentation-v3
- Free tier exists but prediction endpoint is pre-match only and the
  response format for live predictions has not been verified. Treat as
  pre-match benchmark only if we eventually integrate it.

### Opta Predictions (Stats Perform)
- https://www.statsperform.com/products/opta-predictions/
- Commercial data feed. No public free API. Not integrated.

### Betimate
- https://betimate.com
- No documented public API confirmed. Classified as a manual research
  candidate only; not scraped.

## Market-implied probabilities

Odds are **not** fetched by default. If/when we integrate an authorized
odds endpoint, odds get stored in `benchmark_predictions.raw_odds_json`
and converted to probabilities via normalized inverse decimal odds,
removing the bookmaker overround. Odds remain a benchmark, never ground
truth.

## Provenance labelling

Every row in `matches`, `match_events`, `model_predictions` and
`benchmark_predictions` carries a `provenance` ENUM: `DEMO`, `HISTORICAL`,
`REPLAY` or `LIVE`. The UI must display provenance wherever data is shown
so users never confuse replay or demo data with a live feed.
