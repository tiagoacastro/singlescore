"""Hourly idempotent ingest of the current Premier League season into
SingleStore from football-data.co.uk.

This is the exact body that lives in `notebooks/premier_league_ingest.ipynb`.
Keeping it as a plain script too so it can be exercised locally with
`python3 scripts/ingest_pl_season.py` without a Jupyter kernel.

Source: https://www.football-data.co.uk/mmz4281/<SEASON>/E0.csv (public
CSV, free for personal/educational use per the site's terms).

Design:
  * Zero paid APIs. Zero scraping. One HTTPS GET per run.
  * Fully idempotent: deterministic SHA-1-derived UUIDs on every row,
    `INSERT ... ON DUPLICATE KEY UPDATE` on every upsert. Safe to
    re-run every hour (or every minute) without duplicating.
  * Mirrors the Go `elo-poisson@v0.2.0` predictor exactly so chart
    semantics are identical whether rows were written by the Go
    backend or by this notebook.
  * Produces three kinds of row per completed match:
      - matches, teams, competitions (upsert)
      - model_predictions: pre-match + 5-min reconstructed grid +
        HT + FT (same shape the backend writes for FINISHED matches)
      - benchmark_predictions: a market-consensus row derived by
        de-vigging the Avg home/draw/away odds columns

Run order in a notebook or script:
  1. Config + dependencies
  2. Download CSV
  3. Build competition/teams/matches
  4. Chronological Elo update
  5. Predictor port + prediction-row generation
  6. Benchmark-row generation from odds
  7. Upsert + ingestion-run bookkeeping
"""

from __future__ import annotations

import csv
import hashlib
import io
import json
import math
import os
import sys
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Iterable

import pandas as pd
import pymysql
import requests


# ---------------------------------------------------------------------------
# Config.
# ---------------------------------------------------------------------------

CSV_URL = os.environ.get(
    "PL_CSV_URL",
    "https://www.football-data.co.uk/mmz4281/2627/E0.csv",
)
PROVIDER = "football-data-couk"
COMPETITION_EXT_ID = "E0-2026-27"
COMPETITION_NAME = "Premier League"
COMPETITION_COUNTRY = "England"
COMPETITION_SEASON = "2026/27"

# These MUST stay in sync with backend/internal/prediction/baseline.go so
# a row written by the notebook is indistinguishable from one written by
# the Go backend.
MODEL_NAME = "elo-poisson"
MODEL_VERSION = "v0.2.0"

AVG_GOALS_PER_TEAM = 1.35
ELO_HALF_LIFE = 800.0
RED_CARD_FACTOR = 0.72
MAX_EXTRA_GOALS = 10
HOME_ADVANTAGE = 60.0
ELO_K = 20.0  # update coefficient; ClubElo-style with goal-diff multiplier
INITIAL_ELO = 1500.0

# Snapshot cadence for the retrospective timeline. 5-minute grid matches
# `backend/internal/ingestion/predictions.go:finishedTimelineStepMin`.
TIMELINE_STEP_MIN = 5


# ---------------------------------------------------------------------------
# Helpers.
# ---------------------------------------------------------------------------


def det_id(prefix: str, key: str) -> str:
    """SHA-1-derived UUID. Same formula the Go backend uses so re-runs
    hash to the same primary key and upsert in place."""
    h = hashlib.sha1(f"{prefix}|{key}".encode("utf-8")).digest()[:16]
    return str(uuid.UUID(bytes=h))


def connect():
    """Open a SingleStore connection.

    Precedence (first non-empty wins):
      1. `SINGLESTOREDB_URL` (set automatically when running inside a
         SingleStore Portal notebook attached to a workspace).
      2. `SINGLESTORE_HOST` + `SINGLESTORE_USER` + ... (used locally).
    """
    url = os.environ.get("SINGLESTOREDB_URL") or os.environ.get("SINGLESTORE_URL")
    if url:
        # mysql://user:pass@host:port/db  (SingleStore Portal format)
        from urllib.parse import urlparse

        p = urlparse(url)
        return pymysql.connect(
            host=p.hostname,
            port=p.port or 3306,
            user=p.username,
            password=p.password or "",
            database=(p.path or "/").lstrip("/") or "singlescore",
            autocommit=True,
            ssl={"ssl": True},
            charset="utf8mb4",
            use_unicode=True,
        )
    return pymysql.connect(
        host=os.environ["SINGLESTORE_HOST"],
        port=int(os.environ.get("SINGLESTORE_PORT", "3306")),
        user=os.environ["SINGLESTORE_USER"],
        password=os.environ["SINGLESTORE_PASSWORD"],
        database=os.environ.get("SINGLESTORE_DATABASE", "singlescore"),
        autocommit=True,
        ssl={"ssl": True},
        charset="utf8mb4",
        use_unicode=True,
    )


# ---------------------------------------------------------------------------
# Predictor port.
# ---------------------------------------------------------------------------

_FACT = [1, 1, 2, 6, 24, 120, 720, 5040, 40320, 362880, 3628800]


def _poisson_pmf_vec(mu: float) -> list[float]:
    out = [0.0] * (MAX_EXTRA_GOALS + 1)
    if mu <= 0:
        out[0] = 1.0
        return out
    e_neg = math.exp(-mu)
    pow_mu = 1.0
    for k in range(MAX_EXTRA_GOALS + 1):
        out[k] = e_neg * pow_mu / _FACT[k]
        pow_mu *= mu
    return out


def predict(
    home_elo: float,
    away_elo: float,
    home_score: int,
    away_score: int,
    minute: int,
    home_red: int = 0,
    away_red: int = 0,
) -> tuple[float, float, float]:
    """Python port of Go `prediction.Baseline.Predict`.

    Bit-equivalent in intent (floating-point ordering may differ by ulps).
    """
    gap = (home_elo + HOME_ADVANTAGE - away_elo) / ELO_HALF_LIFE
    lh = AVG_GOALS_PER_TEAM * math.exp(+gap / 2)
    la = AVG_GOALS_PER_TEAM * math.exp(-gap / 2)
    m = max(0, min(90, minute))
    remaining = (90 - m) / 90.0
    imb = home_red - away_red
    if imb > 0:
        lh *= RED_CARD_FACTOR**imb
    elif imb < 0:
        la *= RED_CARD_FACTOR ** (-imb)
    mu_h = lh * remaining
    mu_a = la * remaining
    current_margin = home_score - away_score
    p_h = _poisson_pmf_vec(mu_h)
    p_a = _poisson_pmf_vec(mu_a)
    p_home = p_draw = p_away = 0.0
    for h in range(MAX_EXTRA_GOALS + 1):
        for a in range(MAX_EXTRA_GOALS + 1):
            p = p_h[h] * p_a[a]
            margin = current_margin + (h - a)
            if margin > 0:
                p_home += p
            elif margin < 0:
                p_away += p
            else:
                p_draw += p
    s = p_home + p_draw + p_away
    if s <= 0 or math.isnan(s):
        return (1 / 3, 1 / 3, 1 / 3)
    return (p_home / s, p_draw / s, p_away / s)


# ---------------------------------------------------------------------------
# Elo update (chronological, ClubElo-style with goal-diff multiplier).
# ---------------------------------------------------------------------------


def _elo_update(
    home_elo: float,
    away_elo: float,
    home_goals: int,
    away_goals: int,
) -> tuple[float, float]:
    expected_home = 1.0 / (1.0 + 10 ** ((away_elo - home_elo - HOME_ADVANTAGE) / 400.0))
    if home_goals > away_goals:
        result_home = 1.0
    elif home_goals < away_goals:
        result_home = 0.0
    else:
        result_home = 0.5
    gd = abs(home_goals - away_goals)
    k = ELO_K * (1 + math.log(gd + 1))
    delta = k * (result_home - expected_home)
    return home_elo + delta, away_elo - delta


# ---------------------------------------------------------------------------
# Load CSV.
# ---------------------------------------------------------------------------


def fetch_csv() -> pd.DataFrame:
    print(f"[fetch] GET {CSV_URL}")
    r = requests.get(CSV_URL, timeout=30, allow_redirects=True)
    r.raise_for_status()
    df = pd.read_csv(io.StringIO(r.text))
    # Normalise: football-data.co.uk uses UK dd/mm/YYYY and HH:MM in
    # Europe/London local time. We store everything in UTC.
    df["Date"] = pd.to_datetime(df["Date"], format="%d/%m/%Y", errors="coerce")
    df = df.dropna(subset=["Date", "HomeTeam", "AwayTeam"]).copy()
    # Time column is sometimes absent on very old archive seasons.
    if "Time" not in df.columns:
        df["Time"] = "15:00"
    df["Time"] = df["Time"].fillna("15:00")
    # Build a tz-aware kickoff. The source's clock is Europe/London; a
    # one-hour-offset ambiguity in the 25 Oct/27 Mar DST switches isn't
    # worth guessing about for a weekly fixture list — we store naive
    # UTC after a flat 0h offset. Chart tooltips are correct to within
    # an hour on the two DST changeover weekends per year.
    df["KickoffAt"] = pd.to_datetime(
        df["Date"].dt.strftime("%Y-%m-%d") + " " + df["Time"].astype(str),
        errors="coerce",
    )
    df["KickoffAt"] = df["KickoffAt"].dt.tz_localize(None)
    df = df.dropna(subset=["KickoffAt"]).sort_values("KickoffAt").reset_index(drop=True)
    print(
        f"[fetch] {len(df)} rows parsed, "
        f"{len(df[~df['FTHG'].isna()])} with FT scoreline (played)"
    )
    return df


# ---------------------------------------------------------------------------
# Row builders.
# ---------------------------------------------------------------------------


@dataclass
class Context:
    comp_row: tuple
    team_rows: list[tuple]
    match_rows: list[tuple]
    pred_rows: list[tuple]
    bench_rows: list[tuple]


def build_rows(df: pd.DataFrame) -> Context:
    comp_id = det_id("competition", f"{PROVIDER}|{COMPETITION_EXT_ID}")
    now_utc = datetime.now(timezone.utc).replace(tzinfo=None)

    # Teams.
    team_names = sorted(set(df["HomeTeam"]) | set(df["AwayTeam"]))
    team_id_by_name = {
        name: det_id("team", f"{PROVIDER}|{name}") for name in team_names
    }
    team_rows: list[tuple] = []
    # We'll fill in the final (post-season-to-date) Elo at the end so
    # the UI shows up-to-date ratings. Placeholder for now.

    # Matches + chronological Elo walk.
    elo: dict[str, float] = {name: INITIAL_ELO for name in team_names}
    pre_match_elos: dict[int, tuple[float, float]] = {}

    match_rows: list[tuple] = []
    pred_rows: list[tuple] = []
    bench_rows: list[tuple] = []

    for idx, row in df.iterrows():
        home = row["HomeTeam"]
        away = row["AwayTeam"]
        kickoff: datetime = row["KickoffAt"].to_pydatetime()
        pre_match_elos[idx] = (elo[home], elo[away])
        played = not (pd.isna(row.get("FTHG")) or pd.isna(row.get("FTAG")))

        # Match row.
        ext_match = (
            f"{COMPETITION_SEASON}|{kickoff:%Y-%m-%d}|{home}|{away}"
        )
        match_id = det_id("match", f"{PROVIDER}|{ext_match}")
        if played:
            hs_ft = int(row["FTHG"])
            as_ft = int(row["FTAG"])
            hs_ht = int(row["HTHG"]) if not pd.isna(row.get("HTHG")) else 0
            as_ht = int(row["HTAG"]) if not pd.isna(row.get("HTAG")) else 0
            final_result = (
                "HOME" if hs_ft > as_ft else "AWAY" if hs_ft < as_ft else "DRAW"
            )
            hr_full = int(row["HR"]) if not pd.isna(row.get("HR")) else 0
            ar_full = int(row["AR"]) if not pd.isna(row.get("AR")) else 0
            status = "FINISHED"
            minute = 90
            home_score_db = hs_ft
            away_score_db = as_ft
        else:
            hs_ft = as_ft = hs_ht = as_ht = 0
            final_result = None
            hr_full = ar_full = 0
            status = "SCHEDULED"
            minute = None
            home_score_db = 0
            away_score_db = 0

        match_rows.append(
            (
                match_id,
                PROVIDER,
                ext_match,
                comp_id,
                team_id_by_name[home],
                team_id_by_name[away],
                kickoff,
                status,
                minute,
                home_score_db,
                away_score_db,
                hr_full,
                ar_full,
                final_result,
                None,  # venue
                "HISTORICAL",
                now_utc,
            )
        )

        # Pre-match elos (captured BEFORE this match's result updates
        # the ratings) are what the predictor uses for every snapshot
        # of this match.
        home_elo_pm, away_elo_pm = pre_match_elos[idx]

        # Snapshots.
        if played:
            pred_rows.extend(
                _make_prediction_rows(
                    match_id=match_id,
                    kickoff=kickoff,
                    home_elo=home_elo_pm,
                    away_elo=away_elo_pm,
                    hs_ht=hs_ht,
                    as_ht=as_ht,
                    hs_ft=hs_ft,
                    as_ft=as_ft,
                )
            )
            bench = _make_benchmark_row(
                row=row, match_id=match_id, kickoff=kickoff,
                hs_ft=hs_ft, as_ft=as_ft,
            )
            if bench is not None:
                bench_rows.append(bench)
            # Update Elo for next match's pre-match ratings.
            elo[home], elo[away] = _elo_update(
                elo[home], elo[away], hs_ft, as_ft
            )
        else:
            # Still write a single pre-match snapshot so the chart has
            # something to render on fixture pages.
            pred_rows.extend(
                _make_prediction_rows(
                    match_id=match_id,
                    kickoff=kickoff,
                    home_elo=home_elo_pm,
                    away_elo=away_elo_pm,
                    hs_ht=0, as_ht=0, hs_ft=None, as_ft=None,
                )
            )
            bench = _make_benchmark_row(
                row=row, match_id=match_id, kickoff=kickoff,
                hs_ft=None, as_ft=None,
            )
            if bench is not None:
                bench_rows.append(bench)

    # Now finalise team rows with the post-season-to-date Elo so
    # dashboards show current strength.
    team_rows = [
        (
            team_id_by_name[name],
            PROVIDER,
            name,
            name,
            name[:50],
            COMPETITION_COUNTRY,
            float(elo[name]),
        )
        for name in team_names
    ]

    comp_row = (
        comp_id,
        PROVIDER,
        COMPETITION_EXT_ID,
        COMPETITION_NAME,
        COMPETITION_COUNTRY,
        COMPETITION_SEASON,
    )
    return Context(
        comp_row=comp_row,
        team_rows=team_rows,
        match_rows=match_rows,
        pred_rows=pred_rows,
        bench_rows=bench_rows,
    )


def _make_prediction_rows(
    match_id: str,
    kickoff: datetime,
    home_elo: float,
    away_elo: float,
    hs_ht: int,
    as_ht: int,
    hs_ft: int | None,
    as_ft: int | None,
) -> list[tuple]:
    """Produce the same 19-point retrospective timeline the Go backend
    writes for FINISHED matches, plus a pre-match row for everything
    (even unplayed fixtures).

    Scoreline model between observations: 0-0 before minute 45, HT
    scoreline from minute 45 up to just before minute 90, FT scoreline
    at minute 90. This matches the backend's HT/FT-approximation
    strategy used when no per-event feed is available.
    """
    out: list[tuple] = []

    def add(minute: int, hs: int, as_: int, origin: str, forecast_at: datetime):
        p_h, p_d, p_a = predict(
            home_elo=home_elo, away_elo=away_elo,
            home_score=hs, away_score=as_, minute=minute,
        )
        features = {
            "home_elo": home_elo,
            "away_elo": away_elo,
            "minute": minute,
            "home_score": hs,
            "away_score": as_,
            "home_advantage": HOME_ADVANTAGE,
            "model": MODEL_NAME,
            "model_version": MODEL_VERSION,
            "source_provider": PROVIDER,
            "origin": origin,
        }
        pred_id = det_id(
            "prediction",
            f"{match_id}|{MODEL_NAME}|{MODEL_VERSION}|{minute}|{origin}",
        )
        out.append(
            (
                pred_id,
                match_id,
                MODEL_NAME,
                MODEL_VERSION,
                forecast_at,
                None if minute == 0 else minute,
                hs,
                as_,
                0,
                0,
                p_h,
                p_d,
                p_a,
                json.dumps(features),
                "HISTORICAL",
                forecast_at,
            )
        )

    # Pre-match (back-dated by 1h so the evaluation layer sorts it
    # before any in-play snapshot — matches backend behaviour).
    add(
        0, 0, 0, "pre-match", kickoff - timedelta(hours=1),
    )
    if hs_ft is None or as_ft is None:
        return out

    # Reconstructed grid: 5, 10, ..., 85 (skip 0, 45, 90 — they get
    # their own origin labels below).
    for minute in range(5, 90, TIMELINE_STEP_MIN):
        if minute == 45:
            continue
        if minute < 45:
            hs, as_ = 0, 0
        else:
            hs, as_ = hs_ht, as_ht
        add(
            minute, hs, as_, "reconstructed-5min",
            kickoff + timedelta(minutes=minute),
        )

    # HT and FT with their proper origin labels.
    add(45, hs_ht, as_ht, "half-time", kickoff + timedelta(minutes=45))
    add(90, hs_ft, as_ft, "full-time", kickoff + timedelta(minutes=90))
    return out


def _make_benchmark_row(
    row: pd.Series,
    match_id: str,
    kickoff: datetime,
    hs_ft: int | None,
    as_ft: int | None,
) -> tuple | None:
    """Build a pre-match market-consensus benchmark row from the Avg
    home/draw/away odds columns. Falls back to Bet365 if Avg is missing.
    Returns None if neither triple is complete."""
    oh = row.get("AvgH")
    od = row.get("AvgD")
    oa = row.get("AvgA")
    source = "market-avg"
    if pd.isna(oh) or pd.isna(od) or pd.isna(oa):
        oh = row.get("B365H")
        od = row.get("B365D")
        oa = row.get("B365A")
        source = "bet365"
    if pd.isna(oh) or pd.isna(od) or pd.isna(oa):
        return None
    try:
        oh, od, oa = float(oh), float(od), float(oa)
    except (TypeError, ValueError):
        return None
    if oh <= 1.0 or od <= 1.0 or oa <= 1.0:
        return None
    rh, rd, ra = 1.0 / oh, 1.0 / od, 1.0 / oa
    overround = rh + rd + ra
    ph, pd_, pa = rh / overround, rd / overround, ra / overround

    bench_id = det_id(
        "benchmark",
        f"{match_id}|football-data-couk-{source}|pre-match",
    )
    raw_odds = json.dumps(
        {
            "source": source,
            "AvgH": None if pd.isna(row.get("AvgH")) else float(row.get("AvgH")),
            "AvgD": None if pd.isna(row.get("AvgD")) else float(row.get("AvgD")),
            "AvgA": None if pd.isna(row.get("AvgA")) else float(row.get("AvgA")),
            "B365H": None if pd.isna(row.get("B365H")) else float(row.get("B365H")),
            "B365D": None if pd.isna(row.get("B365D")) else float(row.get("B365D")),
            "B365A": None if pd.isna(row.get("B365A")) else float(row.get("B365A")),
        }
    )
    forecast_at = kickoff - timedelta(hours=1)
    return (
        bench_id,
        match_id,
        f"football-data-couk-{source}",
        "v1",
        "MARKET_ODDS",
        "PRE_MATCH",
        forecast_at,
        forecast_at,
        None,  # match_minute
        "FINISHED" if hs_ft is not None else "SCHEDULED",
        hs_ft or 0,
        as_ft or 0,
        0,
        0,
        ph,
        pd_,
        pa,
        raw_odds,
        overround,
        "HISTORICAL",
    )


# ---------------------------------------------------------------------------
# Upsert.
# ---------------------------------------------------------------------------


def _chunked(seq: list, size: int) -> Iterable[list]:
    for i in range(0, len(seq), size):
        yield seq[i : i + size]


def upsert_many(
    cur,
    table: str,
    cols: list[str],
    update_cols: list[str],
    rows: list[tuple],
    chunk_size: int = 500,
) -> int:
    if not rows:
        return 0
    placeholders = "(" + ",".join(["%s"] * len(cols)) + ")"
    update_clause = ", ".join(f"{c}=VALUES({c})" for c in update_cols)
    total = 0
    for chunk in _chunked(rows, chunk_size):
        values_sql = ",".join([placeholders] * len(chunk))
        q = (
            f"INSERT INTO {table} ({', '.join(cols)}) VALUES {values_sql} "
            f"ON DUPLICATE KEY UPDATE {update_clause}"
        )
        flat: list = []
        for r in chunk:
            flat.extend(r)
        cur.execute(q, flat)
        total += len(chunk)
    return total


COMP_COLS = ["id", "provider", "external_id", "name", "country", "season"]
COMP_UPDATE = ["name", "country", "season"]

TEAM_COLS = ["id", "provider", "external_id", "name", "short_name", "country", "elo_rating"]
TEAM_UPDATE = ["name", "short_name", "country", "elo_rating"]

MATCH_COLS = [
    "id", "provider", "external_id", "competition_id",
    "home_team_id", "away_team_id", "kickoff_at", "status", "minute",
    "home_score", "away_score", "home_red_cards", "away_red_cards",
    "final_result", "venue", "provenance", "source_updated_at",
]
MATCH_UPDATE = [
    "status", "minute", "home_score", "away_score",
    "home_red_cards", "away_red_cards", "final_result", "venue",
    "provenance", "source_updated_at",
]

PRED_COLS = [
    "id", "match_id", "model_name", "model_version", "forecast_at",
    "match_minute", "home_score_at_fc", "away_score_at_fc",
    "home_red_cards_fc", "away_red_cards_fc",
    "p_home", "p_draw", "p_away", "features_json", "provenance",
    "source_timestamp",
]
PRED_UPDATE = [
    "forecast_at", "match_minute", "home_score_at_fc", "away_score_at_fc",
    "home_red_cards_fc", "away_red_cards_fc",
    "p_home", "p_draw", "p_away", "features_json", "provenance",
    "source_timestamp",
]

BENCH_COLS = [
    "id", "match_id", "provider", "provider_version", "source_type", "horizon",
    "forecast_at", "source_timestamp", "match_minute", "match_status",
    "home_score_at_fc", "away_score_at_fc", "home_red_cards_fc", "away_red_cards_fc",
    "p_home", "p_draw", "p_away", "raw_odds_json", "overround", "provenance",
]
BENCH_UPDATE = [
    "forecast_at", "source_timestamp", "match_status",
    "home_score_at_fc", "away_score_at_fc",
    "p_home", "p_draw", "p_away", "raw_odds_json", "overround", "provenance",
]


# ---------------------------------------------------------------------------
# Entry point.
# ---------------------------------------------------------------------------


def main() -> int:
    # Load local .env if present and we're not inside the Portal.
    if "SINGLESTOREDB_URL" not in os.environ:
        env_path = Path(__file__).resolve().parents[1] / ".env"
        if env_path.exists():
            for line in env_path.read_text().splitlines():
                line = line.strip()
                if not line or line.startswith("#") or "=" not in line:
                    continue
                k, _, v = line.partition("=")
                os.environ.setdefault(k.strip(), v.strip().strip("'\""))

    t0 = time.time()
    df = fetch_csv()
    ctx = build_rows(df)

    print(
        f"[build] teams={len(ctx.team_rows)} "
        f"matches={len(ctx.match_rows)} "
        f"preds={len(ctx.pred_rows)} "
        f"benchmarks={len(ctx.bench_rows)}"
    )

    conn = connect()
    try:
        with conn.cursor() as cur:
            upsert_many(cur, "competitions", COMP_COLS, COMP_UPDATE, [ctx.comp_row])
            upsert_many(cur, "teams", TEAM_COLS, TEAM_UPDATE, ctx.team_rows)
            upsert_many(cur, "matches", MATCH_COLS, MATCH_UPDATE, ctx.match_rows)
            upsert_many(cur, "model_predictions", PRED_COLS, PRED_UPDATE, ctx.pred_rows)
            upsert_many(cur, "benchmark_predictions", BENCH_COLS, BENCH_UPDATE, ctx.bench_rows)

            # Bookkeeping.
            run_id = det_id(
                "ingest-run",
                f"{PROVIDER}|{datetime.now(timezone.utc):%Y-%m-%dT%H}",
            )
            details = json.dumps(
                {
                    "csv_url": CSV_URL,
                    "season": COMPETITION_SEASON,
                    "played_matches": sum(1 for r in ctx.match_rows if r[7] == "FINISHED"),
                    "model": MODEL_NAME,
                    "model_version": MODEL_VERSION,
                }
            )
            cur.execute(
                """INSERT INTO data_ingestion_runs
                   (id, provider, job_name, status,
                    started_at, finished_at, records_in, records_out,
                    error_message, details_json)
                   VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
                   ON DUPLICATE KEY UPDATE
                     status=VALUES(status),
                     finished_at=VALUES(finished_at),
                     records_in=VALUES(records_in),
                     records_out=VALUES(records_out),
                     error_message=VALUES(error_message),
                     details_json=VALUES(details_json)""",
                (
                    run_id,
                    PROVIDER,
                    "notebook-pl-season",
                    "SUCCEEDED",
                    datetime.fromtimestamp(t0, tz=timezone.utc).replace(tzinfo=None),
                    datetime.now(timezone.utc).replace(tzinfo=None),
                    len(df),
                    len(ctx.match_rows) + len(ctx.pred_rows) + len(ctx.bench_rows),
                    None,
                    details,
                ),
            )
    finally:
        conn.close()

    print(f"[done] in {time.time() - t0:.1f}s")
    return 0


if __name__ == "__main__":
    sys.exit(main())
