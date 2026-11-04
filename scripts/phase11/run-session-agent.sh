#!/usr/bin/env bash

set -euo pipefail
set -o pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deployments/docker/compose.yml"
RUN_ID="${SESSION_AGENT_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
RUN_DIR="${SESSION_AGENT_STACK_DIR:-$ROOT_DIR/artifacts/session-agent-stack/$RUN_ID}"
mkdir -p "$RUN_DIR"

POSTGRES_USER="${POSTGRES_USER:-ros}"
POSTGRES_DB="${POSTGRES_DB:-ros}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-localtestpass}"
POSTGRES_PORT="${POSTGRES_PORT:-55432}"
REDIS_PASSWORD="${REDIS_PASSWORD:-localtestpass}"
REDIS_PORT="${REDIS_PORT:-56379}"
TOKEN_SIGNING_SECRET="${TOKEN_SIGNING_SECRET:-phase11-local-secret-change-me-32-bytes-min}"
API_ADDR="${SESSION_AGENT_API_ADDR:-127.0.0.1:18081}"

export POSTGRES_USER POSTGRES_DB POSTGRES_PASSWORD POSTGRES_PORT
export REDIS_PASSWORD REDIS_PORT TOKEN_SIGNING_SECRET
export DATABASE_URL="${DATABASE_URL:-postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/${POSTGRES_DB}?sslmode=disable}"
export REDIS_ADDR="${REDIS_ADDR:-127.0.0.1:${REDIS_PORT}}"
export ROS_API_HTTP_ADDR="$API_ADDR"
export ROS_GAME_SERVER_ID="${ROS_GAME_SERVER_ID:-game-server-local}"
export ROS_GAME_SERVER_IDS="${ROS_GAME_SERVER_IDS:-$ROS_GAME_SERVER_ID}"
export ROS_GAME_SERVER_TOKEN="${ROS_GAME_SERVER_TOKEN:-phase11-game-server-token}"

api_pid=""

cleanup() {
  if [[ -n "$api_pid" ]] && kill -0 "$api_pid" >/dev/null 2>&1; then
    kill "$api_pid" >/dev/null 2>&1 || true
    wait "$api_pid" 2>/dev/null || true
  fi
  docker compose -f "$COMPOSE_FILE" down > "$RUN_DIR/docker-compose-down.log" 2>&1 || true
}
trap cleanup EXIT INT TERM

POSTGRES_USER="$POSTGRES_USER" POSTGRES_DB="$POSTGRES_DB" \
POSTGRES_PASSWORD="$POSTGRES_PASSWORD" REDIS_PASSWORD="$REDIS_PASSWORD" \
POSTGRES_PORT="$POSTGRES_PORT" REDIS_PORT="$REDIS_PORT" \
  "$ROOT_DIR/scripts/phase4/verify.sh" > "$RUN_DIR/storage-verify.log" 2>&1

"$ROOT_DIR/bin/api-server" > "$RUN_DIR/api-server.log" 2>&1 &
api_pid=$!

api_ready=0
for _ in $(seq 1 30); do
  if curl --fail --silent "http://${API_ADDR}/readyz" > "$RUN_DIR/api-ready.txt" 2>&1; then
    api_ready=1
    break
  fi
  if ! kill -0 "$api_pid" >/dev/null 2>&1; then
    cat "$RUN_DIR/api-server.log" >&2
    exit 1
  fi
  sleep 1
done
if [[ "$api_ready" != "1" ]]; then
  cat "$RUN_DIR/api-server.log" >&2
  exit 1
fi

SESSION_AGENT_RUN_ID="$RUN_ID" \
SESSION_AGENT_API_URL="http://${API_ADDR}" \
SESSION_AGENT_OUTPUT_DIR="${SESSION_AGENT_OUTPUT_DIR:-$ROOT_DIR/artifacts/session-agent/$RUN_ID}" \
  python3 "$ROOT_DIR/scripts/phase11/session-agent-test.py" | tee "$RUN_DIR/session-agent-output.json"

printf '%s\n' "session-agent-stack: API, PostgreSQL, Redis, and dedicated-server lifecycle passed"
