package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookRecoverySenderSignsDelivery(t *testing.T) {
	const secret = "test-recovery-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
			return
		}
		digest := hmac.New(sha256.New, []byte(secret))
		_, _ = digest.Write(body)
		wantSignature := "sha256=" + hex.EncodeToString(digest.Sum(nil))
		if got := request.Header.Get("X-Recovery-Signature"); got != wantSignature {
			t.Errorf("signature = %q, want %q", got, wantSignature)
		}
		var payload struct {
			Email string `json:"email"`
			Token string `json:"token"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		if payload.Email != "player@example.com" || payload.Token != "opaque-token" {
			t.Errorf("payload = %+v", payload)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	sender, err := NewWebhookRecoverySender(server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), "player@example.com", "opaque-token", time.Unix(1_700_000_000, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookRecoverySenderRequiresSecret(t *testing.T) {
	if _, err := NewWebhookRecoverySender("https://example.com/recovery", ""); err == nil {
		t.Fatal("webhook sender accepted an empty signing secret")
	}
}
