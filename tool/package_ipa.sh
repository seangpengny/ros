#!/usr/bin/env bash
set -euo pipefail

# Package client.app into an installable IPA
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="${1:-$ROOT_DIR/ipa/RoS-Legacy-iOS/client.app}"
OUT_IPA="${2:-$ROOT_DIR/ipa/RoS-Local-Client.ipa}"

if [[ ! -d "$APP_DIR" ]]; then
    echo "Error: app bundle not found at: $APP_DIR" >&2
    exit 1
fi

echo "==> Packaging IPA from: $APP_DIR"
WORK_DIR=$(mktemp -d /tmp/ros_ipa_pkg.XXXXXX)
trap 'rm -rf "$WORK_DIR"' EXIT

mkdir -p "$WORK_DIR/Payload"
echo "==> Copying app bundle into Payload/..."
cp -R "$APP_DIR" "$WORK_DIR/Payload/client.app"

# Remove any macOS metadata files
find "$WORK_DIR" -name ".DS_Store" -delete

echo "==> Creating IPA archive: $OUT_IPA..."
cd "$WORK_DIR"
zip -qr -9 "$OUT_IPA" Payload

echo "==> Done! Successfully built IPA at: $OUT_IPA ($(du -h "$OUT_IPA" | cut -f1))"

