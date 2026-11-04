BEGIN;

CREATE TABLE IF NOT EXISTS api_lobby_invitations (
    external_id TEXT PRIMARY KEY,
    lobby_external_id TEXT NOT NULL,
    inviter_account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    invitee_account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'declined', 'expired')),
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS api_lobby_invitations_recipient_idx
    ON api_lobby_invitations(invitee_account_id, status, expires_at);

COMMIT;
