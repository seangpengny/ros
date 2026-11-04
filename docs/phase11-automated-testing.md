# Phase 11 — automated iOS simulator and multiplayer testing

The runner is [`scripts/phase11/run.sh`](../scripts/phase11/run.sh). It is an
adapter-driven workflow for an independently implemented server and a
simulator-compatible test client. It does not infer or invent the ROS Legacy
wire protocol.

## What the runner does

On macOS it:

1. Detects Xcode, `simctl`, runtimes, and an available iPhone simulator.
2. Boots the simulator and captures a screenshot.
3. Optionally probes the supplied ROS Legacy IPA without modifying it. The
   probe records bundle metadata, architecture/load-command evidence, install
   output, launch output, simulator logs, and screenshots. A device-only IPA
   is reported as `BLOCKED`, never as a successful simulator test.
4. Starts Docker Compose dependencies when a compose file exists and waits for
   health checks before starting application processes. For the local test
   Compose environment it synchronizes the PostgreSQL test-role password and
   applies all checked-in migrations, so reused local volumes do not silently
   invalidate the run.
5. Starts a configured dedicated server and checks readiness.
6. Runs an externally supplied protocol test client for registration/login,
   lobby creation, party invitations/presence, game-session lifecycle,
   matchmaking, join/disconnect, synchronization, and match completion.
7. Runs 2/10/25/50/100-player protocol profiles and a latency/packet-loss /
   disconnect/reconnect profile.
8. Runs XCTest/XCUITest with `xcodebuild` when a test project/workspace and
   scheme are supplied. The repository's independent client is selected by
   default (`clients/ios-test-client/ROSTestClient.xcodeproj`); a supplied app
   smoke test remains available as an alternative.
9. Collects server/client logs, simulator logs, crash-report paths, screenshots,
   resource samples, protocol byte/RTT measurements, and a JSON/Markdown result
   summary. The performance summary is evidence from the run, not a capacity
   guarantee.

When the runner starts the standalone game-server test process, API-only
dedicated-server lifecycle credentials are removed from that process. This
keeps the standalone protocol matrix separate from the lifecycle-enabled
session-agent test; partial lifecycle configuration therefore cannot cause a
false startup failure.

For a thresholded local capacity run independent of the simulator workflow, use
`make capacity`. The benchmark starts an isolated game server and records the
2/10/25/50/100-player profiles in `artifacts/capacity/<UTC-run-id>/`.
Use `make recovery` for the controlled process-crash/restart test; it records
readiness, exit status, and post-restart protocol evidence under
`artifacts/recovery/<UTC-run-id>/`.
Use `make session-agent-stack` for a self-contained lifecycle integration run;
it starts PostgreSQL/Redis, applies migrations, starts the API, and verifies the
actual game-server lifecycle client against a durable session before tearing
everything down. Its evidence is written under
`artifacts/session-agent-stack/<UTC-run-id>/` and
`artifacts/session-agent/<UTC-run-id>/`. With an API and dependencies already
running, `make session-agent` remains available for the direct agent test.
CI and non-macOS hosts can use `make phase11-backend`; it runs the same
independent HTTP/UDP protocol, load, impairment, and telemetry stages while
explicitly skipping simulator and original-IPA stages. It exits successfully
when those backend stages pass, without treating unavailable Apple tooling as
an original-client compatibility result. The current macOS validation of this
path is recorded in `artifacts/phase11/20261029T180921Z/`; on a Linux host the
Apple-toolchain stage is recorded as `SKIP` rather than `BLOCKED`.

The process exits `0` only when no stage fails or is blocked, `1` when a stage
fails, and `2` when required inputs are missing or a stage is intentionally
blocked.

## Adapter contract

The runner requires real project-specific adapters before it can execute
backend functionality:

```sh
export COMPOSE_FILE=/absolute/path/to/compose.yml       # optional auto-detect
export GAME_SERVER_CMD='./bin/game-server --config ./config/test.yaml'
export SERVER_READY_CMD='curl --fail http://127.0.0.1:8080/healthz'
export SERVER_METRICS_CMD='curl --fail http://127.0.0.1:8080/metrics'
export API_SERVER_CMD='./bin/api-server'
export API_READY_CMD='curl --fail http://127.0.0.1:8081/readyz'
export API_METRICS_CMD='curl --fail http://127.0.0.1:8081/metrics'
export API_BASE_URL='http://127.0.0.1:8081'
export PROTOCOL_CLIENT_CMD='./bin/protocol-client'
export IOS_TEST_WORKSPACE=/absolute/path/to/Client.xcworkspace
export IOS_TEST_SCHEME=ROSClientUITests
```

Use `IOS_TEST_PROJECT` instead of `IOS_TEST_WORKSPACE` for an `.xcodeproj`.
If these variables are omitted, the runner uses the checked-in independent
client at `clients/ios-test-client/ROSTestClient.xcodeproj` with scheme
`ROSTestClient`.

For this repository, the runner also auto-detects
`deployments/docker/compose.yml` and the built `bin/api-server`,
`bin/game-server`, and `bin/protocol-client` when the local database/Redis
environment values above are exported. By default it supplies the test-only
local values `ros`/`ros`/`localtestpass`, ports `55432`/`56379`, and a
non-production token-signing secret so a plain `make phase11` is self-contained.
Override them explicitly for a different local topology, or set
`PHASE11_USE_LOCAL_DEFAULTS=0` when all external connection settings are
provided by the caller.
The API test process also requires `TOKEN_SIGNING_SECRET` with at least 32
bytes; use a test-only value when running locally and never reuse it in
production.
The repository-local defaults use game health `127.0.0.1:18080`, API
`127.0.0.1:18081`, a five-second test match, and a one-millisecond attack
cooldown so the automated suite completes promptly. Override these values when
a different local topology is intentional.
For a prebuilt app smoke test, set `IOS_TEST_APP`, `IOS_TEST_BUNDLE_ID`, and
leave the XCTest variables unset.

The protocol client is invoked with environment variables rather than an
assumed message format:

| Variable | Values used by the runner |
|---|---|
| `ROS_TEST_SUITE` | `registration_login`, `lobby_creation`, `party_invitations`, `session_lifecycle`, `matchmaking`, `join_disconnect`, `synchronization`, `match_completion`, `load`, `network_impairment` |
| `ROS_TEST_PLAYERS` | `2`, `10`, `25`, `50`, `100` |
| `ROS_TEST_LATENCY_MS` | `0` or `200` |
| `ROS_TEST_PACKET_LOSS_PCT` | `0` or `2` |
| `ROS_TEST_DISCONNECT_AFTER_MS` | `0` or `5000` |
| `ROS_TEST_RECONNECT` | `0` or `1` |
| `ROS_RUN_DIR` | current run artifact directory |

The adapter owns protocol encoding, authentication fixtures, assertions, and
the authoritative-server expectations. A zero exit status is recorded as a
pass; any nonzero exit status is recorded as a failure with its log path. The
checked-in protocol client also emits elapsed time, packet/byte counts,
simulated loss counts, and a UDP ping RTT for each multiplayer profile.

## Useful controls

```sh
SIMULATOR_UDID=... LEGACY_IPA=./RoS-Legacy-iOS.ipa \
  scripts/phase11/run.sh

# Skip the IPA probe while iterating on backend tests.
PROBE_LEGACY_IPA=0 scripts/phase11/run.sh

# Keep services running (the default). Set to 0 to tear down services that
# this run started when the process exits.
KEEP_SERVICES=0 scripts/phase11/run.sh
```

Each invocation creates `artifacts/phase11/<UTC-run-id>/`. Generated artifacts
are intentionally kept separate from source code and are not treated as proof
of original-client compatibility.

## Current environment finding

The supplied `RoS-Legacy-iOS.ipa` is a device build: its bundle declares
`iPhoneOS`, its executable is a thin arm64 Mach-O, and its load commands include
`LC_VERSION_MIN_IPHONEOS`. Standard iOS Simulator therefore cannot be used for
original-client testing. The runner can still test an independently developed
simulator client and can run protocol clients at higher player counts. Physical
iPhone validation of the original client remains a separate, authorized test
track. The final host check (`xcrun xctrace list devices`) found the named
physical iPhone offline and no usable USB device session, so no real-device
test was attempted.

Latest full-run evidence is
`artifacts/phase11/20261029T181043Z/report.md` (UTC; local date 2026-10-30),
which records 31 PASS, 0 FAIL, and 2 explicitly BLOCKED original-client
stages. It covers
registration/login, lobby/matchmaking/party-invitation/presence/session
lifecycle API flows, team/reload/spectator mechanics, BR lifecycle state, supply
drops, vehicles, weapon definitions, grenades, attachments, validation
telemetry, bounded snapshot fragmentation, network impairment, and the
2/10/25/50/100-player profiles. The independent Swift/XCUITest passed on the
iPhone 17 Pro simulator; its result bundle is under
`artifacts/phase11/20261029T181043Z/test-results/xcode.xcresult`. The original
IPA probe recorded its unchanged SHA-256, `iPhoneOS` platform, and denied
simulator launch; it was not substituted with the independent client.
Quantitative telemetry is in
`artifacts/phase11/20261029T181043Z/performance-summary.json`. The emulator-only
rerun with the original IPA probe disabled is recorded at
`artifacts/phase11/20261030T002205Z/`; it passed 29 stages and includes the
simulator screenshot and XCTest result bundle. The original IPA remains a
separate device-only compatibility probe.

A subsequent visible manual interaction with the same iPhone 17 Pro simulator
pressed the independent client's `Run API flow` button and observed
`PASS: registration/login/profile/lobby/matchmaking`. Evidence is recorded in
`artifacts/phase11/20261030T002205Z/manual-emulator-flow.md` and the captured
`screenshots/independent-client-api-pass.png`.

The playable independent client now includes a shop, persistent coin purchase,
and a graphical gameplay scene backed by the independent UDP protocol. This
evidence still must not be described as an original ROS Legacy game launch or
original-client compatibility: the supplied IPA remains device-only and the
independent protocol is intentionally separate.
