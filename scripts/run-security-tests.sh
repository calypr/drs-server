#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

CGO_ENABLED=1 go test -count=1 \
  ./internal/access/... \
  ./internal/config \
  ./internal/persistence/credentialcipher \
  ./client/config \
  ./client/request \
  ./client
