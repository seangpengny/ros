# Phase 3 — independent authoritative game server

Status: implemented and verified. This is an original UDP game-session service;
it is not a ROS Legacy protocol implementation.

## Implemented behavior

- Versioned JSON-over-UDP test protocol with explicit `version: 1` messages.
- Authoritative fixed-rate simulation loop (default 20 Hz).
- Bounded movement with server-owned position and arena limits.
- Server-owned health and damage validation with range checks.
- Server-owned rifle, pistol, and shotgun definitions, fire-rate enforcement,
  magazine ammo, reload timing, armor absorption, knockdown, bleed-out, and
  revive state transitions.
- Server-authoritative grenade area damage with cooldown, inventory, sequence,
  range, and elimination validation.
- Deterministic scope attachment pickup/equip state with server-applied weapon
  range effects; the independent protocol keeps attachment semantics explicit.
- Explicit team assignment, same-team revive authorization, elimination
  counters, and spectator state after elimination.
- Battle-royale lifecycle phases (`staging`, `aircraft`, `running`), deterministic
  aircraft path metadata, explicit parachute deployment/landing state, and
  server-authoritative altitude tracking.
- Deterministic loot spawning and server-authoritative pickup/inventory updates.
- Deterministic supply-drop generation/opening and server-authoritative vehicle
  entry, exit, occupancy, and movement.
- A shrinking safe zone with server-authoritative out-of-zone damage.
- Player join, leave, reconnect within a bounded window, and connection state.
- Snapshot replication from server state rather than client state.
- Bounded UDP snapshot fragmentation and protocol-client reassembly for dense
  sessions; each JSON fragment stays within the local 8 KB safety budget.
- Match completion by duration or last-player-standing state.
- HTTP `/healthz`, `/readyz`, and Prometheus-compatible `/metrics` endpoints
  for local operations checks.
- Runtime telemetry for cumulative/max tick duration, tick overruns, active
  sessions/players, snapshot bytes, UDP ingress/egress bytes, and validation
  violations.
- Validation-violation telemetry for malformed versions/IDs, impossible input,
  and replayed sequence numbers; this is an observable heuristic, not a claim
  of a complete commercial anti-cheat system.
- Lightweight protocol client supporting 2–100 simulated players and configurable
  latency, packet loss, disconnect, and reconnect behavior.

## Source layout

- `internal/game/server.go`: authoritative session state machine and UDP loop.
- `internal/game/server_test.go`: deterministic UDP integration tests.
- `cmd/game-server/main.go`: executable server and health endpoints.
- `cmd/protocol-client/main.go`: protocol/load test client.

## Verified commands

```sh
go test ./...
go vet ./...
go test -race ./...
go build -o bin/game-server ./cmd/game-server
go build -o bin/protocol-client ./cmd/protocol-client
```

All four commands completed successfully on 2026-10-29.

With the server running on `127.0.0.1:19000`, the following real client runs
also completed with exit code 0:

| Scenario | Players | Result |
|---|---:|---|
| synchronization | 2 | PASS |
| join/disconnect/rejoin | 2 | PASS |
| match completion | 2 | PASS |
| load profile | 2 | PASS |
| load profile | 10 | PASS |
| load profile | 25 | PASS |
| load profile | 50 | PASS |
| load profile | 100 | PASS |
| 20 ms latency, 2% packet loss, disconnect/reconnect | 2 | PASS |

The initial concurrent 50/100-player run exposed excessive join-time snapshot
fanout and timed out. The server was changed to replicate on the fixed tick
instead of broadcasting a full snapshot after every join. The post-fix 50- and
100-player runs and the complete matrix above passed.

## Explicit limitations

- This is an independent authoritative simulation, not a ROS Legacy protocol
  implementation. The implemented grenade, attachment, weapon, and vehicle
  rules are newly proposed independent behavior; production anti-cheat
  heuristics, balance validation, and abuse-response operations remain separate
  hardening work.
- Durable account/profile/statistics/session schemas and Redis-backed API lobby
  and matchmaking state are implemented in later phases. The server now
  exposes an optional asynchronous PostgreSQL completion sink; mapping
  arbitrary protocol player IDs to authenticated accounts remains an admission
  and deployment concern.
- JSON-over-UDP is a transparent test contract, not a final high-performance
  binary wire format.
- The load results are functional protocol tests with captured tick, bandwidth,
  CPU/RSS, packet-count, and ping-RTT evidence; they are not capacity or SLO
  benchmarks.
- No claim is made that the original IPA can connect to this service.
