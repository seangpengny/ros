package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"roslegacy/internal/security"
)

type Service struct {
	db *pgxpool.Pool
}

type Profile struct {
	AccountID   string         `json:"account_id"`
	DisplayName string         `json:"display_name"`
	Experience  int64          `json:"experience"`
	Level       int            `json:"level"`
	Preferences map[string]any `json:"preferences"`
	Inventory   map[string]any `json:"inventory"`
}

const maxPreferencesBytes = 32 << 10

func NewService(db *pgxpool.Pool) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &Service{db: db}, nil
}

func (s *Service) Get(ctx context.Context, accountID string) (Profile, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return Profile{}, err
	}
	var result Profile
	var preferences, inventory []byte
	err := s.db.QueryRow(ctx, `
		SELECT account_id, display_name, experience, level, preferences, inventory
		FROM profiles WHERE account_id = $1`, accountID).Scan(
		&result.AccountID, &result.DisplayName, &result.Experience, &result.Level,
		&preferences, &inventory,
	)
	if err != nil {
		return Profile{}, fmt.Errorf("get profile: %w", err)
	}
	result.Preferences = decodeObject(preferences)
	result.Inventory = decodeObject(inventory)
	return result, nil
}

func (s *Service) UpdateDisplayName(ctx context.Context, accountID, displayName string) (Profile, error) {
	return s.Update(ctx, accountID, &displayName, nil)
}

func (s *Service) Update(ctx context.Context, accountID string, displayName *string, preferences map[string]any) (Profile, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return Profile{}, err
	}
	current, err := s.Get(ctx, accountID)
	if err != nil {
		return Profile{}, err
	}
	if displayName != nil {
		if err := security.ValidateIdentifier(*displayName); err != nil {
			return Profile{}, err
		}
		current.DisplayName = *displayName
	}
	if preferences != nil {
		encoded, err := json.Marshal(preferences)
		if err != nil {
			return Profile{}, fmt.Errorf("encode preferences: %w", err)
		}
		if len(encoded) > maxPreferencesBytes {
			return Profile{}, errors.New("preferences exceed size limit")
		}
		current.Preferences = preferences
	}
	encodedPreferences, err := json.Marshal(current.Preferences)
	if err != nil {
		return Profile{}, fmt.Errorf("encode current preferences: %w", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE profiles SET display_name = $2, preferences = $3, updated_at = now() WHERE account_id = $1`, accountID, current.DisplayName, encodedPreferences); err != nil {
		return Profile{}, fmt.Errorf("update profile: %w", err)
	}
	return s.Get(ctx, accountID)
}

func decodeObject(value []byte) map[string]any {
	result := map[string]any{}
	if len(value) > 0 {
		_ = json.Unmarshal(value, &result)
	}
	return result
}
