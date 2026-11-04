#!/usr/bin/env bash

# Phase 11 runner for the independent server and compatible iOS test client.
#
# This runner is deliberately adapter-driven. It never invents a ROS protocol,
# rewrites an IPA, or treats the original device IPA as a simulator client.
# Supply the backend/test-client commands through environment variables when
# those independently implemented components exist.

set -u
set -o pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUN_ID="${PHASE11_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
ARTIFACT_ROOT="${PHASE11_ARTIFACT_ROOT:-$ROOT_DIR/artifacts/phase11}"
RUN_DIR="$ARTIFACT_ROOT/$RUN_ID"
LOG_DIR="$RUN_DIR/logs"
SCREENSHOT_DIR="$RUN_DIR/screenshots"
TEST_RESULT_DIR="$RUN_DIR/test-results"
RESULTS_TSV="$RUN_DIR/results.tsv"
RESULTS_JSON="$RUN_DIR/test-results.json"
REPORT_MD="$RUN_DIR/report.md"

mkdir -p "$LOG_DIR" "$SCREENSHOT_DIR" "$TEST_RESULT_DIR"
: > "$RESULTS_TSV"

SIMULATOR_UDID="${SIMULATOR_UDID:-}"
LEGACY_IPA="${LEGACY_IPA:-$ROOT_DIR/RoS-Legacy-iOS.ipa}"
PROBE_LEGACY_IPA="${PROBE_LEGACY_IPA:-1}"
RUN_BACKEND="${RUN_BACKEND:-1}"
RUN_PROTOCOL_TESTS="${RUN_PROTOCOL_TESTS:-1}"
RUN_XCTEST="${RUN_XCTEST:-1}"
RUN_LOAD_TESTS="${RUN_LOAD_TESTS:-1}"
KEEP_SERVICES="${KEEP_SERVICES:-1}"
SIM_LOG_WINDOW="${SIM_LOG_WINDOW:-2m}"
LOAD_PROFILES="${LOAD_PROFILES:-2 10 25 50 100}"
backend_only=0
if [[ "$RUN_XCTEST" == "0" && "$PROBE_LEGACY_IPA" == "0" ]]; then
  backend_only=1
fi

# Keep the checked-in local workflow self-contained. These values are test-only
# defaults for Docker Compose and are overrideable; production callers must
# provide their own credentials or disable this block and supply an external
# backend topology.
if [[ "${PHASE11_USE_LOCAL_DEFAULTS:-1}" == "1" ]]; then
  export POSTGRES_USER="${POSTGRES_USER:-ros}"
  export POSTGRES_DB="${POSTGRES_DB:-ros}"
  export POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-localtestpass}"
  export POSTGRES_PORT="${POSTGRES_PORT:-55432}"
  export REDIS_PASSWORD="${REDIS_PASSWORD:-localtestpass}"
  export REDIS_PORT="${REDIS_PORT:-56379}"
  export TOKEN_SIGNING_SECRET="${TOKEN_SIGNING_SECRET:-phase11-local-secret-change-me-32-bytes-min}"
fi

# Provide local development connection defaults when the compose credentials
# are present. Production callers should supply DATABASE_URL and REDIS_ADDR
# explicitly rather than relying on these test-only derivations.
if [[ -z "${DATABASE_URL:-}" ]] && [[ -n "${POSTGRES_USER:-}" ]] && [[ -n "${POSTGRES_PASSWORD:-}" ]] && [[ -n "${POSTGRES_DB:-}" ]]; then
  DATABASE_URL="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT:-55432}/${POSTGRES_DB}?sslmode=disable"
  export DATABASE_URL
fi
if [[ -z "${REDIS_ADDR:-}" ]] && [[ -n "${REDIS_PORT:-}" ]]; then
  REDIS_ADDR="127.0.0.1:${REDIS_PORT}"
  export REDIS_ADDR
fi

# The checked-in simulator client and local test adapters use these isolated
# ports. Callers can override them and provide matching readiness commands.
export ROS_GAME_HEALTH_ADDR="${ROS_GAME_HEALTH_ADDR:-127.0.0.1:18080}"
export ROS_API_HTTP_ADDR="${ROS_API_HTTP_ADDR:-127.0.0.1:18081}"
export ROS_GAME_SERVER_ID="${ROS_GAME_SERVER_ID:-game-server-local}"
export ROS_GAME_SERVER_TOKEN="${ROS_GAME_SERVER_TOKEN:-phase11-game-server-token}"
export ROS_GAME_MATCH_DURATION="${ROS_GAME_MATCH_DURATION:-5s}"
export ROS_GAME_ATTACK_COOLDOWN="${ROS_GAME_ATTACK_COOLDOWN:-1ms}"
API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:18081}"
export API_BASE_URL

compose_file=""
compose_started=0
game_server_pid=""
api_server_pid=""
resource_monitor_pid=""
simctl_bin=""
simulator_ready=0

record() {
  local stage="$1"
  local status="$2"
  local detail="$3"
  detail="$(printf '%s' "$detail" | tr '\t\r\n' '   ')"
  printf '%s\t%s\t%s\n' "$stage" "$status" "$detail" >> "$RESULTS_TSV"
  printf '[%s] %-7s %s\n' "$status" "$stage" "$detail"
}

command_exists() {
  command -v "$1" >/dev/null 2>&1
}

absolute_path() {
  local candidate="$1"
  if [[ "$candidate" = /* ]]; then
    printf '%s\n' "$candidate"
  else
    printf '%s\n' "$ROOT_DIR/$candidate"
  fi
}

cleanup() {
  terminate_pid "$resource_monitor_pid"
  terminate_pid "$game_server_pid"
  terminate_pid "$api_server_pid"
  if [[ "$KEEP_SERVICES" != "1" ]] && [[ "$compose_started" == "1" ]] && [[ -n "$compose_file" ]]; then
    docker compose -f "$compose_file" down > "$LOG_DIR/docker-compose-down.log" 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

terminate_pid() {
  local pid="$1"
  [[ -n "$pid" ]] || return 0
  if kill -0 "$pid" >/dev/null 2>&1; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" 2>/dev/null || true
  fi
}

start_resource_monitor() {
  local pid="$1"
  (
    printf 'timestamp\tcpu_percent\trss_kb\n'
    while kill -0 "$pid" >/dev/null 2>&1; do
      cpu="$(ps -o %cpu= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
      rss="$(ps -o rss= -p "$pid" 2>/dev/null | tr -d ' ' || true)"
      printf '%s\t%s\t%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "${cpu:-0}" "${rss:-0}"
      sleep 1
    done
  ) > "$LOG_DIR/game-server-resources.tsv" 2>&1 &
  resource_monitor_pid=$!
}

capture_performance_evidence() {
  if [[ -n "${SERVER_METRICS_CMD:-}" ]]; then
    if ! sh -c "$SERVER_METRICS_CMD" > "$LOG_DIR/server-metrics-final.txt" 2>&1; then
      record performance_metrics FAIL "final SERVER_METRICS_CMD failed; see server-metrics-final.txt"
      return
    fi
  fi
  if python3 "$ROOT_DIR/scripts/phase11/summarize-performance.py" \
    "$LOG_DIR/game-server-resources.tsv" "$LOG_DIR/server-metrics-final.txt" "$RUN_DIR/performance-summary.json"; then
    record performance_telemetry PASS "tick timing, active players, UDP/snapshot bytes, and game-server CPU/RSS samples captured; see performance-summary.json"
  else
    record performance_telemetry BLOCKED "performance summary could not prove required telemetry or resource samples; see performance-summary.json"
  fi
}

printf 'Phase 11 run: %s\n' "$RUN_ID"
printf 'Artifacts: %s\n' "$RUN_DIR"

if [[ "$(uname -s)" == "Darwin" ]]; then
  record host PASS "$(sw_vers -productName) $(sw_vers -productVersion), $(uname -m)"
elif [[ "$backend_only" == "1" ]]; then
  record host PASS "$(uname -s) backend-only mode; simulator stages were not requested"
else
  record host BLOCKED "iOS Simulator execution requires macOS; detected $(uname -s)"
fi

if [[ "$backend_only" == "1" ]]; then
  record apple_toolchain SKIP "backend-only mode requested; simulator/toolchain stages skipped"
elif command_exists xcrun && xcrun --find simctl > "$LOG_DIR/simctl-path.txt" 2>&1; then
  simctl_bin="$(cat "$LOG_DIR/simctl-path.txt")"
  xcodebuild -version > "$LOG_DIR/xcode-version.txt" 2>&1 || true
  xcrun simctl list runtimes > "$LOG_DIR/simulator-runtimes.txt" 2>&1 || true
  record apple_toolchain PASS "simctl=$simctl_bin; Xcode version captured in logs"
else
  if [[ "$backend_only" == "1" ]]; then
    record apple_toolchain SKIP "xcrun/simctl unavailable; backend-only mode requested"
  else
    record apple_toolchain BLOCKED "xcrun/simctl is unavailable"
  fi
fi

choose_simulator() {
  if [[ -n "$SIMULATOR_UDID" ]]; then
    return 0
  fi
  SIMULATOR_UDID="$(xcrun simctl list devices 2>/dev/null | sed -n 's/.*(\([A-F0-9-]\{36\}\)) (Booted).*/\1/p' | head -n 1)"
  if [[ -z "$SIMULATOR_UDID" ]]; then
    SIMULATOR_UDID="$(xcrun simctl list devices available 2>/dev/null | sed -n '/iPhone/ s/.*(\([A-F0-9-]\{36\}\)) (Shutdown).*/\1/p' | head -n 1)"
  fi
}

if [[ -n "$simctl_bin" ]]; then
  choose_simulator
  if [[ -z "$SIMULATOR_UDID" ]]; then
    record simulator_selection BLOCKED "no available iPhone simulator was found"
  else
    xcrun simctl list devices > "$LOG_DIR/devices-before.txt" 2>&1 || true
    device_line="$(xcrun simctl list devices 2>/dev/null | rg "$SIMULATOR_UDID" | head -n 1)"
    if printf '%s' "$device_line" | rg -q '\(Booted\)'; then
      simulator_ready=1
    else
      xcrun simctl boot "$SIMULATOR_UDID" > "$LOG_DIR/simulator-boot.txt" 2>&1 || true
      for _ in $(seq 1 60); do
        device_line="$(xcrun simctl list devices 2>/dev/null | rg "$SIMULATOR_UDID" | head -n 1)"
        if printf '%s' "$device_line" | rg -q '\(Booted\)'; then
          simulator_ready=1
          break
        fi
        sleep 1
      done
    fi
    xcrun simctl list devices > "$LOG_DIR/devices-after-boot.txt" 2>&1 || true
    if [[ "$simulator_ready" == "1" ]]; then
      record simulator_boot PASS "$device_line"
      if xcrun simctl io "$SIMULATOR_UDID" screenshot "$SCREENSHOT_DIR/simulator-home.png" > "$LOG_DIR/simulator-home-screenshot.txt" 2>&1; then
        record simulator_screenshot PASS "home-screen screenshot captured"
      else
        record simulator_screenshot FAIL "simctl screenshot failed; see logs"
      fi
    else
      record simulator_boot FAIL "simulator did not reach Booted state within 60 seconds"
    fi
  fi
fi

capture_simulator_evidence() {
  [[ "$simulator_ready" == "1" ]] || return 0
  xcrun simctl listapps "$SIMULATOR_UDID" > "$LOG_DIR/simulator-apps.txt" 2>&1 || true
  xcrun simctl spawn "$SIMULATOR_UDID" log show --last "$SIM_LOG_WINDOW" --style compact \
    --predicate 'process == "runningboardd" OR process == "installd" OR process == "SpringBoard"' \
    > "$LOG_DIR/simulator.log" 2>&1 || true
  crash_root="${HOME}/Library/Developer/CoreSimulator/Devices/$SIMULATOR_UDID/data/Library/Logs/DiagnosticReports"
  if [[ -d "$crash_root" ]]; then
    find "$crash_root" -type f -mmin -15 -maxdepth 1 -print > "$LOG_DIR/recent-crash-reports.txt" 2>&1 || true
  else
    printf 'DiagnosticReports directory not found: %s\n' "$crash_root" > "$LOG_DIR/recent-crash-reports.txt"
  fi
}

wait_for_compose_dependencies() {
  local postgres_container redis_container postgres_health redis_health
  for _ in $(seq 1 60); do
    postgres_container="$(docker compose -f "$compose_file" ps -q postgres 2>/dev/null || true)"
    redis_container="$(docker compose -f "$compose_file" ps -q redis 2>/dev/null || true)"
    if [[ -n "$postgres_container" ]] && [[ -n "$redis_container" ]]; then
      postgres_health="$(docker inspect --format '{{.State.Health.Status}}' "$postgres_container" 2>/dev/null || true)"
      redis_health="$(docker inspect --format '{{.State.Health.Status}}' "$redis_container" 2>/dev/null || true)"
      if [[ "$postgres_health" == "healthy" ]] && [[ "$redis_health" == "healthy" ]]; then
        return 0
      fi
    fi
    sleep 1
  done
  docker compose -f "$compose_file" ps > "$LOG_DIR/docker-compose-health.txt" 2>&1 || true
  docker compose -f "$compose_file" logs --tail=100 postgres redis > "$LOG_DIR/docker-compose-health-logs.txt" 2>&1 || true
  return 1
}

synchronize_local_postgres_password() {
  [[ "${PHASE11_SYNC_LOCAL_POSTGRES_PASSWORD:-1}" == "1" ]] || return 0
  [[ -n "${POSTGRES_USER:-}" && -n "${POSTGRES_DB:-}" && -n "${POSTGRES_PASSWORD:-}" ]] || {
    record database_credentials BLOCKED "POSTGRES_USER, POSTGRES_DB, and POSTGRES_PASSWORD are required to synchronize the local test role"
    return 1
  }
  if docker compose -f "$compose_file" exec -T postgres \
    psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
    -v "ros_test_password=$POSTGRES_PASSWORD" \
    > "$LOG_DIR/postgres-password-sync.txt" 2>&1 <<'SQL'
SELECT format('ALTER ROLE %I PASSWORD %L', current_user, :'ros_test_password') \gexec
SQL
  then
    record database_credentials PASS "local PostgreSQL role password synchronized for this test run"
    return 0
  fi
  record database_credentials FAIL "could not synchronize the local PostgreSQL role password; see postgres-password-sync.txt"
  return 1
}

apply_local_postgres_migrations() {
  [[ "${PHASE11_APPLY_MIGRATIONS:-1}" == "1" ]] || return 0
  local migration
  : > "$LOG_DIR/postgres-migrations.txt"
  for migration in "$ROOT_DIR"/migrations/*.sql; do
    if ! docker compose -f "$compose_file" exec -T postgres \
      psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
      < "$migration" >> "$LOG_DIR/postgres-migrations.txt" 2>&1; then
      record database_migrations FAIL "migration failed for $(basename "$migration"); see postgres-migrations.txt"
      return 1
    fi
  done
  record database_migrations PASS "all checked-in PostgreSQL migrations applied"
  return 0
}

probe_legacy_ipa() {
  [[ "$simulator_ready" == "1" ]] || {
    record legacy_ipa_probe BLOCKED "simulator is not available"
    return
  }
  [[ -f "$LEGACY_IPA" ]] || {
    record legacy_ipa_probe BLOCKED "IPA not found at $LEGACY_IPA"
    return
  }

  local inspect_dir bundle_path info_path executable binary_path bundle_id short_version supported_platforms arch_summary device_load
  inspect_dir="$(mktemp -d /tmp/ros-phase11-ipa.XXXXXX)"
  bundle_path="$(unzip -Z1 "$LEGACY_IPA" | awk 'index($0, "Payload/") == 1 && substr($0, length($0) - 10) == "/Info.plist" { path = substr($0, 1, length($0) - 11); count = split(path, parts, "/"); if (substr(parts[count], length(parts[count]) - 3) == ".app") { print path; exit } }')"
  if [[ -z "$bundle_path" ]]; then
    record legacy_ipa_probe FAIL "could not locate Payload/*.app/Info.plist"
    return
  fi
  info_path="$inspect_dir/Info.plist"
  unzip -p "$LEGACY_IPA" "$bundle_path/Info.plist" > "$info_path" 2> "$LOG_DIR/ipa-unzip-errors.txt"
  executable="$(plutil -extract CFBundleExecutable raw -o - "$info_path" 2>/dev/null || true)"
  bundle_id="$(plutil -extract CFBundleIdentifier raw -o - "$info_path" 2>/dev/null || true)"
  short_version="$(plutil -extract CFBundleShortVersionString raw -o - "$info_path" 2>/dev/null || true)"
  supported_platforms="$(plutil -extract CFBundleSupportedPlatforms.0 raw -o - "$info_path" 2>/dev/null || true)"
  binary_path="$inspect_dir/$executable"
  unzip -p "$LEGACY_IPA" "$bundle_path/$executable" > "$binary_path" 2> "$LOG_DIR/ipa-binary-unzip-errors.txt"
  file "$binary_path" > "$LOG_DIR/ipa-binary-file.txt" 2>&1 || true
  lipo -info "$binary_path" > "$LOG_DIR/ipa-binary-architectures.txt" 2>&1 || true
  otool -hv "$binary_path" > "$LOG_DIR/ipa-mach-header.txt" 2>&1 || true
  otool -l "$binary_path" > "$LOG_DIR/ipa-load-commands.txt" 2>&1 || true
  plutil -p "$info_path" > "$LOG_DIR/ipa-info-plist.txt" 2>&1 || true
  codesign -dvvv --entitlements :- "$binary_path" > "$LOG_DIR/ipa-codesign.txt" 2>&1 || true

  arch_summary="$(cat "$LOG_DIR/ipa-binary-architectures.txt" | tr '\n' ' ')"
  device_load="$(rg -o 'LC_VERSION_MIN_IPHONEOS|platform [0-9]+' "$LOG_DIR/ipa-load-commands.txt" | tr '\n' ' ')"
  record legacy_ipa_inventory PASS "sha256=$(shasum -a 256 "$LEGACY_IPA" | awk '{print $1}'); bundle=$bundle_id; version=$short_version; platform=$supported_platforms; $arch_summary; $device_load"

  local sim_capable=0
  if [[ "$supported_platforms" == "iPhoneSimulator" ]] || rg -q 'platform 7' "$LOG_DIR/ipa-load-commands.txt"; then
    sim_capable=1
  fi
  if [[ "$sim_capable" == "0" ]] && [[ "$supported_platforms" == "iPhoneOS" || "$device_load" == *LC_VERSION_MIN_IPHONEOS* ]]; then
    record legacy_ipa_compatibility BLOCKED "device-only IPA: CFBundleSupportedPlatforms=$supported_platforms and device iOS load command detected"
  fi

  local install_rc launch_rc
  xcrun simctl install "$SIMULATOR_UDID" "$LEGACY_IPA" > "$LOG_DIR/legacy-ipa-install.txt" 2>&1
  install_rc=$?
  xcrun simctl listapps "$SIMULATOR_UDID" > "$LOG_DIR/simulator-apps-after-ipa-install.txt" 2>&1 || true
  if [[ "$install_rc" == "0" ]] && rg -q "\"$bundle_id\"" "$LOG_DIR/simulator-apps-after-ipa-install.txt"; then
    record legacy_ipa_install PASS "simctl accepted the archive into the app registry; this does not prove runtime compatibility"
  else
    record legacy_ipa_install FAIL "simctl install failed with exit $install_rc; see legacy-ipa-install.txt"
  fi

  xcrun simctl launch "$SIMULATOR_UDID" "$bundle_id" > "$LOG_DIR/legacy-ipa-launch.txt" 2>&1
  launch_rc=$?
  if [[ "$launch_rc" == "0" ]]; then
    record legacy_ipa_launch PASS "original IPA launched in Simulator"
    xcrun simctl io "$SIMULATOR_UDID" screenshot "$SCREENSHOT_DIR/legacy-ipa.png" > "$LOG_DIR/legacy-ipa-screenshot.txt" 2>&1 || true
  elif [[ "$sim_capable" == "0" ]]; then
    record legacy_ipa_launch BLOCKED "launch denied for device-only IPA; see legacy-ipa-launch.txt"
  else
    record legacy_ipa_launch FAIL "simulator-compatible metadata was detected but launch failed; see legacy-ipa-launch.txt"
  fi
}

if [[ "$PROBE_LEGACY_IPA" == "1" ]]; then
  probe_legacy_ipa
fi

if [[ "$RUN_BACKEND" == "1" ]]; then
  if [[ -n "${COMPOSE_FILE:-}" ]]; then
    compose_file="$(absolute_path "$COMPOSE_FILE")"
  else
    for candidate in "$ROOT_DIR/docker-compose.yml" "$ROOT_DIR/docker-compose.yaml" "$ROOT_DIR/compose.yml" "$ROOT_DIR/compose.yaml" "$ROOT_DIR/deployments/docker/compose.yml"; do
      if [[ -f "$candidate" ]]; then
        compose_file="$candidate"
        break
      fi
    done
  fi

  if [[ -n "$compose_file" ]] && command_exists docker; then
    if docker compose -f "$compose_file" config -q > "$LOG_DIR/docker-compose-config.txt" 2>&1; then
      if docker compose -f "$compose_file" up -d > "$LOG_DIR/docker-compose-up.txt" 2>&1; then
        compose_started=1
        if wait_for_compose_dependencies; then
          record dependencies PASS "PostgreSQL/Redis/backend compose services started and healthy from $compose_file"
          if ! synchronize_local_postgres_password || ! apply_local_postgres_migrations; then
            record database_setup FAIL "local PostgreSQL setup did not complete; backend results may be invalid"
          fi
        else
          record dependencies FAIL "Compose services started but did not become healthy; see docker-compose-health.txt and docker-compose-health-logs.txt"
        fi
      else
        record dependencies FAIL "docker compose up failed; see docker-compose-up.txt"
      fi
    else
      record dependencies FAIL "docker compose config failed; see docker-compose-config.txt"
    fi
  else
    record dependencies BLOCKED "no docker compose file was found; no PostgreSQL/Redis services were started"
  fi

  game_server_cmd="${GAME_SERVER_CMD:-}"
  if [[ -z "$game_server_cmd" ]]; then
    if [[ -x "$ROOT_DIR/bin/game-server" ]]; then
      game_server_cmd="$ROOT_DIR/bin/game-server"
    elif [[ -x "$ROOT_DIR/game-server" ]]; then
      game_server_cmd="$ROOT_DIR/game-server"
    fi
  fi
  if [[ -z "$game_server_cmd" ]]; then
    record game_server_start BLOCKED "set GAME_SERVER_CMD or provide an executable bin/game-server"
  else
    if [[ -z "${ROS_API_BASE_URL:-}" && -z "${ROS_GAME_SESSION_ID:-}" ]]; then
      (cd "$ROOT_DIR" && exec env -u ROS_API_BASE_URL -u ROS_GAME_SESSION_ID -u ROS_GAME_SERVER_ID -u ROS_GAME_SERVER_TOKEN sh -c "exec $game_server_cmd") > "$LOG_DIR/game-server.log" 2>&1 &
    else
      (cd "$ROOT_DIR" && exec sh -c "exec $game_server_cmd") > "$LOG_DIR/game-server.log" 2>&1 &
    fi
    game_server_pid=$!
    sleep 1
    if kill -0 "$game_server_pid" >/dev/null 2>&1; then
      record game_server_start PASS "game server process started with pid $game_server_pid"
      start_resource_monitor "$game_server_pid"
    else
      record game_server_start FAIL "game server exited during startup; see game-server.log"
    fi
  fi

  if [[ -z "${SERVER_READY_CMD:-}" ]] && [[ -x "$ROOT_DIR/bin/game-server" ]]; then
    SERVER_READY_CMD='curl --fail --silent http://127.0.0.1:18080/healthz'
  fi
  if [[ -z "${SERVER_METRICS_CMD:-}" ]] && [[ -x "$ROOT_DIR/bin/game-server" ]]; then
    SERVER_METRICS_CMD='curl --fail --silent http://127.0.0.1:18080/metrics'
  fi

    if [[ -n "${SERVER_READY_CMD:-}" ]]; then
      ready=0
      for _ in $(seq 1 30); do
        if sh -c "$SERVER_READY_CMD" > "$LOG_DIR/server-ready.txt" 2>&1; then
          ready=1
          break
        fi
        if ! kill -0 "$game_server_pid" >/dev/null 2>&1; then
          break
        fi
        sleep 1
      done
      if [[ "$ready" == "1" ]]; then
      record game_server_ready PASS "SERVER_READY_CMD succeeded"
    else
      record game_server_ready FAIL "SERVER_READY_CMD failed; see server-ready.txt"
    fi
  elif [[ -n "${GAME_SERVER_PORT:-}" ]] && command_exists nc; then
    ready=0
    for _ in $(seq 1 30); do
      if nc -z "${GAME_SERVER_HOST:-127.0.0.1}" "$GAME_SERVER_PORT" > "$LOG_DIR/server-port-check.txt" 2>&1; then
        ready=1
        break
      fi
      sleep 1
    done
    if [[ "$ready" == "1" ]]; then
      record game_server_ready PASS "TCP port $GAME_SERVER_PORT is accepting connections"
    else
      record game_server_ready FAIL "TCP port $GAME_SERVER_PORT did not become ready"
    fi
  else
    record game_server_ready BLOCKED "provide SERVER_READY_CMD or GAME_SERVER_PORT to assert readiness"
  fi
    if [[ -n "${SERVER_METRICS_CMD:-}" ]]; then
      metrics_ready=0
      for _ in $(seq 1 10); do
        if sh -c "$SERVER_METRICS_CMD" > "$LOG_DIR/server-metrics.txt" 2>&1; then
          metrics_ready=1
          break
        fi
        sleep 1
      done
      if [[ "$metrics_ready" == "1" ]]; then
        record game_server_metrics PASS "SERVER_METRICS_CMD succeeded"
      else
        record game_server_metrics FAIL "SERVER_METRICS_CMD failed; see server-metrics.txt"
      fi
    fi

  api_server_cmd="${API_SERVER_CMD:-}"
  if [[ -z "$api_server_cmd" ]] && [[ -x "$ROOT_DIR/bin/api-server" ]]; then
    api_server_cmd="$ROOT_DIR/bin/api-server"
  fi
  if [[ -z "${API_READY_CMD:-}" ]] && [[ -n "$api_server_cmd" ]]; then
    API_READY_CMD='curl --fail --silent http://127.0.0.1:18081/readyz'
  fi
  if [[ -z "${API_METRICS_CMD:-}" ]] && [[ -n "$api_server_cmd" ]]; then
    API_METRICS_CMD='curl --fail --silent http://127.0.0.1:18081/metrics'
  fi
  if [[ -n "$api_server_cmd" ]]; then
    (cd "$ROOT_DIR" && exec sh -c "exec $api_server_cmd") > "$LOG_DIR/api-server.log" 2>&1 &
    api_server_pid=$!
    sleep 1
    if kill -0 "$api_server_pid" >/dev/null 2>&1; then
      record api_server_start PASS "API server process started with pid $api_server_pid"
    else
      record api_server_start FAIL "API server exited during startup; see api-server.log"
    fi
    if [[ -n "${API_READY_CMD:-}" ]]; then
      ready=0
      for _ in $(seq 1 30); do
        if sh -c "$API_READY_CMD" > "$LOG_DIR/api-ready.txt" 2>&1; then
          ready=1
          break
        fi
        if ! kill -0 "$api_server_pid" >/dev/null 2>&1; then
          break
        fi
        sleep 1
      done
      if [[ "$ready" == "1" ]]; then
        record api_server_ready PASS "API_READY_CMD succeeded"
      else
        record api_server_ready FAIL "API_READY_CMD failed; see api-ready.txt"
      fi
    else
      record api_server_ready BLOCKED "provide API_READY_CMD to assert API readiness"
    fi
    if [[ -n "${API_METRICS_CMD:-}" ]]; then
      metrics_ready=0
      for _ in $(seq 1 10); do
        if sh -c "$API_METRICS_CMD" > "$LOG_DIR/api-metrics.txt" 2>&1; then
          metrics_ready=1
          break
        fi
        sleep 1
      done
      if [[ "$metrics_ready" == "1" ]]; then
        record api_server_metrics PASS "API_METRICS_CMD succeeded"
      else
        record api_server_metrics FAIL "API_METRICS_CMD failed; see api-metrics.txt"
      fi
    fi
  elif [[ "$RUN_PROTOCOL_TESTS" == "1" ]]; then
    record api_server_start BLOCKED "set API_SERVER_CMD to run HTTP auth/lobby/matchmaking suites"
  fi
else
  record backend BLOCKED "RUN_BACKEND=0; backend stages were not requested"
fi

protocol_cmd="${PROTOCOL_CLIENT_CMD:-}"
if [[ -z "$protocol_cmd" ]] && [[ -x "$ROOT_DIR/bin/protocol-client" ]]; then
  protocol_cmd="$ROOT_DIR/bin/protocol-client"
fi
if [[ "$RUN_PROTOCOL_TESTS" == "1" ]]; then
  if [[ -z "$protocol_cmd" ]]; then
    for scenario in registration_login lobby_creation party_invitations session_lifecycle matchmaking join_disconnect synchronization match_completion; do
      record "protocol_$scenario" BLOCKED "set PROTOCOL_CLIENT_CMD; no protocol behavior is assumed by this runner"
    done
  else
    scenarios="registration_login lobby_creation party_invitations session_lifecycle matchmaking join_disconnect synchronization match_completion"
    for scenario in $scenarios; do
      log_file="$LOG_DIR/protocol-$scenario.log"
      ROS_TEST_SUITE="$scenario" ROS_TEST_PLAYERS=2 ROS_TEST_LATENCY_MS=0 ROS_TEST_PACKET_LOSS_PCT=0 \
      ROS_TEST_DISCONNECT_AFTER_MS=0 ROS_TEST_RECONNECT=0 ROS_API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8081}" ROS_RUN_DIR="$RUN_DIR" \
        sh -c "$protocol_cmd" > "$log_file" 2>&1
      rc=$?
      if [[ "$rc" == "0" ]]; then
        record "protocol_$scenario" PASS "protocol client exited 0"
      else
        record "protocol_$scenario" FAIL "protocol client exited $rc; see $log_file"
      fi
    done
  fi
fi

if [[ "$RUN_LOAD_TESTS" == "1" ]]; then
  if [[ -z "$protocol_cmd" ]]; then
    record load_profiles BLOCKED "no PROTOCOL_CLIENT_CMD; requested profiles: $LOAD_PROFILES"
    record network_impairment BLOCKED "no PROTOCOL_CLIENT_CMD for latency/packet-loss/disconnect testing"
  else
    for players in $LOAD_PROFILES; do
      log_file="$LOG_DIR/load-${players}-players.log"
      ROS_TEST_SUITE=load ROS_TEST_PLAYERS="$players" ROS_TEST_LATENCY_MS=0 ROS_TEST_PACKET_LOSS_PCT=0 \
        ROS_TEST_DISCONNECT_AFTER_MS=0 ROS_TEST_RECONNECT=0 ROS_API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8081}" ROS_RUN_DIR="$RUN_DIR" \
        sh -c "$protocol_cmd" > "$log_file" 2>&1
      rc=$?
      if [[ "$rc" == "0" ]]; then
        record "load_${players}_players" PASS "protocol client exited 0"
      else
        record "load_${players}_players" FAIL "protocol client exited $rc; see $log_file"
      fi
    done
    log_file="$LOG_DIR/network-impairment.log"
    ROS_TEST_SUITE=network_impairment ROS_TEST_PLAYERS=2 ROS_TEST_LATENCY_MS=200 ROS_TEST_PACKET_LOSS_PCT=2 \
      ROS_TEST_DISCONNECT_AFTER_MS=5000 ROS_TEST_RECONNECT=1 ROS_API_BASE_URL="${API_BASE_URL:-http://127.0.0.1:8081}" ROS_RUN_DIR="$RUN_DIR" \
      sh -c "$protocol_cmd" > "$log_file" 2>&1
    rc=$?
    if [[ "$rc" == "0" ]]; then
      record network_impairment PASS "latency, packet-loss, disconnect, and reconnect profile exited 0"
    else
      record network_impairment FAIL "network impairment profile exited $rc; see $log_file"
    fi
  fi
fi

if [[ "$RUN_XCTEST" == "1" ]]; then
  test_target="${IOS_TEST_WORKSPACE:-${IOS_TEST_PROJECT:-}}"
  test_scheme="${IOS_TEST_SCHEME:-}"
  if [[ -z "$test_target" ]] && [[ -f "$ROOT_DIR/clients/ios-test-client/ROSTestClient.xcodeproj/project.pbxproj" ]]; then
    test_target="$ROOT_DIR/clients/ios-test-client/ROSTestClient.xcodeproj"
  fi
  if [[ -z "$test_scheme" ]] && [[ "$test_target" == *"ROSTestClient.xcodeproj" ]]; then
    test_scheme="ROSTestClient"
  fi
  if [[ -n "$test_target" ]] && [[ -n "$test_scheme" ]] && [[ "$simulator_ready" == "1" ]]; then
    xcode_args=()
    if [[ "$test_target" == *.xcworkspace ]]; then
      xcode_args+=( -workspace "$test_target" )
    else
      xcode_args+=( -project "$test_target" )
    fi
    xcode_args+=( -scheme "$test_scheme" -destination "id=$SIMULATOR_UDID" -parallel-testing-enabled NO -derivedDataPath "$TEST_RESULT_DIR/derived-data" -resultBundlePath "$TEST_RESULT_DIR/xcode.xcresult" CODE_SIGNING_ALLOWED=NO )
    xcodebuild "${xcode_args[@]}" test > "$LOG_DIR/xcodebuild-test.log" 2>&1
    rc=$?
    if [[ "$rc" == "0" ]]; then
      record xctest PASS "xcodebuild test completed successfully"
    else
      record xctest FAIL "xcodebuild test exited $rc; see xcodebuild-test.log"
    fi
  elif [[ -n "${IOS_TEST_APP:-}" ]] && [[ "$simulator_ready" == "1" ]]; then
    test_app="$(absolute_path "$IOS_TEST_APP")"
    test_bundle_id="${IOS_TEST_BUNDLE_ID:-}"
    xcrun simctl install "$SIMULATOR_UDID" "$test_app" > "$LOG_DIR/test-client-install.log" 2>&1
    install_rc=$?
    if [[ "$install_rc" != "0" ]]; then
      record simulator_test_client FAIL "test client install failed; see test-client-install.log"
    elif [[ -z "$test_bundle_id" ]]; then
      record simulator_test_client BLOCKED "IOS_TEST_BUNDLE_ID is required to launch IOS_TEST_APP"
    else
      xcrun simctl launch "$SIMULATOR_UDID" "$test_bundle_id" > "$LOG_DIR/test-client-launch.log" 2>&1
      launch_rc=$?
      if [[ "$launch_rc" == "0" ]]; then
        xcrun simctl io "$SIMULATOR_UDID" screenshot "$SCREENSHOT_DIR/test-client.png" > "$LOG_DIR/test-client-screenshot.log" 2>&1 || true
        record simulator_test_client PASS "compatible test client launched"
      else
        record simulator_test_client FAIL "test client launch failed; see test-client-launch.log"
      fi
    fi
  else
    record xctest BLOCKED "no IOS_TEST_WORKSPACE/IOS_TEST_PROJECT + IOS_TEST_SCHEME was supplied"
    record simulator_test_client BLOCKED "no independently developed simulator-compatible IOS_TEST_APP was supplied"
  fi
fi

capture_performance_evidence
capture_simulator_evidence

python3 - "$RESULTS_TSV" "$RESULTS_JSON" "$REPORT_MD" "$RUN_ID" <<'PY'
import json
import pathlib
import sys

results_path, json_path, report_path, run_id = map(pathlib.Path, sys.argv[1:])
rows = []
for line in results_path.read_text(encoding="utf-8").splitlines():
    stage, status, detail = line.split("\t", 2)
    rows.append({"stage": stage, "status": status, "detail": detail})

payload = {
    "run_id": str(run_id),
    "results": rows,
    "summary": {
        "pass": sum(row["status"] == "PASS" for row in rows),
        "fail": sum(row["status"] == "FAIL" for row in rows),
        "blocked": sum(row["status"] == "BLOCKED" for row in rows),
        "skip": sum(row["status"] == "SKIP" for row in rows),
    },
}
json_path.write_text(json.dumps(payload, indent=2) + "\n", encoding="utf-8")

with report_path.open("w", encoding="utf-8") as report:
    report.write(f"# Phase 11 test report\n\nRun: `{run_id}`\n\n")
    report.write("Statuses are evidence from this run. `BLOCKED` means the required implementation/input was not available; it is not a pass.\n\n")
    report.write("| Stage | Status | Detail |\n|---|---|---|\n")
    for row in rows:
        detail = row["detail"].replace("|", "\\|")
        report.write(f"| `{row['stage']}` | **{row['status']}** | {detail} |\n")
    report.write("\nArtifacts are in this run directory, including logs, screenshots, and any XCTest result bundle.\n")
PY

fail_count="$(awk -F '\t' '$2 == "FAIL" { n++ } END { print n + 0 }' "$RESULTS_TSV")"
blocked_count="$(awk -F '\t' '$2 == "BLOCKED" { n++ } END { print n + 0 }' "$RESULTS_TSV")"
pass_count="$(awk -F '\t' '$2 == "PASS" { n++ } END { print n + 0 }' "$RESULTS_TSV")"
printf 'Summary: PASS=%s FAIL=%s BLOCKED=%s\n' "$pass_count" "$fail_count" "$blocked_count"
printf 'Report: %s\n' "$REPORT_MD"

if [[ "$fail_count" -gt 0 ]]; then
  exit 1
elif [[ "$blocked_count" -gt 0 ]]; then
  exit 2
fi
exit 0
