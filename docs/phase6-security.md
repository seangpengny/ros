# Phase 6 — security foundation

Status: foundational controls implemented, unit-tested, and wired into the
independent API.

## Implemented controls

- Argon2id password hashing with per-password cryptographic salts and a
  versioned encoded format.
- Password policy enforces 12–1024 bytes, matching the published OpenAPI
  contract for registration, login, and recovery completion.
- Constant-time password hash comparison.
- HMAC-SHA-256 signed short-lived access tokens with expiry validation.
- Signing-key rotation support: the API issues with `TOKEN_SIGNING_SECRET` and
  can temporarily verify `TOKEN_SIGNING_PREVIOUS_SECRET` during rollout.
- Cryptographically random opaque refresh-token generation and SHA-256
  persistence hashing; raw refresh tokens are never stored by the helper.
- Owner-scoped device-session listing and revocation, plus one-time account
  recovery tokens hashed at rest. Recovery completion uses Argon2id, revokes
  existing sessions, and never returns or logs the raw token. Delivery is
  injected through an optional HMAC-authenticated webhook and remains a
  deployment-managed integration.
- Bounded in-memory sliding-window rate limiter for deterministic local tests,
  plus an atomic Redis sorted-set sliding-window limiter wired into production
  register/login paths.
- Email and identifier validation helpers.
- Pinned `govulncheck` supply-chain scanning in CI. With the fixed Go
  `go1.26.10` toolchain, the local scan reported zero reachable vulnerabilities;
  the scanner still reports the unmaintained `x/crypto/openpgp` package as an
  uncalled module-level finding because Argon2id is provided by the same module.
- TLS 1.3 minimum server configuration helper, wired to the API through the
  optional `ROS_API_TLS_CERT_FILE` and `ROS_API_TLS_KEY_FILE` environment
  variables; both variables are required together.
- Structured security audit events that accept only redacted identity/device
  fields and never log passwords, tokens, or authorization headers.
- Administrative operations are deliberately not exposed through the player
  API. Operational access belongs to a separately network-segmented deployment
  control plane with deployment-managed credentials, keeping player tokens
  from acquiring administrative authority.

## Verification

```sh
go test ./...
```

The password, token expiry/signature, signing-key rotation, rate-limit, and
validation, recovery-delivery signature, and opt-in PostgreSQL recovery
integration tests pass.
The package uses `golang.org/x/crypto/argon2`. The API now adds account
enumeration-safe login errors, rate limits, rotating refresh tokens,
authorization middleware, request-size limits, and environment-only secret
loading. Production still requires an external secret manager for key/cert
distribution and a reviewed certificate rotation policy; the API can serve
TLS directly when its certificate and key paths are provisioned.
