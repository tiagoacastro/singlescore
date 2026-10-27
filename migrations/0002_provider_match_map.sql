USE singlescore;

-- 0002_provider_match_map.sql
--
-- Maps our internal match ids to secondary providers' fixture ids,
-- so a per-match event lookup can go straight to the right endpoint
-- without an intermediate "find the fixture" API call.
--
-- Specifically used by cmd/ingest-events (api-football.com):
--
--   1. A one-shot --map run calls /v3/fixtures?league=X&season=Y per
--      league (5 calls total for our current coverage) and populates
--      this table by matching on (home team name, away team name,
--      kickoff date).
--
--   2. Per-match event fetches then cost exactly 1 api-football call
--      each: GET /v3/fixtures/events?fixture=<external_fixture_id>.
--
-- Idempotent via (internal_match_id, provider) primary key so re-runs
-- upsert in place as new matches are added.

CREATE ROWSTORE TABLE IF NOT EXISTS provider_match_map (
    internal_match_id    CHAR(36)     NOT NULL,
    provider             VARCHAR(50)  NOT NULL,
    external_fixture_id  VARCHAR(64)  NOT NULL,
    mapped_at            DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (internal_match_id, provider),
    KEY idx_pmm_provider_ext (provider, external_fixture_id)
);
