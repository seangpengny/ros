# Phase 4 — data storage

Status: implemented and verified locally with Docker.

## Durable storage

PostgreSQL is the source of truth for accounts, profiles, device sessions,
lobbies, parties, matchmaking tickets, game sessions, participants, player
inventory, statistics, leaderboards, audit records, API-state snapshots,
committed match results, and game completion events. The schema is split into
`migrations/001_initial.sql`, `migrations/002_indexes.sql`,
`migrations/003_domain_persistence.sql`, `migrations/004_api_state.sql`,
`migrations/005_game_result_events.sql`, `migrations/006_account_recovery.sql`,
`migrations/007_lobby_invitations.sql`,
`migrations/008_game_session_external_lobby.sql`, and
`migrations/009_shop.sql`.

## Ephemeral storage

Redis is configured for disposable presence, pending invitation, queue, lease, rate-limit, and
reconnect state. It uses AOF persistence in the local Compose environment, but
application correctness must not depend on Redis being the durable source of
truth.

## Implementation

- `internal/storage/postgres.go`: validated `pgxpool` construction and ping.
- `internal/storage/redis.go`: authenticated Redis client construction and ping.
- `cmd/storage-smoke`: connectivity smoke test with required environment values.
- `deployments/docker/compose.yml`: password-required PostgreSQL and Redis
  development services with health checks and named volumes.
- `scripts/phase4/verify.sh`: starts services, waits for health, applies all
  migrations and indexes, checks Redis, and runs the Go connectivity smoke
  test. It also synchronizes the local PostgreSQL test-role password when a
  named volume already exists, without deleting the volume or its data.
- `scripts/phase4/backup.sh`: creates and gzip-validates a PostgreSQL logical
  backup without deleting or altering live data. Redis is explicitly treated
  as rebuildable ephemeral state.
- `scripts/phase4/restore.sh`: restores a gzip-validated archive into a new,
  isolated PostgreSQL database and refuses to drop an existing target.

The selected PostgreSQL image is 18-alpine, matching the current PostgreSQL
18 documentation. The Redis image is 8.2-alpine, aligned with the Redis 8.2
runtime available on the host. The Go adapters use `pgx/v5` 5.11.0 and
`go-redis/v9` 9.23.0; the latter requires Go 1.26, so the Go toolchain selected
Go 1.26 automatically during dependency resolution.

## Verification

Executed successfully on 2026-10-29:

```sh
go test ./...
POSTGRES_USER=ros POSTGRES_DB=ros POSTGRES_PASSWORD=<local-value> \
REDIS_PASSWORD=<local-value> scripts/phase4/verify.sh
```

The verification run created the containers, waited for both health checks,
applied all nine migration files without errors, received `PONG` from Redis, and
connected to PostgreSQL through the Go pool. No production credentials are
stored in the repository.

The verifier was rerun successfully on 2026-10-30 against the existing named
volume after the password-synchronization fix; all migrations remained
idempotent and the storage smoke test passed.

Recovery policy: restore PostgreSQL logical backups into an isolated database,
validate migrations and foreign-key integrity, then promote only after an
application smoke test. Redis queues, leases, rate limits, and presence are
recreated from durable records or normal client reconnection; they are not the
authoritative source of account or match-result data.

Retention policy baseline: access tokens expire after 15 minutes, refresh
sessions after 30 days, recovery tokens after 30 minutes, presence after 90
seconds, reconnect leases after 15 seconds, and lobby/invitation/queue state
after 24 hours. Completed match results, account records, and audit events are
durable records and require a deployment-approved legal/business retention
period plus an audited purge process; the local baseline intentionally does not
delete them automatically. Backup generation retention, encryption, and purge
are governed by the production runbook and managed storage policy.

Latest backup verification: `artifacts/backups/ros-20261029T2145Z-recovery.sql.gz`
was created from the local PostgreSQL service and passed `gzip -t`. A restore
rehearsal from that archive loaded 92 accounts, 78 game-result events, 11 API
lobbies, 11 API allocations, and 19 public tables into an isolated database;
the temporary database was removed after verification.

Restore rehearsal:

```sh
BACKUP_FILE=artifacts/backups/ros-20261029T2145Z-recovery.sql.gz \
RESTORE_DB=ros_restore_rehearsal \
POSTGRES_USER=ros POSTGRES_DB=ros POSTGRES_PASSWORD=<local-value> \
REDIS_PASSWORD=<local-value> scripts/phase4/restore.sh
```

The restore command intentionally requires a distinct target database. It does
not back up or restore Redis because Redis queues, leases, rate limits, and
presence are ephemeral and rebuilt from durable PostgreSQL state.
