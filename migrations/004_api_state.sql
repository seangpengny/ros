BEGIN;

CREATE TABLE IF NOT EXISTS api_lobby_state (
    external_id TEXT PRIMARY KEY,
    owner_account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    status TEXT NOT NULL,
    members JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_matchmaking_state (
    external_id TEXT PRIMARY KEY,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    status TEXT NOT NULL,
    queued_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS api_match_allocations (
    external_id TEXT PRIMARY KEY,
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    tickets JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
