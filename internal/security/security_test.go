package security

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil || !ok {
		t.Fatalf("expected password verification to pass: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong password")
	if err == nil && ok {
		t.Fatal("wrong password verified")
	}
}

func TestTokenSigner(t *testing.T) {
	signer, err := NewTokenSigner([]byte("01234567890123456789012345678901"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	token, issued, err := signer.Issue("account-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := signer.Verify(token, now.Add(30*time.Second))
	if err != nil || claims.Subject != issued.Subject {
		t.Fatalf("token verification failed: claims=%+v err=%v", claims, err)
	}
	if _, err := signer.Verify(token, now.Add(2*time.Minute)); err == nil {
		t.Fatal("expired token verified")
	}
}

func TestRotatingTokenSignerAcceptsPreviousKeyAndIssuesCurrentKey(t *testing.T) {
	previous := []byte("previous-token-signing-secret-012345")
	current := []byte("current-token-signing-secret-012345")
	now := time.Unix(1_700_000_000, 0)
	oldSigner, err := NewTokenSigner(previous)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, _, err := oldSigner.Issue("account-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	rotatingSigner, err := NewRotatingTokenSigner(current, previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rotatingSigner.Verify(oldToken, now); err != nil {
		t.Fatalf("previous signing key was not accepted during rotation: %v", err)
	}
	newToken, _, err := rotatingSigner.Issue("account-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	currentOnly, err := NewTokenSigner(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := currentOnly.Verify(newToken, now); err != nil {
		t.Fatalf("current signing key did not verify newly issued token: %v", err)
	}
	if _, err := currentOnly.Verify(oldToken, now); err == nil {
		t.Fatal("previous signing key remained accepted after rotation window closure")
	}
}

func TestRateLimiter(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	if !limiter.Allow("ip", now) || !limiter.Allow("ip", now.Add(time.Second)) || limiter.Allow("ip", now.Add(2*time.Second)) {
		t.Fatal("rate limiter did not enforce the configured window")
	}
	if !limiter.Allow("ip", now.Add(61*time.Second)) {
		t.Fatal("rate limiter did not expire the old window")
	}
}

func TestValidation(t *testing.T) {
	if ValidateEmail("not-an-email") == nil || ValidateEmail("player@example.com") != nil {
		t.Fatal("email validation result is incorrect")
	}
	if ValidateIdentifier("") == nil || ValidateIdentifier("player-1") != nil {
		t.Fatal("identifier validation result is incorrect")
	}
}

func TestPasswordMinimumMatchesAPIContract(t *testing.T) {
	if ValidatePassword("12345678901") == nil {
		t.Fatal("11-byte password was accepted")
	}
	if err := ValidatePassword("123456789012"); err != nil {
		t.Fatalf("12-byte password was rejected: %v", err)
	}
}

func TestLoadServerTLSConfigRequiresTLS13(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	certFile := filepath.Join(directory, "server.crt")
	keyFile := filepath.Join(directory, "server.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)}), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadServerTLSConfig(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	if config.MinVersion != tls.VersionTLS13 || len(config.Certificates) != 1 {
		t.Fatalf("unexpected TLS configuration: min=%d certificates=%d", config.MinVersion, len(config.Certificates))
	}
}
