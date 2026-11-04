BEGIN;

CREATE TABLE IF NOT EXISTS game_result_events (
    external_session_id TEXT PRIMARY KEY,
    reason TEXT NOT NULL,
    winner_player_id TEXT,
    participants JSONB NOT NULL,
    completed_at TIMESTAMPTZ NOT NULL,
    committed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_game_result_events_completed_at ON game_result_events(completed_at);

COMMIT;
