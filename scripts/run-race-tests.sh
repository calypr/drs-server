#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cache_dir="${GOCACHE:-${TMPDIR:-/tmp}/syfon-race-cache}"

cd "$repo_root"
CGO_ENABLED=1 GOCACHE="$cache_dir" go test -race -count=1 \
  ./internal/httpapi \
  ./internal/transfers \
  ./internal/transfers/lfs \
  ./internal/projects/storage \
  ./internal/access/authentication

cd "$repo_root/client"
GOWORK=off CGO_ENABLED=1 GOCACHE="$cache_dir" go test -race -count=1 ./...
