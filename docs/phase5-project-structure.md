# Phase 5 — project structure

Status: structure and repeatable commands established.

```text
cmd/
  game-server/       authoritative UDP session executable
  protocol-client/   independent protocol/load test client
  storage-smoke/     PostgreSQL/Redis connectivity smoke test
internal/
  game/              authoritative simulation and session state
  storage/           PostgreSQL and Redis adapters
migrations/          ordered PostgreSQL schema migrations
deployments/docker/  local dependency Compose configuration
research/            read-only IPA inventories and feasibility findings
scripts/phase4/      storage verification workflow
scripts/phase11/     simulator/protocol/XCTest workflow
docs/                phase decisions and operational notes
```

The dependency direction is intentionally inward: domain and application code
will not import HTTP, database, Redis, or client-specific types. Executables
compose adapters around those packages. Generated binaries, simulator output,
and local credentials are excluded through `.gitignore`.

## Standard commands

```sh
make format
make test
make vet
make race
make build
make capacity
make recovery
make session-agent
```

The Makefile is a convenience layer over the same commands already used in the
phase verification runs. It does not hide test failures or claim compatibility
with the original IPA.
