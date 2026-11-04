# ROS Legacy project requirements traceability

This matrix traces the supplied project brief to current repository evidence.
It distinguishes the independently implemented ROS-style platform from the
supplied ROS Legacy client. No row marked `Blocked external` is represented as
original-client compatibility.

## Phase 1 — research and feasibility

| Requirement | Status | Evidence |
|---|---|---|
| IPA inventory: hash, integrity, metadata, architecture, frameworks, resources, signing indicators | Verified | `research/ipa-metadata.json`, `research/archive-inventory.json`, `research/framework-inventory.json`, `research/resource-summary.json`, `research/sha256.txt`, `research/findings.md` |
| Engine and network evidence documented without modifying the IPA | Verified | `research/findings.md`; preserved IPA hash is checked by the Phase 11 runner |
| Authorized protocol/integration path | Blocked external | No authorized protocol specification, SDK, credentials, or developer integration path is present |
| Original-client simulator/device compatibility | Blocked external | Phase 11 probe records device-only `iPhoneOS` arm64 IPA; physical iPhone is offline |

## Phase 2 — backend architecture

| Requirement | Status | Evidence |
|---|---|---|
| Modular Go backend with authentication, profiles, lobby, matchmaking, sessions, and statistics | Verified independent | `cmd/api-server`, `internal/auth`, `internal/profile`, `internal/lobby`, `internal/matchmaking`, `internal/session`, `internal/statistics` |
| PostgreSQL durable state and Redis ephemeral state | Verified independent | `internal/storage`, migrations `001`–`009`, shop wallet/inventory persistence, Docker Compose, live Phase 4/session-agent checks |
| Versioned HTTP contract and protected routes | Verified independent | `docs/openapi.yaml`, `docs/phase2-backend-api.md`, full API protocol suites |
| Authenticated store catalog, coin debit, purchase receipt, and persistent inventory | Verified independent | `internal/shop`, `migrations/009_shop.sql`, `/v1/shop/catalog`, `/v1/shop/purchase`, live emulator purchase response |
| Original ROS HTTP/wire compatibility | Not asserted | No undocumented ROS behavior is inferred or implemented |

## Phase 3 — authoritative game server

| Requirement | Status | Evidence |
|---|---|---|
| Versioned independent real-time transport with validation, sequence numbers, snapshots, reconnect, and fragmentation | Verified independent | `internal/game/server.go`, `internal/game/server_test.go`, protocol-client synchronization/reconnect/load runs |
| Authoritative movement, health, damage, knockdown, revive, elimination, spectator, and teams | Verified independent | Game-server tests and Phase 11 protocol matrix |
| Weapons, reload/ammunition, armor, grenades, attachments, and validation telemetry | Verified independent | Game-server implementation/tests and Phase 11 telemetry checks |
| Staging, aircraft/parachute, safe zone, loot, supply drops, vehicles, completion/results | Verified independent | Game-server implementation/tests and Phase 11 protocol matrix |
| 2/10/25/50/100-player profiles and network impairment | Verified independent | `artifacts/phase11/20261030T002205Z/report.md`, `artifacts/capacity/20261029T180544Z/`, and `artifacts/recovery/20261029T180615Z/` |
| Commercial ROS mechanics, anti-cheat, or undocumented wire behavior | Not asserted | Independent rules are explicitly proposed and not presented as ROS facts |

## Phase 4 — storage

| Requirement | Status | Evidence |
|---|---|---|
| Idempotent migrations, referential integrity, indexes, transaction boundaries | Verified independent | `migrations/001`–`009`, `scripts/phase4/verify.sh`, Go storage tests |
| Backup and isolated restore | Verified independent | `scripts/phase4/backup.sh`, `scripts/phase4/restore.sh`, recovery artifacts |
| Retention and ephemeral-state policy | Verified baseline | `docs/phase4-data-storage.md`, `docs/operations-runbook.md`; production scheduling and legal retention remain deployment-owned |
| Idempotent match-result event processing | Verified independent | `internal/storage/domain.go` conflict handling and storage tests |

## Phase 5 — project structure and delivery

| Requirement | Status | Evidence |
|---|---|---|
| Maintainable monorepo, Make targets, documentation, CI | Verified independent | Repository structure, `Makefile`, `docs/`, `.github/workflows/ci.yml` |
| Formatting, compilation, unit tests, race tests, vet | Verified | Current full Go test/race/vet runs |

## Phase 6 — security

| Requirement | Status | Evidence |
|---|---|---|
| Argon2id, 12–1024-byte password policy, token expiry/signatures/rotation, refresh-token hashing | Verified independent | `internal/security`, `internal/auth`, security/auth tests and race tests |
| Rate limiting, validation, authorization, audit logging, request limits | Verified independent | API/security implementation and protocol tests |
| TLS 1.3 configuration | Verified implementation | `internal/security/tls.go`, security tests; certificate custody and public termination are deployment-owned |
| Dependency vulnerability scanning | Verified | govulncheck reports zero reachable vulnerabilities; one uncalled module-level finding is documented |
| Secret manager, network segmentation, DDoS controls, operational role separation | Deployment-owned | Runbook and Kubernetes secret references document the required external controls; CI rejects Phase 11 test credentials in Kubernetes manifests |

## Phase 7 — testing

| Requirement | Status | Evidence |
|---|---|---|
| Backend, game-server, recovery, load, reconnect, and impairment tests | Verified independent | Go tests, `scripts/phase11`, `artifacts/phase11/20261030T002205Z/`, capacity/recovery/session-agent artifacts |
| Simulator-compatible independent iOS client with store and live gameplay vertical slice | Verified independent | Swift/XCUITest 2/2 passed in `artifacts/phase11/20261030T003906Z/`; purchase and live UDP state assertions are in the Xcode log |
| Original IPA handshake/session/lobby/match/reconnect compatibility | Blocked external | Requires authorized protocol information and an available physical device |

## Phase 8 — deployment

| Requirement | Status | Evidence |
|---|---|---|
| Docker API/game images and non-root runtime | Verified | Dockerfiles, current image builds, image metadata |
| Automated database migrations | Verified baseline | `Dockerfile.migrations`, migration runtime test, Kubernetes API init container |
| Development/staging/production Kubernetes overlays, health checks, resources, rollback/runbook | Verified baseline | Kustomize overlays, kubeconform, `docs/operations-runbook.md` |
| Metrics, scrape metadata, Prometheus rules, Grafana dashboard | Verified artifacts | `deployments/monitoring`, pod scrape annotations, internal game health/metrics `ClusterIP`, UDP-only public game service, promtool and kubeconform validation |
| Registry promotion, managed services, secret/certificate distribution, alert routing, retention, provider capacity, DDoS controls | Blocked external | Requires an authorized deployment platform and production accounts |

## Phases 9–10 — milestones and coding rules

| Requirement | Status | Evidence |
|---|---|---|
| Sequential milestones with explicit gates and limitations | Verified | `docs/phase9-milestones.md`, `docs/completion-audit.md` |
| Preserve IPA, read-only research, no invented ROS protocol, truthful testing | Verified | `docs/phase10-ai-coding-instructions.md`, research artifacts, `make verify-ipa` CI gate, unchanged IPA hash, test reports |

## Phase 11 — automated iOS and multiplayer testing

| Requirement | Status | Evidence |
|---|---|---|
| Detect host/Xcode/runtimes, boot simulator, collect logs/screenshots/crash paths | Verified independent | `scripts/phase11/run.sh`, latest Phase 11 report and result bundle |
| Start PostgreSQL/Redis/backend/game server and exercise API/game flows | Verified independent | `artifacts/phase11/20261030T003906Z/`, `artifacts/session-agent/20261029T180447Z/` |
| 2/10/25/50/100 players, latency/loss/disconnect/reconnect | Verified independent | `artifacts/phase11/20261029T180646Z/`, `artifacts/capacity/20261029T180544Z/`, and `artifacts/recovery/20261029T180615Z/` |
| Original IPA simulator or physical-device test | Blocked external | Device-only IPA, offline physical iPhone, and no authorized integration path |

## Overall gate

The independent server ecosystem is locally implemented and verified through
Phase 11. The supplied ROS Legacy client cannot be declared compatible without
the external authorization, protocol documentation, and physical-device state
listed above. Production deployment cannot be declared live without the
authorized registry, managed services, secret/certificate, alerting, and
provider-capacity environment.
