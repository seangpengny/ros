package auth

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"roslegacy/internal/security"
	"roslegacy/internal/storage"
)

type captureRecoverySender struct {
	mu        sync.Mutex
	email     string
	token     string
	expiresAt time.Time
}

func (s *captureRecoverySender) Send(_ context.Context, email, token string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.email, s.token, s.expiresAt = email, token, expiresAt
	return nil
}

func (s *captureRecoverySender) snapshot() (string, string, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.email, s.token, s.expiresAt
}

func TestRecoveryIntegration(t *testing.T) {
	if os.Getenv("ROS_INTEGRATION") != "1" {
		t.Skip("set ROS_INTEGRATION=1 with a migrated PostgreSQL database to run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := storage.NewPostgres(ctx, storage.PostgresConfig{URL: os.Getenv("DATABASE_URL"), MinConns: 1, MaxConns: 4})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	signer, err := security.NewTokenSigner([]byte("recovery-integration-secret-32-bytes-min"))
	if err != nil {
		t.Fatal(err)
	}
	sender := &captureRecoverySender{}
	service, err := NewServiceWithRecoverySender(database, signer, nil, nil, sender, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	email := "recovery-integration-" + time.Now().UTC().Format("20060102150405.000000000") + "@example.com"
	initial, err := service.Register(ctx, email, "old-password-12345", "Recovery Test", "recovery-device")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RequestRecovery(ctx, email); err != nil {
		t.Fatal(err)
	}
	sentEmail, token, expiresAt := sender.snapshot()
	if sentEmail != email || token == "" || !expiresAt.After(time.Now().UTC()) {
		t.Fatalf("recovery delivery = email %q token_present=%t expires_at=%s", sentEmail, token != "", expiresAt)
	}
	if err := service.CompleteRecovery(ctx, token, "new-password-12345"); err != nil {
		t.Fatal(err)
	}
	if err := service.CompleteRecovery(ctx, token, "new-password-12345"); !errors.Is(err, ErrRecoveryTokenInvalid) {
		t.Fatalf("replayed recovery token error = %v, want ErrRecoveryTokenInvalid", err)
	}
	if _, err := service.Login(ctx, email, "old-password-12345", "old-device"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("old password login error = %v, want ErrInvalidCredentials", err)
	}
	updated, err := service.Login(ctx, email, "new-password-12345", "new-device")
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessToken == "" || updated.RefreshToken == "" || initial.RefreshToken == updated.RefreshToken {
		t.Fatal("recovery login did not create a new session")
	}
	if _, err := service.Refresh(ctx, initial.RefreshToken, "old-device"); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("pre-recovery refresh error = %v, want ErrSessionRevoked", err)
	}
}
