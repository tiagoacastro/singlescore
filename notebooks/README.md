# Notebooks

Two hourly, idempotent ingest jobs keep SingleStore populated for the five
leagues in `scripts/ingest_common.py` (`LEAGUES`): Premier League, La Liga,
Serie A, Bundesliga and Primeira Liga, for the stored seasons (`SEASONS`:
2025/26 and the current 2026/27).

Both notebooks are **generated** — edit the scripts, then regenerate:

```bash
python3 scripts/build_notebooks.py          # rewrite both notebooks
python3 scripts/build_notebooks.py --check  # exit 1 if they're stale
```

| Notebook | Generated from | Schedule |
| -------- | -------------- | -------- |
| `season_ingest.ipynb` | `scripts/ingest_season.py` | `0 * * * *` |
| `events_espn.ipynb` | `scripts/ingest_events_espn.py` | `10 * * * *` |

SingleStore Portal notebooks can't import files from this repo, so the
generator inlines `scripts/ingest_common.py` (league table, connection,
ids, upsert and run-log helpers) at the top of each notebook.

## `season_ingest.ipynb` — football-data.co.uk

**Source:** [football-data.co.uk](https://www.football-data.co.uk/data.php),
one public CSV per league and season (`mmz4281/<season>/{E0,SP1,I1,D1,P1}.csv`), free for
personal / educational use; no scraping. Each CSV has full-time and
half-time goals, xG, shots, shots on target, corners, fouls, cards and
pre-match 1X2 odds (the referee only for English divisions). Kickoff times
are UK local time and are stored as UTC.

**What it writes** per run:

| Table | Rows (2026/27 as of this writing) |
| ----- | --------------------------------- |
| `competitions` | 10 (5 leagues × 2 seasons) |
| `teams` | 110, with Elo walked chronologically per league across seasons |
| `matches` | 2 155 |
| `model_predictions` | ~45 400: pre-match + 5-min grid + HT + FT, plus one per goal / red-card minute when the ESPN events agree with the CSV score (HT/FT approximation otherwise) |
| `benchmark_predictions` | 2 155 market-consensus rows (Avg odds, else Bet365, de-vigged) |
| `match_stats` | 2 155 |
| `data_ingestion_runs` | 1 per hour (`football-data-couk` / `notebook-season`) |

Row counts stay flat on re-runs: every row has a deterministic SHA-1-derived
UUID and every write is `INSERT ... ON DUPLICATE KEY UPDATE`. A league whose
CSV fails to download is skipped and the run is recorded as `PARTIAL`.
About 45 s end-to-end. Elo is walked through the seed seasons
(`SEED_SEASON_CODES`, 2023/24 and 2024/25, read but not stored) and then
each stored season in order, so every season starts from the previous
one's closing ratings.

## `events_espn.ipynb` — ESPN per-event data + commentary

**Source:** ESPN's public JSON (`site.api.espn.com/apis/site/v2/sports/soccer/<slug>/scoreboard?dates=YYYYMMDD`
and `.../summary?event={id}`), one slug per league (`eng.1`, `esp.1`,
`ita.1`, `ger.1`, `por.1`). This is scraping of an undocumented endpoint,
done at the project owner's explicit direction; it is not a licensed
feed. Requests are 300 ms apart, and each run only fetches matches with
no events yet or that kicked off in the last 7 days (set
`ESPN_LOOKBACK_DAYS` to re-fetch older ones).

**What it writes:**

| Table | Rows (2026/27 as of this writing) |
| ----- | --------------------------------- |
| `match_events` | ~36 600: goals (every `goal---*` subtype), own goals, penalties, cards, subs, VAR / injury delays, with exact minute + stoppage time |
| `match_commentary` | ~243 000: every commentary line, classified into `kind` (`ATTEMPT_SAVED`, `FOUL`, `CORNER`, …), `team_side` and period |

ESPN team names are mapped onto football-data.co.uk names per league
(`TEAM_NAME_MAPS`) so rows attach to the matches the season ingest
writes; run that one first. Own goals are credited to the team that
benefits. A full backfill of one season of all five leagues takes about
30 minutes; an hourly run takes under a minute.

Because the season ingest reads these events to build its timelines, a
newly played match gets its event-driven timeline on the season run
after the ESPN run that scraped it.

## Running locally

```bash
uv venv .venv --python 3.12
uv pip install --python .venv/bin/python pandas requests pymysql

.venv/bin/python scripts/ingest_season.py
.venv/bin/python scripts/ingest_events_espn.py
.venv/bin/python scripts/ingest_season.py   # rebuild timelines from the new events
```

The scripts read SingleStore credentials from `.env` (gitignored).

## Scheduling in SingleStore Portal

1. Portal → **Develop** → **Notebooks** → **Upload** both notebooks.
2. Attach them to the workspace that owns the `singlescore` database.
3. **Jobs** → **Create Job** for each, with the crons above.
4. Jobs inherit the workspace connection via `SINGLESTOREDB_URL`.

## Changing the seasons or leagues

Edit `SEASONS` or `LEAGUES` in `scripts/ingest_common.py` (and
`SEED_SEASON_CODES` in `ingest_season.py`, the two seasons before the
first stored one), add ESPN names for any new clubs to `TEAM_NAME_MAPS`,
then regenerate the notebooks.

## The model

`ingest_season.py` writes `elo-poisson v0.4.0` (`predict_v3`): the
Poisson race of the Go reference (`backend/internal/prediction`, v0.2.0,
mirrored by `predict`) plus stoppage time, a two-sided red-card effect and
a tilt towards the side on top (pressure from the commentary), on Elo
ratings seeded from the previous two seasons
(`PREVIOUS_SEASON_CODES`) and updated on goals + shot quality. A run
replaces snapshots from other versions of the model.

The shared constants (`AVG_GOALS_PER_TEAM`, `ELO_HALF_LIFE`,
`RED_CARD_FACTOR`, `HOME_ADVANTAGE`, `MAX_EXTRA_GOALS`) must match the Go
reference; the parity tests on both sides pin the same golden values.
Measure any model change with `scripts/backtest_model.py` before
shipping it.
