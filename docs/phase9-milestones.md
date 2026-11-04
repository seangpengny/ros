# Phase 9 — development milestones

Status: milestone ledger established from the verified work.

| Phase | Status | Evidence / remaining gate |
|---:|---|---|
| 1 | Complete with compatibility no-go | Read-only IPA inventory and simulator probe in `research/`. |
| 2 | Complete as architecture and independent API baseline | Independent modular-monolith boundaries plus the HTTP auth/lobby/presence/invitation/matchmaking/session API in `docs/phase2-backend-architecture.md` and `docs/phase2-backend-api.md`. |
| 3 | Complete independent simulation baseline | Authoritative UDP server covers movement, team/revive rules, rifle/pistol/shotgun ammo/reload, grenade area damage, scope attachment state/effects, validation-violation telemetry, knockdown/revive/elimination/spectator state, staging/aircraft/parachute lifecycle, deterministic loot and supply drops, server-authoritative vehicle state, safe-zone shrink/damage, reconnect, and 2/10/25/50/100-player tests. Commercial-grade anti-cheat remains out of scope for this independent protocol. |
| 4 | Complete local storage and domain-schema baseline | PostgreSQL/Redis Compose, nine idempotent migration files, domain tables, shop catalog/wallet/purchase persistence, indexes, API-state persistence, invitation persistence, game-session persistence, game completion events, and Go connectivity smoke pass. |
| 5 | Complete structure baseline | Makefile, package layout, ignore rules, and repeatable commands pass. |
| 6 | Complete foundation | Password, token, signing-key rotation, rate-limit, validation, audit, optional API TLS, and auth middleware pass tests. Deployment-managed secret/certificate distribution remains external. |
| 7 | Complete independent test foundation | Game mechanics, API auth/profile/statistics/logout/refresh replay, Redis-backed lobby/matchmaking, security, race, load, and controlled process crash/recovery tests pass. |
| 8 | Baseline complete | Game/API/migration container builds, non-root health-checked workloads, automated migration init container, UDP-only public game service with internal health/metrics service, Prometheus scrape metadata, local PostgreSQL backup/isolated-restore rehearsal, executable 2/10/25/50/100-player capacity benchmark, and Prometheus SLO/error-budget rules pass; production image registry, managed-service wiring, retention scheduling, notification routing, and TLS termination remain deployment-specific. |
| 9 | Complete | This ledger records evidence and gates; it is not a claim that every production feature exists. |
| 10 | Complete | Coding and safety rules are recorded in `docs/phase10-ai-coding-instructions.md`. |
| 11 | Complete for the independent stack; original-client gate remains open | Run `20261030T173642Z` completed 31 checks with 0 failures and 2 expected blocked original-client gates, including services, API/game metrics, performance telemetry, registration/login, lobby creation, party invitations/presence, game-session allocation/admission/heartbeat/completion, matchmaking, independent iOS XCUITest, game synchronization, match completion, fragmented 2/10/25/50/100-player protocol profiles, and network impairment. The original IPA is not simulator-compatible; physical-device compatibility remains a separate authorized gate. |

The API now propagates W3C trace context, creates OpenTelemetry spans, and can
export them over OTLP/HTTP; collector deployment/retention, deployment-managed
secret rotation, and deployment-platform TLS remain production gates. An
authorized physical-device integration path for the original client is still
required if that compatibility goal remains in scope. Production deployment
still requires provider-side capacity validation, alert routing, retention, and
managed-service integration. Game-result persistence now updates `player_statistics` when the
independent protocol uses canonical account UUIDs; arbitrary protocol IDs are
retained as durable events until an admission mapping is supplied.
