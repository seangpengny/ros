package session

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type fakeStore struct {
	next         int
	sessions     map[string]Session
	participants map[string][]Participant
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: make(map[string]Session), participants: make(map[string][]Participant)}
}

func (s *fakeStore) CreateGameSession(_ context.Context, lobbyID, mode, region, assignedServer string, maxPlayers int) (Session, error) {
	s.next++
	value := Session{ID: fmt.Sprintf("session-%d", s.next), LobbyID: lobbyID, Mode: mode, Region: region, AssignedServer: assignedServer, Status: StatusAllocating, MaxPlayers: maxPlayers}
	s.sessions[value.ID] = value
	return value, nil
}

func (s *fakeStore) GetGameSession(_ context.Context, id string) (Session, error) {
	value, ok := s.sessions[id]
	if !ok {
		return Session{}, fmt.Errorf("session %s not found", id)
	}
	return value, nil
}

func (s *fakeStore) UpdateGameSession(_ context.Context, id string, status Status, assignedServer string, startedAt, completedAt *time.Time) (Session, error) {
	value, err := s.GetGameSession(context.Background(), id)
	if err != nil {
		return Session{}, err
	}
	value.Status = status
	value.AssignedServer = assignedServer
	if startedAt != nil {
		value.StartedAt = startedAt
	}
	if completedAt != nil {
		value.CompletedAt = completedAt
	}
	s.sessions[id] = value
	return value, nil
}

func (s *fakeStore) AdmitGamePlayer(_ context.Context, id, accountID, teamID string) error {
	for _, value := range s.participants[id] {
		if value.AccountID == accountID {
			return nil
		}
	}
	now := time.Now().UTC()
	s.participants[id] = append(s.participants[id], Participant{AccountID: accountID, TeamID: teamID, ConnectedAt: &now})
	return nil
}

func (s *fakeStore) ListGameParticipants(_ context.Context, id string) ([]Participant, error) {
	return append([]Participant(nil), s.participants[id]...), nil
}

func TestSessionLifecycleAndAdmission(t *testing.T) {
	store := newFakeStore()
	service, err := NewService(store, nil, "game-server-1")
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Create(context.Background(), "lobby-1", "duo", "ap-southeast", 2)
	if err != nil || value.Status != StatusAllocating || value.AssignedServer != "game-server-1" {
		t.Fatalf("create = %+v err=%v", value, err)
	}
	if err := service.Admit(context.Background(), value.ID, "account-1", "team-1"); err != nil {
		t.Fatal(err)
	}
	if err := service.Admit(context.Background(), value.ID, "account-1", "team-1"); err != nil {
		t.Fatal("idempotent admission: ", err)
	}
	started, err := service.Start(context.Background(), value.ID, "game-server-1")
	if err != nil || started.Status != StatusRunning || started.StartedAt == nil {
		t.Fatalf("start = %+v err=%v", started, err)
	}
	if _, err := service.Heartbeat(context.Background(), value.ID, "wrong-server"); err == nil {
		t.Fatal("expected server assignment guard")
	}
	completed, err := service.Complete(context.Background(), value.ID, "game-server-1")
	if err != nil || completed.Status != StatusComplete || completed.CompletedAt == nil || len(completed.Participants) != 1 {
		t.Fatalf("complete = %+v err=%v", completed, err)
	}
	if err := service.Admit(context.Background(), value.ID, "account-2", "team-1"); err == nil {
		t.Fatal("expected admission rejection after completion")
	}
}

func TestSessionValidation(t *testing.T) {
	service, err := NewService(newFakeStore(), nil, "server")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		mode string
		max  int
	}{
		{name: "unknown mode", mode: "battle_royale", max: 2},
		{name: "too small", mode: "duo", max: 1},
		{name: "too large", mode: "duo", max: 101},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Create(context.Background(), "lobby-1", test.mode, "na", test.max); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSessionServerPoolRoundRobin(t *testing.T) {
	store := newFakeStore()
	service, err := NewServiceWithServers(store, nil, []string{"game-server-a", "game-server-b", "game-server-a"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"game-server-a", "game-server-b", "game-server-a", "game-server-b"}
	for index, expected := range want {
		value, createErr := service.Create(context.Background(), fmt.Sprintf("lobby-%d", index), "duo", "na", 2)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if value.AssignedServer != expected {
			t.Fatalf("session %d assigned server = %q, want %q", index, value.AssignedServer, expected)
		}
	}
	if service.AssignedServerID() != "game-server-a" {
		t.Fatalf("primary assigned server = %q", service.AssignedServerID())
	}
}

func TestSessionServerPoolRequiresServer(t *testing.T) {
	if _, err := NewServiceWithServers(newFakeStore(), nil, nil); err == nil {
		t.Fatal("empty server pool was accepted")
	}
}
