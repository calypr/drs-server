#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
example_dir="$(mktemp -d "$repo_root/client/.readme-example.XXXXXX")"
trap 'rm -r "$example_dir"' EXIT

awk '
  /^## Usage$/ { usage = 1; next }
  usage && /^```go$/ { example = 1; next }
  example && /^```$/ { exit }
  example { print }
' "$repo_root/client/README.md" > "$example_dir/main.go"

if [[ ! -s "$example_dir/main.go" ]]; then
  echo "client/README.md does not contain a Go usage example" >&2
  exit 1
fi

relative_dir="${example_dir#"$repo_root/client/"}"
(
  cd "$repo_root/client"
  GOWORK=off go build -o "$example_dir/readme-example" "./$relative_dir"
)
