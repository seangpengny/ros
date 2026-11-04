# Phase 2 — independent backend architecture

Status: architecture complete; implementation proceeds only against this
independent contract. Nothing in this document claims compatibility with the
ROS Legacy IPA.

## Scope and boundary

The backend is a modular monolith first. It owns accounts, profiles, lobbies,
matchmaking, session allocation, authoritative match state, statistics, and
operational telemetry for an original client. A future client-facing protocol
is versioned under this project and is not inferred from the packaged IPA.

```text
Original iOS test client / future authorized client
                    |
                    v
            API + realtime gateway
                    |
      +-------------+-------------+
      |                           |
  domain modules              session allocator
      |                           |
      +----------+----------------+
                 |
       PostgreSQL + Redis adapters
```

## Modules

| Module | Owns | Persistence / transport |
|---|---|---|
| Authentication | registration, login, refresh rotation, device sessions, recovery workflows, rate-limit decisions | PostgreSQL account/session records; Redis rate-limit and short-lived token state |
| Profile | player identity, preferences, progression, inventory projections | PostgreSQL transaction boundary |
| Lobby | presence, parties, invitations, ready state, room membership | Redis ephemeral state with bounded invitation TTLs and PostgreSQL invitation state persistence |
| Matchmaking | solo/duo/squad queues, region and mode constraints, cancellation, timeout | Redis sorted/partitioned queues; durable match allocation record in PostgreSQL |
| Game session | admission, lifecycle, health, reconnect window, completion | PostgreSQL session record; Redis lease/heartbeat state |
| Statistics | match results, eliminations, assists, survival time, rankings, history | PostgreSQL append-only result transaction and read models |
| Gateway | HTTP API, websocket/realtime upgrade, request IDs, auth middleware, schema validation | No domain persistence; translates versioned contracts |
| Operations | health, readiness, metrics, structured logs, traces, audit events | stdout/OTel/Prometheus; no secrets in logs |

## Dependency rules

- Domain modules do not import HTTP, Redis, PostgreSQL, or vendor SDK types.
- Application services depend on narrow repository/queue/clock interfaces.
- Infrastructure adapters implement those interfaces and are replaceable by
  deterministic test doubles.
- Handlers validate input, authorize the caller, invoke an application service,
  and map typed errors to versioned API responses.
- The realtime game server is authoritative. Clients submit bounded inputs;
  the server owns simulation, validation, state transitions, and match results.
- No module may use the IPA’s embedded configuration, credentials, or binary as
  a runtime dependency.

## Initial versioned contracts

The first independent API namespace is `/v1`. The minimum contract set is:

- `POST /v1/auth/register`
- `POST /v1/auth/login`
- `POST /v1/auth/refresh`
- `POST /v1/auth/logout`
- `GET /v1/profile`
- `POST /v1/lobbies`
- `POST /v1/lobbies/{id}/join`
- `POST /v1/lobbies/{id}/ready`
- `POST /v1/matchmaking/queues`
- `DELETE /v1/matchmaking/queues/{id}`
- `GET /v1/sessions/{id}`
- `GET /healthz` and `GET /readyz`

Realtime transport is intentionally not selected until the original client is
out of scope for the independent protocol. The independent client contract can
choose a documented websocket or UDP transport with explicit framing,
authentication, sequence numbers, acknowledgements, and reconnect semantics.

## Data ownership

PostgreSQL is the source of truth for durable identity, profile, inventory,
session allocation, and completed match results. Redis is disposable state for
presence, queues, leases, rate limits, and reconnect windows. Redis loss must
not silently create durable account or match-result mutations.

Every durable mutation has an idempotency key where a client retry could create
duplicates. Match completion is committed once using a unique session/result
constraint. Queue and lease operations include expiry and owner checks.

## Security baseline

- Argon2id password hashing with per-user salts and calibrated parameters.
- Short-lived access tokens and rotating, one-time refresh tokens.
- TLS at the gateway and encrypted service-to-service connections in staging
  and production.
- Strict request size, field, enum, and state-transition validation.
- Per-account, per-device, and per-IP rate limits.
- Redacted structured logs and security audit events for auth/session changes.
- No hardcoded secrets; configuration comes from environment/secret mounts.
- Server-side authorization and gameplay validation; client input is untrusted.

## Observability baseline

Every request carries an `X-Request-ID` and W3C `traceparent` correlation
context. The API emits structured JSON request completion records with trace
and span IDs through the OpenTelemetry Go SDK. Set
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` (or the general
`OTEL_EXPORTER_OTLP_ENDPOINT`) to enable the OTLP/HTTP batch exporter; without
either, local runs keep an SDK provider but do not send telemetry externally. The
game server exposes Prometheus-compatible tick duration,
overrun, active-session/player, replication-byte, UDP-byte, and validation
metrics; the API exposes request latency/error metrics. Queue depth, database
pool, Redis, and match-completion metrics remain adapter/deployment additions.
The collector endpoint and retention policy remain deployment responsibilities.

## Failure and scaling model

- Stateless API instances scale horizontally behind a load balancer.
- PostgreSQL uses migrations, connection limits, backups, and read models only
  where measurement justifies them.
- Redis uses bounded TTL state; no unbounded presence or telemetry keys.
- Matchmaking workers claim queue partitions with leases and idempotent
  allocation records.
- Queue tickets have a bounded five-minute wait window; expired tickets are
  removed from admission before allocation.
- Dedicated game servers expose health/heartbeat and are drained before
  termination.
- Reconnect is a bounded session policy, not an indefinite reservation.

## Phase 2 acceptance criteria

- Module ownership and dependency direction are documented.
- Durable versus ephemeral state is explicit.
- API and realtime protocol versioning is explicit.
- Security, observability, failure, and scaling decisions are documented.
- No undocumented ROS Legacy protocol behavior is presented as fact.

All acceptance criteria are satisfied by this architecture document. Phase 3
implementation must build an original authoritative session service against
these boundaries and add tests before any compatibility work is considered.
