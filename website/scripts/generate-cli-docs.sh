#!/usr/bin/env bash
# Generate CLI reference markdown from cobra into docs/cli/generated.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="$ROOT/website/docs/cli/generated"

rm -rf "$OUT"
mkdir -p "$OUT"

(
  cd "$ROOT/shoulders-cli"
  go run . docs --dir "$OUT"
)

# Cobra emits bare <arg> placeholders that MDX parses as JSX — escape them.
python3 "$ROOT/website/scripts/sanitize-cli-docs.py" "$OUT"

# Docusaurus sidebar front matter: rename README-style index and add category label.
cat > "$OUT/_category_.json" <<'EOF'
{"label": "Generated reference", "position": 99}
EOF

echo "CLI docs generated in $OUT ($(ls "$OUT" | wc -l | tr -d ' ') files)"
