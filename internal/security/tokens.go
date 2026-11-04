package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Claims struct {
	Subject   string `json:"sub"`
	TokenID   string `json:"jti"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
}

type TokenSigner struct{ secrets [][]byte }

func NewTokenSigner(secret []byte) (*TokenSigner, error) {
	return NewRotatingTokenSigner(secret)
}

// NewRotatingTokenSigner issues tokens with current and accepts tokens signed
// by current or any configured previous key. Previous keys can be removed
// after the longest access-token TTL has elapsed.
func NewRotatingTokenSigner(current []byte, previous ...[]byte) (*TokenSigner, error) {
	keys := make([][]byte, 0, len(previous)+1)
	for index, secret := range append([][]byte{current}, previous...) {
		if len(secret) < 32 {
			if index == 0 {
				return nil, errors.New("token signing secret must be at least 32 bytes")
			}
			return nil, errors.New("previous token signing secret must be at least 32 bytes")
		}
		keys = append(keys, append([]byte(nil), secret...))
	}
	return &TokenSigner{secrets: keys}, nil
}

func (s *TokenSigner) activeSecret() ([]byte, error) {
	if s == nil || len(s.secrets) == 0 {
		return nil, errors.New("token signer is not configured")
	}
	return s.secrets[0], nil
}

func (s *TokenSigner) Issue(subject string, now time.Time, ttl time.Duration) (string, Claims, error) {
	if s == nil || subject == "" || ttl <= 0 {
		return "", Claims{}, errors.New("subject, signer, and positive token TTL are required")
	}
	id, err := randomBytes(16)
	if err != nil {
		return "", Claims{}, err
	}
	claims := Claims{Subject: subject, TokenID: base64.RawURLEncoding.EncodeToString(id), IssuedAt: now.Unix(), ExpiresAt: now.Add(ttl).Unix()}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", Claims{}, err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	message := "v1." + encodedPayload
	secret, err := s.activeSecret()
	if err != nil {
		return "", Claims{}, err
	}
	signature := signWithSecret(secret, []byte(message))
	return message + "." + base64.RawURLEncoding.EncodeToString(signature), claims, nil
}

func (s *TokenSigner) Verify(token string, now time.Time) (Claims, error) {
	parts := strings.Split(token, ".")
	if s == nil || len(parts) != 3 || parts[0] != "v1" {
		return Claims{}, errors.New("invalid access token")
	}
	actual, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !s.matchesAnyKey([]byte(parts[0]+"."+parts[1]), actual) {
		return Claims{}, errors.New("invalid access token signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("invalid access token payload")
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Subject == "" || claims.TokenID == "" {
		return Claims{}, errors.New("invalid access token claims")
	}
	if now.Unix() >= claims.ExpiresAt {
		return Claims{}, errors.New("access token expired")
	}
	return claims, nil
}

func NewOpaqueToken() (string, error) {
	value, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func HashOpaqueToken(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func (s *TokenSigner) matchesAnyKey(value, actual []byte) bool {
	if s == nil {
		return false
	}
	for _, secret := range s.secrets {
		if hmac.Equal(signWithSecret(secret, value), actual) {
			return true
		}
	}
	return false
}

func signWithSecret(secret, value []byte) []byte {
	h := hmac.New(sha256.New, secret)
	_, _ = h.Write(value)
	return h.Sum(nil)
}

func randomBytes(size int) ([]byte, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return nil, fmt.Errorf("generate random token: %w", err)
	}
	return value, nil
}
