# Notebooks

Hourly idempotent ingest jobs that keep SingleStore populated without hitting
any paid APIs. The notebook is also runnable as a plain Python script
(`scripts/ingest_pl_season.py`) so local dev doesn't require a Jupyter kernel.

## `premier_league_ingest.ipynb`

**Source:** [football-data.co.uk](https://www.football-data.co.uk/englandm.php)
— a long-running free archive of English football match data. Permitted for
personal / educational use; no HTML scraping (one HTTPS `GET` of a `.csv`
file per run). CSV includes:

- Full-time and half-time goals + referee
- Expected goals (`HxG`, `AxG`)
- Shots, shots on target, corners, fouls, yellows, reds
- Pre-match 1X2 odds from Bet365, BetVictor, William Hill, Pinnacle,
  plus `Avg*` market consensus

**What it writes** per run:

| Table                   | Rows (2026/27 as of this writing) |
| ----------------------- | --------------------------------- |
| `competitions`          | 1 (Premier League 2026/27)        |
| `teams`                 | 20 (with chronologically-updated Elo) |
| `matches`               | ~80 (one row per fixture)         |
| `model_predictions`     | ~1 520 (19 snapshots per played match) |
| `benchmark_predictions` | ~80 (one market-consensus row per played match) |
| `match_stats`           | ~80 (xG, shots, SOT, corners, fouls, cards, referee per played match) |
| `data_ingestion_runs`   | 1 (keyed on `provider|yyyy-mm-ddTHH`) |

Row counts stay flat on hourly re-runs because every row has a deterministic
SHA-1-derived UUID and every write uses `INSERT ... ON DUPLICATE KEY UPDATE`.

Walltime: ~4 s end-to-end against a starter-tier SingleStore cluster.

### Running locally

```bash
# One-off deps for the venv:
uv venv .venv --python 3.12
uv pip install --python .venv/bin/python pandas requests pymysql

# Run it:
.venv/bin/python scripts/ingest_pl_season.py
```

Reads SingleStore credentials from `.env` (gitignored).

### Scheduling in SingleStore Portal

1. Portal → **Develop** → **Notebooks** → **Upload** → pick
   `notebooks/premier_league_ingest.ipynb`.
2. Attach the notebook to the workspace that owns the `singlescore`
   database.
3. **Jobs** → **Create Job** → choose the notebook, set cron `0 * * * *`
   (hourly on the hour), save.
4. The job inherits the workspace connection via `SINGLESTOREDB_URL`;
   the notebook's `connect()` helper picks it up automatically.

### Changing the season

Set the `PL_CSV_URL` env var (local) or notebook variable to the desired
season's E0 CSV. Season codes are two-digit yy-yy, e.g. `2425` for
2024/25, `2526` for 2025/26, `2627` for 2026/27.

## `premier_league_events_espn.ipynb`

**Source:** ESPN's public JSON endpoints (`site.api.espn.com/apis/site/v2/sports/soccer/eng.1/scoreboard?dates=YYYYMMDD`
and `.../summary?event={id}`). This is scraping of an undocumented
endpoint, done at the project owner's explicit direction; it is not a
licensed feed. Requests are throttled to one every 300 ms and hourly
re-runs only touch matches with no events yet or kicked off in the last
7 days (~10 requests per run after the first backfill).

Generated from `scripts/ingest_pl_events_espn.py` — edit the script and
regenerate the notebook rather than editing cells by hand.

**What it writes:**

| Table              | Rows (2026/27 as of this writing) |
| ------------------ | --------------------------------- |
| `match_events`     | ~1 510 — goals (incl. `goal---volley`/`header` subtypes), own goals, penalties, cards, subs, VAR/injury delays, with exact minute + stoppage time |
| `match_commentary` | ~8 880 — every commentary line, classified into `kind` (`ATTEMPT_SAVED`, `ATTEMPT_BLOCKED`, `ATTEMPT_MISSED`, `FOUL`, `CORNER`, `OFFSIDE`, …) and `team_side` |

ESPN team names are mapped onto football-data.co.uk names
(`TEAM_NAME_MAP`) so rows attach to the matches written by
`premier_league_ingest.ipynb`; run that one first. Own goals are
attributed to the team that benefits.

Locally: `.venv/bin/python scripts/ingest_pl_events_espn.py` (~85 s for a
full-season backfill, ~11 s incremental). Schedule in Portal the same way
as above, e.g. cron `10 * * * *` so it runs after the CSV ingest.

### Keeping the model in sync with the Go backend

The Poisson-race constants in cell 4 (`AVG_GOALS_PER_TEAM`,
`ELO_HALF_LIFE`, `RED_CARD_FACTOR`, `HOME_ADVANTAGE`,
`MAX_EXTRA_GOALS`) and the model identity (`MODEL_NAME`,
`MODEL_VERSION`) **must** match
`backend/internal/prediction/baseline.go`. If you bump the Go version,
bump the Python one too (and vice versa) so a row written by the
notebook is indistinguishable from one written by the Go backend.
