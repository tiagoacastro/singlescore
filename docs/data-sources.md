# Data sources

Budget for external services and football data is **$0**. SingleStoreDB is
exempt because we already have cluster access. Every provider below is
tracked for license, free-tier capability and permitted use before it is
used.

> The app is **historical-only**. The backend never calls a provider;
> completed matches are loaded by the hourly notebooks in `notebooks/`
> (see their README). There is no live feed.

## Competition scope (v1)

The MVP intentionally covers only the following top-flight leagues:

| Country  | Competition      | football-data.org code |
| -------- | ---------------- | ---------------------- |
| Portugal | Primeira Liga    | `PPL`                  |
| England  | Premier League   | `PL`                   |
| Italy    | Serie A          | `SA`                   |
| Spain    | La Liga          | `PD`                   |
| Germany  | Bundesliga       | `BL1`                  |

Ingestion notebooks must restrict their output to these competitions so
no out-of-scope data reaches `matches` or `model_predictions`. Only the
Premier League is loaded today.

## Picked (free-tier only)

### football-data.co.uk
- https://www.football-data.co.uk/englandm.php
- Role: **primary** source. One public CSV per season with full-time and
  half-time scores, referee, xG, shots, corners, fouls, cards and
  pre-match 1X2 odds. Free for personal / educational use.
- Loaded by `notebooks/premier_league_ingest.ipynb`
  (`scripts/ingest_pl_season.py`).

### ESPN (public JSON endpoints)
- Role: per-event data (exact goal / card / substitution / VAR minutes)
  and minute-by-minute commentary for completed matches.
- Undocumented endpoint, scraped with the project owner's explicit
  authorisation and throttled; not a licensed feed.
- Loaded by `notebooks/premier_league_events_espn.ipynb`
  (`scripts/ingest_pl_events_espn.py`).

### StatsBomb Open Data
- Repo: https://github.com/statsbomb/open-data
- License: see `LICENSE.pdf` in the StatsBomb repo (non-commercial,
  attribution required). Verify before import.
- Role: historical event-level data for replay, model experimentation and
  demo content.
- Not a live feed; always flag `provenance = 'HISTORICAL'` or `'REPLAY'`.

## Removed

### football-data.org and API-Football
- Live APIs (10 requests/minute and 100 requests/day on their free tiers)
  that powered an earlier live-tracking version. Their Go adapters, the
  `cmd/ingest` / `cmd/ingest-events` CLIs and the server's per-match live
  refresher were removed when the app went historical-only.

## Observed but **not** integrated (need confirmed free access)

### Sportmonks (Football + Predictions API)
- https://www.sportmonks.com/football-api/
- Predictions endpoint is a paid add-on. Deferred.

### Opta Predictions (Stats Perform)
- https://www.statsperform.com/products/opta-predictions/
- Commercial data feed. No public free API. Not integrated.

### Betimate
- https://betimate.com
- No documented public API confirmed. Classified as a manual research
  candidate only; not scraped.

## Market-implied probabilities

Pre-match 1X2 odds come from the football-data.co.uk CSV (market average,
falling back to Bet365). They are stored in
`benchmark_predictions.raw_odds_json` and converted to probabilities via
normalized inverse decimal odds, removing the bookmaker overround. Odds
remain a benchmark, never ground truth.

## Provenance labelling

Every row in `matches`, `match_events`, `model_predictions` and
`benchmark_predictions` carries a `provenance` ENUM: `DEMO`, `HISTORICAL`
or `REPLAY` (the column still allows `LIVE`, which nothing writes any
more). The UI must display provenance wherever data is shown.
