"""Shared config and helpers for the ingest scripts.

`scripts/build_notebooks.py` inlines this module at the top of each
generated notebook (SingleStore Portal notebooks can't import files from
the repo), so keep it dependency-light and free of side effects.
"""

from __future__ import annotations

import hashlib
import json
import os
import uuid
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Iterable

import pymysql


# ---------------------------------------------------------------------------
# Season + league scope.
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Season:
    name: str  # e.g. "2026/27"
    code: str  # football-data.co.uk path segment, e.g. "2627"


# Seasons loaded into the database, oldest first; the last is current.
SEASONS: tuple[Season, ...] = (Season("2025/26", "2526"), Season("2026/27", "2627"))


@dataclass(frozen=True)
class League:
    code: str  # football-data.co.uk division code, e.g. "E0"
    name: str
    country: str
    espn_slug: str  # ESPN league slug, e.g. "eng.1"

    def competition_external_id(self, season: Season) -> str:
        """`competitions.external_id`, e.g. "E0-2026-27"."""
        return f"{self.code}-{season.name.replace('/', '-')}"


LEAGUES: tuple[League, ...] = (
    League("E0", "Premier League", "England", "eng.1"),
    League("SP1", "La Liga", "Spain", "esp.1"),
    League("I1", "Serie A", "Italy", "ita.1"),
    League("D1", "Bundesliga", "Germany", "ger.1"),
    League("P1", "Primeira Liga", "Portugal", "por.1"),
)


# ---------------------------------------------------------------------------
# Helpers.
# ---------------------------------------------------------------------------


def det_id(prefix: str, key: str) -> str:
    """SHA-1-derived UUID, so re-runs hash to the same primary key and
    upsert in place."""
    h = hashlib.sha1(f"{prefix}|{key}".encode("utf-8")).digest()[:16]
    return str(uuid.UUID(bytes=h))


def utc_now() -> datetime:
    """Naive UTC now, matching the DATETIME(6) columns."""
    return datetime.now(timezone.utc).replace(tzinfo=None)


def load_local_env() -> None:
    """Load the repo's `.env` for local script runs. A no-op inside the
    Portal (which injects `SINGLESTOREDB_URL`) and in notebooks, which
    have no `__file__`."""
    script = globals().get("__file__")
    if "SINGLESTOREDB_URL" in os.environ or not script:
        return
    env_path = Path(script).resolve().parents[1] / ".env"
    if not env_path.exists():
        return
    for line in env_path.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, _, v = line.partition("=")
        os.environ.setdefault(k.strip(), v.strip().strip("'\""))


def connect():
    """Open a SingleStore connection from `SINGLESTOREDB_URL` (set inside
    a Portal notebook attached to a workspace) or, locally, from
    `SINGLESTORE_HOST` / `SINGLESTORE_USER` / ..."""
    url = os.environ.get("SINGLESTOREDB_URL") or os.environ.get("SINGLESTORE_URL")
    if url:
        from urllib.parse import urlparse

        p = urlparse(url)
        params = dict(
            host=p.hostname,
            port=p.port or 3306,
            user=p.username,
            password=p.password or "",
            database=(p.path or "/").lstrip("/") or "singlescore",
        )
    else:
        params = dict(
            host=os.environ["SINGLESTORE_HOST"],
            port=int(os.environ.get("SINGLESTORE_PORT", "3306")),
            user=os.environ["SINGLESTORE_USER"],
            password=os.environ["SINGLESTORE_PASSWORD"],
            database=os.environ.get("SINGLESTORE_DATABASE", "singlescore"),
        )
    return pymysql.connect(
        **params, autocommit=True, ssl={"ssl": True}, charset="utf8mb4", use_unicode=True
    )


def chunked(seq: list, size: int) -> Iterable[list]:
    for i in range(0, len(seq), size):
        yield seq[i : i + size]


def upsert_many(
    cur, table: str, cols: list[str], update_cols: list[str], rows: list[tuple],
    chunk_size: int = 500,
) -> int:
    """Multi-row `INSERT ... ON DUPLICATE KEY UPDATE`, chunked to keep the
    SQL text a sensible size."""
    if not rows:
        return 0
    placeholders = "(" + ",".join(["%s"] * len(cols)) + ")"
    update_clause = ", ".join(f"{c}=VALUES({c})" for c in update_cols)
    for chunk in chunked(rows, chunk_size):
        cur.execute(
            f"INSERT INTO {table} ({', '.join(cols)}) "
            f"VALUES {','.join([placeholders] * len(chunk))} "
            f"ON DUPLICATE KEY UPDATE {update_clause}",
            [v for r in chunk for v in r],
        )
    return len(rows)


def record_run(
    cur, *, provider: str, job_name: str, started_at: datetime, status: str,
    records_in: int, records_out: int, error: str | None, details: dict,
) -> None:
    """Upsert this run into `data_ingestion_runs` (one row per job per
    hour, so hourly re-runs overwrite rather than pile up)."""
    run_id = det_id("ingest-run", f"{provider}|{job_name}|{started_at:%Y-%m-%dT%H}")
    cur.execute(
        """INSERT INTO data_ingestion_runs
           (id, provider, job_name, status, started_at, finished_at,
            records_in, records_out, error_message, details_json)
           VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s)
           ON DUPLICATE KEY UPDATE
             status=VALUES(status), finished_at=VALUES(finished_at),
             records_in=VALUES(records_in), records_out=VALUES(records_out),
             error_message=VALUES(error_message), details_json=VALUES(details_json)""",
        (run_id, provider, job_name, status, started_at, utc_now(),
         records_in, records_out, error, json.dumps(details)),
    )
