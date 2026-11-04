package lobby

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusOpen      Status = "open"
	StatusReady     Status = "ready"
	StatusAllocated Status = "allocated"
	StatusClosed    Status = "closed"
)

type Member struct {
	AccountID string `json:"account_id"`
	Ready     bool   `json:"ready"`
}

type Lobby struct {
	ID      string   `json:"id"`
	OwnerID string   `json:"owner_id"`
	Mode    string   `json:"mode"`
	Region  string   `json:"region"`
	Status  Status   `json:"status"`
	Members []Member `json:"members"`
}

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationDeclined InvitationStatus = "declined"
	InvitationExpired  InvitationStatus = "expired"
)

type Invitation struct {
	ID        string           `json:"id"`
	LobbyID   string           `json:"lobby_id"`
	InviterID string           `json:"inviter_id"`
	InviteeID string           `json:"invitee_id"`
	Status    InvitationStatus `json:"status"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt time.Time        `json:"expires_at"`
}

type Presence struct {
	AccountID string    `json:"account_id"`
	Online    bool      `json:"online"`
	LastSeen  time.Time `json:"last_seen"`
}

const invitationTTL = 24 * time.Hour

type Service struct {
	mu           sync.RWMutex
	nextID       uint64
	nextInviteID uint64
	maxParty     int
	lobbies      map[string]*Lobby
	invitations  map[string]*Invitation
	presence     map[string]Presence
}

func NewService(maxParty int) (*Service, error) {
	if maxParty <= 0 {
		return nil, errors.New("max party size must be positive")
	}
	return &Service{maxParty: maxParty, lobbies: make(map[string]*Lobby), invitations: make(map[string]*Invitation), presence: make(map[string]Presence)}, nil
}

func ValidateMode(mode string) error {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "solo", "duo", "squad":
		return nil
	default:
		return errors.New("mode must be solo, duo, or squad")
	}
}

func ValidateRegion(region string) error {
	region = strings.TrimSpace(region)
	if region == "" || len(region) > 32 || strings.ContainsAny(region, " :/\\{}[]\"'") {
		return errors.New("region is required and bounded")
	}
	return nil
}

func (s *Service) Create(ownerID, mode, region string) (Lobby, error) {
	if ownerID == "" || mode == "" || region == "" {
		return Lobby{}, errors.New("owner, mode, and region are required")
	}
	if err := ValidateMode(mode); err != nil {
		return Lobby{}, err
	}
	if err := ValidateRegion(region); err != nil {
		return Lobby{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := fmt.Sprintf("lobby-%08d", s.nextID)
	lobby := &Lobby{ID: id, OwnerID: ownerID, Mode: mode, Region: region, Status: StatusOpen, Members: []Member{{AccountID: ownerID}}}
	s.lobbies[id] = lobby
	return clone(*lobby), nil
}

func (s *Service) Join(lobbyID, accountID string) (Lobby, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lobby, ok := s.lobbies[lobbyID]
	if !ok {
		return Lobby{}, errors.New("lobby not found")
	}
	if lobby.Status != StatusOpen {
		return Lobby{}, errors.New("lobby is not open")
	}
	if accountID == "" {
		return Lobby{}, errors.New("account is required")
	}
	for _, member := range lobby.Members {
		if member.AccountID == accountID {
			return clone(*lobby), nil
		}
	}
	if len(lobby.Members) >= s.maxParty {
		return Lobby{}, errors.New("lobby is full")
	}
	lobby.Members = append(lobby.Members, Member{AccountID: accountID})
	return clone(*lobby), nil
}

func (s *Service) Invite(lobbyID, inviterID, inviteeID string) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lobby, ok := s.lobbies[lobbyID]
	if !ok {
		return Invitation{}, errors.New("lobby not found")
	}
	if lobby.OwnerID != inviterID {
		return Invitation{}, errors.New("lobby owner required")
	}
	if lobby.Status != StatusOpen {
		return Invitation{}, errors.New("lobby is not open")
	}
	if inviteeID == "" || inviteeID == inviterID {
		return Invitation{}, errors.New("a different invitee is required")
	}
	for _, member := range lobby.Members {
		if member.AccountID == inviteeID {
			return Invitation{}, errors.New("account is already a lobby member")
		}
	}
	now := time.Now().UTC()
	for _, value := range s.invitations {
		if value.LobbyID == lobbyID && value.InviteeID == inviteeID && value.Status == InvitationPending && now.Before(value.ExpiresAt) {
			return cloneInvitation(*value), nil
		}
	}
	s.nextInviteID++
	value := &Invitation{ID: fmt.Sprintf("invite-%08d", s.nextInviteID), LobbyID: lobbyID, InviterID: inviterID, InviteeID: inviteeID, Status: InvitationPending, CreatedAt: now, ExpiresAt: now.Add(invitationTTL)}
	s.invitations[value.ID] = value
	return cloneInvitation(*value), nil
}

func (s *Service) ListInvitations(accountID string) ([]Invitation, error) {
	if accountID == "" {
		return nil, errors.New("account is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	result := make([]Invitation, 0)
	for _, value := range s.invitations {
		if value.InviteeID != accountID || value.Status != InvitationPending {
			continue
		}
		if !now.Before(value.ExpiresAt) {
			value.Status = InvitationExpired
			continue
		}
		result = append(result, cloneInvitation(*value))
	}
	return result, nil
}

func (s *Service) GetInvitation(invitationID string) (Invitation, error) {
	if invitationID == "" {
		return Invitation{}, errors.New("invitation id is required")
	}
	s.mu.RLock()
	value, ok := s.invitations[invitationID]
	s.mu.RUnlock()
	if !ok {
		return Invitation{}, errors.New("invitation not found")
	}
	return cloneInvitation(*value), nil
}

func (s *Service) AcceptInvitation(invitationID, accountID string) (Lobby, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.invitations[invitationID]
	if !ok {
		return Lobby{}, errors.New("invitation not found")
	}
	if value.InviteeID != accountID {
		return Lobby{}, errors.New("invitation recipient required")
	}
	if value.Status != InvitationPending {
		return Lobby{}, errors.New("invitation is no longer pending")
	}
	if !time.Now().UTC().Before(value.ExpiresAt) {
		value.Status = InvitationExpired
		return Lobby{}, errors.New("invitation expired")
	}
	lobby, ok := s.lobbies[value.LobbyID]
	if !ok {
		return Lobby{}, errors.New("lobby not found")
	}
	if lobby.Status != StatusOpen {
		return Lobby{}, errors.New("lobby is not open")
	}
	for _, member := range lobby.Members {
		if member.AccountID == accountID {
			value.Status = InvitationAccepted
			return clone(*lobby), nil
		}
	}
	if len(lobby.Members) >= s.maxParty {
		return Lobby{}, errors.New("lobby is full")
	}
	lobby.Members = append(lobby.Members, Member{AccountID: accountID})
	value.Status = InvitationAccepted
	return clone(*lobby), nil
}

func (s *Service) DeclineInvitation(invitationID, accountID string) (Invitation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.invitations[invitationID]
	if !ok {
		return Invitation{}, errors.New("invitation not found")
	}
	if value.InviteeID != accountID {
		return Invitation{}, errors.New("invitation recipient required")
	}
	if value.Status != InvitationPending {
		return Invitation{}, errors.New("invitation is no longer pending")
	}
	if !time.Now().UTC().Before(value.ExpiresAt) {
		value.Status = InvitationExpired
		return Invitation{}, errors.New("invitation expired")
	}
	value.Status = InvitationDeclined
	return cloneInvitation(*value), nil
}

func (s *Service) SetPresence(accountID string, online bool) (Presence, error) {
	if accountID == "" {
		return Presence{}, errors.New("account is required")
	}
	value := Presence{AccountID: accountID, Online: online, LastSeen: time.Now().UTC()}
	s.mu.Lock()
	s.presence[accountID] = value
	s.mu.Unlock()
	return value, nil
}

func (s *Service) GetPresence(accountID string) (Presence, error) {
	if accountID == "" {
		return Presence{}, errors.New("account is required")
	}
	s.mu.RLock()
	value, ok := s.presence[accountID]
	s.mu.RUnlock()
	if !ok {
		return Presence{AccountID: accountID}, nil
	}
	return value, nil
}

func (s *Service) SetReady(lobbyID, accountID string, ready bool) (Lobby, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lobby, ok := s.lobbies[lobbyID]
	if !ok {
		return Lobby{}, errors.New("lobby not found")
	}
	if lobby.Status != StatusOpen && lobby.Status != StatusReady {
		return Lobby{}, errors.New("lobby cannot change ready state")
	}
	found := false
	allReady := true
	for index := range lobby.Members {
		if lobby.Members[index].AccountID == accountID {
			lobby.Members[index].Ready = ready
			found = true
		}
		if !lobby.Members[index].Ready {
			allReady = false
		}
	}
	if !found {
		return Lobby{}, errors.New("member not found")
	}
	if allReady {
		lobby.Status = StatusReady
	} else {
		lobby.Status = StatusOpen
	}
	return clone(*lobby), nil
}

func (s *Service) Allocate(lobbyID string) (Lobby, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lobby, ok := s.lobbies[lobbyID]
	if !ok {
		return Lobby{}, errors.New("lobby not found")
	}
	if lobby.Status != StatusReady {
		return Lobby{}, errors.New("lobby is not ready")
	}
	lobby.Status = StatusAllocated
	return clone(*lobby), nil
}

func (s *Service) Close(lobbyID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	lobby, ok := s.lobbies[lobbyID]
	if !ok {
		return errors.New("lobby not found")
	}
	lobby.Status = StatusClosed
	return nil
}

func (s *Service) Get(lobbyID string) (Lobby, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lobby, ok := s.lobbies[lobbyID]
	if !ok {
		return Lobby{}, errors.New("lobby not found")
	}
	return clone(*lobby), nil
}

func clone(value Lobby) Lobby {
	value.Members = append([]Member(nil), value.Members...)
	return value
}

func cloneInvitation(value Invitation) Invitation { return value }
