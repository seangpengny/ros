package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"roslegacy/internal/security"
)

var ErrRecoveryTokenInvalid = errors.New("invalid recovery token")

// RecoverySender delivers a reset token through a separately operated,
// authenticated delivery system. The auth service never returns the token in
// an API response or writes it to logs.
type RecoverySender interface {
	Send(context.Context, string, string, time.Time) error
}

type WebhookRecoverySender struct {
	endpoint *url.URL
	secret   []byte
	client   *http.Client
}

func NewWebhookRecoverySender(endpoint, secret string) (*WebhookRecoverySender, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, errors.New("recovery webhook must be an http or https URL")
	}
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("recovery webhook secret is required")
	}
	return &WebhookRecoverySender{endpoint: parsed, secret: []byte(secret), client: &http.Client{Timeout: 5 * time.Second}}, nil
}

func (s *WebhookRecoverySender) Send(ctx context.Context, email, token string, expiresAt time.Time) error {
	payload, err := json.Marshal(struct {
		Email     string    `json:"email"`
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}{Email: email, Token: token, ExpiresAt: expiresAt.UTC()})
	if err != nil {
		return fmt.Errorf("encode recovery delivery: %w", err)
	}
	digest := hmac.New(sha256.New, s.secret)
	_, _ = digest.Write(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create recovery delivery request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Recovery-Signature", "sha256="+hex.EncodeToString(digest.Sum(nil)))
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("send recovery delivery: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("recovery delivery returned status %d", response.StatusCode)
	}
	return nil
}

func (s *Service) RequestRecovery(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := security.ValidateEmail(email); err != nil {
		return err
	}
	if s.recoverySender == nil {
		return nil
	}
	var accountID string
	if err := s.db.QueryRow(ctx, `SELECT id FROM accounts WHERE email = $1`, email).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("find recovery account: %w", err)
	}
	token, err := security.NewOpaqueToken()
	if err != nil {
		return fmt.Errorf("generate recovery token: %w", err)
	}
	expiresAt := time.Now().UTC().Add(s.recoveryTTL)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin recovery request: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE account_recovery_tokens SET used_at = now() WHERE account_id = $1 AND used_at IS NULL`, accountID); err != nil {
		return fmt.Errorf("expire previous recovery tokens: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO account_recovery_tokens(account_id, token_hash, expires_at) VALUES($1, $2, $3)`, accountID, security.HashOpaqueToken(token), expiresAt); err != nil {
		return fmt.Errorf("store recovery token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit recovery request: %w", err)
	}
	if err := s.recoverySender.Send(ctx, email, token, expiresAt); err != nil {
		security.RecordAudit(s.logger, security.AuditEvent{Event: "recovery_delivery", Subject: accountID, Success: false})
		return fmt.Errorf("deliver recovery token: %w", err)
	}
	security.RecordAudit(s.logger, security.AuditEvent{Event: "recovery_requested", Subject: accountID, Success: true})
	return nil
}

func (s *Service) CompleteRecovery(ctx context.Context, token, password string) error {
	if strings.TrimSpace(token) == "" || security.ValidatePassword(password) != nil {
		return ErrRecoveryTokenInvalid
	}
	passwordHash, err := security.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash recovery password: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin recovery completion: %w", err)
	}
	defer tx.Rollback(ctx)
	var accountID string
	if err := tx.QueryRow(ctx, `
		SELECT account_id FROM account_recovery_tokens
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		FOR UPDATE`, security.HashOpaqueToken(token)).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRecoveryTokenInvalid
		}
		return fmt.Errorf("find recovery token: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET password_hash = $2, updated_at = now() WHERE id = $1`, accountID, passwordHash); err != nil {
		return fmt.Errorf("update recovered password: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE device_sessions SET revoked_at = now() WHERE account_id = $1 AND revoked_at IS NULL`, accountID); err != nil {
		return fmt.Errorf("revoke recovered sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE account_recovery_tokens SET used_at = now() WHERE token_hash = $1`, security.HashOpaqueToken(token)); err != nil {
		return fmt.Errorf("consume recovery token: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit recovery completion: %w", err)
	}
	security.RecordAudit(s.logger, security.AuditEvent{Event: "recovery_completed", Subject: accountID, Success: true})
	return nil
}
