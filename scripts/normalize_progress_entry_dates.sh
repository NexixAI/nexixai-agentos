\
#!/usr/bin/env bash
set -euo pipefail

# Normalize docs/plan/progress-entries filenames to match their internal header date.
# This script prints git mv commands; it does NOT execute them automatically.

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIR="$ROOT_DIR/docs/plan/progress-entries"

if [[ ! -d "$DIR" ]]; then
  echo "progress-entries dir not found: $DIR" >&2
  exit 1
fi

shopt -s nullglob
for f in "$DIR"/*.md; do
  base="$(basename "$f")"
  # Expect first header like: ## YYYY-MM-DD — Phase X
  header_date="$(grep -E '^## [0-9]{4}-[0-9]{2}-[0-9]{2} ' "$f" | head -n1 | sed -E 's/^## ([0-9]{4}-[0-9]{2}-[0-9]{2}).*$/\1/')"
  if [[ -z "$header_date" ]]; then
    continue
  fi

  file_date="$(echo "$base" | grep -Eo '^[0-9]{4}-[0-9]{2}-[0-9]{2}' || true)"
  if [[ -z "$file_date" ]]; then
    continue
  fi

  if [[ "$file_date" != "$header_date" ]]; then
    new="${base/$file_date/$header_date}"
    echo "git mv \"$DIR/$base\" \"$DIR/$new\""
  fi
done
