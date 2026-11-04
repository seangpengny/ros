# Independent iOS simulator playable client

This is an independently developed client for the documented ROS Legacy
independent API. It is not the supplied `RoS-Legacy-iOS.ipa`, does not modify
that IPA, and does not claim to implement the original ROS wire protocol.

Build and run the XCTest/XCUITest target against a booted simulator while the
local API is listening on port 18081:

```sh
xcodebuild \
  -project clients/ios-test-client/ROSTestClient.xcodeproj \
  -scheme ROSTestClient \
  -destination 'platform=iOS Simulator,id=CECA2A4E-C621-46A2-8046-DA0ACDBA108B' \
  -derivedDataPath artifacts/ios-test-client \
  CODE_SIGNING_ALLOWED=NO \
  test
```

The app now includes the independent playable vertical slice: authenticated
store catalog, persistent coin purchase, UDP match entry, live authoritative
state rendering, movement, loot collection, reload, and disconnect. The UI
test launches with `-apiBaseURL`, `-gameHost`, and `-gamePort`, then verifies
purchase and live match entry against the real backend. The backend must
already be running; the Phase 11 runner starts it.

## Physical iPhone against the Mac

The app includes a server settings panel for a physical device. Put the iPhone
and Mac on the same Wi-Fi network, run the API on TCP port `18081` and the game
server on UDP port `9000`, then enter the Mac's LAN IP in the app (for example,
`192.168.1.20`). Do not enter `127.0.0.1`; on a physical iPhone that points to
the iPhone itself.

Start the local dependencies and apply migrations:

```sh
export POSTGRES_USER=ros POSTGRES_DB=ros POSTGRES_PASSWORD=localtestpass
export REDIS_PASSWORD=localtestpass POSTGRES_PORT=55432 REDIS_PORT=56379
scripts/phase4/verify.sh
```

Build and start the API and UDP server so other devices on the LAN can reach
them:

```sh
export DATABASE_URL="postgres://ros:localtestpass@127.0.0.1:55432/ros?sslmode=disable"
export REDIS_ADDR=127.0.0.1:56379 REDIS_PASSWORD=localtestpass
export TOKEN_SIGNING_SECRET=local-device-test-secret-change-me-32-bytes
export ROS_GAME_SERVER_TOKEN=local-device-game-token
export ROS_GAME_SERVER_ID=game-server-local ROS_GAME_SERVER_IDS=game-server-local
export ROS_API_HTTP_ADDR=0.0.0.0:18081
export ROS_GAME_LISTEN_ADDR=0.0.0.0:9000
export ROS_GAME_HEALTH_ADDR=127.0.0.1:18080

make build
```

Find the Mac's Wi-Fi address with `ipconfig getifaddr en0` (use the active
network interface if it is not `en0`). Start the API in one terminal using the
exported environment:

```sh
./bin/api-server
```

Start the game server in a second terminal with the same game settings:

```sh
export DATABASE_URL="postgres://ros:localtestpass@127.0.0.1:55432/ros?sslmode=disable"
export ROS_GAME_LISTEN_ADDR=0.0.0.0:9000
export ROS_GAME_HEALTH_ADDR=127.0.0.1:18080
./bin/game-server
```

Allow incoming connections if the macOS firewall asks. In Xcode, select the `ROSTestClient`
scheme, select the connected iPhone, and set your Apple Development Team under
Signing & Capabilities. Then run the app. The project uses automatic signing
for physical-device builds; simulator and CI commands can still pass
`CODE_SIGNING_ALLOWED=NO`.
Allow local-network access when iOS asks. The app stores the host and ports in
local preferences so they do not need to be re-entered on every launch.

This remains an independently implemented client. It is not the supplied
`RoS-Legacy-iOS.ipa` and does not claim compatibility with undocumented ROS
Legacy wire behavior.
