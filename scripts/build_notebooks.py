"""Regenerate the SingleStore Portal notebooks from the ingest scripts.

Portal notebooks can't import files from this repo, so each notebook
inlines `ingest_common.py` followed by its script body. Run after editing
any ingest script:

    python3 scripts/build_notebooks.py          # rewrite notebooks/
    python3 scripts/build_notebooks.py --check  # exit 1 if out of date
"""

from __future__ import annotations

import ast
import json
import sys
from dataclasses import dataclass
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
COMMON = ROOT / "scripts" / "ingest_common.py"


@dataclass(frozen=True)
class Notebook:
    path: str
    script: str
    packages: tuple[str, ...]
    intro: str


NOTEBOOKS = (
    Notebook(
        path="notebooks/season_ingest.ipynb",
        script="scripts/ingest_season.py",
        packages=("pandas", "requests", "pymysql"),
        intro="""\
# Season ingest — football-data.co.uk (hourly)

Downloads the current season's CSV for every league in `LEAGUES` (Premier League, La Liga, Serie A, Bundesliga, Primeira Liga) and idempotently upserts competitions, teams (with chronological Elo), matches, the model's retrospective probability timelines, market-odds benchmarks and match stats.

Timelines use the real goal / red-card minutes from `events_espn.ipynb` when they agree with the CSV score, so a new match picks up its event-driven timeline on the run after the ESPN notebook has scraped it.

**Schedule** in SingleStore Portal: Jobs → Create Job → this notebook, cron `0 * * * *`. The connection comes from the workspace's `SINGLESTOREDB_URL`.

*Generated from `scripts/ingest_season.py` + `scripts/ingest_common.py` by `scripts/build_notebooks.py` — edit those, not this notebook.*""",
    ),
    Notebook(
        path="notebooks/events_espn.ipynb",
        script="scripts/ingest_events_espn.py",
        packages=("requests", "pymysql"),
        intro="""\
# Match events + minute-by-minute commentary — ESPN (hourly)

Scrapes ESPN's public JSON (scoreboard per league per date + `summary?event=` per match) for every league in `LEAGUES` and upserts:

- `match_events` — goals, own goals, penalties, cards, substitutions, VAR / injury delays with exact minute + stoppage time
- `match_commentary` — every commentary line, classified by `kind` and `team_side`

Run **after** `season_ingest.ipynb` (matches must exist). Each run only re-fetches matches with no events yet or that kicked off in the last 7 days, with a 300 ms delay between requests.

**Schedule** in SingleStore Portal: Jobs → Create Job → this notebook, cron `10 * * * *`.

*Generated from `scripts/ingest_events_espn.py` + `scripts/ingest_common.py` by `scripts/build_notebooks.py` — edit those, not this notebook.*""",
    ),
)


def _strip(source: str, drop) -> str:
    """Remove the top-level statements matched by `drop` (whole lines)."""
    lines = source.splitlines(keepends=True)
    for node in reversed(ast.parse(source).body):
        if drop(node):
            del lines[node.lineno - 1 : node.end_lineno]
    return "".join(lines).strip("\n")


def _is_future_import(node: ast.stmt) -> bool:
    return isinstance(node, ast.ImportFrom) and node.module == "__future__"


def _is_docstring(node: ast.stmt) -> bool:
    return isinstance(node, ast.Expr) and isinstance(node.value, ast.Constant) \
        and isinstance(node.value.value, str)


def _is_main_guard(node: ast.stmt) -> bool:
    return isinstance(node, ast.If) and "__main__" in ast.unparse(node.test)


def build_source(script: Path) -> str:
    common = _strip(COMMON.read_text(), lambda n: _is_future_import(n) or _is_docstring(n))
    body = _strip(
        script.read_text(),
        lambda n: _is_future_import(n) or _is_main_guard(n)
        or (isinstance(n, ast.ImportFrom) and n.module == "ingest_common"),
    )
    source = f"from __future__ import annotations\n\n{common}\n\n\n{body}\n"
    compile(source, str(script), "exec")  # fail fast on a bad splice
    return source


def _cell(stem: str, i: int, kind: str, text: str) -> dict:
    cell = {
        "cell_type": kind,
        "id": f"{stem}-{i}",
        "metadata": {},
        "source": text.splitlines(keepends=True),
    }
    if kind == "code":
        cell.update(execution_count=None, outputs=[])
    return cell


def render(nb: Notebook) -> str:
    stem = Path(nb.path).stem.replace("_", "-")
    deps = (
        "import importlib, subprocess, sys\n"
        f"for pkg in {nb.packages!r}:\n"
        "    try:\n"
        "        importlib.import_module(pkg)\n"
        "    except ImportError:\n"
        '        subprocess.check_call([sys.executable, "-m", "pip", "install", "--quiet", pkg])\n'
        'print("deps ok")'
    )
    cells = [
        ("markdown", nb.intro),
        ("markdown", "## 1 – Dependencies"),
        ("code", deps),
        ("markdown", "## 2 – Ingest code"),
        ("code", build_source(ROOT / nb.script)),
        ("markdown", "## 3 – Run"),
        ("code", 'rc = main()\nprint("exit code", rc)'),
    ]
    doc = {
        "cells": [_cell(stem, i, kind, text) for i, (kind, text) in enumerate(cells)],
        "metadata": {
            "kernelspec": {"display_name": "Python 3", "language": "python", "name": "python3"},
            "language_info": {"name": "python", "version": "3.12"},
        },
        "nbformat": 4,
        "nbformat_minor": 5,
    }
    return json.dumps(doc, indent=1) + "\n"


def main() -> int:
    check = "--check" in sys.argv[1:]
    stale = []
    for nb in NOTEBOOKS:
        path = ROOT / nb.path
        rendered = render(nb)
        if path.exists() and path.read_text() == rendered:
            continue
        stale.append(nb.path)
        if not check:
            path.write_text(rendered)
            print(f"wrote {nb.path}")
    if check and stale:
        print("out of date (run scripts/build_notebooks.py):", ", ".join(stale))
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
