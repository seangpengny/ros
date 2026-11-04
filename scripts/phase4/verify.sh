#!/usr/bin/env bash

set -euo pipefail
set -o pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deployments/docker/compose.yml"
POSTGRES_USER="${POSTGRES_USER:?set POSTGRES_USER}"
POSTGRES_DB="${POSTGRES_DB:?set POSTGRES_DB}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}"
REDIS_PASSWORD="${REDIS_PASSWORD:?set REDIS_PASSWORD}"
POSTGRES_PORT="${POSTGRES_PORT:-55432}"
REDIS_PORT="${REDIS_PORT:-56379}"

export POSTGRES_USER POSTGRES_DB POSTGRES_PASSWORD REDIS_PASSWORD POSTGRES_PORT REDIS_PORT

docker compose -f "$COMPOSE_FILE" config -q
docker compose -f "$COMPOSE_FILE" up -d

for _ in $(seq 1 30); do
	postgres_container="$(docker compose -f "$COMPOSE_FILE" ps -q postgres)"
	redis_container="$(docker compose -f "$COMPOSE_FILE" ps -q redis)"
	postgres_health="$(docker inspect --format '{{.State.Health.Status}}' "$postgres_container" 2>/dev/null || true)"
	redis_health="$(docker inspect --format '{{.State.Health.Status}}' "$redis_container" 2>/dev/null || true)"
	if [ "$postgres_health" = "healthy" ] && [ "$redis_health" = "healthy" ]; then
		break
	fi
	sleep 2
done

if [ "$postgres_health" != "healthy" ] || [ "$redis_health" != "healthy" ]; then
	docker compose -f "$COMPOSE_FILE" ps -a
	docker compose -f "$COMPOSE_FILE" logs --tail=100 postgres redis
	exit 1
fi

# The official PostgreSQL image only applies POSTGRES_PASSWORD during first
# initialization. Keep repeated local verification runs deterministic when a
# named volume already exists, without deleting the volume or its test data.
if ! docker compose -f "$COMPOSE_FILE" exec -T postgres \
	psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
	-v "ros_test_password=$POSTGRES_PASSWORD" > /tmp/ros-postgres-password-sync.log 2>&1 <<'SQL'
SELECT format('ALTER ROLE %I PASSWORD %L', current_user, :'ros_test_password') \gexec
SQL
then
	cat /tmp/ros-postgres-password-sync.log
	exit 1
fi

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/001_initial.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/003_domain_persistence.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/004_api_state.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/005_game_result_events.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/006_account_recovery.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/007_lobby_invitations.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/008_game_session_external_lobby.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/009_shop.sql"
docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" < "$ROOT_DIR/migrations/002_indexes.sql"
docker compose -f "$COMPOSE_FILE" exec -T redis redis-cli -a "$REDIS_PASSWORD" ping

DATABASE_URL="postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@127.0.0.1:${POSTGRES_PORT}/${POSTGRES_DB}?sslmode=disable" \
REDIS_ADDR="127.0.0.1:${REDIS_PORT}" REDIS_PASSWORD="$REDIS_PASSWORD" \
go run ./cmd/storage-smoke

printf '%s\n' 'phase4: PostgreSQL, Redis, migrations, and Go connectivity smoke test passed'
