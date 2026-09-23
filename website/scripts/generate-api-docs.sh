#!/usr/bin/env bash
# Generate API reference markdown from Crossplane XRDs into docs/api/generated.
# Implemented in Go (shoulders-cli/tools/genapidocs) — no Python needed.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/website/docs/api/generated"

rm -rf "$OUT"
mkdir -p "$OUT"

(
  cd "$ROOT/shoulders-cli"
  go run ./tools/genapidocs \
    --defs "$ROOT/2-addons/manifests/crossplane/definitions" \
    --out "$OUT"
)
