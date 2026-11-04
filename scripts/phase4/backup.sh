#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/deployments/docker/compose.yml"
POSTGRES_USER="${POSTGRES_USER:?set POSTGRES_USER}"
POSTGRES_DB="${POSTGRES_DB:?set POSTGRES_DB}"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD}"
REDIS_PASSWORD="${REDIS_PASSWORD:?set REDIS_PASSWORD}"
BACKUP_DIR="${BACKUP_DIR:-$ROOT_DIR/artifacts/backups}"
BACKUP_ID="${BACKUP_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
BACKUP_FILE="$BACKUP_DIR/ros-${BACKUP_ID}.sql.gz"

export POSTGRES_USER POSTGRES_DB POSTGRES_PASSWORD REDIS_PASSWORD
mkdir -p "$BACKUP_DIR"

docker compose -f "$COMPOSE_FILE" exec -T postgres pg_dump \
	--no-owner --no-privileges -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
	| gzip -c > "$BACKUP_FILE"
gzip -t "$BACKUP_FILE"

printf 'postgres backup: %s\n' "$BACKUP_FILE"
printf 'redis policy: ephemeral AOF state is not treated as the durable source of truth\n'
