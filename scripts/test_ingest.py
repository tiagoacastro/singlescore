"""Offline unit tests for the ingest scripts (no network, no database).

    .venv/bin/python -m unittest scripts/test_ingest.py
"""

from __future__ import annotations

import json
import sys
import unittest
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import build_notebooks  # noqa: E402
import ingest_events_espn as espn  # noqa: E402
import ingest_season as season  # noqa: E402
from ingest_common import LEAGUES  # noqa: E402

# Same table as TestBaseline_MatchesPythonPort in
# backend/internal/prediction/baseline_test.go: (home_elo, away_elo,
# home_score, away_score, minute, home_red, away_red) -> (home, draw, away).
PARITY_GOLDEN = [
    ((1500, 1500, 0, 0, 0, 0, 0), (0.3944749963608933, 0.2576905015450894, 0.34783450209401734)),
    ((1650, 1480, 0, 0, 0, 0, 0), (0.4632505844903906, 0.25106548104672943, 0.28568393446288)),
    ((1500, 1500, 1, 0, 60, 0, 0), (0.7581577910980946, 0.19455434444911848, 0.04728786445278695)),
    ((1480, 1620, 1, 1, 75, 1, 0), (0.11474430524945022, 0.7015156766421249, 0.1837400181084249)),
    ((1550, 1500, 0, 2, 30, 0, 1), (0.046202034223123356, 0.117014163838894, 0.8367838019379826)),
    ((1500, 1500, 2, 1, 90, 0, 0), (1.0, 0.0, 0.0)),
]

KICKOFF = datetime(2026, 10, 25, 14, 0)
E = season.TimelineEvent


def timeline(events, hs_ht=0, as_ht=0, hs_ft=2, as_ft=0):
    """{minute: (home_score, away_score, home_red, away_red, p_home, origin)}."""
    rows = season._make_prediction_rows(
        "m1", KICKOFF, 1500.0, 1500.0, hs_ht, as_ht, hs_ft, as_ft, events
    )
    return {
        (r[5] or 0): (r[6], r[7], r[8], r[9], r[10], json.loads(r[13])["origin"])
        for r in rows
    }


class PredictorTest(unittest.TestCase):
    def test_matches_go_reference(self):
        for args, want in PARITY_GOLDEN:
            for got, exp in zip(season.predict(*args), want):
                self.assertAlmostEqual(got, exp, places=12, msg=args)


class ModelV3Test(unittest.TestCase):
    def test_pre_match_matches_v2(self):
        # Without in-play information the two versions agree.
        self.assertEqual(season.predict_v3(1600, 1500, 0, 0, 0), season.predict(1600, 1500, 0, 0, 0))

    def test_stoppage_time_is_counted(self):
        self.assertEqual(season.minutes_remaining(85), 5 + season.EXPECTED_ADDED_2H)
        self.assertEqual(season.minutes_remaining(45), 45 + season.EXPECTED_ADDED_2H)
        self.assertEqual(season.minutes_remaining(90), 0)
        # A level game at 85' is less settled than the 90-minute clock thought.
        self.assertLess(season.predict_v3(1500, 1500, 0, 0, 85)[1], season.predict(1500, 1500, 0, 0, 85)[1])

    def test_red_card_also_helps_the_opponent(self):
        base = season.predict_v3(1500, 1500, 0, 0, 30)
        carded = season.predict_v3(1500, 1500, 0, 0, 30, home_red=1)
        self.assertGreater(carded[2], base[2])
        self.assertGreater(carded[2], season.predict(1500, 1500, 0, 0, 30, home_red=1)[2])

    def test_season_start_ratings(self):
        prev = {"A": 1600.0, "B": 1500.0, "C": 1400.0}
        start = season.season_start_ratings(prev, {"A", "B", "D"})
        mean = 1500.0
        self.assertAlmostEqual(start["A"], mean + 100 * season.SEASON_CARRYOVER)
        self.assertAlmostEqual(start["B"], mean)
        self.assertAlmostEqual(start["D"], 1400.0)  # promoted: the departed club's rating
        self.assertEqual(season.season_start_ratings({}, {"A"}), {"A": season.INITIAL_ELO})

    def test_match_performance_blends_goals_and_shots(self):
        import pandas as pd
        row = pd.Series({"FTHG": 1, "FTAG": 0, "HS": 10, "HST": 4, "AS": 20, "AST": 10})
        hp, ap = season.match_performance(row)
        self.assertAlmostEqual(hp, 0.5 * 1 + 0.5 * (0.3 * 4 + 0.05 * 6))
        self.assertAlmostEqual(ap, 0.5 * 0 + 0.5 * (0.3 * 10 + 0.05 * 10))
        # No shot columns: plain goals.
        self.assertEqual(season.match_performance(pd.Series({"FTHG": 2, "FTAG": 1})), (2.0, 1.0))


class TimelineTest(unittest.TestCase):
    def test_goals_land_at_their_real_minutes(self):
        t = timeline([E(17, 0, "GOAL", "HOME"), E(21, 0, "GOAL", "HOME")])
        self.assertEqual(t[15][:2], (0, 0))
        self.assertEqual(t[17][:2], (1, 0))
        self.assertEqual(t[17][5], "goal-event")
        self.assertEqual(t[21][:2], (2, 0))
        self.assertGreater(t[17][4], t[15][4])  # the goal lifts the home win chance

    def test_stoppage_time_goal_counts_towards_half_time(self):
        t = timeline([E(45, 3, "GOAL", "AWAY")], hs_ht=0, as_ht=1, hs_ft=0, as_ft=1)
        self.assertEqual(t[40][:2], (0, 0))
        self.assertEqual(t[45][:2], (0, 1))
        self.assertEqual(t[45][5], "half-time")

    def test_red_card_feeds_the_model(self):
        t = timeline([E(32, 0, "RED_CARD", "HOME")], hs_ft=0, as_ft=0)
        self.assertEqual(t[30][2:4], (0, 0))
        self.assertEqual(t[32][2:4], (1, 0))
        self.assertEqual(t[32][5], "red-card-event")
        self.assertLess(t[32][4], t[30][4])  # the carded side's chances drop
        self.assertEqual(t[90][2:4], (1, 0))

    def test_no_event_snapshot_on_grid_minutes(self):
        # A goal at 30' is already reflected in the 30' grid snapshot.
        t = timeline([E(30, 0, "GOAL", "HOME")], hs_ft=1, as_ft=0)
        self.assertEqual(t[30][5], "reconstructed-5min")
        self.assertEqual(t[30][:2], (1, 0))

    def test_inconsistent_events_fall_back_to_ht_ft(self):
        # One goal in the feed but 2-0 in the CSV: don't trust the feed.
        t = timeline([E(17, 0, "GOAL", "HOME")], hs_ht=1, as_ht=0)
        self.assertNotIn(17, t)
        self.assertEqual(t[40][:2], (0, 0))
        self.assertEqual(t[50][:2], (1, 0))

    def test_goalless_match_with_coverage_uses_events(self):
        rows = season._make_prediction_rows("m1", KICKOFF, 1500.0, 1500.0, 0, 0, 0, 0, [])
        self.assertTrue(all(json.loads(r[13])["score_source"] == "events" for r in rows))

    def test_unplayed_fixture_only_gets_pre_match(self):
        rows = season._make_prediction_rows("m1", KICKOFF, 1500.0, 1500.0, 0, 0, None, None)
        self.assertEqual([json.loads(r[13])["origin"] for r in rows], ["pre-match"])


class EspnParsingTest(unittest.TestCase):
    def test_keyevent_classification(self):
        self.assertEqual(espn.classify_keyevent({"type": {"type": "goal---volley"}, "scoringPlay": True}), "GOAL")
        self.assertEqual(espn.classify_keyevent({"type": {"type": "own-goal"}, "scoringPlay": True}), "OWN_GOAL")
        self.assertEqual(espn.classify_keyevent({"type": {"type": "second-yellow"}}), "RED_CARD")
        self.assertIsNone(espn.classify_keyevent({"type": {"type": "kickoff"}}))

    def test_commentary_classification(self):
        self.assertEqual(espn.classify_commentary("Attempt saved. Isak (Liverpool) ..."), "ATTEMPT_SAVED")
        self.assertEqual(espn.classify_commentary("Second Half ends, Liverpool 2, Brighton 0."), "PERIOD_END")
        self.assertEqual(espn.classify_commentary("Lineups are announced and players are warming up."), "LINEUP")
        self.assertEqual(espn.classify_commentary("Delay in match because of an injury."), "COMMENTARY")

    def test_minute_parsing(self):
        self.assertEqual(espn._minute_from_clock({"displayValue": "36'"}), (36, None))
        self.assertEqual(espn._minute_from_clock({"displayValue": "45'+3'"}), (45, 3))
        self.assertEqual(espn._minute_from_clock({"value": 1260}), (21, None))
        self.assertEqual(espn._minute_from_clock({}), (0, None))

    def test_commentary_side(self):
        aliases = {"liverpool": "HOME", "brighton and hove albion": "AWAY"}
        self.assertEqual(espn._commentary_side("Foul by Gakpo (Liverpool).", "FOUL", aliases), "HOME")
        self.assertEqual(espn._commentary_side("Corner, Brighton and Hove Albion. Conceded by Van Dijk.", "CORNER", aliases), "AWAY")
        # Own goals are credited to the side that benefits.
        self.assertEqual(espn._commentary_side("Own Goal by Groß, Brighton and Hove Albion.", "OWN_GOAL", aliases), "HOME")

    def test_commentary_period_comes_from_the_play(self):
        match = espn.DBMatch("m1", LEAGUES[0], KICKOFF, "Liverpool", "Brighton")
        summary = {"commentary": [{"sequence": 5, "time": {"displayValue": "5'"},
                                   "text": "Delay over. They are ready to continue.",
                                   "play": {"period": {"number": 1}}}]}
        self.assertEqual(espn.build_commentary_rows(match, summary)[0][4], 1)

    def test_every_league_has_a_team_map(self):
        self.assertEqual({league.code for league in LEAGUES}, set(espn.TEAM_NAME_MAPS))
        mapped = [fd for m in espn.TEAM_NAME_MAPS.values() for fd in m.values()]
        self.assertEqual(len(mapped), len(set(mapped)), "a CSV team name is mapped twice")


class CommonTest(unittest.TestCase):
    def test_competition_external_id_is_stable(self):
        # Hashed into competition ids; changing it would orphan rows.
        self.assertEqual(LEAGUES[0].competition_external_id, "E0-2026-27")


class NotebooksTest(unittest.TestCase):
    def test_notebooks_are_up_to_date(self):
        for nb in build_notebooks.NOTEBOOKS:
            path = build_notebooks.ROOT / nb.path
            self.assertEqual(path.read_text(), build_notebooks.render(nb),
                             f"{nb.path} is stale: run scripts/build_notebooks.py")


if __name__ == "__main__":
    unittest.main()
