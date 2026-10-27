-- SingleScore schema v1
--
-- Target: SingleStoreDB 10.x (reports MySQL 5.7 wire).
--
-- Design notes:
--   * Reference tables for small, read-mostly lookups (competitions, teams)
--     so we can keep useful UNIQUE indexes on provider/external_id.
--   * Rowstore sharded tables for per-match data. SHARD KEY defaults to
--     the primary key when not specified; we'll revisit colocation when
--     scaling to multiple leaf nodes.
--   * All timestamps are UTC. Use DATETIME(6) for microsecond resolution.
--   * Probability invariants (p_home + p_draw + p_away ~= 1.0, each in
--     [0,1]) are enforced in Go. CHECK constraints are intentionally
--     omitted to keep this migration portable across SingleStore versions.

CREATE DATABASE IF NOT EXISTS singlescore;
USE singlescore;

-- ------------------------------------------------------------
-- Reference lookups
-- ------------------------------------------------------------

-- NOTE: reference tables default to columnstore on this cluster, and
-- columnstore reference tables don't allow multiple multi-column UNIQUE
-- indexes. We keep (provider, external_id) as a non-unique lookup KEY and
-- enforce dedup in the Go repository layer via SELECT-then-INSERT.

CREATE REFERENCE TABLE IF NOT EXISTS competitions (
    id          CHAR(36) NOT NULL,
    provider    VARCHAR(50) NOT NULL,
    external_id VARCHAR(64),
    name        VARCHAR(200) NOT NULL,
    country     VARCHAR(100),
    season      VARCHAR(20),
    logo_url    VARCHAR(500),
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_competitions_provider_ext (provider, external_id),
    KEY idx_competitions_name (name)
);

CREATE REFERENCE TABLE IF NOT EXISTS teams (
    id          CHAR(36) NOT NULL,
    provider    VARCHAR(50) NOT NULL,
    external_id VARCHAR(64),
    name        VARCHAR(200) NOT NULL,
    short_name  VARCHAR(50),
    country     VARCHAR(100),
    logo_url    VARCHAR(500),
    elo_rating  DOUBLE NOT NULL DEFAULT 1500,
    created_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_teams_provider_ext (provider, external_id),
    KEY idx_teams_name (name)
);

-- ------------------------------------------------------------
-- Matches
-- ------------------------------------------------------------

CREATE ROWSTORE TABLE IF NOT EXISTS matches (
    id                CHAR(36) NOT NULL,
    provider          VARCHAR(50) NOT NULL,
    external_id       VARCHAR(64),
    competition_id    CHAR(36) NOT NULL,
    home_team_id      CHAR(36) NOT NULL,
    away_team_id      CHAR(36) NOT NULL,
    kickoff_at        DATETIME(6) NOT NULL,
    status            ENUM('SCHEDULED','LIVE','HALFTIME','FINISHED','POSTPONED','CANCELLED') NOT NULL DEFAULT 'SCHEDULED',
    minute            INT,
    home_score        INT NOT NULL DEFAULT 0,
    away_score        INT NOT NULL DEFAULT 0,
    home_red_cards    INT NOT NULL DEFAULT 0,
    away_red_cards    INT NOT NULL DEFAULT 0,
    final_result      ENUM('HOME','DRAW','AWAY'),
    venue             VARCHAR(200),
    provenance        ENUM('DEMO','HISTORICAL','REPLAY','LIVE') NOT NULL DEFAULT 'DEMO',
    source_updated_at DATETIME(6),
    created_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at        DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_matches_kickoff (kickoff_at),
    KEY idx_matches_status_time (status, kickoff_at),
    KEY idx_matches_competition_time (competition_id, kickoff_at),
    KEY idx_matches_provider_ext (provider, external_id)
);

-- ------------------------------------------------------------
-- Match events
-- ------------------------------------------------------------

CREATE ROWSTORE TABLE IF NOT EXISTS match_events (
    id               CHAR(36) NOT NULL,
    match_id         CHAR(36) NOT NULL,
    provider         VARCHAR(50) NOT NULL,
    external_id      VARCHAR(64),
    event_type       VARCHAR(40) NOT NULL,
    team_side        ENUM('HOME','AWAY','NEUTRAL'),
    minute           INT,
    added_minute     INT,
    description      TEXT,
    event_time       DATETIME(6) NOT NULL,
    source_timestamp DATETIME(6),
    created_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_match_events_match_time (match_id, event_time),
    KEY idx_match_events_provider_ext (provider, external_id)
);

-- ------------------------------------------------------------
-- Model predictions (ours; append-only snapshots)
-- ------------------------------------------------------------

CREATE ROWSTORE TABLE IF NOT EXISTS model_predictions (
    id                   CHAR(36) NOT NULL,
    match_id             CHAR(36) NOT NULL,
    model_name           VARCHAR(100) NOT NULL,
    model_version        VARCHAR(50) NOT NULL,
    forecast_at          DATETIME(6) NOT NULL,
    match_minute         INT,
    home_score_at_fc     INT NOT NULL DEFAULT 0,
    away_score_at_fc     INT NOT NULL DEFAULT 0,
    home_red_cards_fc    INT NOT NULL DEFAULT 0,
    away_red_cards_fc    INT NOT NULL DEFAULT 0,
    p_home               DOUBLE NOT NULL,
    p_draw               DOUBLE NOT NULL,
    p_away               DOUBLE NOT NULL,
    features_json        JSON,
    provenance           ENUM('DEMO','HISTORICAL','REPLAY','LIVE') NOT NULL DEFAULT 'DEMO',
    source_timestamp     DATETIME(6),
    created_at           DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_model_pred_match_time (match_id, forecast_at),
    KEY idx_model_pred_model (model_name, model_version, forecast_at)
);

-- ------------------------------------------------------------
-- Benchmark predictions (external providers; append-only snapshots)
-- ------------------------------------------------------------

CREATE ROWSTORE TABLE IF NOT EXISTS benchmark_predictions (
    id                   CHAR(36) NOT NULL,
    match_id             CHAR(36) NOT NULL,
    provider             VARCHAR(100) NOT NULL,
    provider_version     VARCHAR(50),
    source_type          ENUM('ALGORITHM','MARKET_ODDS','MANUAL') NOT NULL DEFAULT 'ALGORITHM',
    horizon              ENUM('PRE_MATCH','LIVE') NOT NULL DEFAULT 'PRE_MATCH',
    forecast_at          DATETIME(6) NOT NULL,
    source_timestamp     DATETIME(6),
    match_minute         INT,
    match_status         VARCHAR(40),
    home_score_at_fc     INT NOT NULL DEFAULT 0,
    away_score_at_fc     INT NOT NULL DEFAULT 0,
    home_red_cards_fc    INT NOT NULL DEFAULT 0,
    away_red_cards_fc    INT NOT NULL DEFAULT 0,
    p_home               DOUBLE NOT NULL,
    p_draw               DOUBLE NOT NULL,
    p_away               DOUBLE NOT NULL,
    raw_odds_json        JSON,
    overround            DOUBLE,
    provenance           ENUM('DEMO','HISTORICAL','REPLAY','LIVE') NOT NULL DEFAULT 'DEMO',
    created_at           DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_bench_pred_match_time (match_id, forecast_at),
    KEY idx_bench_pred_provider_time (provider, forecast_at)
);

-- ------------------------------------------------------------
-- Prediction evaluations (one row per evaluated forecast)
-- ------------------------------------------------------------

CREATE ROWSTORE TABLE IF NOT EXISTS prediction_evaluations (
    id              CHAR(36) NOT NULL,
    subject_type    ENUM('MODEL','BENCHMARK') NOT NULL,
    subject_id      CHAR(36) NOT NULL,
    match_id        CHAR(36) NOT NULL,
    actual_result   ENUM('HOME','DRAW','AWAY') NOT NULL,
    brier           DOUBLE NOT NULL,
    log_loss        DOUBLE NOT NULL,
    predicted_class ENUM('HOME','DRAW','AWAY') NOT NULL,
    correct         TINYINT(1) NOT NULL,
    forecast_minute INT,
    evaluated_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    KEY idx_pred_eval_subject (subject_type, subject_id),
    KEY idx_pred_eval_match (match_id),
    KEY idx_pred_eval_minute (forecast_minute, subject_type)
);

-- ------------------------------------------------------------
-- Data ingestion runs
-- ------------------------------------------------------------

CREATE ROWSTORE TABLE IF NOT EXISTS data_ingestion_runs (
    id            CHAR(36) NOT NULL,
    provider      VARCHAR(100) NOT NULL,
    job_name      VARCHAR(100) NOT NULL,
    status        ENUM('STARTED','SUCCEEDED','FAILED','PARTIAL') NOT NULL,
    started_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    finished_at   DATETIME(6),
    records_in    INT NOT NULL DEFAULT 0,
    records_out   INT NOT NULL DEFAULT 0,
    error_message TEXT,
    details_json  JSON,
    PRIMARY KEY (id),
    KEY idx_ingest_provider_time (provider, started_at)
);

-- ------------------------------------------------------------
-- Schema version tracking
-- ------------------------------------------------------------

CREATE REFERENCE TABLE IF NOT EXISTS schema_migrations (
    version     INT NOT NULL,
    name        VARCHAR(200) NOT NULL,
    applied_at  DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (version)
);

INSERT INTO schema_migrations (version, name)
VALUES (1, '0001_init')
ON DUPLICATE KEY UPDATE applied_at = applied_at;
