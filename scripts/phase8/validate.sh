#!/usr/bin/env bash

set -euo pipefail
set -o pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KUBECONFORM_IMAGE="${KUBECONFORM_IMAGE:-ghcr.io/yannh/kubeconform:v0.6.7}"
PROMETHEUS_IMAGE="${PROMETHEUS_IMAGE:-prom/prometheus:v3.7.3}"
ACTIONLINT_IMAGE="${ACTIONLINT_IMAGE:-rhysd/actionlint:1.7.7}"

command -v kubectl >/dev/null 2>&1 || { echo "kubectl is required" >&2; exit 1; }
command -v docker >/dev/null 2>&1 || { echo "docker is required" >&2; exit 1; }
command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }

for overlay in base overlays/dev overlays/staging overlays/prod; do
	printf 'validating Kubernetes %s\n' "$overlay"
	rendered="$(kubectl kustomize "$ROOT_DIR/deployments/kubernetes/$overlay")"
	printf '%s\n' "$rendered" |
		docker run --rm -i "$KUBECONFORM_IMAGE" \
			-strict -summary -kubernetes-version 1.30.0
	ROS_KUSTOMIZE_RENDER="$rendered" python3 - "$overlay" <<'PY'
import os
import re
import sys

overlay = sys.argv[1]
documents = os.environ["ROS_KUSTOMIZE_RENDER"].split("\n---\n")
services = {}
for document in documents:
    if not re.search(r"(?m)^kind: Service$", document):
        continue
    name_match = re.search(r"(?m)^  name: ([^\s]+)$", document)
    if name_match:
        services[name_match.group(1)] = document

game_name = next((name for name in services if name.endswith("ros-game-server")), None)
health_name = next((name for name in services if name.endswith("ros-game-server-health")), None)
if game_name is None or health_name is None:
    raise SystemExit(f"{overlay}: expected game and health services")

game = services[game_name]
health = services[health_name]
if "type: LoadBalancer" not in game:
    raise SystemExit(f"{overlay}: game service must remain a LoadBalancer")
if not re.search(r"(?m)^  - name: game-udp$", game):
    raise SystemExit(f"{overlay}: public game service is missing UDP gameplay")
if re.search(r"(?m)^  - name: health-http$", game):
    raise SystemExit(f"{overlay}: public game service exposes health HTTP")
if "type: ClusterIP" not in health or not re.search(r"(?m)^  - name: health-http$", health):
    raise SystemExit(f"{overlay}: health service must be an internal ClusterIP")
PY
done

printf 'validating Kubernetes credential hygiene\n'
if rg -n --glob '*.yaml' --glob '*.yml' \
  'localtestpass|phase11-local-secret|phase11-game-server-token' \
  "$ROOT_DIR/deployments/kubernetes"; then
  echo 'development test credentials must not appear in Kubernetes manifests' >&2
  exit 1
fi

printf 'validating Prometheus rules\n'
docker run --rm --entrypoint /bin/promtool \
	-v "$ROOT_DIR:/repo:ro" "$PROMETHEUS_IMAGE" \
		check rules /repo/deployments/monitoring/prometheus/ros-alerts.yaml \
		/repo/deployments/monitoring/prometheus/ros-slo-rules.yaml

printf 'validating Grafana dashboard JSON\n'
python3 -m json.tool "$ROOT_DIR/deployments/monitoring/grafana/ros-dashboard.json" >/dev/null

printf 'validating GitHub Actions workflows\n'
docker run --rm --entrypoint /usr/local/bin/actionlint \
	-v "$ROOT_DIR:/repo:ro" -w /repo "$ACTIONLINT_IMAGE" \
		.github/workflows/ci.yml .github/workflows/release.yml

printf '%s\n' 'phase8: deployment manifests, monitoring, dashboard, and workflows passed'
