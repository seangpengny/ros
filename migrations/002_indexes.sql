BEGIN;

CREATE INDEX IF NOT EXISTS idx_device_sessions_account ON device_sessions(account_id);
CREATE INDEX IF NOT EXISTS idx_device_sessions_expiry ON device_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_lobbies_queue ON lobbies(region, mode, status, created_at);
CREATE INDEX IF NOT EXISTS idx_match_sessions_status ON match_sessions(status, region, mode, created_at);
CREATE INDEX IF NOT EXISTS idx_player_inventory_account ON player_inventory(account_id);
CREATE INDEX IF NOT EXISTS idx_party_members_account ON party_members(account_id);
CREATE INDEX IF NOT EXISTS idx_matchmaking_tickets_queue ON matchmaking_tickets(status, mode, region, queued_at);
CREATE INDEX IF NOT EXISTS idx_game_sessions_status ON game_sessions(status, region, mode, created_at);
CREATE INDEX IF NOT EXISTS idx_match_participants_account ON match_participants(account_id);
CREATE INDEX IF NOT EXISTS idx_leaderboards_lookup ON leaderboards(season, mode, region, rating DESC);
CREATE INDEX IF NOT EXISTS idx_audit_logs_subject ON audit_logs(subject_id, created_at);
CREATE INDEX IF NOT EXISTS idx_api_lobby_status ON api_lobby_state(status, region, mode, updated_at);
CREATE INDEX IF NOT EXISTS idx_api_matchmaking_queue ON api_matchmaking_state(status, region, mode, queued_at);
CREATE INDEX IF NOT EXISTS idx_api_matchmaking_account ON api_matchmaking_state(account_id, status);

COMMIT;
