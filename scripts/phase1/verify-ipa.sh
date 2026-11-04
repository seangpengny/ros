#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
IPA_PATH="${LEGACY_IPA:-$ROOT_DIR/RoS-Legacy-iOS.ipa}"
HASH_FILE="${LEGACY_IPA_HASH_FILE:-$ROOT_DIR/research/sha256.txt}"

[[ -f "$IPA_PATH" ]] || { echo "IPA not found: $IPA_PATH" >&2; exit 1; }
[[ -f "$HASH_FILE" ]] || { echo "IPA hash record not found: $HASH_FILE" >&2; exit 1; }

ipa_name="$(basename "$IPA_PATH")"
expected="$(awk -v name="$ipa_name" '$2 == name { print $1; exit }' "$HASH_FILE")"
if [[ ! "$expected" =~ ^[[:xdigit:]]{64}$ ]]; then
  echo "no valid SHA-256 record for $ipa_name in $HASH_FILE" >&2
  exit 1
fi

if command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$IPA_PATH" | awk '{print $1}')"
else
  actual="$(sha256sum "$IPA_PATH" | awk '{print $1}')"
fi

if [[ "$actual" != "$expected" ]]; then
  printf 'IPA integrity check failed: expected %s, got %s\n' "$expected" "$actual" >&2
  exit 1
fi

size_bytes="$(wc -c < "$IPA_PATH" | tr -d '[:space:]')"
printf 'IPA integrity: PASS sha256=%s file=%s bytes=%s\n' "$actual" "$ipa_name" "$size_bytes"
