package statistics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"roslegacy/internal/security"
)

type Service struct {
	db *pgxpool.Pool
}

type Stats struct {
	AccountID       string `json:"account_id"`
	MatchesPlayed   int    `json:"matches_played"`
	MatchesWon      int    `json:"matches_won"`
	Eliminations    int    `json:"eliminations"`
	Assists         int    `json:"assists"`
	SurvivalSeconds int64  `json:"survival_seconds"`
	Rating          int    `json:"rating"`
}

type MatchResult struct {
	MatchID         string
	AccountID       string
	Mode            string
	Region          string
	Placement       int
	Eliminations    int
	Assists         int
	SurvivalSeconds int64
	MaxPlayers      int
	StartedAt       time.Time
}

type LeaderboardEntry struct {
	AccountID string `json:"account_id"`
	Rating    int    `json:"rating"`
	Rank      int    `json:"rank"`
}

type HistoryEntry struct {
	MatchID         string     `json:"match_id"`
	Mode            string     `json:"mode"`
	Region          string     `json:"region"`
	Status          string     `json:"status"`
	Placement       int        `json:"placement"`
	Eliminations    int        `json:"eliminations"`
	Assists         int        `json:"assists"`
	SurvivalSeconds int64      `json:"survival_seconds"`
	StartedAt       time.Time  `json:"started_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

func NewService(db *pgxpool.Pool) (*Service, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &Service{db: db}, nil
}

func (s *Service) Get(ctx context.Context, accountID string) (Stats, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return Stats{}, err
	}
	result := Stats{AccountID: accountID, Rating: 1000}
	err := s.db.QueryRow(ctx, `
		SELECT account_id, matches_played, matches_won, eliminations, assists,
		       survival_seconds, rating
		FROM player_statistics WHERE account_id = $1`, accountID).Scan(
		&result.AccountID, &result.MatchesPlayed, &result.MatchesWon,
		&result.Eliminations, &result.Assists, &result.SurvivalSeconds,
		&result.Rating,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return result, nil
		}
		return Stats{}, fmt.Errorf("get statistics: %w", err)
	}
	return result, nil
}

func (s *Service) Record(ctx context.Context, result MatchResult) error {
	if err := security.ValidateIdentifier(result.MatchID); err != nil {
		return err
	}
	if err := security.ValidateIdentifier(result.AccountID); err != nil {
		return err
	}
	if !uuidPattern.MatchString(result.MatchID) || !uuidPattern.MatchString(result.AccountID) {
		return errors.New("match and account identifiers must be UUIDs")
	}
	if result.Mode == "" || result.Region == "" || result.Placement <= 0 || result.MaxPlayers <= 0 {
		return errors.New("match result mode, region, placement, and max players are required")
	}
	if result.Eliminations < 0 || result.Assists < 0 || result.SurvivalSeconds < 0 {
		return errors.New("match result counters cannot be negative")
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = time.Now().UTC()
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin statistics transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `
		INSERT INTO game_sessions(id, mode, region, status, max_players, started_at, completed_at)
		VALUES($1, $2, $3, 'complete', $4, $5, now())
		ON CONFLICT (id) DO UPDATE SET status = 'complete', completed_at = now(), updated_at = now()`,
		result.MatchID, result.Mode, result.Region, result.MaxPlayers, result.StartedAt); err != nil {
		return fmt.Errorf("persist game session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO match_participants(game_session_id, account_id, placement, eliminations, assists, survival_seconds, connected_at)
		VALUES($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (game_session_id, account_id) DO UPDATE SET placement = EXCLUDED.placement,
		eliminations = EXCLUDED.eliminations, assists = EXCLUDED.assists,
		survival_seconds = EXCLUDED.survival_seconds`, result.MatchID, result.AccountID,
		result.Placement, result.Eliminations, result.Assists, result.SurvivalSeconds, result.StartedAt); err != nil {
		return fmt.Errorf("persist match participant: %w", err)
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode match result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO match_sessions(id, region, mode, status, started_at, completed_at)
		VALUES($1, $2, $3, 'complete', $4, now())
		ON CONFLICT (id) DO UPDATE SET status = 'complete', completed_at = now()`,
		result.MatchID, result.Region, result.Mode, result.StartedAt); err != nil {
		return fmt.Errorf("persist match session: %w", err)
	}
	inserted, err := tx.Exec(ctx, `
		INSERT INTO match_results(match_session_id, result) VALUES($1, $2)
		ON CONFLICT (match_session_id) DO NOTHING`, result.MatchID, payload)
	if err != nil {
		return fmt.Errorf("persist match result: %w", err)
	}
	if inserted.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit idempotent statistics transaction: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO player_statistics(account_id, matches_played, matches_won, eliminations, assists, survival_seconds)
		VALUES($1, 1, CASE WHEN $2 = 1 THEN 1 ELSE 0 END, $3, $4, $5)
		ON CONFLICT (account_id) DO UPDATE SET matches_played = player_statistics.matches_played + 1,
		matches_won = player_statistics.matches_won + CASE WHEN $2 = 1 THEN 1 ELSE 0 END,
		eliminations = player_statistics.eliminations + $3,
		assists = player_statistics.assists + $4,
		survival_seconds = player_statistics.survival_seconds + $5,
		updated_at = now()`, result.AccountID, result.Placement, result.Eliminations,
		result.Assists, result.SurvivalSeconds); err != nil {
		return fmt.Errorf("update player statistics: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit statistics transaction: %w", err)
	}
	return nil
}

func (s *Service) Leaderboard(ctx context.Context, season, mode, region string, limit int) ([]LeaderboardEntry, error) {
	if season == "" || mode == "" || region == "" || limit <= 0 || limit > 100 {
		return nil, errors.New("season, mode, region, and bounded limit are required")
	}
	rows, err := s.db.Query(ctx, `
		SELECT account_id, rating, COALESCE(rank, row_number() OVER (ORDER BY rating DESC))::int
		FROM leaderboards WHERE season = $1 AND mode = $2 AND region = $3
		ORDER BY rating DESC LIMIT $4`, season, mode, region, limit)
	if err != nil {
		return nil, fmt.Errorf("query leaderboard: %w", err)
	}
	defer rows.Close()
	result := make([]LeaderboardEntry, 0, limit)
	for rows.Next() {
		var entry LeaderboardEntry
		if err := rows.Scan(&entry.AccountID, &entry.Rating, &entry.Rank); err != nil {
			return nil, fmt.Errorf("scan leaderboard: %w", err)
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read leaderboard: %w", err)
	}
	return result, nil
}

func (s *Service) History(ctx context.Context, accountID string, limit int) ([]HistoryEntry, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		return nil, errors.New("history limit must be between 1 and 100")
	}
	rows, err := s.db.Query(ctx, `
		SELECT g.id, g.mode, g.region, g.status, p.placement, p.eliminations,
		       p.assists, p.survival_seconds, g.started_at, g.completed_at
		FROM match_participants p
		JOIN game_sessions g ON g.id = p.game_session_id
		WHERE p.account_id = $1
		ORDER BY COALESCE(g.completed_at, g.created_at) DESC
		LIMIT $2`, accountID, limit)
	if err != nil {
		return nil, fmt.Errorf("query match history: %w", err)
	}
	defer rows.Close()
	result := make([]HistoryEntry, 0, limit)
	for rows.Next() {
		var entry HistoryEntry
		if err := rows.Scan(&entry.MatchID, &entry.Mode, &entry.Region, &entry.Status,
			&entry.Placement, &entry.Eliminations, &entry.Assists, &entry.SurvivalSeconds,
			&entry.StartedAt, &entry.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan match history: %w", err)
		}
		result = append(result, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read match history: %w", err)
	}
	return result, nil
}
