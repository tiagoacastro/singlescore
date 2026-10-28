"""Hourly idempotent ingest of the current season for every league in
`ingest_common.LEAGUES` into SingleStore, from football-data.co.uk.

Generated into `notebooks/season_ingest.ipynb` by
`scripts/build_notebooks.py`; run locally with
`python3 scripts/ingest_season.py`.

Source: https://www.football-data.co.uk/mmz4281/<SEASON>/<DIV>.csv (public
CSVs, free for personal/educational use per the site's terms).

Design:
  * Zero paid APIs. Zero scraping. One HTTPS GET per league per run.
  * Fully idempotent: deterministic SHA-1-derived UUIDs on every row,
    `INSERT ... ON DUPLICATE KEY UPDATE` on every upsert.
  * Model `elo-poisson@v0.3.0` (`predict_v3`): the Poisson race of the Go
    reference (v0.2.0, `predict`, backend/internal/prediction) plus
    stoppage time and a two-sided red-card effect. Elo ratings are seeded
    from the previous two seasons and updated on goals + shot quality;
    scripts/backtest_model.py measures each change.
  * Elo ratings are walked chronologically per league (each league is a
    closed pool of teams).
  * Per completed match it writes:
      - matches, teams, competitions
      - model_predictions: pre-match + 5-min grid + HT + FT, plus one
        snapshot per goal / red-card minute when ESPN `match_events`
        exist and agree with the CSV score (HT/FT approximation otherwise)
      - benchmark_predictions: market consensus from the Avg (or Bet365)
        1X2 odds, de-vigged
      - match_stats: shots, corners, fouls, cards, xG, referee
"""

from __future__ import annotations

import io
import json
import math
import sys
import time
from dataclasses import dataclass
from datetime import datetime, timedelta
from os import environ

import pandas as pd
import requests

from ingest_common import (
    LEAGUES, SEASON, SEASON_CODE, League, chunked, connect, det_id,
    load_local_env, record_run, upsert_many, utc_now,
)


# ---------------------------------------------------------------------------
# Config.
# ---------------------------------------------------------------------------

CSV_BASE_URL = environ.get("FD_CSV_BASE_URL", "https://www.football-data.co.uk/mmz4281")

# Seasons walked before the current one so Elo doesn't restart at 1500
# for everyone (oldest first). Between seasons each rating keeps
# SEASON_CARRYOVER of its distance from the league mean, and promoted
# clubs start at the average of the clubs they replaced.
PREVIOUS_SEASON_CODES = ("2425", "2526")
SEASON_CARRYOVER = 0.8
PROVIDER = "football-data-couk"
JOB_NAME = "notebook-season"

# These MUST stay in sync with backend/internal/prediction/baseline.go;
# the parity tests on both sides (TestBaseline_MatchesPythonPort and
# scripts/test_ingest.py) pin the same golden values.
MODEL_NAME = "elo-poisson"
MODEL_VERSION = "v0.3.0"  # predict_v3; v0.2.0 (predict) is the Go baseline

AVG_GOALS_PER_TEAM = 1.35
ELO_HALF_LIFE = 800.0  # goal-rate ratio is exp(elo_gap / this)
RED_CARD_FACTOR = 0.72
MAX_EXTRA_GOALS = 10
HOME_ADVANTAGE = 60.0
ELO_K = 20.0  # update coefficient; ClubElo-style with goal-diff multiplier
INITIAL_ELO = 1500.0

# Retrospective timeline grid: every 5 minutes, with 45 and 90 written
# as the "half-time" / "full-time" snapshots instead.
GRID_MINUTES = tuple(m for m in range(5, 90, 5) if m != 45)


# ---------------------------------------------------------------------------
# Predictor port.
# ---------------------------------------------------------------------------

_FACT = [math.factorial(k) for k in range(MAX_EXTRA_GOALS + 1)]


def _poisson_pmf_vec(mu: float) -> list[float]:
    if mu <= 0:
        return [1.0] + [0.0] * MAX_EXTRA_GOALS
    e_neg = math.exp(-mu)
    return [e_neg * mu**k / _FACT[k] for k in range(MAX_EXTRA_GOALS + 1)]


def poisson_race(mu_home: float, mu_away: float, margin: int) -> tuple[float, float, float]:
    """Outcome probabilities when each side still expects `mu` goals and
    home currently leads by `margin` (independent Poissons)."""
    p_h = _poisson_pmf_vec(mu_home)
    p_a = _poisson_pmf_vec(mu_away)
    p_home = p_draw = p_away = 0.0
    for h in range(MAX_EXTRA_GOALS + 1):
        for a in range(MAX_EXTRA_GOALS + 1):
            p = p_h[h] * p_a[a]
            final = margin + h - a
            if final > 0:
                p_home += p
            elif final < 0:
                p_away += p
            else:
                p_draw += p
    s = p_home + p_draw + p_away
    if s <= 0 or math.isnan(s):
        return (1 / 3, 1 / 3, 1 / 3)
    return (p_home / s, p_draw / s, p_away / s)


def elo_goal_rates(home_elo: float, away_elo: float) -> tuple[float, float]:
    """Full-match expected goals per side from the Elo gap."""
    gap = (home_elo + HOME_ADVANTAGE - away_elo) / ELO_HALF_LIFE
    return AVG_GOALS_PER_TEAM * math.exp(+gap / 2), AVG_GOALS_PER_TEAM * math.exp(-gap / 2)


def predict(
    home_elo: float, away_elo: float, home_score: int, away_score: int,
    minute: int, home_red: int = 0, away_red: int = 0,
) -> tuple[float, float, float]:
    """v0.2.0, the Python port of Go `prediction.Baseline.Predict`: a
    Poisson race over the remaining fraction of 90 minutes, with
    Elo-driven scoring rates and a penalty for the side with more red
    cards. Kept as the baseline the v0.3.0 backtest compares against."""
    lh, la = elo_goal_rates(home_elo, away_elo)
    imb = home_red - away_red
    if imb > 0:
        lh *= RED_CARD_FACTOR**imb
    elif imb < 0:
        la *= RED_CARD_FACTOR ** (-imb)
    remaining = (90 - max(0, min(90, minute))) / 90.0
    return poisson_race(lh * remaining, la * remaining, home_score - away_score)


# ---------------------------------------------------------------------------
# v0.3.0: in-play information beyond the scoreline.
# ---------------------------------------------------------------------------

# Average stoppage time actually played (2026/27 commentary: 3.4 and 5.9
# minutes); a match is ~99 minutes, not 90.
EXPECTED_ADDED_1H = 3.5
EXPECTED_ADDED_2H = 6.0
MATCH_MINUTES = 90 + EXPECTED_ADDED_1H + EXPECTED_ADDED_2H

# Red cards: the carded side's scoring rate drops (RED_CARD_FACTOR) and
# the opponent's rises, per extra red card.
RED_CARD_OPPONENT = 1.3


def minutes_remaining(minute: int) -> float:
    """Expected playing time left after base `minute` (45 = half-time,
    stoppage time included)."""
    if minute <= 0:
        return MATCH_MINUTES
    if minute < 45:
        return (45 - minute) + EXPECTED_ADDED_1H + 45 + EXPECTED_ADDED_2H
    if minute < 90:
        return (90 - minute) + EXPECTED_ADDED_2H
    return 0.0


def predict_v3(
    home_elo: float, away_elo: float, home_score: int, away_score: int,
    minute: int, home_red: int = 0, away_red: int = 0,
) -> tuple[float, float, float]:
    """v0.3.0: the v0.2.0 Poisson race with stoppage time counted in the
    remaining time, and red cards that also lift the opponent's rate.
    (Its Elo ratings are seeded from previous seasons and updated on goals
    + shot quality; see seed_ratings / match_performance.)"""
    rate_h, rate_a = elo_goal_rates(home_elo, away_elo)
    imb = home_red - away_red
    if imb > 0:
        rate_h *= RED_CARD_FACTOR**imb
        rate_a *= RED_CARD_OPPONENT**imb
    elif imb < 0:
        rate_a *= RED_CARD_FACTOR ** (-imb)
        rate_h *= RED_CARD_OPPONENT ** (-imb)
    share = minutes_remaining(minute) / MATCH_MINUTES
    return poisson_race(rate_h * share, rate_a * share, home_score - away_score)


# Elo updates score each match on a blend of goals and shot quality
# (goals alone are noisy): shots on target are worth ~0.3 goals, other
# shots ~0.05. The backtest found a 50/50 blend best for pre-match
# accuracy.
PERFORMANCE_GOAL_WEIGHT = 0.5
SOT_GOAL_VALUE = 0.3
OTHER_SHOT_GOAL_VALUE = 0.05


def match_performance(row: pd.Series) -> tuple[float, float]:
    """(home, away) performance in goals: the blend above, or plain goals
    when the CSV has no shot columns."""
    hg, ag = float(row["FTHG"]), float(row["FTAG"])
    shots = [_int_or_none(row.get(c)) for c in ("HS", "HST", "AS", "AST")]
    if any(v is None for v in shots):
        return hg, ag
    hs, hst, as_, ast = shots
    hp = SOT_GOAL_VALUE * hst + OTHER_SHOT_GOAL_VALUE * max(hs - hst, 0)
    ap = SOT_GOAL_VALUE * ast + OTHER_SHOT_GOAL_VALUE * max(as_ - ast, 0)
    w = PERFORMANCE_GOAL_WEIGHT
    return w * hg + (1 - w) * hp, w * ag + (1 - w) * ap


def _elo_update(
    home_elo: float, away_elo: float, home_perf: float, away_perf: float,
) -> tuple[float, float]:
    """ClubElo-style update with a margin multiplier. A performance
    margin within a quarter of a goal counts as a draw."""
    expected_home = 1.0 / (1.0 + 10 ** ((away_elo - home_elo - HOME_ADVANTAGE) / 400.0))
    margin = home_perf - away_perf
    result_home = 1.0 if margin > 0.25 else 0.0 if margin < -0.25 else 0.5
    k = ELO_K * (1 + math.log(abs(margin) + 1))
    delta = k * (result_home - expected_home)
    return home_elo + delta, away_elo - delta


# ---------------------------------------------------------------------------
# Load CSV.
# ---------------------------------------------------------------------------


def fetch_csv(league: League, season_code: str = SEASON_CODE) -> pd.DataFrame:
    url = f"{CSV_BASE_URL}/{season_code}/{league.code}.csv"
    print(f"[fetch] {league.code} GET {url}")
    r = requests.get(url, timeout=30, allow_redirects=True)
    r.raise_for_status()
    df = pd.read_csv(io.StringIO(r.text))
    # football-data.co.uk uses dd/mm/YYYY and HH:MM in UK local time for
    # every league; kickoff_at is stored as naive UTC.
    df["Date"] = pd.to_datetime(df["Date"], format="%d/%m/%Y", errors="coerce")
    df = df.dropna(subset=["Date", "HomeTeam", "AwayTeam"]).copy()
    df["Time"] = df["Time"].fillna("15:00") if "Time" in df.columns else "15:00"
    df["KickoffAt"] = (
        pd.to_datetime(df["Date"].dt.strftime("%Y-%m-%d") + " " + df["Time"].astype(str),
                       errors="coerce")
        .dt.tz_localize("Europe/London", ambiguous=False, nonexistent="shift_forward")
        .dt.tz_convert("UTC")
        .dt.tz_localize(None)
    )
    df = df.dropna(subset=["KickoffAt"]).sort_values("KickoffAt").reset_index(drop=True)
    print(f"[fetch] {league.code} {len(df)} rows, {int(df['FTHG'].notna().sum())} played")
    return df


def season_start_ratings(previous: dict[str, float], teams: set[str]) -> dict[str, float]:
    """Opening ratings for `teams` from last season's closing ones."""
    if not previous:
        return {t: INITIAL_ELO for t in teams}
    mean = sum(previous.values()) / len(previous)
    departed = [r for t, r in previous.items() if t not in teams]
    promoted = sum(departed) / len(departed) if departed else mean
    return {
        t: mean + (previous[t] - mean) * SEASON_CARRYOVER if t in previous else promoted
        for t in teams
    }


def walk_elo(df: pd.DataFrame, ratings: dict[str, float]) -> dict[str, float]:
    """Ratings after every played match in `df` (chronological)."""
    ratings = dict(ratings)
    for _, row in df.iterrows():
        if pd.isna(row.get("FTHG")) or pd.isna(row.get("FTAG")):
            continue
        h, a = row["HomeTeam"], row["AwayTeam"]
        ratings[h], ratings[a] = _elo_update(ratings[h], ratings[a], *match_performance(row))
    return ratings


def seed_ratings(league: League, teams: set[str]) -> dict[str, float]:
    """Opening ratings for the current season, from the previous seasons'
    results. Falls back to INITIAL_ELO for everyone if they can't be
    fetched."""
    ratings: dict[str, float] = {}
    try:
        for code in PREVIOUS_SEASON_CODES:
            df = fetch_csv(league, code)
            ratings = walk_elo(df, season_start_ratings(ratings, set(df["HomeTeam"]) | set(df["AwayTeam"])))
    except (requests.RequestException, ValueError, KeyError) as e:
        print(f"[seed] {league.code} previous seasons unavailable ({e}); starting at {INITIAL_ELO}")
        ratings = {}
    return season_start_ratings(ratings, teams)


# ---------------------------------------------------------------------------
# Per-event data (ESPN scrape) for event-driven timelines.
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class TimelineEvent:
    minute: int
    added: int
    kind: str  # GOAL | RED_CARD (own goals arrive already credited to the beneficiary)
    side: str  # HOME | AWAY


def load_timeline_events(conn) -> dict[str, list[TimelineEvent]]:
    """Goals and red cards per match from the ESPN scrape, ordered by
    on-pitch time. Every match ESPN covered gets an entry, so a 0-0 with
    no reds maps to [] (real "nothing happened") rather than being
    missing (no per-event data at all)."""
    out: dict[str, list[TimelineEvent]] = {}
    with conn.cursor() as cur:
        cur.execute("SELECT DISTINCT match_id FROM match_events WHERE provider = 'espn'")
        for (match_id,) in cur.fetchall():
            out[match_id] = []
        cur.execute(
            """SELECT match_id, event_type, team_side, minute, COALESCE(added_minute, 0)
               FROM match_events
               WHERE provider = 'espn'
                 AND event_type IN ('GOAL', 'OWN_GOAL', 'RED_CARD')
                 AND team_side IN ('HOME', 'AWAY')
                 AND minute IS NOT NULL
               ORDER BY match_id, minute, added_minute"""
        )
        for match_id, etype, side, minute, added in cur.fetchall():
            kind = "RED_CARD" if etype == "RED_CARD" else "GOAL"
            out[match_id].append(TimelineEvent(int(minute), int(added), kind, side))
    return out


def _state_at(events: list[TimelineEvent], minute: int) -> tuple[int, int, int, int]:
    """(home goals, away goals, home reds, away reds) after everything
    up to and including `minute` (stoppage-time events count towards
    their base minute, so a 45'+3 goal is in the half-time state)."""
    hs = as_ = hr = ar = 0
    for e in events:
        if e.minute > minute:
            break
        if e.kind == "GOAL":
            if e.side == "HOME":
                hs += 1
            else:
                as_ += 1
        elif e.side == "HOME":
            hr += 1
        else:
            ar += 1
    return hs, as_, hr, ar


# ---------------------------------------------------------------------------
# Row builders.
# ---------------------------------------------------------------------------


def _make_prediction_rows(
    match_id: str, kickoff: datetime, home_elo: float, away_elo: float,
    hs_ht: int, as_ht: int, hs_ft: int | None, as_ft: int | None,
    events: list[TimelineEvent] | None = None,
) -> list[tuple]:
    """Retrospective timeline for a match: a pre-match row for every
    fixture, and for played matches the 5-minute grid + half-time +
    full-time.

    When per-event data exists and its goals add up to the CSV final
    score, the scoreline and red cards at each snapshot come from the
    real events and an extra "goal-event"/"red-card-event" snapshot is
    written at each event minute. Otherwise the scoreline falls back to
    the HT/FT approximation (0-0 before 45', HT score until 90').
    """
    played = hs_ft is not None and as_ft is not None
    use_events = (
        played and events is not None and _state_at(events, 200)[:2] == (hs_ft, as_ft)
    )
    score_source = "events" if use_events else "ht-ft-approximation"
    out: list[tuple] = []

    def add(minute: int, origin: str, forecast_at: datetime,
            hs: int, as_: int, hr: int = 0, ar: int = 0) -> None:
        p_h, p_d, p_a = predict_v3(home_elo, away_elo, hs, as_, minute, hr, ar)
        features = {
            "home_elo": home_elo, "away_elo": away_elo, "minute": minute,
            "home_score": hs, "away_score": as_, "home_red": hr, "away_red": ar,
            "home_advantage": HOME_ADVANTAGE, "model": MODEL_NAME,
            "model_version": MODEL_VERSION, "source_provider": PROVIDER,
            "score_source": score_source, "origin": origin,
        }
        pred_id = det_id(
            "prediction", f"{match_id}|{MODEL_NAME}|{MODEL_VERSION}|{minute}|{origin}"
        )
        out.append((
            pred_id, match_id, MODEL_NAME, MODEL_VERSION, forecast_at,
            None if minute == 0 else minute, hs, as_, hr, ar,
            p_h, p_d, p_a, json.dumps(features), "HISTORICAL", forecast_at,
        ))

    # Pre-match, back-dated 1h so it sorts before every in-play snapshot.
    add(0, "pre-match", kickoff - timedelta(hours=1), 0, 0)
    if not played:
        return out

    def at(minute: int) -> datetime:
        return kickoff + timedelta(minutes=minute)

    if use_events:
        def state(minute: int) -> tuple[int, int, int, int]:
            return _state_at(events, minute)
    else:
        def state(minute: int) -> tuple[int, int, int, int]:
            return (0, 0, 0, 0) if minute < 45 else (hs_ht, as_ht, 0, 0)

    for minute in GRID_MINUTES:
        add(minute, "reconstructed-5min", at(minute), *state(minute))
    if use_events:
        # One snapshot per event minute the grid doesn't already cover
        # (grid snapshots at the same minute already include the event).
        taken = set(GRID_MINUTES) | {45, 90}
        for e in events:
            if e.minute <= 0 or e.minute in taken:
                continue
            taken.add(e.minute)
            origin = "goal-event" if e.kind == "GOAL" else "red-card-event"
            add(e.minute, origin, at(e.minute + e.added), *state(e.minute))
    add(45, "half-time", at(45), *state(45))
    final_reds = state(200)[2:] if use_events else (0, 0)
    add(90, "full-time", at(90), hs_ft, as_ft, *final_reds)
    return out


def _float_or_none(v) -> float | None:
    try:
        return None if v is None or pd.isna(v) else float(v)
    except (TypeError, ValueError):
        return None


ODDS_COLUMNS = ("AvgH", "AvgD", "AvgA", "B365H", "B365D", "B365A")


def _make_benchmark_row(
    row: pd.Series, match_id: str, kickoff: datetime, hs_ft: int | None, as_ft: int | None,
) -> tuple | None:
    """Pre-match market-consensus benchmark from the Avg 1X2 odds
    (Bet365 if Avg is missing), de-vigged. None if neither triple is
    complete and valid."""
    odds = {c: _float_or_none(row.get(c)) for c in ODDS_COLUMNS}
    for source, cols in (("market-avg", ODDS_COLUMNS[:3]), ("bet365", ODDS_COLUMNS[3:])):
        triple = [odds[c] for c in cols]
        if all(o is not None and o > 1.0 for o in triple):
            break
    else:
        return None
    inv = [1.0 / o for o in triple]
    overround = sum(inv)
    ph, pd_, pa = (x / overround for x in inv)
    provider = f"football-data-couk-{source}"
    forecast_at = kickoff - timedelta(hours=1)
    return (
        det_id("benchmark", f"{match_id}|{provider}|pre-match"),
        match_id, provider, "v1", "MARKET_ODDS", "PRE_MATCH",
        forecast_at, forecast_at, None,
        "FINISHED" if hs_ft is not None else "SCHEDULED",
        hs_ft or 0, as_ft or 0, 0, 0,
        ph, pd_, pa, json.dumps({"source": source, **odds}), overround, "HISTORICAL",
    )


def _int_or_none(v) -> int | None:
    try:
        return None if v is None or pd.isna(v) else int(v)
    except (TypeError, ValueError):
        return None


def _str_or_none(v) -> str | None:
    if v is None or pd.isna(v):
        return None
    return str(v).strip() or None


# match_stats column -> CSV column. Referee is only published for the
# English divisions.
STATS_SOURCE_COLUMNS = {
    "home_shots": "HS", "away_shots": "AS",
    "home_shots_on_t": "HST", "away_shots_on_t": "AST",
    "home_corners": "HC", "away_corners": "AC",
    "home_fouls": "HF", "away_fouls": "AF",
    "home_yellow": "HY", "away_yellow": "AY",
    "home_red": "HR", "away_red": "AR",
}


def _make_stats_row(row: pd.Series, match_id: str) -> tuple | None:
    """match_stats row from the CSV aggregate columns, or None when none
    of them are populated."""
    values = [_int_or_none(row.get(c)) for c in STATS_SOURCE_COLUMNS.values()]
    values += [
        _float_or_none(row.get("HxG")), _float_or_none(row.get("AxG")),
        _str_or_none(row.get("Referee")),
    ]
    if all(v is None for v in values):
        return None
    return (match_id, *values, PROVIDER)


@dataclass
class Context:
    comp_row: tuple
    team_rows: list[tuple]
    match_rows: list[tuple]
    pred_rows: list[tuple]
    bench_rows: list[tuple]
    stats_rows: list[tuple]


def build_rows(
    league: League, df: pd.DataFrame,
    events_by_match: dict[str, list[TimelineEvent]] | None = None,
) -> Context:
    """All rows for one league's season, walking Elo chronologically so
    every snapshot of a match uses the pre-match ratings."""
    events_by_match = events_by_match or {}
    comp_id = det_id("competition", f"{PROVIDER}|{league.competition_external_id}")
    now = utc_now()

    team_names = sorted(set(df["HomeTeam"]) | set(df["AwayTeam"]))
    team_id = {name: det_id("team", f"{PROVIDER}|{name}") for name in team_names}
    elo = seed_ratings(league, set(team_names))
    ctx = Context(
        comp_row=(comp_id, PROVIDER, league.competition_external_id,
                  league.name, league.country, SEASON),
        team_rows=[], match_rows=[], pred_rows=[], bench_rows=[], stats_rows=[],
    )

    for _, row in df.iterrows():
        home, away = row["HomeTeam"], row["AwayTeam"]
        kickoff: datetime = row["KickoffAt"].to_pydatetime()
        # The id keys on the CSV's (UK) match date, not the UTC one.
        ext_match = f"{SEASON}|{row['Date']:%Y-%m-%d}|{home}|{away}"
        match_id = det_id("match", f"{PROVIDER}|{ext_match}")
        home_elo, away_elo = elo[home], elo[away]
        played = not (pd.isna(row.get("FTHG")) or pd.isna(row.get("FTAG")))

        if played:
            hs_ft, as_ft = int(row["FTHG"]), int(row["FTAG"])
            hs_ht, as_ht = _int_or_none(row.get("HTHG")) or 0, _int_or_none(row.get("HTAG")) or 0
            reds = (_int_or_none(row.get("HR")) or 0, _int_or_none(row.get("AR")) or 0)
            result = "HOME" if hs_ft > as_ft else "AWAY" if hs_ft < as_ft else "DRAW"
            status, minute, score = "FINISHED", 90, (hs_ft, as_ft)
        else:
            hs_ft = as_ft = None
            hs_ht = as_ht = 0
            reds, result = (0, 0), None
            status, minute, score = "SCHEDULED", None, (0, 0)

        ctx.match_rows.append((
            match_id, PROVIDER, ext_match, comp_id, team_id[home], team_id[away],
            kickoff, status, minute, *score, *reds, result,
            None,  # venue
            "HISTORICAL", now,
        ))
        ctx.pred_rows.extend(_make_prediction_rows(
            match_id, kickoff, home_elo, away_elo, hs_ht, as_ht, hs_ft, as_ft,
            events_by_match.get(match_id) if played else None,
        ))
        bench = _make_benchmark_row(row, match_id, kickoff, hs_ft, as_ft)
        if bench is not None:
            ctx.bench_rows.append(bench)
        if played:
            stats = _make_stats_row(row, match_id)
            if stats is not None:
                ctx.stats_rows.append(stats)
            elo[home], elo[away] = _elo_update(home_elo, away_elo, *match_performance(row))

    # Teams carry the season-to-date Elo so the UI shows current strength.
    ctx.team_rows = [
        (team_id[n], PROVIDER, n, n, n[:50], league.country, float(elo[n]))
        for n in team_names
    ]
    return ctx


# ---------------------------------------------------------------------------
# Upsert.
# ---------------------------------------------------------------------------

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
MATCH_UPDATE = MATCH_COLS[6:]  # kickoff_at onwards, so reschedules land too

PRED_COLS = [
    "id", "match_id", "model_name", "model_version", "forecast_at",
    "match_minute", "home_score_at_fc", "away_score_at_fc",
    "home_red_cards_fc", "away_red_cards_fc",
    "p_home", "p_draw", "p_away", "features_json", "provenance",
    "source_timestamp",
]
PRED_UPDATE = PRED_COLS[4:]
PRED_FEATURES_IDX = PRED_COLS.index("features_json")

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

STATS_COLS = ["match_id", *STATS_SOURCE_COLUMNS, "home_xg", "away_xg", "referee", "source"]
STATS_UPDATE = STATS_COLS[1:]


# ---------------------------------------------------------------------------
# Entry point.
# ---------------------------------------------------------------------------


def main() -> int:
    load_local_env()
    started = utc_now()
    t0 = time.time()

    frames: dict[str, pd.DataFrame] = {}
    failed: dict[str, str] = {}
    for league in LEAGUES:
        try:
            frames[league.code] = fetch_csv(league)
        except (requests.RequestException, ValueError, KeyError) as e:
            print(f"[fetch] {league.code} failed: {e}")
            failed[league.code] = str(e)

    conn = connect()
    try:
        events_by_match = load_timeline_events(conn)
        contexts = [
            build_rows(league, frames[league.code], events_by_match)
            for league in LEAGUES if league.code in frames
        ]
        # Team ids hash the bare CSV name, so the same name in two
        # leagues would merge two clubs into one row.
        seen: dict[str, str] = {}
        for ctx in contexts:
            for t in ctx.team_rows:
                if seen.setdefault(t[2], ctx.comp_row[2]) != ctx.comp_row[2]:
                    raise SystemExit(f"team name {t[2]!r} appears in two leagues")

        def rows(attr: str) -> list[tuple]:
            return [r for ctx in contexts for r in getattr(ctx, attr)]

        pred_rows = rows("pred_rows")
        event_driven = sorted({
            r[1] for r in pred_rows
            if json.loads(r[PRED_FEATURES_IDX])["score_source"] == "events"
        })
        per_league = {}
        for ctx in contexts:
            match_ids = {r[0] for r in ctx.match_rows}
            per_league[ctx.comp_row[2]] = {
                "matches": len(ctx.match_rows),
                "played": sum(1 for r in ctx.match_rows if r[7] == "FINISHED"),
                "event_driven": len(match_ids.intersection(event_driven)),
            }
            print(f"[build] {ctx.comp_row[3]}: {per_league[ctx.comp_row[2]]}")

        with conn.cursor() as cur:
            # Event-minute snapshots move if ESPN corrects a minute, so
            # drop them before re-inserting the current set.
            for chunk in chunked(event_driven, 500):
                cur.execute(
                    f"""DELETE FROM model_predictions
                        WHERE model_name = %s AND model_version = %s
                          AND match_id IN ({",".join(["%s"] * len(chunk))})
                          AND JSON_EXTRACT_STRING(features_json, 'origin')
                              IN ('goal-event', 'red-card-event')""",
                    (MODEL_NAME, MODEL_VERSION, *chunk),
                )
            # Snapshots from other versions of this model would be mixed
            # into the charts and the evaluation; they're derived data,
            # so the current version replaces them.
            cur.execute(
                "DELETE FROM model_predictions WHERE model_name = %s AND model_version <> %s",
                (MODEL_NAME, MODEL_VERSION),
            )
            written = sum((
                upsert_many(cur, "competitions", COMP_COLS, COMP_UPDATE,
                            [ctx.comp_row for ctx in contexts]),
                upsert_many(cur, "teams", TEAM_COLS, TEAM_UPDATE, rows("team_rows")),
                upsert_many(cur, "matches", MATCH_COLS, MATCH_UPDATE, rows("match_rows")),
                upsert_many(cur, "model_predictions", PRED_COLS, PRED_UPDATE, pred_rows),
                upsert_many(cur, "benchmark_predictions", BENCH_COLS, BENCH_UPDATE,
                            rows("bench_rows")),
                upsert_many(cur, "match_stats", STATS_COLS, STATS_UPDATE, rows("stats_rows")),
            ))
            status = "SUCCEEDED" if not failed else "PARTIAL" if contexts else "FAILED"
            record_run(
                cur, provider=PROVIDER, job_name=JOB_NAME, started_at=started,
                status=status, records_in=sum(len(df) for df in frames.values()),
                records_out=written,
                error="; ".join(f"{k}: {v}" for k, v in failed.items()) or None,
                details={"season": SEASON, "csv_base_url": CSV_BASE_URL,
                         "model": MODEL_NAME, "model_version": MODEL_VERSION,
                         "leagues": per_league},
            )
    finally:
        conn.close()

    print(f"[done] {status} in {time.time() - t0:.1f}s")
    return 0 if status == "SUCCEEDED" else 1


if __name__ == "__main__":
    sys.exit(main())
