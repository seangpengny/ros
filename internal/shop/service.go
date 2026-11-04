package shop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"roslegacy/internal/security"
)

type Service struct {
	db *pgxpool.Pool
}

type Item struct {
	ID           string `json:"item_id"`
	DisplayName  string `json:"display_name"`
	Description  string `json:"description"`
	Price        int    `json:"price"`
	GameplayItem bool   `json:"gameplay_item"`
}

type Catalog struct {
	Coins int    `json:"coins"`
	Items []Item `json:"items"`
}

type Receipt struct {
	ItemID    string         `json:"item_id"`
	Quantity  int            `json:"quantity"`
	Coins     int            `json:"coins"`
	Inventory map[string]int `json:"inventory"`
}

func NewService(db *pgxpool.Pool) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &Service{db: db}, nil
}

func (s *Service) Catalog(ctx context.Context, accountID string) (Catalog, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return Catalog{}, err
	}
	if err := s.ensureWallet(ctx, accountID); err != nil {
		return Catalog{}, err
	}
	result := Catalog{}
	if err := s.db.QueryRow(ctx, `SELECT coins FROM player_wallets WHERE account_id = $1`, accountID).Scan(&result.Coins); err != nil {
		return Catalog{}, fmt.Errorf("read wallet: %w", err)
	}
	rows, err := s.db.Query(ctx, `SELECT item_id, display_name, description, price, gameplay_item FROM shop_catalog ORDER BY item_id`)
	if err != nil {
		return Catalog{}, fmt.Errorf("read shop catalog: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.Description, &item.Price, &item.GameplayItem); err != nil {
			return Catalog{}, fmt.Errorf("scan shop catalog: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return Catalog{}, fmt.Errorf("read shop catalog: %w", err)
	}
	return result, nil
}

func (s *Service) Purchase(ctx context.Context, accountID, itemID string, quantity int) (Receipt, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return Receipt{}, err
	}
	itemID = strings.TrimSpace(itemID)
	if err := security.ValidateIdentifier(itemID); err != nil {
		return Receipt{}, err
	}
	if quantity < 1 || quantity > 10 {
		return Receipt{}, errors.New("quantity must be between 1 and 10")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Receipt{}, fmt.Errorf("begin purchase: %w", err)
	}
	defer tx.Rollback(ctx)
	var item Item
	if err := tx.QueryRow(ctx, `SELECT item_id, display_name, description, price, gameplay_item FROM shop_catalog WHERE item_id = $1`, itemID).
		Scan(&item.ID, &item.DisplayName, &item.Description, &item.Price, &item.GameplayItem); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Receipt{}, errors.New("shop item not found")
		}
		return Receipt{}, fmt.Errorf("read shop item: %w", err)
	}
	if err := s.ensureWalletTx(ctx, tx, accountID); err != nil {
		return Receipt{}, err
	}
	var coins int
	if err := tx.QueryRow(ctx, `
		UPDATE player_wallets SET coins = coins - ($2::integer * $3::integer), updated_at = now()
		WHERE account_id = $1 AND coins >= ($2::integer * $3::integer)
		RETURNING coins`, accountID, item.Price, quantity).Scan(&coins); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Receipt{}, errors.New("insufficient coins")
		}
		return Receipt{}, fmt.Errorf("debit wallet: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO player_inventory(account_id, item_id, quantity)
		VALUES($1, $2, $3)
		ON CONFLICT (account_id, item_id) DO UPDATE SET quantity = player_inventory.quantity + EXCLUDED.quantity, updated_at = now()`, accountID, itemID, quantity); err != nil {
		return Receipt{}, fmt.Errorf("persist purchase inventory: %w", err)
	}
	var inventoryQuantity int
	if err := tx.QueryRow(ctx, `SELECT quantity FROM player_inventory WHERE account_id = $1 AND item_id = $2`, accountID, itemID).Scan(&inventoryQuantity); err != nil {
		return Receipt{}, fmt.Errorf("read purchased inventory: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE profiles
		SET inventory = jsonb_set(inventory, ARRAY[$2]::text[], to_jsonb($3::integer), true), updated_at = now()
		WHERE account_id = $1`, accountID, itemID, inventoryQuantity); err != nil {
		return Receipt{}, fmt.Errorf("update profile inventory: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Receipt{}, fmt.Errorf("commit purchase: %w", err)
	}
	return Receipt{ItemID: itemID, Quantity: quantity, Coins: coins, Inventory: map[string]int{itemID: inventoryQuantity}}, nil
}

func (s *Service) ensureWallet(ctx context.Context, accountID string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO player_wallets(account_id) VALUES($1) ON CONFLICT (account_id) DO NOTHING`, accountID)
	if err != nil {
		return fmt.Errorf("ensure wallet: %w", err)
	}
	return nil
}

func (s *Service) ensureWalletTx(ctx context.Context, tx pgx.Tx, accountID string) error {
	if _, err := tx.Exec(ctx, `INSERT INTO player_wallets(account_id) VALUES($1) ON CONFLICT (account_id) DO NOTHING`, accountID); err != nil {
		return fmt.Errorf("ensure wallet: %w", err)
	}
	return nil
}
