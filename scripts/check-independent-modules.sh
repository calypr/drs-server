#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
go_cmd="${GO:-go}"
cache_dir="${GOCACHE:-${TMPDIR:-/tmp}/syfon-independent-modules-cache}"
export GOCACHE="$cache_dir"
export GOWORK=off

fixture_root="$(mktemp -d "${TMPDIR:-/tmp}/syfon-module-consumer.XXXXXX")"
trap 'rm -rf "$fixture_root"' EXIT

cd "$repo_root"
"$go_cmd" mod download
root_modfile="$fixture_root/root.mod"
cp go.mod "$root_modfile"
cp go.sum "${root_modfile%.mod}.sum"
"$go_cmd" mod edit -modfile="$root_modfile" -replace="github.com/calypr/syfon/apigen=$repo_root/apigen"
"$go_cmd" mod edit -modfile="$root_modfile" -replace="github.com/calypr/syfon/client=$repo_root/client"
GOFLAGS="-modfile=$root_modfile" "$go_cmd" test -mod=readonly ./...

(
	cd apigen
	"$go_cmd" test -mod=readonly ./...
)

(
	cd client
	"$go_cmd" test -mod=readonly ./...
)

apigen_version="$("$go_cmd" list -m -f '{{.Version}}' github.com/calypr/syfon/apigen)"
client_version="$("$go_cmd" list -m -f '{{.Version}}' github.com/calypr/syfon/client)"
run_consumer() {
	local name="$1"
	local apigen_requirement="$2"
	local client_requirement="$3"
	local fixture_dir="$fixture_root/$name"

	mkdir -p "$fixture_dir"
	cp -R "$repo_root/testdata/independent-consumer/." "$fixture_dir/"
	sed \
		-e "s|__APIGEN_VERSION__|$apigen_requirement|g" \
		-e "s|__CLIENT_VERSION__|$client_requirement|g" \
		"$fixture_dir/go.mod.tmpl" >"$fixture_dir/go.mod"
	rm "$fixture_dir/go.mod.tmpl"
	(
		cd "$fixture_dir"
		if [[ "$name" == "local" ]]; then
			"$go_cmd" mod edit -replace="github.com/calypr/syfon/apigen=$repo_root/apigen"
			"$go_cmd" mod edit -replace="github.com/calypr/syfon/client=$repo_root/client"
		fi
		"$go_cmd" mod tidy
		"$go_cmd" test -mod=readonly ./...
	)
}

run_consumer local v0.0.0 v0.0.0
run_consumer published "$apigen_version" "$client_version"
