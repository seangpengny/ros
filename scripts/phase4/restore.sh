#!/usr/bin/env bash

set -euo pipefail
set -o pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deployments/docker/compose.yml"
POSTGRES_USER="${POSTGRES_USER:?set POSTGRES_USER}"
POSTGRES_DB="${POSTGRES_DB:?set POSTGRES_DB}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}"
REDIS_PASSWORD="${REDIS_PASSWORD:?set REDIS_PASSWORD}"
BACKUP_FILE="${BACKUP_FILE:?set BACKUP_FILE to a gzip-compressed pg_dump archive}"
RESTORE_DB="${RESTORE_DB:?set RESTORE_DB to a new isolated restore database}"

case "$BACKUP_FILE" in
	/*) ;;
	*) BACKUP_FILE="$ROOT_DIR/$BACKUP_FILE" ;;
esac

if [ ! -f "$BACKUP_FILE" ]; then
	printf 'backup archive does not exist: %s\n' "$BACKUP_FILE" >&2
	exit 1
fi

if [[ ! "$RESTORE_DB" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
	printf 'RESTORE_DB must be a simple PostgreSQL identifier\n' >&2
	exit 1
fi

if [ "$RESTORE_DB" = "$POSTGRES_DB" ]; then
	printf 'RESTORE_DB must differ from POSTGRES_DB; restore into an isolated database first\n' >&2
	exit 1
fi

gzip -t "$BACKUP_FILE"
export POSTGRES_USER POSTGRES_DB POSTGRES_PASSWORD REDIS_PASSWORD

database_exists="$({
	docker compose -f "$COMPOSE_FILE" exec -T postgres psql -Atq \
		-U "$POSTGRES_USER" -d postgres \
		-c "SELECT 1 FROM pg_database WHERE datname = '$RESTORE_DB'"
} || true)"
if [ "$database_exists" = "1" ]; then
	printf 'restore target database already exists: %s\n' "$RESTORE_DB" >&2
	printf 'choose a new RESTORE_DB; this script never drops an existing database\n' >&2
	exit 1
fi

docker compose -f "$COMPOSE_FILE" exec -T postgres psql -v ON_ERROR_STOP=1 \
	-U "$POSTGRES_USER" -d postgres \
	-c "CREATE DATABASE \"$RESTORE_DB\""

gzip -dc "$BACKUP_FILE" | docker compose -f "$COMPOSE_FILE" exec -T postgres psql \
	-v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$RESTORE_DB"

table_count="$(docker compose -f "$COMPOSE_FILE" exec -T postgres psql -Atq \
	-U "$POSTGRES_USER" -d "$RESTORE_DB" \
	-c "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public'")"
printf 'postgres restore: %s -> database %s (%s public tables)\n' "$BACKUP_FILE" "$RESTORE_DB" "$table_count"
printf 'restore policy: target databases are isolated and never dropped by this script\n'
