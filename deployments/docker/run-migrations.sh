#!/bin/sh

set -eu

: "${DATABASE_URL:?DATABASE_URL is required}"

for migration in /migrations/*.sql; do
	psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f "$migration"
done
