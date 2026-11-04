package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"roslegacy/internal/security"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrSessionRevoked     = errors.New("session revoked or expired")
)

type Service struct {
	db             *pgxpool.Pool
	signer         *security.TokenSigner
	accessTTL      time.Duration
	refreshTTL     time.Duration
	recoveryTTL    time.Duration
	recoverySender RecoverySender
	loginLimiter   security.Limiter
	registerLimit  security.Limiter
	logger         *slog.Logger
}

type Session struct {
	SessionID    string `json:"session_id"`
	AccountID    string `json:"account_id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type DeviceSession struct {
	ID        string    `json:"id"`
	DeviceID  string    `json:"device_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func NewService(db *pgxpool.Pool, signer *security.TokenSigner, logger *slog.Logger) (*Service, error) {
	return NewServiceWithLimiters(db, signer, nil, nil, logger)
}

func NewServiceWithLimiters(db *pgxpool.Pool, signer *security.TokenSigner, loginLimiter, registerLimiter security.Limiter, logger *slog.Logger) (*Service, error) {
	return NewServiceWithRecoverySender(db, signer, loginLimiter, registerLimiter, nil, logger)
}

func NewServiceWithRecoverySender(db *pgxpool.Pool, signer *security.TokenSigner, loginLimiter, registerLimiter security.Limiter, recoverySender RecoverySender, logger *slog.Logger) (*Service, error) {
	if db == nil || signer == nil {
		return nil, errors.New("database and token signer are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if loginLimiter == nil {
		loginLimiter = security.NewRateLimiter(10, time.Minute)
	}
	if registerLimiter == nil {
		registerLimiter = security.NewRateLimiter(5, time.Hour)
	}
	return &Service{
		db:             db,
		signer:         signer,
		accessTTL:      15 * time.Minute,
		refreshTTL:     30 * 24 * time.Hour,
		recoveryTTL:    30 * time.Minute,
		recoverySender: recoverySender,
		loginLimiter:   loginLimiter,
		registerLimit:  registerLimiter,
		logger:         logger,
	}, nil
}

func (s *Service) Register(ctx context.Context, email, password, displayName, deviceID string) (Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := security.ValidateEmail(email); err != nil {
		return Session{}, err
	}
	if err := security.ValidatePassword(password); err != nil {
		return Session{}, err
	}
	if displayName == "" {
		displayName = strings.Split(email, "@")[0]
	}
	if err := security.ValidateIdentifier(displayName); err != nil {
		return Session{}, err
	}
	if deviceID == "" {
		deviceID = "unknown"
	}
	now := time.Now().UTC()
	if !s.registerLimit.Allow(email, now) {
		return Session{}, errors.New("registration rate limit exceeded")
	}
	passwordHash, err := security.HashPassword(password)
	if err != nil {
		return Session{}, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin registration: %w", err)
	}
	defer tx.Rollback(ctx)
	var accountID string
	if err := tx.QueryRow(ctx, `INSERT INTO accounts(email, password_hash) VALUES($1, $2) RETURNING id`, email, passwordHash).Scan(&accountID); err != nil {
		return Session{}, fmt.Errorf("create account: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO profiles(account_id, display_name) VALUES($1, $2)`, accountID, displayName); err != nil {
		return Session{}, fmt.Errorf("create profile: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO player_statistics(account_id) VALUES($1)`, accountID); err != nil {
		return Session{}, fmt.Errorf("create player statistics: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO player_wallets(account_id) VALUES($1) ON CONFLICT (account_id) DO NOTHING`, accountID); err != nil {
		return Session{}, fmt.Errorf("create player wallet: %w", err)
	}
	result, err := s.createSession(ctx, tx, accountID, deviceID, now)
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit registration: %w", err)
	}
	signerToken, _, err := s.signer.Issue(accountID, now, s.accessTTL)
	if err != nil {
		return Session{}, err
	}
	result.AccessToken = signerToken
	security.RecordAudit(s.logger, security.AuditEvent{Event: "account_registered", Subject: accountID, DeviceID: deviceID, Success: true})
	return result, nil
}

func (s *Service) Login(ctx context.Context, email, password, deviceID string) (Session, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if err := security.ValidateEmail(email); err != nil || security.ValidatePassword(password) != nil {
		return Session{}, ErrInvalidCredentials
	}
	if !s.loginLimiter.Allow(email, time.Now().UTC()) {
		return Session{}, errors.New("login rate limit exceeded")
	}
	var accountID, passwordHash string
	if err := s.db.QueryRow(ctx, `SELECT id, password_hash FROM accounts WHERE email = $1`, email).Scan(&accountID, &passwordHash); err != nil {
		security.RecordAudit(s.logger, security.AuditEvent{Event: "login", Subject: email, DeviceID: deviceID, Success: false})
		return Session{}, ErrInvalidCredentials
	}
	valid, err := security.VerifyPassword(passwordHash, password)
	if err != nil || !valid {
		security.RecordAudit(s.logger, security.AuditEvent{Event: "login", Subject: accountID, DeviceID: deviceID, Success: false})
		return Session{}, ErrInvalidCredentials
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin login: %w", err)
	}
	defer tx.Rollback(ctx)
	result, err := s.createSession(ctx, tx, accountID, deviceID, time.Now().UTC())
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit login: %w", err)
	}
	result.AccessToken, _, err = s.signer.Issue(accountID, time.Now().UTC(), s.accessTTL)
	if err != nil {
		return Session{}, err
	}
	security.RecordAudit(s.logger, security.AuditEvent{Event: "login", Subject: accountID, DeviceID: deviceID, Success: true})
	return result, nil
}

func (s *Service) Refresh(ctx context.Context, refreshToken, deviceID string) (Session, error) {
	if refreshToken == "" {
		return Session{}, ErrSessionRevoked
	}
	hash := security.HashOpaqueToken(refreshToken)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin refresh: %w", err)
	}
	defer tx.Rollback(ctx)
	var sessionID, accountID string
	if err := tx.QueryRow(ctx, `SELECT id, account_id FROM device_sessions WHERE refresh_token_hash = $1 AND revoked_at IS NULL AND expires_at > now() FOR UPDATE`, hash).Scan(&sessionID, &accountID); err != nil {
		return Session{}, ErrSessionRevoked
	}
	if _, err := tx.Exec(ctx, `UPDATE device_sessions SET revoked_at = now() WHERE id = $1`, sessionID); err != nil {
		return Session{}, fmt.Errorf("revoke refresh token: %w", err)
	}
	result, err := s.createSession(ctx, tx, accountID, deviceID, time.Now().UTC())
	if err != nil {
		return Session{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit refresh: %w", err)
	}
	result.AccessToken, _, err = s.signer.Issue(accountID, time.Now().UTC(), s.accessTTL)
	if err != nil {
		return Session{}, err
	}
	security.RecordAudit(s.logger, security.AuditEvent{Event: "refresh_rotated", Subject: accountID, DeviceID: deviceID, Success: true})
	return result, nil
}

func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	if refreshToken == "" {
		return ErrSessionRevoked
	}
	var accountID string
	err := s.db.QueryRow(ctx, `UPDATE device_sessions SET revoked_at = now() WHERE refresh_token_hash = $1 AND revoked_at IS NULL RETURNING account_id`, security.HashOpaqueToken(refreshToken)).Scan(&accountID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("revoke session: %w", err)
	}
	security.RecordAudit(s.logger, security.AuditEvent{Event: "logout", Subject: accountID, Success: true})
	return nil
}

func (s *Service) ListSessions(ctx context.Context, accountID string) ([]DeviceSession, error) {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, device_id, expires_at, created_at
		FROM device_sessions
		WHERE account_id = $1 AND revoked_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, fmt.Errorf("list device sessions: %w", err)
	}
	defer rows.Close()
	result := make([]DeviceSession, 0)
	for rows.Next() {
		var session DeviceSession
		if err := rows.Scan(&session.ID, &session.DeviceID, &session.ExpiresAt, &session.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan device session: %w", err)
		}
		result = append(result, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read device sessions: %w", err)
	}
	return result, nil
}

func (s *Service) RevokeSession(ctx context.Context, accountID, sessionID string) error {
	if err := security.ValidateIdentifier(accountID); err != nil {
		return err
	}
	if err := security.ValidateIdentifier(sessionID); err != nil {
		return ErrSessionRevoked
	}
	var revokedAccount string
	err := s.db.QueryRow(ctx, `
		UPDATE device_sessions SET revoked_at = now()
		WHERE id = $1 AND account_id = $2 AND revoked_at IS NULL
		RETURNING account_id`, sessionID, accountID).Scan(&revokedAccount)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionRevoked
		}
		return fmt.Errorf("revoke device session: %w", err)
	}
	security.RecordAudit(s.logger, security.AuditEvent{Event: "device_session_revoked", Subject: accountID, Success: true})
	return nil
}

func (s *Service) VerifyAccess(token string, now time.Time) (security.Claims, error) {
	return s.signer.Verify(token, now)
}

func (s *Service) IssueToken(accountID string, ttl time.Duration) (string, error) {
	token, _, err := s.signer.Issue(accountID, time.Now().UTC(), ttl)
	return token, err
}

func (s *Service) createSession(ctx context.Context, tx pgx.Tx, accountID, deviceID string, now time.Time) (Session, error) {
	refresh, err := security.NewOpaqueToken()
	if err != nil {
		return Session{}, err
	}
	var sessionID string
	if err := tx.QueryRow(ctx, `INSERT INTO device_sessions(account_id, refresh_token_hash, device_id, expires_at) VALUES($1, $2, $3, $4) RETURNING id`, accountID, security.HashOpaqueToken(refresh), deviceID, now.Add(s.refreshTTL)).Scan(&sessionID); err != nil {
		return Session{}, fmt.Errorf("create device session: %w", err)
	}
	return Session{SessionID: sessionID, AccountID: accountID, RefreshToken: refresh}, nil
}
