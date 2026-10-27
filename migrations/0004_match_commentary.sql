-- ============================================================
-- 0004_match_commentary.sql
--
-- Minute-by-minute match commentary ingested from ESPN's public
-- summary endpoint. One row per commentary line; a typical PL
-- match has ~100-150 lines (fouls, free kicks, corners, shots,
-- cards, subs, goals, VAR). Keyed on (match_id, sequence) so
-- ingest is idempotent via ON DUPLICATE KEY UPDATE.
--
-- `kind` is derived client-side from the leading phrase of the
-- commentary text ("Goal!" -> GOAL, "Attempt saved" ->
-- ATTEMPT_SAVED, etc.) so the UI can colour and filter rows
-- without re-parsing.
-- ============================================================

CREATE ROWSTORE TABLE IF NOT EXISTS match_commentary (
    match_id      CHAR(36) NOT NULL,
    sequence      INT NOT NULL,
    minute        INT,
    added_minute  INT,
    period        INT,
    kind          VARCHAR(40) NOT NULL,
    team_side     ENUM('HOME','AWAY','NEUTRAL'),
    text          TEXT NOT NULL,
    source        VARCHAR(50) NOT NULL,
    created_at    DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (match_id, sequence),
    KEY idx_commentary_match_minute (match_id, minute),
    KEY idx_commentary_kind (match_id, kind)
);

INSERT INTO schema_migrations (version, name)
VALUES (4, '0004_match_commentary')
ON DUPLICATE KEY UPDATE name = VALUES(name);
