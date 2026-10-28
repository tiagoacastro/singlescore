"""Hourly ingest of per-event timings + minute-by-minute commentary
from ESPN's public JSON endpoints for every league in
`ingest_common.LEAGUES`.

Generated into `notebooks/events_espn.ipynb` by
`scripts/build_notebooks.py`; run locally with
`python3 scripts/ingest_events_espn.py`. Run after the season ingest:
it attaches rows to the matches that one writes.

Source: https://site.api.espn.com/apis/site/v2/sports/soccer/<slug>/
(scoreboard per date + summary per match), the same JSON ESPN's own
public match pages consume.

**Important** — this script scrapes ESPN. The project constraint
"Do not scrape sites without verifying that the method is permitted"
was explicitly waived by the project owner for this script
(2026-10-27) because free + permitted sources (football-data.co.uk,
football-data.org, openfootball) do not expose per-event minutes or
play-by-play commentary. ESPN was chosen over fotmob / SofaScore /
Flashscore because its endpoint is served without anti-bot friction and
is widely used by open-source analytics libraries. The script sends a
descriptive User-Agent and sleeps 300 ms between calls.

What it writes per run:
  * match_events: one row per keyEvent (goal, yellow/red card,
    substitution, VAR / delay). Replaces existing ESPN rows for each
    match it touches so re-runs don't duplicate.
  * match_commentary: one row per commentary line, keyed on
    (match_id, sequence) so re-runs upsert.
"""

from __future__ import annotations

import re
import sys
from os import environ
import time
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Optional

import requests

from ingest_common import (
    LEAGUES, League, chunked, connect, det_id, load_local_env, record_run,
    upsert_many, utc_now,
)


# ---------------------------------------------------------------------------
# Config.
# ---------------------------------------------------------------------------

ESPN_BASE = "https://site.api.espn.com/apis/site/v2/sports/soccer"
POLITE_DELAY_S = 0.3  # between HTTP calls to ESPN
HTTP_HEADERS = {
    "User-Agent": "SingleScore/0.1 (+github.com/tiagoacastro/singlescore)",
    "Accept-Encoding": "gzip",
    "Accept": "application/json",
}
PROVIDER = "espn"
JOB_NAME = "espn-events"

# Only re-fetch matches that kicked off within this many days (plus any
# match with no events yet). ESPN's data is effectively immutable once a
# match is final.
BACKFILL_LOOKBACK_DAYS = int(environ.get("ESPN_LOOKBACK_DAYS", "7"))

# ESPN team displayName -> football-data.co.uk name (the name the season
# ingest hashes into team/match ids), per league code, covering every
# club in the stored seasons.
TEAM_NAME_MAPS: dict[str, dict[str, str]] = {
    "E0": {
        "AFC Bournemouth": "Bournemouth",
        "Burnley": "Burnley",
        "West Ham United": "West Ham",
        "Wolverhampton Wanderers": "Wolves",
        "Arsenal": "Arsenal",
        "Aston Villa": "Aston Villa",
        "Brentford": "Brentford",
        "Brighton & Hove Albion": "Brighton",
        "Chelsea": "Chelsea",
        "Coventry City": "Coventry",
        "Crystal Palace": "Crystal Palace",
        "Everton": "Everton",
        "Fulham": "Fulham",
        "Hull City": "Hull",
        "Ipswich Town": "Ipswich",
        "Leeds United": "Leeds",
        "Liverpool": "Liverpool",
        "Manchester City": "Man City",
        "Manchester United": "Man United",
        "Newcastle United": "Newcastle",
        "Nottingham Forest": "Nott'm Forest",
        "Sunderland": "Sunderland",
        "Tottenham Hotspur": "Tottenham",
    },
    "SP1": {
        "Alavés": "Alaves",
        "Girona": "Girona",
        "Mallorca": "Mallorca",
        "Real Oviedo": "Oviedo",
        "Athletic Club": "Ath Bilbao",
        "Atlético Madrid": "Ath Madrid",
        "Barcelona": "Barcelona",
        "Celta Vigo": "Celta",
        "Deportivo": "La Coruna",
        "Elche": "Elche",
        "Espanyol": "Espanol",
        "Getafe": "Getafe",
        "Levante": "Levante",
        "Málaga": "Malaga",
        "Osasuna": "Osasuna",
        "Racing Santander": "Santander",
        "Rayo Vallecano": "Vallecano",
        "Real Betis": "Betis",
        "Real Madrid": "Real Madrid",
        "Real Sociedad": "Sociedad",
        "Sevilla": "Sevilla",
        "Valencia": "Valencia",
        "Villarreal": "Villarreal",
    },
    "I1": {
        "AC Milan": "Milan",
        "Cremonese": "Cremonese",
        "Hellas Verona": "Verona",
        "Pisa": "Pisa",
        "AS Roma": "Roma",
        "Atalanta": "Atalanta",
        "Bologna": "Bologna",
        "Cagliari": "Cagliari",
        "Como": "Como",
        "Fiorentina": "Fiorentina",
        "Frosinone": "Frosinone",
        "Genoa": "Genoa",
        "Internazionale": "Inter",
        "Juventus": "Juventus",
        "Lazio": "Lazio",
        "Lecce": "Lecce",
        "Monza": "Monza",
        "Napoli": "Napoli",
        "Parma": "Parma",
        "Sassuolo": "Sassuolo",
        "Torino": "Torino",
        "Udinese": "Udinese",
        "Venezia": "Venezia",
    },
    "D1": {
        "1. FC Heidenheim 1846": "Heidenheim",
        "1. FC Union Berlin": "Union Berlin",
        "St. Pauli": "St Pauli",
        "VfL Wolfsburg": "Wolfsburg",
        "Bayer Leverkusen": "Leverkusen",
        "Bayern Munich": "Bayern Munich",
        "Borussia Dortmund": "Dortmund",
        "Borussia Mönchengladbach": "M'gladbach",
        "Eintracht Frankfurt": "Ein Frankfurt",
        "FC Augsburg": "Augsburg",
        "FC Cologne": "FC Koln",
        "Hamburg SV": "Hamburg",
        "Mainz": "Mainz",
        "RB Leipzig": "RB Leipzig",
        "SC Freiburg": "Freiburg",
        "SC Paderborn 07": "Paderborn",
        "SV Elversberg": "Elversberg",
        "Schalke 04": "Schalke 04",
        "TSG Hoffenheim": "Hoffenheim",
        "VfB Stuttgart": "Stuttgart",
        "Werder Bremen": "Werder Bremen",
    },
    "P1": {
        "AVS": "AVS",
        "Académico de Viseu": "Academico Viseu",
        "Tondela": "Tondela",
        "Alverca": "Alverca",
        "Arouca": "Arouca",
        "Benfica": "Benfica",
        "Braga": "Sp Braga",
        "C.D. Nacional": "Nacional",
        "Casa Pia": "Casa Pia",
        "Estoril": "Estoril",
        "Estrela": "Estrela",
        "FC Famalicao": "Famalicao",
        "FC Porto": "Porto",
        "Gil Vicente": "Gil Vicente",
        "Maritimo": "Maritimo",
        "Moreirense": "Moreirense",
        "Rio Ave": "Rio Ave",
        "Santa Clara": "Santa Clara",
        "Sporting CP": "Sp Lisbon",
        "Vitória de Guimaraes": "Guimaraes",
    },
}
LEAGUE_BY_CODE = {league.code: league for league in LEAGUES}


# ---------------------------------------------------------------------------
# Classification.
# ---------------------------------------------------------------------------

# ESPN keyEvent types -> match_events.event_type. Goals come in many
# subtypes (`goal`, `goal---volley`, `goal---header`, `goal---free-kick`,
# ...), so any other `scoringPlay` event is treated as a GOAL.
EVENT_TYPE_MAP: dict[str, str] = {
    "own-goal": "OWN_GOAL",
    "penalty": "GOAL",
    "penalty-missed": "PENALTY_MISSED",
    "yellow-card": "YELLOW_CARD",
    "red-card": "RED_CARD",
    "second-yellow": "RED_CARD",
    "substitution": "SUBSTITUTION",
    "start-delay": "VAR",  # VAR check or injury pause
    "end-delay": "VAR",
}


def classify_keyevent(ev: dict) -> Optional[str]:
    """Map an ESPN keyEvent to a match_events.event_type, or None to skip
    it (kickoff, half-time and other markers)."""
    espn_type = (ev.get("type") or {}).get("type", "")
    if espn_type in EVENT_TYPE_MAP:
        return EVENT_TYPE_MAP[espn_type]
    if ev.get("scoringPlay") and not espn_type.startswith("own-goal"):
        return "GOAL"
    return None


# Commentary text patterns -> kind. Order matters (more specific first).
COMMENTARY_PATTERNS: list[tuple[re.Pattern, str]] = [
    (re.compile(r"^Goal!", re.I), "GOAL"),
    (re.compile(r"^Own Goal", re.I), "OWN_GOAL"),
    (re.compile(r"^Penalty saved", re.I), "PENALTY_SAVED"),
    (re.compile(r"^Penalty missed", re.I), "PENALTY_MISSED"),
    (re.compile(r"^Attempt saved", re.I), "ATTEMPT_SAVED"),
    (re.compile(r"^Attempt blocked", re.I), "ATTEMPT_BLOCKED"),
    (re.compile(r"^Attempt missed", re.I), "ATTEMPT_MISSED"),
    (re.compile(r"shown the yellow card|is shown the yellow", re.I), "YELLOW_CARD"),
    (re.compile(r"shown the red card|is shown the red|second yellow", re.I), "RED_CARD"),
    (re.compile(r"^Substitution", re.I), "SUBSTITUTION"),
    (re.compile(r"^VAR", re.I), "VAR"),
    (re.compile(r"^Corner", re.I), "CORNER"),
    (re.compile(r"^Foul by", re.I), "FOUL"),
    (re.compile(r"wins a free kick", re.I), "FREE_KICK"),
    (re.compile(r"^Offside", re.I), "OFFSIDE"),
    (re.compile(r"First Half begins|Second Half begins", re.I), "PERIOD_START"),
    (re.compile(r"^Half Time|First Half ends|ends,|Match ends", re.I), "PERIOD_END"),
    (re.compile(r"Lineups are announced", re.I), "LINEUP"),
]


def classify_commentary(text: str) -> str:
    for pat, kind in COMMENTARY_PATTERNS:
        if pat.search(text):
            return kind
    return "COMMENTARY"


# ---------------------------------------------------------------------------
# ESPN fetching.
# ---------------------------------------------------------------------------


def _get(url: str, **params) -> dict:
    r = requests.get(url, params=params, headers=HTTP_HEADERS, timeout=20)
    r.raise_for_status()
    return r.json()


@dataclass
class DBMatch:
    id: str
    league: League
    kickoff: datetime
    home: str
    away: str


def load_matches_to_backfill(cur) -> list[DBMatch]:
    """Finished matches of the configured leagues that kicked off within
    the lookback window or have no ESPN events yet."""
    cutoff = utc_now() - timedelta(days=BACKFILL_LOOKBACK_DAYS)
    cur.execute(
        """SELECT m.id, c.external_id, m.kickoff_at, th.name, ta.name,
                  EXISTS (SELECT 1 FROM match_events e
                          WHERE e.match_id = m.id AND e.provider = %s) AS has_events
           FROM matches m
           JOIN competitions c ON c.id = m.competition_id
           JOIN teams th ON th.id = m.home_team_id
           JOIN teams ta ON ta.id = m.away_team_id
           WHERE m.status = 'FINISHED'""",
        (PROVIDER,),
    )
    out = []
    for mid, comp_ext, kickoff, home, away, has_events in cur.fetchall():
        league = LEAGUE_BY_CODE.get(comp_ext.split("-")[0])
        if league and (kickoff >= cutoff or not has_events):
            out.append(DBMatch(mid, league, kickoff, home, away))
    return out


def _fd_name(league: League, espn_name: str) -> str:
    return TEAM_NAME_MAPS.get(league.code, {}).get(espn_name, espn_name)


def fetch_espn_event_ids(league: League, date: str) -> dict[tuple[str, str], str]:
    """{(home, away): espn_event_id} for `league` on `date` (YYYYMMDD),
    with team names translated to football-data.co.uk names."""
    data = _get(f"{ESPN_BASE}/{league.espn_slug}/scoreboard", dates=date)
    out: dict[tuple[str, str], str] = {}
    for ev in data.get("events", []):
        competitors = ((ev.get("competitions") or [{}])[0]).get("competitors") or []
        names = {
            c.get("homeAway"): _fd_name(league, (c.get("team") or {}).get("displayName", ""))
            for c in competitors
        }
        if "home" in names and "away" in names:
            out[(names["home"], names["away"])] = str(ev["id"])
    return out


def fetch_match_summary(league: League, event_id: str) -> dict:
    return _get(f"{ESPN_BASE}/{league.espn_slug}/summary", event=event_id)


# ---------------------------------------------------------------------------
# Row builders.
# ---------------------------------------------------------------------------


def _minute_from_clock(clock: dict) -> tuple[int, int | None]:
    """Parse ESPN's clock.displayValue ("36'", "45'+1'", "90'+3'") to
    (minute, added_minute). Falls back to clock.value seconds / 60."""
    display = ((clock or {}).get("displayValue") or "").strip()
    m = re.match(r"(\d+)'\s*(?:\+(\d+)'?)?", display)
    if m:
        return int(m.group(1)), int(m.group(2)) if m.group(2) else None
    v = (clock or {}).get("value")
    return (int(v // 60), None) if v is not None else (0, None)


def build_event_rows(match: DBMatch, summary: dict) -> list[tuple]:
    rows = []
    now = utc_now()
    for ev in summary.get("keyEvents", []):
        mapped = classify_keyevent(ev)
        if not mapped:
            continue
        text = (ev.get("text") or "").strip() or None
        # ESPN emits each delay start/end once per team; only one copy
        # carries text.
        if mapped == "VAR" and text is None:
            continue
        minute, added = _minute_from_clock(ev.get("clock") or {})
        team = _fd_name(match.league, (ev.get("team") or {}).get("displayName", ""))
        side = {match.home: "HOME", match.away: "AWAY"}.get(team)
        rows.append((
            det_id("event", f"espn|{match.id}|{ev.get('id', '')}|{mapped}|{minute}"),
            match.id, PROVIDER, str(ev.get("id") or ""), mapped, side,
            minute if minute > 0 else None, added, text,
            match.kickoff + timedelta(minutes=minute + (added or 0)), now,
        ))
    return rows


def _team_aliases(summary: dict) -> dict[str, str]:
    """Lower-cased team name variants -> HOME/AWAY, taken from the
    summary header. Commentary spells "&" as "and", so both forms are
    registered."""
    aliases: dict[str, str] = {}
    comp = ((summary.get("header") or {}).get("competitions") or [{}])[0]
    for competitor in comp.get("competitors", []):
        side = {"home": "HOME", "away": "AWAY"}.get(competitor.get("homeAway"))
        team = competitor.get("team") or {}
        if not side:
            continue
        for key in ("displayName", "shortDisplayName", "name", "location"):
            name = (team.get(key) or "").strip().lower()
            if name:
                aliases[name] = side
                aliases[name.replace("&", "and")] = side
    return aliases


_PREFIX_TEAM_RE = re.compile(r"^(?:Offside|Substitution|Corner|Own Goal by [^,]+), ([^.]+)\.")


def _commentary_side(text: str, kind: str, aliases: dict[str, str]) -> Optional[str]:
    """Team a commentary line refers to: a parenthesised team name
    ("... (Liverpool) ...") or a "Kind, Team." prefix."""
    side: Optional[str] = None
    for paren in re.findall(r"\(([^)]+)\)", text):
        side = aliases.get(paren.strip().lower())
        if side:
            break
    if not side:
        m = _PREFIX_TEAM_RE.match(text)
        if m:
            side = aliases.get(m.group(1).strip().lower())
    # Own goals are credited to the team that benefits, matching the
    # keyEvents attribution used for match_events.
    if side and kind == "OWN_GOAL":
        side = "AWAY" if side == "HOME" else "HOME"
    return side


def build_commentary_rows(match: DBMatch, summary: dict) -> list[tuple]:
    rows = []
    aliases = _team_aliases(summary)
    for i, c in enumerate(summary.get("commentary", [])):
        text = (c.get("text") or "").strip()
        if not text:
            continue
        minute, added = _minute_from_clock(c.get("time") or {})
        kind = classify_commentary(text)
        rows.append((
            match.id, int(c.get("sequence", i)), minute if minute > 0 else None, added,
            ((c.get("play") or {}).get("period") or {}).get("number"), kind,
            _commentary_side(text, kind, aliases), text, PROVIDER,
        ))
    return rows


# ---------------------------------------------------------------------------
# Upsert.
# ---------------------------------------------------------------------------

EVENT_COLS = [
    "id", "match_id", "provider", "external_id", "event_type", "team_side",
    "minute", "added_minute", "description", "event_time", "source_timestamp",
]
EVENT_UPDATE = EVENT_COLS[4:]

COMMENTARY_COLS = [
    "match_id", "sequence", "minute", "added_minute", "period",
    "kind", "team_side", "text", "source",
]
COMMENTARY_UPDATE = COMMENTARY_COLS[2:]


# ---------------------------------------------------------------------------
# Entry point.
# ---------------------------------------------------------------------------


def main() -> int:
    load_local_env()
    started = utc_now()
    t0 = time.time()
    conn = connect()
    try:
        with conn.cursor() as cur:
            matches = load_matches_to_backfill(cur)
            print(f"[load] {len(matches)} matches to (re)fetch")

            # Pass 1: resolve ESPN event ids, one scoreboard call per
            # (league, date).
            by_day: dict[tuple[str, str], list[DBMatch]] = {}
            for m in matches:
                by_day.setdefault((m.league.code, m.kickoff.strftime("%Y%m%d")), []).append(m)
            espn_id: dict[str, str] = {}
            unresolved: list[str] = []
            for (code, date), day_matches in sorted(by_day.items()):
                try:
                    ids = fetch_espn_event_ids(LEAGUE_BY_CODE[code], date)
                except requests.RequestException as e:
                    print(f"[scoreboard {code} {date}] failed: {e}")
                    unresolved += [m.id for m in day_matches]
                    continue
                for m in day_matches:
                    if (m.home, m.away) in ids:
                        espn_id[m.id] = ids[(m.home, m.away)]
                    else:
                        unresolved.append(m.id)
                        print(f"[scoreboard {code} {date}] no ESPN event for "
                              f"{m.home} vs {m.away} (have {sorted(ids)[:3]}...)")
                time.sleep(POLITE_DELAY_S)
            print(f"[resolve] matched {len(espn_id)}/{len(matches)} to ESPN event ids")

            # Pass 2: one summary call per match.
            event_rows: list[tuple] = []
            commentary_rows: list[tuple] = []
            touched: list[str] = []
            for m in matches:
                if m.id not in espn_id:
                    continue
                try:
                    summary = fetch_match_summary(m.league, espn_id[m.id])
                except requests.RequestException as e:
                    print(f"[summary {espn_id[m.id]}] failed: {e}")
                    continue
                event_rows += build_event_rows(m, summary)
                commentary_rows += build_commentary_rows(m, summary)
                touched.append(m.id)
                time.sleep(POLITE_DELAY_S)

            # Replace the ESPN events of every touched match so stale rows
            # don't linger if ESPN re-classifies something.
            for chunk in chunked(touched, 500):
                cur.execute(
                    f"DELETE FROM match_events WHERE provider = %s "
                    f"AND match_id IN ({','.join(['%s'] * len(chunk))})",
                    (PROVIDER, *chunk),
                )
            events_written = upsert_many(cur, "match_events", EVENT_COLS, EVENT_UPDATE, event_rows)
            commentary_written = upsert_many(
                cur, "match_commentary", COMMENTARY_COLS, COMMENTARY_UPDATE, commentary_rows
            )
            print(f"[write] events={events_written} commentary={commentary_written} "
                  f"matches={len(touched)}")

            missed = len(matches) - len(touched)
            status = "SUCCEEDED" if not missed else "PARTIAL" if touched else "FAILED"
            record_run(
                cur, provider=PROVIDER, job_name=JOB_NAME, started_at=started,
                status=status, records_in=len(matches),
                records_out=events_written + commentary_written,
                error=f"{missed} matches not fetched" if missed else None,
                details={"lookback_days": BACKFILL_LOOKBACK_DAYS,
                         "matches_touched": len(touched), "unresolved": len(unresolved)},
            )
    finally:
        conn.close()
    print(f"[done] {status} in {time.time() - t0:.1f}s")
    return 0 if status == "SUCCEEDED" else 1


if __name__ == "__main__":
    sys.exit(main())
