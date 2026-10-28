"""Backtest prediction-model variants on the current season.

    .venv/bin/python scripts/backtest_model.py

Replays every played match at the standard snapshot minutes (pre-match,
5-minute grid, half-time), using only what was known at each minute, and
scores the forecasts with the same multiclass Brier score and log loss
as the evaluation page. Each v0.3.0 change is added in turn on top of
v0.2.0; results are shown for the earlier and later half of the matches
(by date) separately, because a change that only helps one half is noise.

Reads matches / events / commentary from SingleStore and the CSVs from
football-data.co.uk (current + previous seasons, for Elo seeding).
"""

from __future__ import annotations

import math
import sys
from dataclasses import dataclass
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import ingest_season as m  # noqa: E402
from ingest_common import LEAGUES, SEASONS, connect, det_id, load_local_env  # noqa: E402

SNAPSHOT_MINUTES = (0, *range(5, 45, 5), 45, *range(50, 90, 5))
BUCKETS = (("pre-match", 0, 0), ("1-40", 1, 40), ("45-60", 45, 60), ("65-85", 65, 85))


@dataclass
class Snapshot:
    minute: int
    hs: int
    as_: int
    hr: int
    ar: int
    hp: float  # pressure over the last PRESSURE_WINDOW minutes
    ap: float


@dataclass
class Case:
    match_id: str
    kickoff: object
    outcome: int  # 0 home, 1 draw, 2 away
    elo: dict  # ratings name -> (home_elo, away_elo) before the match
    snapshots: list[Snapshot]


def goals_only(row):
    return float(row["FTHG"]), float(row["FTAG"])


RATINGS = {
    # name: (seeded from previous seasons?, performance measure)
    "flat, goals": (False, goals_only),
    "seeded, goals": (True, goals_only),
    "seeded, goals+shots": (True, m.match_performance),
}


def walk(ratings: dict[str, float], df, measure) -> dict[str, float]:
    ratings = dict(ratings)
    for _, row in df.iterrows():
        if not row.isna()[["FTHG", "FTAG"]].any():
            h, a = row["HomeTeam"], row["AwayTeam"]
            ratings[h], ratings[a] = m._elo_update(ratings[h], ratings[a], *measure(row))
    return ratings


def build_cases() -> list[Case]:
    """Every stored-season match with consistent per-event data, with the
    pre-match ratings of each variant (seeded variants chain through the
    seasons; the flat one restarts at 1500 every season, like v0.2.0)."""
    conn = connect()
    try:
        events = m.load_timeline_events(conn)
        actions = m.load_pressure_actions(conn)
    finally:
        conn.close()
    cases: list[Case] = []
    for league in LEAGUES:
        closing: dict[str, dict[str, float]] = {}
        for name, (seeded, measure) in RATINGS.items():
            r: dict[str, float] = {}
            if seeded:
                for code in m.SEED_SEASON_CODES:
                    prev = m.fetch_csv(league, code)
                    r = walk(m.season_start_ratings(r, set(prev["HomeTeam"]) | set(prev["AwayTeam"])), prev, measure)
            closing[name] = r
        for season in SEASONS:
            df = m.fetch_csv(league, season.code)
            teams = set(df["HomeTeam"]) | set(df["AwayTeam"])
            ratings = {
                name: m.season_start_ratings(closing[name] if RATINGS[name][0] else {}, teams)
                for name in RATINGS
            }
            for _, row in df.iterrows():
                if row.isna()[["FTHG", "FTAG"]].any():
                    continue
                home, away = row["HomeTeam"], row["AwayTeam"]
                hs_ft, as_ft = int(row["FTHG"]), int(row["FTAG"])
                match_id = det_id("match", f"{m.PROVIDER}|{season.name}|{row['Date']:%Y-%m-%d}|{home}|{away}")
                ev = events.get(match_id)
                if ev is not None and m._state_at(ev, 200)[:2] == (hs_ft, as_ft):
                    acts = actions.get(match_id, [])
                    snaps = [Snapshot(t, *m._state_at(ev, t), *m.pressure_at(acts, t))
                             for t in SNAPSHOT_MINUTES]
                    outcome = 0 if hs_ft > as_ft else 2 if hs_ft < as_ft else 1
                    cases.append(Case(match_id, row["KickoffAt"], outcome,
                                      {n: (r[home], r[away]) for n, r in ratings.items()}, snaps))
                for name, r in ratings.items():
                    r[home], r[away] = m._elo_update(r[home], r[away], *RATINGS[name][1](row))
            closing = ratings
    return cases


def score(probs: tuple[float, float, float], outcome: int) -> tuple[float, float]:
    brier = sum((p - (1.0 if i == outcome else 0.0)) ** 2 for i, p in enumerate(probs))
    return brier, -math.log(max(probs[outcome], 1e-12))


def evaluate(cases: list[Case], model, ratings: str) -> dict[str, tuple[float, float, int]]:
    acc: dict[str, list[float]] = {b[0]: [0.0, 0.0, 0] for b in BUCKETS}
    for c in cases:
        he, ae = c.elo[ratings]
        for s in c.snapshots:
            bucket = next(b[0] for b in BUCKETS if b[1] <= s.minute <= b[2])
            brier, ll = score(model(he, ae, s), c.outcome)
            a = acc[bucket]
            a[0] += brier
            a[1] += ll
            a[2] += 1
    return {k: (a[0] / a[2], a[1] / a[2], a[2]) for k, a in acc.items() if a[2]}


def overall(result) -> float:
    """Mean Brier over all snapshots."""
    n = sum(v[2] for v in result.values())
    return sum(v[0] * v[2] for v in result.values()) / n


def main() -> int:
    load_local_env()
    cases = sorted(build_cases(), key=lambda c: c.kickoff)
    cut = cases[len(cases) // 2].kickoff
    halves = {
        "earlier": [c for c in cases if c.kickoff < cut],
        "later": [c for c in cases if c.kickoff >= cut],
        "all": cases,
    }
    print(f"{len(cases)} matches with consistent per-event data (halves split at {cut:%Y-%m-%d})")
    print("Brier score, lower is better: pre-match | all snapshots, earlier / later half of the matches\n")
    def v2(he, ae, s):
        return m.predict(he, ae, s.hs, s.as_, s.minute, s.hr, s.ar)

    def v3(he, ae, s):
        return m.predict_v3(he, ae, s.hs, s.as_, s.minute, s.hr, s.ar)

    def v4(he, ae, s):
        return m.predict_v3(he, ae, s.hs, s.as_, s.minute, s.hr, s.ar, s.hp, s.ap)

    variants = [
        ("v0.2.0 (flat Elo from 1500, 90-minute clock)", v2, "flat, goals"),
        ("+ Elo seeded from 2 previous seasons", v2, "seeded, goals"),
        ("+ Elo updated on goals + shot quality", v2, "seeded, goals+shots"),
        ("+ stoppage time, two-sided red cards (v0.3.0)", v3, "seeded, goals+shots"),
        ("+ tilt towards the side on top = v0.4.0", v4, "seeded, goals+shots"),
    ]
    for name, model, ratings in variants:
        cells = []
        for subset in halves.values():
            r = evaluate(subset, model, ratings)
            cells.append(f"{r['pre-match'][0]:.4f} | {overall(r):.4f}")
        print(f"{name:48} " + "    ".join(f"{h}: {c}" for h, c in zip(halves, cells)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
