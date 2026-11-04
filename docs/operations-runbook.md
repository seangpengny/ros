# Operations runbook

This runbook applies to the independent ROS-style backend. It does not provide
instructions for bypassing or modifying the supplied ROS Legacy client.

## Release and rollout

1. Build immutable API and game-server image tags from a reviewed commit.
2. Run `make format`, `make test`, `make vet`, `make race`, `make build`, and
   the pinned `govulncheck` command from `README.md`.
3. Render the base and dev/staging/prod Kustomize overlays and inspect the
   resulting image references, namespaces, resource limits, probes, and secret
   references.
4. Apply backward-compatible migrations before increasing application traffic.
5. Roll out one environment at a time. Watch readiness, API 5xx rate, tick
   duration/overruns, active players, validation violations, and match
   completion before promoting.
6. Drain dedicated game servers before terminating them so reconnect windows and
   match completion can run to policy.

## Health and incident triage

- API `/healthz` indicates process health; `/readyz` is the rollout gate and
  `/metrics` is the Prometheus scrape endpoint.
- Game-server TCP health uses `/healthz`, `/readyz`, and `/metrics`; gameplay
  traffic is UDP and must not be routed through an HTTP-only load balancer.
- Correlate API `X-Request-ID`, W3C `traceparent`, `X-Trace-ID`, and structured
  `trace_id`/`span_id` logs. Never copy authorization headers, refresh tokens,
  passwords, or recovery tokens into incident tickets.
- Investigate `ROSAPIHighErrorRate`, `ROSGameTickOverruns`, and
  `ROSGameValidationViolations` using the Prometheus rules in
  `deployments/monitoring/prometheus/ros-alerts.yaml`.

## Rollback

1. Stop promotion and preserve the failing image digest, deployment events,
   logs, metrics, and trace IDs.
2. Roll back only to the last known-good immutable application image.
3. Do not reverse a migration destructively. If the schema is incompatible,
   restore the application first, then perform a reviewed forward migration.
4. Drain or terminate only game-server instances that have passed their
   reconnect/completion policy; do not silently discard active match state.

## PostgreSQL backup and recovery

Create a gzip-validated logical backup with:

```sh
POSTGRES_USER=... POSTGRES_DB=... POSTGRES_PASSWORD=... \
REDIS_PASSWORD=... scripts/phase4/backup.sh
```

Validate a backup with `gzip -t`, restore it into a new database using
`scripts/phase4/restore.sh`, apply the current migrations, and run the storage
smoke test before considering promotion. The restore script refuses to drop an
existing target. Redis queues, leases, presence, rate limits, and reconnect
state are ephemeral and must be rebuilt; PostgreSQL is the durable source of
truth for identity and results.

Production scheduling must retain multiple encrypted backup generations,
periodically exercise isolated restores, and record recovery-point and
recovery-time measurements. The repository scripts provide the safe local
mechanics; scheduling, encryption-key custody, and retention storage belong to
the deployment platform.

## Secrets and network controls

- Inject database credentials, token-signing current/previous keys, TLS
  certificates, OTLP endpoint credentials, and the optional recovery-webhook
  secret from a managed secret store or mounted secret; never use
  `secret.example.yaml` as a live secret.
- Keep migration credentials separate from the runtime database role, and keep
  operator/deployment credentials separate from player access tokens. No
  administrative operations should be exposed through the player API.
- Rotate signing keys by issuing with the current key while temporarily
  accepting the previous key, then remove the previous key after the access
  token maximum lifetime.
- Terminate public TLS at a reviewed gateway or use the API's TLS 1.3 listener;
  keep PostgreSQL and Redis on private network segments with no public ingress.
- Allow UDP only from the game gateway/allocated client network to the game
  service, allow API TCP only through the gateway, and restrict database/Redis
  ingress to application workloads.
- Use provider-level DDoS protection, connection limits, UDP rate controls, and
  abuse monitoring. Application rate limits and validation metrics are defense
  in depth, not a substitute for upstream capacity protection.
