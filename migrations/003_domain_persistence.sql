BEGIN;

CREATE TABLE IF NOT EXISTS player_statistics (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    matches_played INTEGER NOT NULL DEFAULT 0 CHECK (matches_played >= 0),
    matches_won INTEGER NOT NULL DEFAULT 0 CHECK (matches_won >= 0),
    eliminations INTEGER NOT NULL DEFAULT 0 CHECK (eliminations >= 0),
    assists INTEGER NOT NULL DEFAULT 0 CHECK (assists >= 0),
    survival_seconds BIGINT NOT NULL DEFAULT 0 CHECK (survival_seconds >= 0),
    rating INTEGER NOT NULL DEFAULT 1000,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS player_inventory (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, item_id)
);

CREATE TABLE IF NOT EXISTS parties (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_account_id UUID NOT NULL REFERENCES accounts(id),
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('open', 'ready', 'allocated', 'closed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS party_members (
    party_id UUID NOT NULL REFERENCES parties(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    ready BOOLEAN NOT NULL DEFAULT false,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (party_id, account_id)
);

CREATE TABLE IF NOT EXISTS matchmaking_tickets (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'matched', 'cancelled', 'expired')),
    queued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    matched_at TIMESTAMPTZ,
    UNIQUE (account_id, mode, region, status)
);

CREATE TABLE IF NOT EXISTS game_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    lobby_id UUID REFERENCES lobbies(id) ON DELETE SET NULL,
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    assigned_server TEXT,
    status TEXT NOT NULL CHECK (status IN ('allocating', 'staging', 'running', 'complete', 'failed')),
    max_players INTEGER NOT NULL CHECK (max_players > 0),
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS match_participants (
    game_session_id UUID NOT NULL REFERENCES game_sessions(id) ON DELETE CASCADE,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    team_id TEXT,
    placement INTEGER CHECK (placement IS NULL OR placement > 0),
    eliminations INTEGER NOT NULL DEFAULT 0 CHECK (eliminations >= 0),
    assists INTEGER NOT NULL DEFAULT 0 CHECK (assists >= 0),
    survival_seconds BIGINT NOT NULL DEFAULT 0 CHECK (survival_seconds >= 0),
    connected_at TIMESTAMPTZ,
    disconnected_at TIMESTAMPTZ,
    PRIMARY KEY (game_session_id, account_id)
);

CREATE TABLE IF NOT EXISTS leaderboards (
    season TEXT NOT NULL,
    mode TEXT NOT NULL,
    region TEXT NOT NULL,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    rating INTEGER NOT NULL DEFAULT 1000,
    rank INTEGER,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (season, mode, region, account_id)
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id BIGSERIAL PRIMARY KEY,
    event_type TEXT NOT NULL,
    subject_id TEXT,
    account_id UUID REFERENCES accounts(id) ON DELETE SET NULL,
    device_id TEXT,
    success BOOLEAN NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
