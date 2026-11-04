package session

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLifecycleClientSendsAuthorizedActions(t *testing.T) {
	var (
		mu      sync.Mutex
		actions []string
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		actions = append(actions, request.URL.Path)
		mu.Unlock()
		if request.Header.Get("X-Game-Server-ID") != "server-a" || request.Header.Get("X-Game-Server-Token") != "token-a" {
			t.Errorf("lifecycle headers = id %q token %q", request.Header.Get("X-Game-Server-ID"), request.Header.Get("X-Game-Server-Token"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(Session{ID: "session-a", AssignedServer: "server-a", Status: StatusRunning, Healthy: true})
	}))
	defer server.Close()

	client, err := NewLifecycleClient(server.URL, "session-a", "server-a", "token-a", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []func(context.Context) (Session, error){client.Start, client.Heartbeat, client.Complete} {
		result, actionErr := action(context.Background())
		if actionErr != nil {
			t.Fatal(actionErr)
		}
		if result.ID != "session-a" || result.AssignedServer != "server-a" {
			t.Fatalf("lifecycle result = %+v", result)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{"/v1/sessions/session-a/start", "/v1/sessions/session-a/heartbeat", "/v1/sessions/session-a/complete"}
	if len(actions) != len(want) {
		t.Fatalf("actions = %v, want %v", actions, want)
	}
	for index := range want {
		if actions[index] != want[index] {
			t.Fatalf("actions[%d] = %q, want %q", index, actions[index], want[index])
		}
	}
}

func TestLifecycleClientHeartbeatLoopStopsWithContext(t *testing.T) {
	var heartbeats int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		heartbeats++
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(Session{ID: "session-a", Status: StatusRunning})
	}))
	defer server.Close()

	client, err := NewLifecycleClient(server.URL, "session-a", "server-a", "token-a", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.HeartbeatLoop(ctx, time.Millisecond) }()
	time.Sleep(5 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if heartbeats == 0 {
		t.Fatal("heartbeat loop made no request")
	}
}

func TestNewLifecycleClientRejectsInvalidConfiguration(t *testing.T) {
	for name, values := range map[string][4]string{
		"bad url":       {"not-a-url", "session", "server", "token"},
		"bad session":   {"http://example.com", "", "server", "token"},
		"bad server":    {"http://example.com", "session", "", "token"},
		"missing token": {"http://example.com", "session", "server", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewLifecycleClient(values[0], values[1], values[2], values[3], nil); err == nil {
				t.Fatal("NewLifecycleClient accepted invalid configuration")
			}
		})
	}
}

func TestLifecycleClientReportsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "denied", http.StatusForbidden)
	}))
	defer server.Close()
	client, err := NewLifecycleClient(server.URL, "session-a", "server-a", "token-a", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Heartbeat(context.Background()); err == nil {
		t.Fatal("Heartbeat accepted forbidden response")
	}
}
