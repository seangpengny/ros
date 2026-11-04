# Kubernetes environments

The manifests under `base/` are the common base. Kustomize overlays provide explicit
development, staging, and production namespaces and replica counts:

```sh
kubectl kustomize deployments/kubernetes/overlays/dev
kubectl kustomize deployments/kubernetes/overlays/staging
kubectl kustomize deployments/kubernetes/overlays/prod
```

The overlays intentionally do not create `ros-runtime`. Inject
`DATABASE_URL`, `REDIS_ADDR`, `REDIS_PASSWORD`, `TOKEN_SIGNING_SECRET`,
`ROS_GAME_SERVER_TOKEN`, and any `ROS_API_TLS_*` or
`OTEL_EXPORTER_OTLP_ENDPOINT` settings through the
deployment platform's secret manager. Production image references must be
replaced with immutable registry digests during CI/CD promotion; the example
manifests are not production credentials or a live cluster configuration.

The API Deployment runs the `ros-migrations` image as a non-root init container.
The image applies `/migrations/*.sql` in lexical order using the
`DATABASE_URL` from `ros-runtime`; migration image promotion must use the same
immutable release revision as the API image. Both API and game pods expose
Prometheus scrape annotations for their `/metrics` endpoints; the cluster's
Prometheus installation remains responsible for discovery and retention.
The game `LoadBalancer` exposes only UDP gameplay on port 9000. Its health and
metrics port is available through the separate internal
`ros-game-server-health` `ClusterIP` service and is not publicly exposed.
