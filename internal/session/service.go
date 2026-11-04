package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type Status string

const (
	StatusAllocating Status = "allocating"
	StatusStaging    Status = "staging"
	StatusRunning    Status = "running"
	StatusComplete   Status = "complete"
	StatusFailed     Status = "failed"
)

type Participant struct {
	AccountID   string     `json:"account_id"`
	TeamID      string     `json:"team_id,omitempty"`
	ConnectedAt *time.Time `json:"connected_at,omitempty"`
}

type Session struct {
	ID             string        `json:"id"`
	LobbyID        string        `json:"lobby_id,omitempty"`
	Mode           string        `json:"mode"`
	Region         string        `json:"region"`
	AssignedServer string        `json:"assigned_server"`
	Status         Status        `json:"status"`
	MaxPlayers     int           `json:"max_players"`
	StartedAt      *time.Time    `json:"started_at,omitempty"`
	CompletedAt    *time.Time    `json:"completed_at,omitempty"`
	LastHeartbeat  *time.Time    `json:"last_heartbeat,omitempty"`
	Healthy        bool          `json:"healthy"`
	Participants   []Participant `json:"participants"`
}

type Store interface {
	CreateGameSession(context.Context, string, string, string, string, int) (Session, error)
	GetGameSession(context.Context, string) (Session, error)
	UpdateGameSession(context.Context, string, Status, string, *time.Time, *time.Time) (Session, error)
	AdmitGamePlayer(context.Context, string, string, string) error
	ListGameParticipants(context.Context, string) ([]Participant, error)
}

const defaultHeartbeatTTL = 15 * time.Second

type Service struct {
	store          Store
	redis          *redis.Client
	heartbeatTTL   time.Duration
	assignedServer []string
	nextServer     atomic.Uint64
}

func NewService(store Store, cache *redis.Client, assignedServer string) (*Service, error) {
	return NewServiceWithServers(store, cache, []string{assignedServer})
}

func NewServiceWithServers(store Store, cache *redis.Client, assignedServers []string) (*Service, error) {
	if store == nil {
		return nil, errors.New("session store is required")
	}
	servers := make([]string, 0, len(assignedServers))
	seen := make(map[string]struct{}, len(assignedServers))
	for _, assignedServer := range assignedServers {
		assignedServer = strings.TrimSpace(assignedServer)
		if assignedServer == "" || len(assignedServer) > 128 || strings.ContainsAny(assignedServer, "{}[]\\\"'") {
			return nil, errors.New("assigned server is required and bounded")
		}
		if _, exists := seen[assignedServer]; exists {
			continue
		}
		seen[assignedServer] = struct{}{}
		servers = append(servers, assignedServer)
	}
	if len(servers) == 0 {
		return nil, errors.New("at least one assigned server is required")
	}
	return &Service{store: store, redis: cache, heartbeatTTL: defaultHeartbeatTTL, assignedServer: servers}, nil
}

func (s *Service) AssignedServerID() string { return s.assignedServer[0] }

func (s *Service) chooseServer() string {
	index := s.nextServer.Add(1) - 1
	return s.assignedServer[index%uint64(len(s.assignedServer))]
}

func (s *Service) Create(ctx context.Context, lobbyID, mode, region string, maxPlayers int) (Session, error) {
	if err := validateLobbyID(lobbyID); err != nil {
		return Session{}, err
	}
	if err := validateMode(mode); err != nil {
		return Session{}, err
	}
	if err := validateRegion(region); err != nil {
		return Session{}, err
	}
	if maxPlayers < 2 || maxPlayers > 100 {
		return Session{}, errors.New("max players must be between 2 and 100")
	}
	value, err := s.store.CreateGameSession(ctx, lobbyID, mode, region, s.chooseServer(), maxPlayers)
	if err != nil {
		return Session{}, err
	}
	value.Healthy = false
	value.Participants = nil
	return value, nil
}

func (s *Service) Get(ctx context.Context, id string) (Session, error) {
	if err := validateID(id); err != nil {
		return Session{}, err
	}
	value, err := s.store.GetGameSession(ctx, id)
	if err != nil {
		return Session{}, err
	}
	value.Participants, err = s.store.ListGameParticipants(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if value.Status == StatusComplete || value.Status == StatusFailed || s.redis == nil {
		return value, nil
	}
	heartbeat, err := s.readHeartbeat(ctx, id)
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return value, nil
		}
		return Session{}, err
	}
	value.LastHeartbeat = &heartbeat.LastHeartbeat
	value.Healthy = time.Since(heartbeat.LastHeartbeat) <= s.heartbeatTTL
	return value, nil
}

func (s *Service) Admit(ctx context.Context, id, accountID, teamID string) error {
	if err := validateID(id); err != nil {
		return err
	}
	if err := validateAccountID(accountID); err != nil {
		return err
	}
	if len(teamID) > 64 || strings.ContainsAny(teamID, "{}[]\\\"'") {
		return errors.New("team id is bounded")
	}
	value, err := s.store.GetGameSession(ctx, id)
	if err != nil {
		return err
	}
	if value.Status == StatusComplete || value.Status == StatusFailed {
		return errors.New("session is no longer admitting players")
	}
	participants, err := s.store.ListGameParticipants(ctx, id)
	if err != nil {
		return err
	}
	for _, participant := range participants {
		if participant.AccountID == accountID {
			return nil
		}
	}
	if len(participants) >= value.MaxPlayers {
		return errors.New("session is full")
	}
	return s.store.AdmitGamePlayer(ctx, id, accountID, teamID)
}

func (s *Service) Start(ctx context.Context, id, serverID string) (Session, error) {
	value, err := s.authorizeServer(ctx, id, serverID)
	if err != nil {
		return Session{}, err
	}
	if value.Status != StatusAllocating && value.Status != StatusStaging {
		return Session{}, errors.New("session cannot start from its current state")
	}
	now := time.Now().UTC()
	if _, err := s.store.UpdateGameSession(ctx, id, StatusRunning, serverID, &now, nil); err != nil {
		return Session{}, err
	}
	if err := s.writeHeartbeat(ctx, id, serverID, now); err != nil {
		return Session{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Heartbeat(ctx context.Context, id, serverID string) (Session, error) {
	value, err := s.authorizeServer(ctx, id, serverID)
	if err != nil {
		return Session{}, err
	}
	if value.Status == StatusComplete || value.Status == StatusFailed {
		return Session{}, errors.New("session is no longer active")
	}
	now := time.Now().UTC()
	if value.Status == StatusAllocating || value.Status == StatusStaging {
		if _, err := s.store.UpdateGameSession(ctx, id, StatusRunning, serverID, &now, nil); err != nil {
			return Session{}, err
		}
	}
	if err := s.writeHeartbeat(ctx, id, serverID, now); err != nil {
		return Session{}, err
	}
	return s.Get(ctx, id)
}

func (s *Service) Complete(ctx context.Context, id, serverID string) (Session, error) {
	if _, err := s.authorizeServer(ctx, id, serverID); err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	if _, err := s.store.UpdateGameSession(ctx, id, StatusComplete, serverID, nil, &now); err != nil {
		return Session{}, err
	}
	if s.redis != nil {
		if err := s.redis.Del(ctx, heartbeatKey(id)).Err(); err != nil {
			return Session{}, fmt.Errorf("clear session heartbeat: %w", err)
		}
	}
	return s.Get(ctx, id)
}

func (s *Service) Fail(ctx context.Context, id, serverID string) error {
	if _, err := s.authorizeServer(ctx, id, serverID); err != nil {
		return err
	}
	_, err := s.store.UpdateGameSession(ctx, id, StatusFailed, serverID, nil, nil)
	return err
}

func (s *Service) authorizeServer(ctx context.Context, id, serverID string) (Session, error) {
	if err := validateID(id); err != nil {
		return Session{}, err
	}
	if serverID == "" || !s.isAssignedServer(serverID) {
		return Session{}, errors.New("assigned game server required")
	}
	value, err := s.store.GetGameSession(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if value.AssignedServer != serverID {
		return Session{}, errors.New("game server is not assigned to this session")
	}
	return value, nil
}

func (s *Service) isAssignedServer(serverID string) bool {
	for _, assignedServer := range s.assignedServer {
		if assignedServer == serverID {
			return true
		}
	}
	return false
}

type heartbeat struct {
	ServerID      string    `json:"server_id"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

func (s *Service) writeHeartbeat(ctx context.Context, id, serverID string, now time.Time) error {
	if s.redis == nil {
		return nil
	}
	encoded, err := json.Marshal(heartbeat{ServerID: serverID, LastHeartbeat: now})
	if err != nil {
		return fmt.Errorf("encode session heartbeat: %w", err)
	}
	if err := s.redis.Set(ctx, heartbeatKey(id), encoded, s.heartbeatTTL).Err(); err != nil {
		return fmt.Errorf("write session heartbeat: %w", err)
	}
	return nil
}

func (s *Service) readHeartbeat(ctx context.Context, id string) (heartbeat, error) {
	raw, err := s.redis.Get(ctx, heartbeatKey(id)).Bytes()
	if err != nil {
		return heartbeat{}, err
	}
	var value heartbeat
	if err := json.Unmarshal(raw, &value); err != nil {
		return heartbeat{}, fmt.Errorf("decode session heartbeat: %w", err)
	}
	return value, nil
}

func heartbeatKey(id string) string { return "ros:session:heartbeat:" + id }

func validateID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 || strings.ContainsAny(value, "{}[]\\\"'") {
		return errors.New("session id is required and bounded")
	}
	return nil
}

func validateLobbyID(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 64 || strings.ContainsAny(value, "{}[]\\\"'") {
		return errors.New("lobby id is required and bounded")
	}
	return nil
}

func validateAccountID(value string) error {
	if strings.TrimSpace(value) == "" || len(value) > 64 || strings.ContainsAny(value, "{}[]\\\"'") {
		return errors.New("account id is required and bounded")
	}
	return nil
}

func validateMode(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "solo", "duo", "squad":
		return nil
	default:
		return errors.New("mode must be solo, duo, or squad")
	}
}

func validateRegion(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 32 || strings.ContainsAny(value, " :/\\{}[]\"'") {
		return errors.New("region is required and bounded")
	}
	return nil
}
