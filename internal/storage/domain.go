package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"roslegacy/internal/game"
	"roslegacy/internal/lobby"
	"roslegacy/internal/matchmaking"
	gamesession "roslegacy/internal/session"
)

type DomainStore struct {
	db *pgxpool.Pool
}

func (s *DomainStore) RecordGameCompletion(ctx context.Context, completion game.Completion) error {
	participants, err := json.Marshal(completion.Participants)
	if err != nil {
		return fmt.Errorf("encode game completion: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin game completion persistence: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `
		INSERT INTO game_result_events(external_session_id, reason, winner_player_id, participants, completed_at)
		VALUES($1, $2, $3, $4, $5)
		ON CONFLICT (external_session_id) DO NOTHING`, completion.SessionID, completion.Reason,
		completion.WinnerID, participants, completion.CompletedAt)
	if err != nil {
		return fmt.Errorf("persist game completion: %w", err)
	}
	if result.RowsAffected() == 0 {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit duplicate game completion: %w", err)
		}
		return nil
	}
	winnerID := ""
	if looksLikeUUID(completion.WinnerID) {
		winnerID = completion.WinnerID
	}
	for _, participant := range completion.Participants {
		if !looksLikeUUID(participant.PlayerID) {
			continue
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO player_statistics(account_id)
			VALUES($1::uuid)
			ON CONFLICT (account_id) DO NOTHING`, participant.PlayerID)
		if err != nil {
			return fmt.Errorf("ensure player statistics for %q: %w", participant.PlayerID, err)
		}
		_, err = tx.Exec(ctx, `
			UPDATE player_statistics
			SET matches_played = matches_played + 1,
				matches_won = matches_won + CASE WHEN $2 <> '' AND account_id = $2::uuid THEN 1 ELSE 0 END,
				eliminations = eliminations + $3,
				updated_at = now()
			WHERE account_id = $1::uuid`, participant.PlayerID, winnerID, participant.Eliminations)
		if err != nil {
			return fmt.Errorf("update player statistics for %q: %w", participant.PlayerID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit game completion persistence: %w", err)
	}
	return nil
}

func looksLikeUUID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 36 {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func NewDomainStore(db *pgxpool.Pool) (*DomainStore, error) {
	if db == nil {
		return nil, errors.New("database is required")
	}
	return &DomainStore{db: db}, nil
}

func (s *DomainStore) UpsertLobby(ctx context.Context, value lobby.Lobby) error {
	members, err := json.Marshal(value.Members)
	if err != nil {
		return fmt.Errorf("encode lobby members: %w", err)
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO api_lobby_state(external_id, owner_account_id, mode, region, status, members)
		VALUES($1, $2, $3, $4, $5, $6)
		ON CONFLICT (external_id) DO UPDATE SET mode = EXCLUDED.mode,
		region = EXCLUDED.region, status = EXCLUDED.status, members = EXCLUDED.members,
		updated_at = now()`, value.ID, value.OwnerID, value.Mode, value.Region, value.Status, members)
	if err != nil {
		return fmt.Errorf("persist lobby: %w", err)
	}
	return nil
}

func (s *DomainStore) UpsertLobbyInvitation(ctx context.Context, value lobby.Invitation) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO api_lobby_invitations(external_id, lobby_external_id, inviter_account_id, invitee_account_id, status, created_at, expires_at)
		VALUES($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (external_id) DO UPDATE SET status = EXCLUDED.status, expires_at = EXCLUDED.expires_at, updated_at = now()`,
		value.ID, value.LobbyID, value.InviterID, value.InviteeID, value.Status, value.CreatedAt, value.ExpiresAt)
	if err != nil {
		return fmt.Errorf("persist lobby invitation: %w", err)
	}
	return nil
}

func (s *DomainStore) CreateGameSession(ctx context.Context, lobbyID, mode, region, assignedServer string, maxPlayers int) (gamesession.Session, error) {
	var value gamesession.Session
	if err := s.db.QueryRow(ctx, `
		INSERT INTO game_sessions(external_lobby_id, mode, region, assigned_server, status, max_players)
		VALUES($1, $2, $3, $4, 'allocating', $5)
		RETURNING id::text, COALESCE(external_lobby_id, ''), mode, region, COALESCE(assigned_server, ''), status, max_players, started_at, completed_at`,
		lobbyID, mode, region, assignedServer, maxPlayers).Scan(
		&value.ID, &value.LobbyID, &value.Mode, &value.Region, &value.AssignedServer, &value.Status,
		&value.MaxPlayers, &value.StartedAt, &value.CompletedAt); err != nil {
		return gamesession.Session{}, fmt.Errorf("create game session: %w", err)
	}
	return value, nil
}

func (s *DomainStore) GetGameSession(ctx context.Context, sessionID string) (gamesession.Session, error) {
	var value gamesession.Session
	if err := s.db.QueryRow(ctx, `
		SELECT id::text, COALESCE(external_lobby_id, ''), mode, region, COALESCE(assigned_server, ''), status, max_players, started_at, completed_at
		FROM game_sessions WHERE id = $1`, sessionID).Scan(
		&value.ID, &value.LobbyID, &value.Mode, &value.Region, &value.AssignedServer, &value.Status,
		&value.MaxPlayers, &value.StartedAt, &value.CompletedAt); err != nil {
		return gamesession.Session{}, fmt.Errorf("get game session: %w", err)
	}
	return value, nil
}

func (s *DomainStore) UpdateGameSession(ctx context.Context, sessionID string, status gamesession.Status, assignedServer string, startedAt, completedAt *time.Time) (gamesession.Session, error) {
	var value gamesession.Session
	if err := s.db.QueryRow(ctx, `
		UPDATE game_sessions
		SET status = $2, assigned_server = COALESCE(NULLIF($3, ''), assigned_server),
			started_at = COALESCE($4, started_at), completed_at = COALESCE($5, completed_at), updated_at = now()
		WHERE id = $1
		RETURNING id::text, COALESCE(external_lobby_id, ''), mode, region, COALESCE(assigned_server, ''), status, max_players, started_at, completed_at`,
		sessionID, status, assignedServer, startedAt, completedAt).Scan(
		&value.ID, &value.LobbyID, &value.Mode, &value.Region, &value.AssignedServer, &value.Status,
		&value.MaxPlayers, &value.StartedAt, &value.CompletedAt); err != nil {
		return gamesession.Session{}, fmt.Errorf("update game session: %w", err)
	}
	return value, nil
}

func (s *DomainStore) AdmitGamePlayer(ctx context.Context, sessionID, accountID, teamID string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO match_participants(game_session_id, account_id, team_id, connected_at)
		VALUES($1, $2, NULLIF($3, ''), now())
		ON CONFLICT (game_session_id, account_id) DO UPDATE SET team_id = COALESCE(NULLIF(EXCLUDED.team_id, ''), match_participants.team_id), connected_at = COALESCE(match_participants.connected_at, EXCLUDED.connected_at)`,
		sessionID, accountID, teamID)
	if err != nil {
		return fmt.Errorf("admit game player: %w", err)
	}
	return nil
}

func (s *DomainStore) ListGameParticipants(ctx context.Context, sessionID string) ([]gamesession.Participant, error) {
	rows, err := s.db.Query(ctx, `
		SELECT account_id::text, COALESCE(team_id, ''), connected_at
		FROM match_participants WHERE game_session_id = $1 ORDER BY connected_at, account_id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("list game participants: %w", err)
	}
	defer rows.Close()
	result := make([]gamesession.Participant, 0)
	for rows.Next() {
		var value gamesession.Participant
		if err := rows.Scan(&value.AccountID, &value.TeamID, &value.ConnectedAt); err != nil {
			return nil, fmt.Errorf("scan game participant: %w", err)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read game participants: %w", err)
	}
	return result, nil
}

func (s *DomainStore) UpsertTicket(ctx context.Context, ticket matchmaking.Ticket) error {
	status := ticket.Status
	if status == "" {
		status = "queued"
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO api_matchmaking_state(external_id, account_id, mode, region, status, queued_at)
		VALUES($1, $2, $3, $4, $5, $6)
		ON CONFLICT (external_id) DO UPDATE SET status = EXCLUDED.status,
		mode = EXCLUDED.mode, region = EXCLUDED.region, updated_at = now()`,
		ticket.ID, ticket.AccountID, ticket.Mode, ticket.Region, status, ticket.QueuedAt)
	if err != nil {
		return fmt.Errorf("persist matchmaking ticket: %w", err)
	}
	return nil
}

func (s *DomainStore) CancelTicket(ctx context.Context, ticketID string) error {
	_, err := s.db.Exec(ctx, `UPDATE api_matchmaking_state SET status = 'cancelled', updated_at = now() WHERE external_id = $1`, ticketID)
	if err != nil {
		return fmt.Errorf("persist cancelled ticket: %w", err)
	}
	return nil
}

func (s *DomainStore) PersistAllocation(ctx context.Context, allocation matchmaking.Allocation) error {
	tickets, err := json.Marshal(allocation.Tickets)
	if err != nil {
		return fmt.Errorf("encode allocation tickets: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin allocation persistence: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, ticket := range allocation.Tickets {
		if _, err := tx.Exec(ctx, `UPDATE api_matchmaking_state SET status = 'matched', updated_at = now() WHERE external_id = $1`, ticket.ID); err != nil {
			return fmt.Errorf("persist matched ticket: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO api_match_allocations(external_id, mode, region, tickets)
		VALUES($1, $2, $3, $4)
		ON CONFLICT (external_id) DO NOTHING`, allocation.ID, allocation.Mode, allocation.Region, tickets); err != nil {
		return fmt.Errorf("persist allocation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit allocation persistence: %w", err)
	}
	return nil
}
