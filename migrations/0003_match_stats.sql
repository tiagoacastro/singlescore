-- ============================================================
-- 0003_match_stats.sql
--
-- Match-aggregate statistics ingested from the football-data.co.uk
-- CSV (and any future equivalent source). Flashscore-style "Match
-- stats" tiles: shots, shots on target, corners, fouls, cards, xG,
-- plus the referee name.
--
-- Deliberately a separate table rather than more columns on
-- `matches` so the hot path (list / detail resolvers) stays lean;
-- stats are only loaded when the detail page is opened.
--
-- Primary key is `match_id` alone (1:1 with matches). Upsert via
-- `INSERT ... ON DUPLICATE KEY UPDATE` from the hourly notebook.
-- ============================================================

CREATE ROWSTORE TABLE IF NOT EXISTS match_stats (
    match_id         CHAR(36)      NOT NULL,
    home_shots       INT,
    away_shots       INT,
    home_shots_on_t  INT,
    away_shots_on_t  INT,
    home_corners     INT,
    away_corners     INT,
    home_fouls       INT,
    away_fouls       INT,
    home_yellow      INT,
    away_yellow      INT,
    home_red         INT,
    away_red         INT,
    home_xg          DOUBLE,
    away_xg          DOUBLE,
    referee          VARCHAR(200),
    source           VARCHAR(50) NOT NULL,
    updated_at       DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
                                   ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (match_id)
);

INSERT INTO schema_migrations (version, name)
VALUES (3, '0003_match_stats')
ON DUPLICATE KEY UPDATE name = VALUES(name);
