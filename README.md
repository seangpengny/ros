# ROS Legacy independent multiplayer server

This repository contains an independently implemented, versioned multiplayer
backend and test protocol. It does not claim compatibility with the supplied
ROS Legacy iOS client, and it does not use the IPA as a runtime dependency.

## Verified local workflow

The development stack is Go, PostgreSQL, Redis, Docker, and the macOS iOS
Simulator toolchain. Use local-only test credentials when running the checks:

```sh
export POSTGRES_USER=ros POSTGRES_DB=ros POSTGRES_PASSWORD=localtestpass
export REDIS_PASSWORD=localtestpass POSTGRES_PORT=55432 REDIS_PORT=56379
export TOKEN_SIGNING_SECRET=phase11-local-secret-change-me-32-bytes-min
export ROS_GAME_SERVER_TOKEN=phase11-game-server-token ROS_GAME_SERVER_ID=game-server-local
export ROS_GAME_SERVER_IDS=game-server-local

make format
make verify-ipa
make test
make vet
make race
make build
make capacity
make recovery
make session-agent-stack
make phase11-backend
make validate-deploy
GOTOOLCHAIN=go1.26.10 go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
make phase4
PROBE_LEGACY_IPA=0 make phase11
```

`make phase11` runs the independent HTTP and UDP test clients, including
registration/login, lobby creation, party invitations/presence, game-session
lifecycle, matchmaking, synchronization,
disconnect/reconnect, match completion, 2/10/25/50/100-player profiles, and
network impairment. It also builds/runs the independent Swift/XCUITest client
on the available simulator, probes the simulator/toolchain, checks API/game
metrics endpoints, and records logs, screenshots, and XCTest result bundles
under `artifacts/phase11/`. The original IPA remains a separate compatibility
probe and is never substituted with this test client.
Each run also records game tick timing, active-player and UDP/snapshot byte
metrics, game-server CPU/RSS samples, client packet counts, and UDP ping RTT;
these are functional-run measurements, not a capacity or production-SLO claim.
The runner supplies the local test-only database, Redis, and token defaults
shown above when they are not already set; set `PHASE11_USE_LOCAL_DEFAULTS=0`
when providing an external test environment.

The checked-in independent iOS client is now playable against the independent
stack. It loads an authenticated shop catalog, purchases persistent starter
items, enters a live UDP match, renders authoritative player/loot state, and
provides movement, loot collection, reload, and disconnect controls. The
emulator verification is recorded in
`artifacts/phase11/20261030T003906Z/playable-client-verification.md`.

## Compatibility boundary

Phase 1 recorded the supplied IPA metadata and performed a real simulator
probe. The IPA declares `iPhoneOS`, contains a device-only arm64 executable,
and was denied launch by `simctl`; no authorized ROS protocol specification was
available. Therefore the original-client compatibility gate remains open. The
independent server and protocol client are testable without modifying or
reverse-engineering protected client behavior.

See `docs/phase9-milestones.md` for the phase ledger and
`docs/completion-audit.md` plus `docs/requirements-traceability.md` for the
requirement-by-requirement evidence and remaining production-hardening gates.
