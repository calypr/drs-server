#!/usr/bin/env bash
set -euo pipefail

# Check direct production imports for the target domain and adapter packages.
repo_root="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "${repo_root}"

# Keep source inspection usable in restricted workspaces whose default Go
# cache is not writable. Callers may provide their own cache as usual.
if [[ -z "${GOCACHE:-}" ]]; then
	export GOCACHE=/tmp/syfon-import-policy-gocache
fi

violations=()

is_generated_or_http() {
	case "$1" in
		github.com/calypr/syfon/apigen/*|github.com/calypr/syfon/internal/api/*|github.com/calypr/syfon/internal/httpapi/*|github.com/gofiber/fiber*)
			return 0
		;;
		*)
			return 1
		;;
	esac
}

is_sql_dependency() {
	case "$1" in
		github.com/mattn/go-sqlite3|github.com/lib/pq|github.com/jackc/pgx*)
			return 0
		;;
		*)
			return 1
		;;
	esac
}

is_cloud_dependency() {
	case "$1" in
		github.com/aws/aws-sdk-go*|cloud.google.com/go*|github.com/Azure/azure-sdk-for-go*)
			return 0
		;;
		*)
			return 1
		;;
	esac
}

is_standard_library_dependency() {
	local first_segment="${1%%/*}"
	[[ "${first_segment}" != *.* ]]
}

check_edge() {
	local pkg="$1"
	local dep="$2"
	local forbidden=0

	case "$dep" in
		github.com/calypr/syfon/internal/testsupport*)
			violations+=("${pkg} -> ${dep}")
			return 0
		;;
		github.com/calypr/syfon/internal/api|github.com/calypr/syfon/internal/api/*|\
		github.com/calypr/syfon/internal/auth|github.com/calypr/syfon/internal/auth/*|\
		github.com/calypr/syfon/internal/authz|github.com/calypr/syfon/internal/authz/*|\
		github.com/calypr/syfon/internal/common|github.com/calypr/syfon/internal/common/*|\
		github.com/calypr/syfon/internal/core|github.com/calypr/syfon/internal/core/*|\
		github.com/calypr/syfon/internal/crypto|github.com/calypr/syfon/internal/crypto/*|\
		github.com/calypr/syfon/internal/db|github.com/calypr/syfon/internal/db/*|\
		github.com/calypr/syfon/internal/models|github.com/calypr/syfon/internal/models/*|\
		github.com/calypr/syfon/internal/repair|github.com/calypr/syfon/internal/repair/*|\
		github.com/calypr/syfon/internal/requestmeta|github.com/calypr/syfon/internal/requestmeta/*|\
		github.com/calypr/syfon/internal/signer|github.com/calypr/syfon/internal/signer/*|\
		github.com/calypr/syfon/internal/testutils|github.com/calypr/syfon/internal/testutils/*|\
		github.com/calypr/syfon/internal/urlmanager|github.com/calypr/syfon/internal/urlmanager/*)
			violations+=("${pkg} -> ${dep}")
			return 0
		;;
	esac

	case "$pkg" in
		github.com/calypr/syfon/internal/requestid|github.com/calypr/syfon/internal/faults)
			if ! is_standard_library_dependency "$dep"; then forbidden=1; fi
		;;
		github.com/calypr/syfon/internal/httpapi/records)
			case "$dep" in
				github.com/calypr/syfon/internal/httpapi/drs) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/objects)
			if is_generated_or_http "$dep" || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/buckets)
			if is_generated_or_http "$dep" || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/objects|github.com/calypr/syfon/internal/storage) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/storage)
			if is_generated_or_http "$dep" || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/objects|github.com/calypr/syfon/internal/storage/*)
					if [[ "$dep" != github.com/calypr/syfon/internal/storage/address ]]; then forbidden=1; fi
				;;
			esac
		;;
		github.com/calypr/syfon/internal/storage/address)
			if ! is_standard_library_dependency "$dep"; then forbidden=1; fi
		;;
		github.com/calypr/syfon/internal/storage/*)
			# Provider children are the one place where cloud SDK imports are
			# allowed. They still cannot depend on SQL, HTTP/generated code,
			# core, or objects.
			if is_generated_or_http "$dep" || is_sql_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/objects) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/transfers)
			if is_generated_or_http "$dep" || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/usage)
			if is_generated_or_http "$dep" || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/transfers) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/maintenance|github.com/calypr/syfon/internal/maintenance/*)
			if is_generated_or_http "$dep" || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/maintenance/*) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/persistence/*)
			# Dialect adapters own their SQL driver imports. Cloud SDKs remain
			# forbidden here.
			if is_generated_or_http "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/persistence/*) forbidden=1 ;;
			esac
		;;
		*)
			return 0
		;;
	esac

	if ((forbidden)); then
		violations+=("${pkg} -> ${dep}")
	fi
}

expect_allowed() {
	local pkg="$1"
	local dep="$2"
	violations=()
	check_edge "$pkg" "$dep"
	if ((${#violations[@]} != 0)); then
		printf 'self-test expected allowed edge but found forbidden: %s -> %s\n' "$pkg" "$dep" >&2
		return 1
	fi
}

expect_forbidden() {
	local pkg="$1"
	local dep="$2"
	violations=()
	check_edge "$pkg" "$dep"
	if ((${#violations[@]} == 0)); then
		printf 'self-test expected forbidden edge but allowed: %s -> %s\n' "$pkg" "$dep" >&2
		return 1
	fi
}

run_self_tests() {
	expect_allowed github.com/calypr/syfon/internal/storage/s3 github.com/aws/aws-sdk-go-v2/aws
	expect_allowed github.com/calypr/syfon/internal/persistence/sqlite github.com/mattn/go-sqlite3
	expect_allowed github.com/calypr/syfon/internal/storage github.com/calypr/syfon/internal/storage/address
	expect_allowed github.com/calypr/syfon/internal/storage/address net/url
	expect_allowed github.com/calypr/syfon/cmd/server github.com/calypr/syfon/internal/credentialcipher
	expect_allowed github.com/calypr/syfon/internal/persistence/sqlite github.com/calypr/syfon/internal/objects
	expect_forbidden github.com/calypr/syfon/internal/objects github.com/mattn/go-sqlite3
	expect_forbidden github.com/calypr/syfon/internal/storage github.com/calypr/syfon/internal/storage/s3
	expect_forbidden github.com/calypr/syfon/internal/storage/address github.com/google/uuid
	expect_forbidden github.com/calypr/syfon/internal/buckets github.com/calypr/syfon/internal/storage
	expect_forbidden github.com/calypr/syfon/internal/usage github.com/calypr/syfon/internal/transfers
	expect_allowed github.com/calypr/syfon/internal/requestid context
	expect_allowed github.com/calypr/syfon/internal/faults errors
	expect_forbidden github.com/calypr/syfon/internal/requestid github.com/calypr/syfon/internal/httpapi/middleware
	expect_forbidden github.com/calypr/syfon/internal/faults github.com/calypr/syfon/internal/objects
	expect_forbidden github.com/calypr/syfon/internal/httpapi/records github.com/calypr/syfon/internal/httpapi/drs
	expect_forbidden github.com/calypr/syfon/internal/objects github.com/calypr/syfon/internal/testsupport/sqlite
	expect_forbidden github.com/calypr/syfon/internal/arbitrary github.com/calypr/syfon/internal/testsupport/sqlite
	expect_forbidden github.com/calypr/syfon/cmd/server github.com/calypr/syfon/internal/testsupport/sqlite
	for retired in api auth authz common core crypto db models repair requestmeta signer testutils urlmanager; do
		expect_forbidden github.com/calypr/syfon/cmd/server "github.com/calypr/syfon/internal/${retired}"
		expect_forbidden github.com/calypr/syfon/cmd/server "github.com/calypr/syfon/internal/${retired}/child"
	done
	echo "import policy self-tests passed"
}

if [[ "${1:-}" == "--self-test" ]]; then
	run_self_tests
	exit 0
fi

if ! import_listing="$(go list -f '{{.ImportPath}}{{"\t"}}{{join .Imports " "}}' ./...)"; then
	echo "import policy could not inspect Go packages" >&2
	exit 1
fi

while IFS=$'\t' read -r pkg imports; do
	[[ -z "${pkg}" ]] && continue
	for dep in ${imports}; do
		check_edge "${pkg}" "${dep}"
	done
done <<<"${import_listing}"

if ((${#violations[@]} > 0)); then
	printf 'forbidden direct production imports:\n' >&2
	printf '  %s\n' "${violations[@]}" >&2
	exit 1
fi

echo "import policy passed: no forbidden direct production imports"
