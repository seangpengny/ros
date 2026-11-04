package lobby

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisLobbyTTL = 24 * time.Hour
const redisPresenceTTL = 90 * time.Second

// RedisService stores lobby state in Redis so multiple API instances can
// observe the same lobby. The in-memory Service remains available for unit
// tests and deterministic local domain testing.
type RedisService struct {
	client   *redis.Client
	maxParty int
	timeout  time.Duration
}

func NewRedisService(client *redis.Client, maxParty int) (*RedisService, error) {
	if client == nil || maxParty <= 0 {
		return nil, errors.New("redis client and positive max party size are required")
	}
	return &RedisService{client: client, maxParty: maxParty, timeout: 3 * time.Second}, nil
}

func (s *RedisService) Create(ownerID, mode, region string) (Lobby, error) {
	if err := validateField(ownerID, "owner"); err != nil {
		return Lobby{}, err
	}
	if err := validateField(mode, "mode"); err != nil {
		return Lobby{}, err
	}
	if err := ValidateMode(mode); err != nil {
		return Lobby{}, err
	}
	if err := ValidateRegion(region); err != nil {
		return Lobby{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	id, err := s.client.Incr(ctx, "ros:lobby:next").Result()
	if err != nil {
		return Lobby{}, fmt.Errorf("allocate lobby id: %w", err)
	}
	value := Lobby{ID: fmt.Sprintf("lobby-%08d", id), OwnerID: ownerID, Mode: mode, Region: region, Status: StatusOpen, Members: []Member{{AccountID: ownerID}}}
	if err := s.write(ctx, value); err != nil {
		return Lobby{}, err
	}
	return value, nil
}

func (s *RedisService) Join(lobbyID, accountID string) (Lobby, error) {
	if err := validateField(accountID, "account"); err != nil {
		return Lobby{}, err
	}
	return s.update(lobbyID, func(value *Lobby) error {
		if value.Status != StatusOpen {
			return errors.New("lobby is not open")
		}
		for _, member := range value.Members {
			if member.AccountID == accountID {
				return nil
			}
		}
		if len(value.Members) >= s.maxParty {
			return errors.New("lobby is full")
		}
		value.Members = append(value.Members, Member{AccountID: accountID})
		return nil
	})
}

func (s *RedisService) Invite(lobbyID, inviterID, inviteeID string) (Invitation, error) {
	if err := validateField(inviterID, "inviter"); err != nil {
		return Invitation{}, err
	}
	if err := validateField(inviteeID, "invitee"); err != nil {
		return Invitation{}, err
	}
	if inviterID == inviteeID {
		return Invitation{}, errors.New("a different invitee is required")
	}
	current, err := s.Get(lobbyID)
	if err != nil {
		return Invitation{}, err
	}
	if current.OwnerID != inviterID {
		return Invitation{}, errors.New("lobby owner required")
	}
	if current.Status != StatusOpen {
		return Invitation{}, errors.New("lobby is not open")
	}
	for _, member := range current.Members {
		if member.AccountID == inviteeID {
			return Invitation{}, errors.New("account is already a lobby member")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	now := time.Now().UTC()
	ids, err := s.client.SMembers(ctx, invitationInboxKey(inviteeID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return Invitation{}, fmt.Errorf("read invitation inbox: %w", err)
	}
	for _, id := range ids {
		value, readErr := s.readInvitation(ctx, id)
		if readErr != nil {
			continue
		}
		if value.LobbyID == lobbyID && value.InviteeID == inviteeID && value.Status == InvitationPending && now.Before(value.ExpiresAt) {
			return value, nil
		}
	}
	sequence, err := s.client.Incr(ctx, "ros:lobby:invite:next").Result()
	if err != nil {
		return Invitation{}, fmt.Errorf("allocate invitation id: %w", err)
	}
	value := Invitation{ID: fmt.Sprintf("invite-%08d", sequence), LobbyID: lobbyID, InviterID: inviterID, InviteeID: inviteeID, Status: InvitationPending, CreatedAt: now, ExpiresAt: now.Add(invitationTTL)}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Invitation{}, fmt.Errorf("encode invitation: %w", err)
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, invitationKey(value.ID), encoded, invitationTTL)
	pipe.SAdd(ctx, invitationInboxKey(inviteeID), value.ID)
	pipe.Expire(ctx, invitationInboxKey(inviteeID), invitationTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return Invitation{}, fmt.Errorf("write invitation: %w", err)
	}
	return value, nil
}

func (s *RedisService) ListInvitations(accountID string) ([]Invitation, error) {
	if err := validateField(accountID, "account"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	ids, err := s.client.SMembers(ctx, invitationInboxKey(accountID)).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("read invitation inbox: %w", err)
	}
	now := time.Now().UTC()
	result := make([]Invitation, 0, len(ids))
	for _, id := range ids {
		value, readErr := s.readInvitation(ctx, id)
		if readErr != nil || value.Status != InvitationPending {
			continue
		}
		if !now.Before(value.ExpiresAt) {
			continue
		}
		result = append(result, value)
	}
	return result, nil
}

func (s *RedisService) GetInvitation(invitationID string) (Invitation, error) {
	return s.getInvitation(invitationID)
}

func (s *RedisService) AcceptInvitation(invitationID, accountID string) (Lobby, error) {
	value, err := s.getInvitation(invitationID)
	if err != nil {
		return Lobby{}, err
	}
	if value.InviteeID != accountID {
		return Lobby{}, errors.New("invitation recipient required")
	}
	if value.Status != InvitationPending {
		return Lobby{}, errors.New("invitation is no longer pending")
	}
	if !time.Now().UTC().Before(value.ExpiresAt) {
		return Lobby{}, errors.New("invitation expired")
	}
	lobbyValue, err := s.Join(value.LobbyID, accountID)
	if err != nil {
		return Lobby{}, err
	}
	value.Status = InvitationAccepted
	if err := s.writeInvitation(value); err != nil {
		return Lobby{}, err
	}
	return lobbyValue, nil
}

func (s *RedisService) DeclineInvitation(invitationID, accountID string) (Invitation, error) {
	value, err := s.getInvitation(invitationID)
	if err != nil {
		return Invitation{}, err
	}
	if value.InviteeID != accountID {
		return Invitation{}, errors.New("invitation recipient required")
	}
	if value.Status != InvitationPending {
		return Invitation{}, errors.New("invitation is no longer pending")
	}
	if !time.Now().UTC().Before(value.ExpiresAt) {
		return Invitation{}, errors.New("invitation expired")
	}
	value.Status = InvitationDeclined
	if err := s.writeInvitation(value); err != nil {
		return Invitation{}, err
	}
	return value, nil
}

func (s *RedisService) SetPresence(accountID string, online bool) (Presence, error) {
	if err := validateField(accountID, "account"); err != nil {
		return Presence{}, err
	}
	value := Presence{AccountID: accountID, Online: online, LastSeen: time.Now().UTC()}
	encoded, err := json.Marshal(value)
	if err != nil {
		return Presence{}, fmt.Errorf("encode presence: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	if err := s.client.Set(ctx, presenceKey(accountID), encoded, redisPresenceTTL).Err(); err != nil {
		return Presence{}, fmt.Errorf("write presence: %w", err)
	}
	return value, nil
}

func (s *RedisService) GetPresence(accountID string) (Presence, error) {
	if err := validateField(accountID, "account"); err != nil {
		return Presence{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	raw, err := s.client.Get(ctx, presenceKey(accountID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Presence{AccountID: accountID}, nil
	}
	if err != nil {
		return Presence{}, fmt.Errorf("read presence: %w", err)
	}
	var value Presence
	if err := json.Unmarshal(raw, &value); err != nil {
		return Presence{}, fmt.Errorf("decode presence: %w", err)
	}
	return value, nil
}

func (s *RedisService) SetReady(lobbyID, accountID string, ready bool) (Lobby, error) {
	return s.update(lobbyID, func(value *Lobby) error {
		if value.Status != StatusOpen && value.Status != StatusReady {
			return errors.New("lobby cannot change ready state")
		}
		found := false
		allReady := len(value.Members) > 0
		for index := range value.Members {
			if value.Members[index].AccountID == accountID {
				value.Members[index].Ready = ready
				found = true
			}
			if !value.Members[index].Ready {
				allReady = false
			}
		}
		if !found {
			return errors.New("member not found")
		}
		if allReady {
			value.Status = StatusReady
		} else {
			value.Status = StatusOpen
		}
		return nil
	})
}

func (s *RedisService) Allocate(lobbyID string) (Lobby, error) {
	return s.update(lobbyID, func(value *Lobby) error {
		if value.Status != StatusReady {
			return errors.New("lobby is not ready")
		}
		value.Status = StatusAllocated
		return nil
	})
}

func (s *RedisService) Close(lobbyID string) error {
	_, err := s.update(lobbyID, func(value *Lobby) error {
		value.Status = StatusClosed
		return nil
	})
	return err
}

func (s *RedisService) Get(lobbyID string) (Lobby, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	return s.read(ctx, lobbyID)
}

func (s *RedisService) update(lobbyID string, mutate func(*Lobby) error) (Lobby, error) {
	if !validLobbyID(lobbyID) {
		return Lobby{}, errors.New("lobby id is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	var result Lobby
	for attempt := 0; attempt < 5; attempt++ {
		err := s.client.Watch(ctx, func(tx *redis.Tx) error {
			value, err := s.read(ctx, lobbyID)
			if err != nil {
				return err
			}
			if err := mutate(&value); err != nil {
				return err
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if _, err := tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, lobbyKey(lobbyID), encoded, redisLobbyTTL)
				return nil
			}); err != nil {
				return err
			}
			result = value
			return nil
		}, lobbyKey(lobbyID))
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, redis.TxFailedErr) {
			return Lobby{}, fmt.Errorf("update lobby: %w", err)
		}
	}
	return Lobby{}, errors.New("lobby update conflicted repeatedly")
}

func (s *RedisService) read(ctx context.Context, lobbyID string) (Lobby, error) {
	if !validLobbyID(lobbyID) {
		return Lobby{}, errors.New("lobby id is required")
	}
	raw, err := s.client.Get(ctx, lobbyKey(lobbyID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Lobby{}, errors.New("lobby not found")
	}
	if err != nil {
		return Lobby{}, fmt.Errorf("read lobby: %w", err)
	}
	var value Lobby
	if err := json.Unmarshal(raw, &value); err != nil {
		return Lobby{}, fmt.Errorf("decode lobby: %w", err)
	}
	return value, nil
}

func (s *RedisService) getInvitation(invitationID string) (Invitation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	return s.readInvitation(ctx, invitationID)
}

func (s *RedisService) readInvitation(ctx context.Context, invitationID string) (Invitation, error) {
	if !validInvitationID(invitationID) {
		return Invitation{}, errors.New("invitation id is required")
	}
	raw, err := s.client.Get(ctx, invitationKey(invitationID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Invitation{}, errors.New("invitation not found")
	}
	if err != nil {
		return Invitation{}, fmt.Errorf("read invitation: %w", err)
	}
	var value Invitation
	if err := json.Unmarshal(raw, &value); err != nil {
		return Invitation{}, fmt.Errorf("decode invitation: %w", err)
	}
	return value, nil
}

func (s *RedisService) writeInvitation(value Invitation) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode invitation: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	if err := s.client.Set(ctx, invitationKey(value.ID), encoded, invitationTTL).Err(); err != nil {
		return fmt.Errorf("write invitation: %w", err)
	}
	return nil
}

func (s *RedisService) write(ctx context.Context, value Lobby) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode lobby: %w", err)
	}
	if err := s.client.Set(ctx, lobbyKey(value.ID), encoded, redisLobbyTTL).Err(); err != nil {
		return fmt.Errorf("write lobby: %w", err)
	}
	return nil
}

func lobbyKey(id string) string                  { return "ros:lobby:" + id }
func invitationKey(id string) string             { return "ros:lobby:invitation:" + id }
func invitationInboxKey(accountID string) string { return "ros:lobby:invitations:" + accountID }
func presenceKey(accountID string) string        { return "ros:presence:" + accountID }

func validLobbyID(value string) bool {
	return strings.HasPrefix(value, "lobby-") && len(value) <= 32
}

func validInvitationID(value string) bool {
	return strings.HasPrefix(value, "invite-") && len(value) <= 32
}

func validateField(value, name string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "{}[]\\\"'") {
		return fmt.Errorf("%s is required and bounded", name)
	}
	return nil
}

var _ interface {
	Create(string, string, string) (Lobby, error)
	Join(string, string) (Lobby, error)
	SetReady(string, string, bool) (Lobby, error)
	Invite(string, string, string) (Invitation, error)
	ListInvitations(string) ([]Invitation, error)
	GetInvitation(string) (Invitation, error)
	AcceptInvitation(string, string) (Lobby, error)
	DeclineInvitation(string, string) (Invitation, error)
	SetPresence(string, bool) (Presence, error)
	GetPresence(string) (Presence, error)
	Allocate(string) (Lobby, error)
	Close(string) error
	Get(string) (Lobby, error)
} = (*RedisService)(nil)
