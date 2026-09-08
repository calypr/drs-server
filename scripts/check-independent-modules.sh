#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_cmd="${GO:-go}"
cache_dir="${GOCACHE:-${TMPDIR:-/tmp}/syfon-independent-modules-cache}"
export GOCACHE="$cache_dir"
export GOWORK=off

cd "$repo_root"
"$go_cmd" mod download
"$go_cmd" test -mod=readonly ./...

(
	cd apigen
	"$go_cmd" test -mod=readonly ./...
)

(
	cd client
	"$go_cmd" test -mod=readonly ./...
)

fixture_dir="$(mktemp -d "${TMPDIR:-/tmp}/syfon-module-consumer.XXXXXX")"
trap 'rm -rf "$fixture_dir"' EXIT

apigen_version="$("$go_cmd" list -m -f '{{.Version}}' github.com/calypr/syfon/apigen)"
client_version="$("$go_cmd" list -m -f '{{.Version}}' github.com/calypr/syfon/client)"
cp -R "$repo_root/testdata/independent-consumer/." "$fixture_dir/"
sed \
	-e "s|__APIGEN_VERSION__|$apigen_version|g" \
	-e "s|__CLIENT_VERSION__|$client_version|g" \
	"$fixture_dir/go.mod.tmpl" >"$fixture_dir/go.mod"
rm "$fixture_dir/go.mod.tmpl"
(
	cd "$fixture_dir"
	"$go_cmd" mod tidy
	"$go_cmd" test -mod=readonly ./...
)
