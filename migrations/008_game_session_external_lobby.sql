BEGIN;

ALTER TABLE game_sessions
    ADD COLUMN IF NOT EXISTS external_lobby_id TEXT;

CREATE INDEX IF NOT EXISTS idx_game_sessions_external_lobby
    ON game_sessions(external_lobby_id);

COMMIT;
