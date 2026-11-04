# Phase 7 — testing foundation

Status: implemented domain and game-server tests; verified with race detection.

## Covered behavior

- Game-server join, movement, attack validation, match completion, reconnect
  behavior, packet-loss/latency simulation, and 2/10/25/50/100-player protocol
  profiles.
- Lobby creation, capacity, ready-state transitions, allocation guards, closed-state
  behavior, invitation ownership/acceptance/decline, and bounded online/offline
  presence.
- Matchmaking FIFO selection by mode/region, cancellation, duplicate queue
  protection, bounded ticket timeout, ownership checks, and no partial
  allocation.
- Game-session allocation, participant admission limits, dedicated-server
  authorization, Redis heartbeat health, completion, and lobby cleanup.
- Argon2id password verification, signed token expiry/signature validation,
  rate-limit windows, and input validation.
- PostgreSQL/Redis migration and connectivity smoke checks are provided by
  `scripts/phase4/verify.sh`.
- The reproducible capacity benchmark in
  `scripts/phase11/capacity-benchmark.py` executes the 2/10/25/50/100-player
  profiles with explicit tick-overrun and RTT thresholds, and writes JSON and
  Markdown evidence under `artifacts/capacity/`.
- The process-level crash/recovery test in `scripts/phase11/recovery-test.py`
  force-terminates and restarts the independent game server, then verifies
  readiness and a new session; it deliberately does not claim that in-flight
  state survives a process crash.
- The live session-agent test in `scripts/phase11/session-agent-test.py`
  creates and admits a real API session, launches the actual game-server
  binary, verifies heartbeat health, and verifies graceful completion.
- Live HTTP integration suites cover registration/login, lobby creation and
  allocation, party invitations/presence, game-session lifecycle, matchmaking,
  and bearer-token/internal-server authorization through `cmd/protocol-client`.

## Verified commands

```sh
go test ./...
go vet ./...
go test -race ./...
```

All completed successfully on 2026-10-29. The game-server protocol matrix was
also run against a live local server and passed synchronization,
join/disconnect/rejoin, match completion, 2/10/25/50/100-player profiles, and
network impairment with latency, packet loss, disconnect, and reconnect.

The latest persisted Phase 11 full-stack run `20261030T173642Z` passed 31 checks
with zero failures and two expected blocked original-client gates, including
the live Redis-backed party-invitation/presence and game-session lifecycle
flows, API/game metrics probes, and the independent iOS XCUITest. It captured
game-server CPU/RSS samples, tick timing, UDP/snapshot byte counters, client
packet counts, and ping RTT with complete telemetry. The report is under
`artifacts/phase11/20261030T173642Z/` and the XCTest result bundle is under its
`test-results/` directory.

The latest capacity benchmark `artifacts/capacity/20261029T170849Z/` passed all
five profiles with zero tick overruns. Its maximum observed local protocol RTT
was 0.4ms, maximum tick duration was 11.083ms, maximum sampled CPU was 16.5%,
and maximum sampled RSS was 19,984 KiB. These are local independent-server
measurements, not a production capacity or original-client compatibility claim.

The latest process recovery test `artifacts/recovery/20261029T170849Z/`
passed forced termination, restart readiness, and a post-restart protocol
session. In-flight match-state durability across a process crash remains a
separate persistence/allocator requirement.

The latest lifecycle-agent run `artifacts/session-agent/20261030T174018Z/`
passed authenticated session creation/admission, actual game-server start and
heartbeat health, and graceful completion persistence against the current
PostgreSQL/Redis-backed API.

The original IPA remains outside this test scope because Phase 1 established
that it is a device-only build without a verified authorized protocol.
