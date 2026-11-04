# ROS Legacy project completion audit

This audit is for the independent ROS-style server and its authorized test
clients. It never treats the supplied ROS Legacy IPA as a substitute for an
independent client and never infers undocumented ROS wire behavior.

The detailed requirement-level matrix is maintained in
[`docs/requirements-traceability.md`](requirements-traceability.md).

## Phase status

| Phase | Current status | Authoritative evidence |
|---:|---|---|
| 1 | Complete with an evidence-based original-client no-go | IPA findings, metadata, and the Phase 11 simulator probe |
| 2 | Complete for the independent backend architecture/API | Architecture, API contract, and OpenAPI |
| 3 | Complete for the independent authoritative simulation | Game-server design, full Go tests, protocol matrix, and the 100-player profile |
| 4 | Complete for the local PostgreSQL/Redis baseline | Storage phase, migrations 001–009 including shop wallet/inventory state, backup/restore rehearsal, and scripts/phase4/verify.sh |
| 5 | Complete | Project structure, Makefile, CI workflow, and formatting/build checks |
| 6 | Complete for application security controls | Security phase; secret/certificate custody and network enforcement remain deployment-owned |
| 7 | Complete for independent automated testing | Testing phase, full test/race/vet runs, capacity benchmark, and crash/recovery report |
| 8 | Baseline complete; provider deployment gates remain | Deployment phase, API/game/migration images, automated migration init container, Kustomize overlays, scrape metadata, monitoring rules, backup/restore, capacity and recovery workflows |
| 9 | Complete as a verified milestone ledger | Milestones |
| 10 | Complete | AI coding and safety rules |
| 11 | Complete for the independent stack; original-client gate remains open | Phase 11 report and testing workflow |

## Executed evidence

- Phase 11 full-stack run 20261029T170122Z: 27 passed, 0 failed, 0
  blocked.
- Phase 11 full-stack rerun 20261030T173642Z: 31 passed, 0 failed, and 2
  expected blocked original-client gates. It also verified that the runner
  synchronizes reused local PostgreSQL volumes, applies migrations, isolates
  standalone game-server lifecycle variables, and captures complete telemetry.
  See artifacts/phase11/20261030T173642Z/.
- Fresh independent-stack rerun 20261029T175805Z (UTC; local date
  2026-10-30) passed 29 stages with 0 failures and 0 blocked stages using a
  plain environmentless invocation. It verified the new local Compose
  defaults, database setup, all protocol suites, 2/10/25/50/100-player
  profiles, network impairment, XCTest/XCUITest, and complete telemetry. See
  artifacts/phase11/20261029T175805Z/.
- Full compatibility-boundary rerun 20261029T180010Z (UTC; local date
  2026-10-30) passed 31 stages with 0 failures and 2 explicitly blocked
  original-client stages. It recorded the unchanged IPA hash and device-only
  metadata, while all independent server, protocol, load, impairment, and
  XCTest stages passed. See artifacts/phase11/20261029T180010Z/.
- Latest full Phase 11 rerun `20261029T181043Z` passed 31 stages with 0
  failures and 2 explicitly blocked original-client stages. It recorded 74
  resource samples with complete telemetry; the independent XCTest and all
  backend/load/impairment stages passed, and `make verify-ipa` still matched
  the recorded hash. See artifacts/phase11/20261029T181043Z/.
- Emulator-only rerun `20261030T002205Z` passed 29 stages with 0 failures and
  0 blocked stages on the iPhone 17 Pro iOS Simulator. It captured a simulator
  screenshot and XCTest result bundle, applied migrations, and passed all
  backend, load, impairment, and telemetry stages. See
  artifacts/phase11/20261030T002205Z/.
- Playable-client emulator rerun `20261030T003906Z` passed 29 stages with 0
  failures and 0 blocked stages. XCTest reported 2/2 passed, including a live
  store purchase (`starter_medkit`) followed by UDP match entry, live state,
  movement-control visibility, and disconnect. The run also passed the
  2/10/25/50/100-player profiles and network impairment. See
  artifacts/phase11/20261030T003906Z/.
- Visible emulator follow-up on the same independent client pressed `Run API
  flow` and observed `PASS: registration/login/profile/lobby/matchmaking`.
  The captured result is
  `artifacts/phase11/20261030T002205Z/screenshots/independent-client-api-pass.png`.
  The playable client now has shop/purchase UI and endpoint coverage; gameplay
  and loot/inventory remain verified through the real UDP protocol client and
  game tests as well as the simulator UI test.
- The emulator run first exposed a migration-log append failure in the test
  harness; `run.sh` now truncates that log before applying migrations, and the
  clean rerun above passed.
- Corrected backend-only Phase 11 workflow `20261029T180921Z` passed 25
  backend stages with 0 failures and 0 blocked stages, plus one explicit
  `SKIP` for Apple tooling. It validates storage startup, all HTTP/UDP
  protocol scenarios, 2/10/25/50/100-player profiles, impairment, and
  complete telemetry without running simulator, IPA, or XCTest stages. See
  artifacts/phase11/20261029T180921Z/.
- Independent iOS XCUITest: 1 passed on the iPhone 17 Pro iOS 26.0
  simulator.
- Full Go tests, full race tests, vet, build, and govulncheck: passed.
- Capacity run 20261029T170849Z: 2/10/25/50/100 players passed, zero tick
  overruns, maximum observed RTT 0.4ms, maximum tick duration 11.083ms,
  maximum sampled CPU 16.5%, and maximum sampled RSS 19,984 KiB. See the
  capacity report under artifacts/capacity/20261029T170849Z/.
- Recovery run 20261029T171049Z: forced process termination, restart
  readiness, and post-restart protocol session all passed. See the recovery
  report under artifacts/recovery/20261029T171049Z/.
- Session-agent run 20261030T002046Z: a real authenticated API session was
  created and admitted, the actual game-server binary reported running and
  heartbeat-healthy through the API, and graceful shutdown persisted complete
  state. See artifacts/session-agent/20261030T002046Z/.
- Session-agent rerun 20261030T174018Z: the same lifecycle path passed against
  the current binaries and local PostgreSQL/Redis services, with explicit
  `running`/`healthy` and `complete` state evidence. See
  artifacts/session-agent/20261030T174018Z/.
- Phase 4 verifier rerun on 2026-10-30: reused-volume PostgreSQL password
  synchronization, all eight idempotent migrations, Redis `PONG`, and the Go
  storage smoke test passed; containers were then stopped without deleting
  named volumes.
- The same Phase 4 verifier was executed again after CI integration with the
  test-only workflow credentials; migrations, Redis `PONG`, and storage smoke
  passed, and the Compose project was explicitly torn down afterward.
- Self-contained session-agent stack run `20261029T180447Z` passed: it started
  PostgreSQL/Redis, applied migrations, started the API, created and admitted a
  durable session, observed the actual game server as `running` and healthy,
  and persisted graceful `complete` state. The wrapper propagated one run ID
  across all artifacts and tore down its services afterward.
- Phase 6 password-contract hardening: the implementation now enforces the
  documented 12-byte minimum, with normal and race-tested security/auth suites
  plus the full Go test and vet suites passing.
- Post-change container rebuild: current API and game-server images rebuilt as
  non-root Linux arm64 images after the security change; IDs are recorded in
  docs/phase8-deployment.md.
- Phase 8 automated migration path: the `ros-migrations` image built and ran
  on the local Compose network, applying the then-current eight migrations
  idempotently;
  the API Kubernetes Deployment now runs the same image as a non-root init
  container. Kustomize, kubeconform, CI/release actionlint, and runtime image
  checks passed.
- Kubernetes API and game pods now declare Prometheus scrape metadata for their
  `/metrics` endpoints; rendered overlays remain schema-valid. Cluster
  discovery, retention, and notification routing remain deployment-owned.
- Deployment validation is now a checked-in `make validate-deploy` workflow
  covering all Kustomize overlays, kubeconform, Prometheus rules, Grafana JSON,
  both GitHub Actions workflows, and the UDP-only public game-service
  exposure and Kubernetes credential-hygiene invariants; it is invoked by CI.
- `make validate-deploy` was executed successfully after this change: each of
  base/dev/staging/prod had 7 valid Kubernetes resources, Prometheus reported
  3 base and 8 SLO rules, Kubernetes credential hygiene passed, Grafana JSON
  parsed, and actionlint passed.
- Current post-change verification also passed `make test vet build`: all Go
  packages passed, `go vet ./...` passed, and the API, game-server,
  protocol-client, and storage-smoke binaries rebuilt successfully.
- The same current tree passed `make race` across all Go packages.
- Current regression pass `make test vet build race capacity recovery` passed
  all Go tests/vet/build/race gates, the 2/10/25/50/100-player capacity
  profiles with zero tick overruns, and forced-crash recovery with readiness
  and post-restart protocol success. Evidence is under
  `artifacts/capacity/20261029T180544Z/` and
  `artifacts/recovery/20261029T180615Z/`.
- `make verify-ipa` passed against the preserved 846,004,086-byte IPA and
  matched the recorded SHA-256; CI now enforces this read-only preservation
  check before running the rest of the workflow.
- CI now executes the Phase 4 PostgreSQL/Redis verifier and the self-contained
  API/dedicated-server lifecycle integration with test-only credentials; both
  workflows tear down their Compose services, and the workflow passes
  actionlint validation.
- The migration init container has explicit CPU/memory requests and limits,
  and the rendered production overlay remains schema-valid.
- The migration image also passed a runtime test with a read-only root
  filesystem and all Linux capabilities dropped while applying the existing
  PostgreSQL schema.
- The game-server `LoadBalancer` now exposes only UDP gameplay; health and
  metrics use a separate internal `ClusterIP` service, and all rendered
  environments passed the 7-resource schema validation.
- Current-binary live API check: `registration_login` and `session_lifecycle`
  protocol suites passed against PostgreSQL/Redis; the lifecycle suite also
  confirmed unauthorized internal start is rejected before authorized
  start/heartbeat/complete succeed.
- The API session allocator now validates and round-robins a configurable
  ROS_GAME_SERVER_IDS pool, while retaining the single-server default; the
  pool behavior is covered by race-tested session unit tests.
- Prometheus rule validation: promtool reported 3 base rules and 8 SLO rules
  valid.
- Docker image builds, all Kubernetes Kustomize renders, and kubeconform
  validation of the base plus dev/staging/prod overlays passed.
- The post-change API and game-server images also built successfully on
  2026-10-30 as non-root Linux arm64 images; local image IDs are recorded in
  docs/phase8-deployment.md.
- CI and tag-triggered release workflows are present; the release workflow is
  configured for GHCR publication with provenance/SBOM output, but has not been
  executed against an authorized repository registry from this workspace. Both
  workflow files pass actionlint 1.7.7.

## Requirements that cannot be honestly closed in this workspace

1. The IPA is a signed, device-only iPhoneOS arm64 build. The simulator
   rejected its launch, so the independent simulator client is not evidence
   that the original client works.
2. No authorized ROS server protocol specification, SDK, or developer
   integration path is available. Therefore handshake, session establishment,
   lobby, match join, disconnect, and reconnect compatibility with the original
   client cannot be implemented or claimed.
3. The only discovered physical iPhone is offline and no usable USB device
   session is present. No original-client device test was attempted.
4. Registry promotion, managed PostgreSQL/Redis, production TLS/certificate
   distribution, secret-manager integration, OTel collector retention,
   notification routing, provider-side capacity validation, and upstream DDoS
   controls require an authorized deployment environment. The repository
   contains the reviewed artifacts and runbooks, not a live production
   deployment.

Closing these items requires the corresponding authorized external state change;
it cannot be achieved by modifying the IPA, bypassing signing/security controls,
or inventing a protocol.

