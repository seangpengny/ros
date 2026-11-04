BEGIN;

CREATE TABLE IF NOT EXISTS shop_catalog (
    item_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    description TEXT NOT NULL,
    price INTEGER NOT NULL CHECK (price >= 0),
    gameplay_item BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS player_wallets (
    account_id UUID PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    coins INTEGER NOT NULL DEFAULT 1000 CHECK (coins >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO shop_catalog(item_id, display_name, description, price, gameplay_item)
VALUES
    ('starter_medkit', 'Starter Medkit', 'A match-ready medkit added to your persistent inventory.', 100, true),
    ('starter_armor', 'Starter Armor', 'A match-ready armor item added to your persistent inventory.', 150, true),
    ('scope', 'Scope', 'A scope attachment available for the independent game protocol.', 200, true)
ON CONFLICT (item_id) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    description = EXCLUDED.description,
    price = EXCLUDED.price,
    gameplay_item = EXCLUDED.gameplay_item;

COMMIT;
