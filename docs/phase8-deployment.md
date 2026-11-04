# Phase 8 — deployment

Status: development container and Kubernetes deployment baseline added.

## Local

- `deployments/docker/compose.yml` runs password-required PostgreSQL and Redis.
- `deployments/docker/Dockerfile.game-server` builds a static Linux game-server
  image and runs it as a non-root user.
- `deployments/docker/Dockerfile.api-server` builds the independent HTTP API
  image and runs it as a non-root user.
- `deployments/docker/Dockerfile.migrations` builds a PostgreSQL-client image
  containing the ordered migrations. The Kubernetes API Deployment runs it as
  a non-root init container before serving traffic, so a rollout cannot become
  ready before the schema is applied.
- `.github/workflows/ci.yml` verifies code, tests, PostgreSQL/Redis storage,
  API/dedicated-server lifecycle integration, images, capacity, and recovery
  on pushes and pull requests.
  `make validate-deploy` validates all
  Kustomize overlays, Kubernetes schemas, Prometheus rules, Grafana JSON, and
  workflow syntax in the same CI path. `.github/workflows/release.yml`
  publishes tag-triggered API/game images to GHCR with immutable SHA/semver
  tags plus BuildKit provenance and SBOM attestations. Registry access and
  deployment promotion remain environment-owned.
- Health endpoints are `/healthz` and `/readyz`; game traffic is UDP 9000 and
  health/metrics traffic is TCP 8080 on an internal `ClusterIP` service. Only
  UDP gameplay is exposed by the game `LoadBalancer`. The API listens on TCP
  8081 and exposes the same health endpoints through its internal service.

Build locally with:

```sh
docker build -f deployments/docker/Dockerfile.game-server -t ros-game-server:local .
docker build -f deployments/docker/Dockerfile.api-server -t ros-api-server:local .
docker build -f deployments/docker/Dockerfile.migrations -t ros-migrations:local .
```

Verified on 2026-10-29: both builds completed successfully as Linux arm64
images. The resulting local image digests were
`sha256:f2419c1fe473691594b2e6c00da70f9ffe042d0fffddd4e487c1e76db5e5f1fd`
for the game server and
`sha256:d54ad7f6aabff76ae0fc45deadf0cc8ef916d4c15618fc0563e20e6bf14e59eb`
for the API server. The Kubernetes files passed local YAML parsing, and
kubeconform v0.6.7 validated the base plus dev/staging/prod overlays against
Kubernetes 1.30 schemas.

Verified again on 2026-10-30 after the lifecycle-client and server-pool
changes: `ros-api-server:current` and `ros-game-server:current` both built
successfully as non-root Linux arm64 images. Their local image IDs were
`sha256:f7c0623116eb4604ed224a24b6cd4f32b0c1c467299ac14e112c8cd8982ca55b`
and
`sha256:d52118eeca750944b62fbd48fe0d9aab77ec824a7ec64c6431e35058ce8a4db2`,
respectively.

Verified on 2026-10-30 after adding automated migrations:
`ros-migrations:current` built as a Linux arm64 image running as the `postgres`
user with image ID
`sha256:6c1f4831a5e4ed253bb31e6fea2560ce932847391aef0d03e8085ced43a97fdb`.
The image was executed on the local Compose network against PostgreSQL and
applied all nine migrations successfully on an existing schema.
It also passed with a read-only root filesystem and all Linux capabilities
dropped, matching the Kubernetes init-container security context.

## Kubernetes

The manifests configure two game-server replicas, an API migration init
container, Prometheus scrape annotations for API/game metrics, internal health
services, UDP gameplay service ports, readiness/liveness probes, bounded
migration resources, non-root execution, a read-only root filesystem, resource
limits, and a UDP-only LoadBalancer service. The image name is intentionally a
placeholder and must be replaced by the deployment pipeline.

`deployments/kubernetes/overlays/dev`, `staging`, and `prod` provide explicit
Kustomize namespaces and replica counts. `kubectl kustomize` successfully
renders the base and all three overlays locally. The overlays deliberately
leave runtime credentials and registry promotion to the deployment platform.

The game-server binary has an optional control-plane lifecycle client. A
session launcher must inject ROS_API_BASE_URL, ROS_GAME_SESSION_ID,
ROS_GAME_SERVER_ID, and ROS_GAME_SERVER_TOKEN together; the process then calls
the independent API's internal start, heartbeat, and graceful-completion
endpoints. When those variables are absent, the game server remains a
standalone UDP process. A production allocator must inject a unique server ID
per dedicated instance and assign that ID to the session; the local baseline
uses one configured server ID for deterministic tests.

The API accepts ROS_GAME_SERVER_IDS as a comma-separated, validated assignment
pool and selects IDs round-robin when creating sessions. Each ID must map to a
distinct dedicated instance in the deployment platform; the local default is a
single entry for reproducibility.

PostgreSQL, Redis, the API gateway, and certificate/secret distribution should
use managed or separately operated production services. The API also supports
direct TLS when `ROS_API_TLS_CERT_FILE` and `ROS_API_TLS_KEY_FILE` are mounted
as a reviewed deployment secret. The example secret manifest contains
placeholders only and must not be applied as-is. Set
`OTEL_EXPORTER_OTLP_ENDPOINT` through the deployment secret/configuration to
export API traces over OTLP/HTTP to the approved collector.

Monitoring artifacts are versioned under `deployments/monitoring/`: the
Prometheus rule files alert on API 5xx growth, availability/error-budget burn,
average API duration, game tick overruns, tick-compliance SLOs, and validation
violations. The Grafana dashboard visualizes request errors, API availability,
tick duration/compliance, active players, replication bytes, and validation
events. The independent-server baseline SLO rules use 99.5% API availability,
250ms average API duration, and 99.9% tick compliance; production owners must
review these thresholds against measured traffic. A deployment must still
provide Prometheus scraping, Grafana provisioning, notification routing, and
retention.

Capacity planning is executable with `make capacity` or
`/usr/bin/python3 scripts/phase11/capacity-benchmark.py`. It starts an isolated
game-server process, runs the 2/10/25/50/100-player protocol profiles, records
tick/packet/RTT/CPU/RSS evidence, and fails on protocol errors, tick overruns,
or RTT above the configured local threshold. The generated report is explicitly
local independent-server evidence, not a production capacity guarantee or
original-client compatibility claim.

## Rollback and recovery

1. Deploy an immutable image tag and migration version.
2. Observe readiness, active sessions, tick latency, error rate, and match
   completion metrics before increasing traffic.
3. Roll back the Deployment image to the previous immutable tag if health or
   gameplay SLOs regress.
4. Apply only backward-compatible migrations before the application rollout;
   destructive schema changes require a separately reviewed migration.
5. Back up PostgreSQL before production migrations and verify restore in a
   separate environment. Redis state is disposable and must be rebuilt from
   durable records where necessary.

The local PostgreSQL backup command is `scripts/phase4/backup.sh`; it writes a
gzip-validated logical SQL archive under `artifacts/backups/` by default. The
companion `scripts/phase4/restore.sh` restores into a new isolated database and
refuses to drop an existing target. The scripts do not back up Redis as durable
state; production retention, scheduling, and promotion remain deployment
responsibilities.

The release, rollback, health, backup, secret-rotation, network-segmentation,
and incident procedures are collected in
[`docs/operations-runbook.md`](operations-runbook.md).
