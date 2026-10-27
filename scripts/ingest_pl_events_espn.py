"""Hourly ingest of per-event timings + minute-by-minute commentary
from ESPN's public JSON endpoint for the current Premier League season.

Source: https://site.api.espn.com/apis/site/v2/sports/soccer/eng.1/
This is the same JSON ESPN's own public match pages consume. No auth,
no rate limit header, no anti-scraping header. See the module docstring
in `scripts/ingest_pl_season.py` for the project-level data-source
rationale.

**Important** — this script scrapes ESPN. The project constraint
"Do not scrape sites without verifying that the method is permitted"
was explicitly waived by the project owner for this script
(2026-10-27) because free + permitted sources (football-data.co.uk,
football-data.org, openfootball) do not expose per-event minutes or
play-by-play commentary. ESPN was chosen over fotmob / SofaScore /
Flashscore because its endpoint is served without anti-bot friction and
is widely used by open-source analytics libraries, making it the
lowest-risk option. This script sends a reasonable User-Agent and
inserts a 300 ms sleep between calls to be polite.

What it writes per run:
  * match_events: one row per keyEvent (goal, yellow/red card,
    substitution, VAR / delay). Replaces any existing ESPN-sourced
    rows for the match first so re-runs don't duplicate.
  * match_commentary: one row per commentary line. Keyed on
    (match_id, sequence) so re-runs upsert.

Typical PL match yields ~15-20 keyEvents and ~100-150 commentary lines.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import sys
import time
import uuid
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import Optional

import pymysql
import requests


# ---------------------------------------------------------------------------
# Config.
# ---------------------------------------------------------------------------

ESPN_SCOREBOARD = "https://site.api.espn.com/apis/site/v2/sports/soccer/eng.1/scoreboard"
ESPN_SUMMARY = "https://site.api.espn.com/apis/site/v2/sports/soccer/eng.1/summary"
POLITE_DELAY_S = 0.3  # between HTTP calls to ESPN
HTTP_HEADERS = {
    "User-Agent": "SingleScore/0.1 (+github.com/tiagoacastro/singlescore)",
    "Accept-Encoding": "gzip",
    "Accept": "application/json",
}
PROVIDER = "espn"
SOURCE_TAG = "espn"

# Only re-fetch events for matches whose kickoff was within this many
# days of now. Older matches are already backfilled and ESPN's data is
# effectively immutable once a match is final, so we save ~70+ requests
# per hourly re-run.
BACKFILL_LOOKBACK_DAYS = 7

# ESPN team names -> football-data.co.uk team names (what the main
# ingest script uses as the match-ID input). All 20 current PL teams.
TEAM_NAME_MAP: dict[str, str] = {
    "AFC Bournemouth": "Bournemouth",
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
}

# ESPN keyEvent types -> match_events.event_type values that the
# frontend MatchEventList already knows how to render.
#
# Note: ESPN uses `goal`, `goal---volley`, `goal---header`,
# `goal---tap-in`, `goal---chip`, `goal---bicycle`, etc. as separate
# types. Rather than enumerate every subtype we treat any event whose
# `scoringPlay` flag is True (and isn't an own-goal / penalty-missed)
# as a GOAL. The map below is the overrides for everything else.
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
    """Map an ESPN keyEvent to one of our match_events.event_type
    values, or None to skip (kickoff / halftime / etc.)."""
    espn_type = (ev.get("type") or {}).get("type", "")
    if espn_type in EVENT_TYPE_MAP:
        return EVENT_TYPE_MAP[espn_type]
    # Any other scoringPlay = GOAL (covers goal---volley, goal---header,
    # goal---tap-in, etc.).
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
    (re.compile(r"First Half begins", re.I), "PERIOD_START"),
    (re.compile(r"Second Half begins", re.I), "PERIOD_START"),
    (re.compile(r"^Half Time|First Half ends", re.I), "PERIOD_END"),
    (re.compile(r"ends,|Match ends", re.I), "PERIOD_END"),
    (re.compile(r"Lineups are announced", re.I), "LINEUP"),
]


def classify_commentary(text: str) -> str:
    for pat, kind in COMMENTARY_PATTERNS:
        if pat.search(text):
            return kind
    return "COMMENTARY"


# ---------------------------------------------------------------------------
# Shared helpers (duplicated from ingest_pl_season.py so this script
# stays self-contained).
# ---------------------------------------------------------------------------


def det_id(prefix: str, key: str) -> str:
    h = hashlib.sha1(f"{prefix}|{key}".encode("utf-8")).digest()[:16]
    return str(uuid.UUID(bytes=h))


def connect():
    url = os.environ.get("SINGLESTOREDB_URL") or os.environ.get("SINGLESTORE_URL")
    if url:
        from urllib.parse import urlparse

        p = urlparse(url)
        return pymysql.connect(
            host=p.hostname, port=p.port or 3306,
            user=p.username, password=p.password or "",
            database=(p.path or "/").lstrip("/") or "singlescore",
            autocommit=True, ssl={"ssl": True},
            charset="utf8mb4", use_unicode=True,
        )
    return pymysql.connect(
        host=os.environ["SINGLESTORE_HOST"],
        port=int(os.environ.get("SINGLESTORE_PORT", "3306")),
        user=os.environ["SINGLESTORE_USER"],
        password=os.environ["SINGLESTORE_PASSWORD"],
        database=os.environ.get("SINGLESTORE_DATABASE", "singlescore"),
        autocommit=True, ssl={"ssl": True},
        charset="utf8mb4", use_unicode=True,
    )


# ---------------------------------------------------------------------------
# ESPN scraping.
# ---------------------------------------------------------------------------


def _get(url: str, **params) -> dict:
    r = requests.get(url, params=params, headers=HTTP_HEADERS, timeout=20)
    r.raise_for_status()
    return r.json()


@dataclass
class DBMatch:
    id: str
    kickoff: datetime
    home: str
    away: str


def load_matches_to_backfill(cur) -> list[DBMatch]:
    cutoff = datetime.now(timezone.utc).replace(tzinfo=None) - timedelta(
        days=BACKFILL_LOOKBACK_DAYS
    )
    # Pull every finished match whose kickoff is within the lookback
    # window OR which has zero events already (first-ever backfill).
    cur.execute(
        """
        SELECT m.id, m.kickoff_at, th.name, ta.name,
               (SELECT COUNT(*) FROM match_events e WHERE e.match_id = m.id) AS ec
        FROM matches m
        JOIN teams th ON th.id = m.home_team_id
        JOIN teams ta ON ta.id = m.away_team_id
        WHERE m.status = 'FINISHED'
    """
    )
    out = []
    for mid, k, h, a, ec in cur.fetchall():
        if k >= cutoff or ec == 0:
            out.append(DBMatch(id=mid, kickoff=k, home=h, away=a))
    return out


def fetch_espn_event_ids_for_date(date: str) -> dict[tuple[str, str], str]:
    """Return a {(home_fd, away_fd): espn_event_id} map for all PL
    events on `date` (YYYYMMDD)."""
    data = _get(ESPN_SCOREBOARD, dates=date)
    out: dict[tuple[str, str], str] = {}
    for ev in data.get("events", []):
        comp = (ev.get("competitions") or [{}])[0]
        competitors = comp.get("competitors") or []
        if len(competitors) != 2:
            continue
        home = next((c for c in competitors if c.get("homeAway") == "home"), None)
        away = next((c for c in competitors if c.get("homeAway") == "away"), None)
        if not home or not away:
            continue
        h_espn = (home.get("team") or {}).get("displayName", "")
        a_espn = (away.get("team") or {}).get("displayName", "")
        h_fd = TEAM_NAME_MAP.get(h_espn, h_espn)
        a_fd = TEAM_NAME_MAP.get(a_espn, a_espn)
        out[(h_fd, a_fd)] = str(ev["id"])
    return out


def fetch_match_summary(event_id: str) -> dict:
    return _get(ESPN_SUMMARY, event=event_id)


def _minute_from_clock(clock: dict, period: Optional[int] = None) -> tuple[int, int | None]:
    """Parse ESPN's clock.displayValue ("36'", "45'+1'", "90'+3'") to
    (minute, added_minute). Falls back to clock.value seconds / 60."""
    display = (clock or {}).get("displayValue") or ""
    m = re.match(r"(\d+)'\s*(?:\+(\d+)'?)?", display.strip())
    if m:
        return int(m.group(1)), int(m.group(2)) if m.group(2) else None
    v = (clock or {}).get("value")
    if v is not None:
        return int(v // 60), None
    return 0, None


def _team_side_from_event(ev: dict, home_fd: str, away_fd: str) -> Optional[str]:
    team = ev.get("team") or {}
    name = team.get("displayName", "")
    fd = TEAM_NAME_MAP.get(name, name)
    if fd == home_fd:
        return "HOME"
    if fd == away_fd:
        return "AWAY"
    return None


# ---------------------------------------------------------------------------
# Row builders.
# ---------------------------------------------------------------------------


def build_event_rows(
    match: DBMatch, summary: dict
) -> list[tuple]:
    rows = []
    for ev in summary.get("keyEvents", []):
        mapped = classify_keyevent(ev)
        if not mapped:
            continue  # skip kickoff/halftime/etc. — not real events
        minute, added = _minute_from_clock(
            ev.get("clock") or {}, (ev.get("period") or {}).get("number")
        )
        side = _team_side_from_event(ev, match.home, match.away)
        text = (ev.get("text") or "").strip() or None
        # ESPN emits each delay start/end once per team; only one copy
        # carries text.
        if mapped == "VAR" and text is None:
            continue
        event_id = det_id(
            "event",
            f"espn|{match.id}|{ev.get('id','')}|{mapped}|{minute}",
        )
        event_time = match.kickoff + timedelta(
            minutes=minute, seconds=(added or 0) * 60
        )
        rows.append(
            (
                event_id,
                match.id,
                PROVIDER,
                str(ev.get("id") or ""),
                mapped,
                side,
                minute if minute > 0 else None,
                added,
                text,
                event_time,
                datetime.now(timezone.utc).replace(tzinfo=None),
            )
        )
    return rows


def _team_aliases(summary: dict) -> dict[str, str]:
    """Lower-cased team name variants -> HOME/AWAY, taken from the
    summary header. Commentary spells "&" as "and", so both forms are
    registered."""
    aliases: dict[str, str] = {}
    comps = ((summary.get("header") or {}).get("competitions") or [{}])[0]
    for comp in comps.get("competitors", []):
        side = {"home": "HOME", "away": "AWAY"}.get(comp.get("homeAway"))
        team = comp.get("team") or {}
        if not side:
            continue
        for key in ("displayName", "shortDisplayName", "name", "location"):
            name = (team.get(key) or "").strip().lower()
            if name:
                aliases[name] = side
                aliases[name.replace("&", "and")] = side
    return aliases


_PREFIX_TEAM_RE = re.compile(
    r"^(?:Offside|Substitution|Corner|Own Goal by [^,]+), ([^.]+)\."
)


def _commentary_side(text: str, kind: str, aliases: dict[str, str]) -> Optional[str]:
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


def build_commentary_rows(
    match: DBMatch, summary: dict
) -> list[tuple]:
    rows = []
    aliases = _team_aliases(summary)
    for i, c in enumerate(summary.get("commentary", [])):
        text = (c.get("text") or "").strip()
        if not text:
            continue
        minute, added = _minute_from_clock(c.get("time") or {})
        kind = classify_commentary(text)
        side = _commentary_side(text, kind, aliases)
        rows.append(
            (
                match.id,
                int(c.get("sequence", i)),
                minute if minute > 0 else None,
                added,
                (c.get("period") or {}).get("number"),
                kind,
                side,
                text,
                SOURCE_TAG,
            )
        )
    return rows


# ---------------------------------------------------------------------------
# Upsert.
# ---------------------------------------------------------------------------


def _chunked(seq, size):
    for i in range(0, len(seq), size):
        yield seq[i : i + size]


EVENT_COLS = [
    "id", "match_id", "provider", "external_id", "event_type", "team_side",
    "minute", "added_minute", "description", "event_time", "source_timestamp",
]
EVENT_UPDATE = [
    "event_type", "team_side", "minute", "added_minute",
    "description", "event_time", "source_timestamp",
]

COMMENTARY_COLS = [
    "match_id", "sequence", "minute", "added_minute", "period",
    "kind", "team_side", "text", "source",
]
COMMENTARY_UPDATE = [
    "minute", "added_minute", "period", "kind", "team_side", "text", "source",
]


def upsert_many(cur, table, cols, update_cols, rows, chunk_size=400):
    if not rows:
        return 0
    placeholders = "(" + ",".join(["%s"] * len(cols)) + ")"
    upd = ", ".join(f"{c}=VALUES({c})" for c in update_cols)
    total = 0
    for chunk in _chunked(rows, chunk_size):
        vals = ",".join([placeholders] * len(chunk))
        q = (
            f"INSERT INTO {table} ({', '.join(cols)}) VALUES {vals} "
            f"ON DUPLICATE KEY UPDATE {upd}"
        )
        flat = []
        for r in chunk:
            flat.extend(r)
        cur.execute(q, flat)
        total += len(chunk)
    return total


# ---------------------------------------------------------------------------
# Entry point.
# ---------------------------------------------------------------------------


def main() -> int:
    # Local .env for script runs outside the Portal.
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
    conn = connect()
    try:
        with conn.cursor() as cur:
            matches = load_matches_to_backfill(cur)
            print(f"[load] {len(matches)} matches to (re)fetch")

            # Build date -> list[DBMatch] so we group scoreboard calls.
            by_date: dict[str, list[DBMatch]] = {}
            for m in matches:
                by_date.setdefault(m.kickoff.strftime("%Y%m%d"), []).append(m)

            # Pass 1: get ESPN event IDs per date (one scoreboard call
            # per date).
            espn_id_by_match: dict[str, str] = {}
            for date, mlist in sorted(by_date.items()):
                try:
                    id_map = fetch_espn_event_ids_for_date(date)
                except Exception as e:
                    print(f"[scoreboard {date}] failed: {e}")
                    continue
                for m in mlist:
                    espn_id = id_map.get((m.home, m.away))
                    if espn_id:
                        espn_id_by_match[m.id] = espn_id
                    else:
                        print(
                            f"[scoreboard {date}] no match for {m.home} vs {m.away} "
                            f"(available: {list(id_map.keys())[:3]}...)"
                        )
                time.sleep(POLITE_DELAY_S)
            print(f"[resolve] matched {len(espn_id_by_match)}/{len(matches)} to ESPN event IDs")

            # Pass 2: per match, fetch summary + extract rows.
            all_event_rows: list[tuple] = []
            all_commentary_rows: list[tuple] = []
            match_ids_touched: set[str] = set()
            for m in matches:
                espn_id = espn_id_by_match.get(m.id)
                if not espn_id:
                    continue
                try:
                    summary = fetch_match_summary(espn_id)
                except Exception as e:
                    print(f"[summary {espn_id}] failed: {e}")
                    continue
                erows = build_event_rows(m, summary)
                crows = build_commentary_rows(m, summary)
                all_event_rows.extend(erows)
                all_commentary_rows.extend(crows)
                match_ids_touched.add(m.id)
                time.sleep(POLITE_DELAY_S)

            # Replace any existing ESPN-sourced events for touched
            # matches so stale rows don't linger if ESPN re-classifies
            # something. Non-ESPN providers (if any) are left alone.
            if match_ids_touched:
                placeholders = ",".join(["%s"] * len(match_ids_touched))
                cur.execute(
                    f"DELETE FROM match_events WHERE provider = %s AND match_id IN ({placeholders})",
                    (PROVIDER, *match_ids_touched),
                )

            events_written = upsert_many(
                cur, "match_events", EVENT_COLS, EVENT_UPDATE, all_event_rows
            )
            commentary_written = upsert_many(
                cur, "match_commentary", COMMENTARY_COLS, COMMENTARY_UPDATE,
                all_commentary_rows,
            )

            print(
                f"[write] events={events_written} "
                f"commentary={commentary_written} "
                f"matches_touched={len(match_ids_touched)}"
            )

            # Record run.
            run_id = det_id(
                "espn-run",
                f"{PROVIDER}|{datetime.now(timezone.utc):%Y-%m-%dT%H}",
            )
            cur.execute(
                """INSERT INTO data_ingestion_runs
                   (id, provider, job_name, status, started_at, finished_at,
                    records_in, records_out, error_message, details_json)
                   VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)
                   ON DUPLICATE KEY UPDATE status=VALUES(status),
                     finished_at=VALUES(finished_at), records_in=VALUES(records_in),
                     records_out=VALUES(records_out), error_message=VALUES(error_message),
                     details_json=VALUES(details_json)""",
                (
                    run_id, PROVIDER, "espn-events", "SUCCEEDED",
                    datetime.fromtimestamp(t0, tz=timezone.utc).replace(tzinfo=None),
                    datetime.now(timezone.utc).replace(tzinfo=None),
                    len(matches),
                    events_written + commentary_written,
                    None,
                    json.dumps({
                        "lookback_days": BACKFILL_LOOKBACK_DAYS,
                        "matches_touched": len(match_ids_touched),
                    }),
                ),
            )
    finally:
        conn.close()
    print(f"[done] in {time.time() - t0:.1f}s")
    return 0


if __name__ == "__main__":
    sys.exit(main())
