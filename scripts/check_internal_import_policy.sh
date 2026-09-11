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

is_shared_error_contract() {
	[[ "$1" == github.com/calypr/syfon/apigen/errorapi ]]
}

# Generated API packages combine wire models with clients and server bindings.
# Each allowed edge therefore names the model selectors that its owner uses.
generated_model_symbols() {
	case "$1 -> $2" in
		"github.com/calypr/syfon/internal/objects -> github.com/calypr/syfon/apigen/drs")
			echo "AccessMethod AccessMethodUpdate Checksum DrsObject DrsObjectCandidate"
		;;
		"github.com/calypr/syfon/internal/persistence/store -> github.com/calypr/syfon/apigen/drs")
			echo "AccessMethod AccessMethodType AccessURL Checksum DrsObject"
		;;
		"github.com/calypr/syfon/internal/projects/storage -> github.com/calypr/syfon/apigen/drs")
			echo "AccessMethod AccessMethodTypeS3 AccessURL Checksum DrsObject"
		;;
		"github.com/calypr/syfon/internal/transfers -> github.com/calypr/syfon/apigen/drs")
			echo "AccessMethod DrsObject"
		;;
		"github.com/calypr/syfon/internal/transfers/lfs -> github.com/calypr/syfon/apigen/drs")
			echo "AccessMethod AccessMethodType AccessURL Checksum DrsObject DrsObjectCandidate"
		;;
		"github.com/calypr/syfon/internal/usage -> github.com/calypr/syfon/apigen/metricsapi"|\
		"github.com/calypr/syfon/internal/persistence/store -> github.com/calypr/syfon/apigen/metricsapi")
			echo "FileUsage FileUsageSummary ProviderTransferDirection ProviderTransferEvent ProviderTransferReconciliationStatus TransferAttributionBreakdown TransferAttributionSummary"
		;;
		"github.com/calypr/syfon/internal/persistence/store -> github.com/calypr/syfon/apigen/lfsapi"|\
		"github.com/calypr/syfon/internal/transfers/lfs -> github.com/calypr/syfon/apigen/lfsapi")
			echo "AccessMethod AccessMethodAccessUrl Checksum DrsObjectCandidate"
		;;
		"github.com/calypr/syfon/internal/projects/storage -> github.com/calypr/syfon/apigen/internalapi")
			echo "InternalDeleteProjectBucketObjectsItem InternalInspectObjectBulkItem InternalInspectObjectRequest InternalInspectObjectResponse InternalInspectProjectBucketItem InternalInspectProjectBucketResponse InternalInspectProjectBucketSummary ProjectCleanupResponse ScopeRepairApplyResult ScopeRepairFinding ScopeRepairObjectReport ScopeRepairOptions ScopeRepairReport"
		;;
		*)
			return 1
		;;
	esac
}

is_canonical_generated_model() {
	generated_model_symbols "$1" "$2" >/dev/null 2>&1
}

check_generated_model_usage() {
	local pkg="$1"
	local dep="$2"
	local package_dir="${3:-${repo_root}/${pkg#github.com/calypr/syfon/}}"
	local allowed_symbols
	local usage
	local kind
	local location
	local selector
	local symbol
	local imported=0

	allowed_symbols=" $(generated_model_symbols "${pkg}" "${dep}") "
	if ! usage="$(GOWORK=off go run ./scripts/internal-import-policy "${package_dir}" "${dep}")"; then
		violations+=("${pkg} -> ${dep} (generated usage inspection failed)")
		return 0
	fi
	while IFS=$'\t' read -r kind location selector; do
		[[ -z "${kind}" ]] && continue
		case "${kind}" in
			IMPORT)
				imported=1
				case "${selector}" in
					.|_) violations+=("${pkg} -> ${dep} (generated ${selector} import at ${location})") ;;
				esac
				;;
			SELECTOR)
				symbol="${selector#*.}"
				if [[ "${allowed_symbols}" != *" ${symbol} "* ]]; then
					violations+=("${pkg} -> ${dep}.${symbol} (${location})")
				fi
				;;
		esac
	done <<<"${usage}"
	if (( ! imported )); then
		violations+=("${pkg} -> ${dep} (generated import not found)")
	fi
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
		github.com/calypr/syfon/common)
			violations+=("${pkg} -> ${dep}")
			return 0
		;;
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
		github.com/calypr/syfon/internal/credentialcipher|github.com/calypr/syfon/internal/credentialcipher/*|\
		github.com/calypr/syfon/internal/maintenance|github.com/calypr/syfon/internal/maintenance/*|\
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
		github.com/calypr/syfon/internal/requestid)
			if ! is_standard_library_dependency "$dep"; then forbidden=1; fi
		;;
		github.com/calypr/syfon/internal/objects|github.com/calypr/syfon/internal/objects/*)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep" && ! is_canonical_generated_model "$pkg" "$dep") || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*) forbidden=1 ;;
			esac
			if [[ "$pkg" == github.com/calypr/syfon/internal/objects ]]; then
				case "$dep" in
					github.com/calypr/syfon/internal/objects/*) forbidden=1 ;;
				esac
			fi
		;;
		github.com/calypr/syfon/internal/buckets)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep") || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/objects|github.com/calypr/syfon/internal/objects/*|github.com/calypr/syfon/internal/storage) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/storage)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep") || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/objects|github.com/calypr/syfon/internal/objects/*|github.com/calypr/syfon/internal/storage/*)
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
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep") || is_sql_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/objects|github.com/calypr/syfon/internal/objects/*) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/transfers|github.com/calypr/syfon/internal/transfers/*)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep" && ! is_canonical_generated_model "$pkg" "$dep") || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*) forbidden=1 ;;
			esac
			if [[ "$pkg" == github.com/calypr/syfon/internal/transfers ]]; then
				case "$dep" in
					github.com/calypr/syfon/internal/transfers/*) forbidden=1 ;;
				esac
			fi
		;;
		github.com/calypr/syfon/internal/usage)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep" && ! is_canonical_generated_model "$pkg" "$dep") || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/transfers) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/projects/storage)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep" && ! is_canonical_generated_model "$pkg" "$dep") || is_sql_dependency "$dep" || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/api*|github.com/calypr/syfon/internal/httpapi*|github.com/calypr/syfon/internal/core*|github.com/calypr/syfon/internal/db*|github.com/calypr/syfon/internal/persistence*|github.com/calypr/syfon/internal/models*|github.com/calypr/syfon/internal/common*|github.com/calypr/syfon/internal/maintenance/*) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/persistence/credentialcipher)
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep") || is_sql_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/persistence/*|github.com/calypr/syfon/internal/httpapi*) forbidden=1 ;;
			esac
		;;
		github.com/calypr/syfon/internal/persistence/*)
			# Dialect adapters own their SQL driver imports. Cloud SDKs remain
			# forbidden here.
			if (is_generated_or_http "$dep" && ! is_shared_error_contract "$dep" && ! is_canonical_generated_model "$pkg" "$dep") || is_cloud_dependency "$dep"; then forbidden=1; fi
			case "$dep" in
				github.com/calypr/syfon/internal/persistence/store) ;;
				github.com/calypr/syfon/internal/persistence/credentialcipher) ;;
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

expect_allowed_generated_usage() {
	local pkg="$1"
	local dep="$2"
	local package_dir="$3"
	violations=()
	check_generated_model_usage "${pkg}" "${dep}" "${package_dir}"
	if ((${#violations[@]} != 0)); then
		printf 'self-test expected generated model fixture to pass but found: %s\n' "${violations[*]}" >&2
		return 1
	fi
}

expect_forbidden_generated_usage() {
	local pkg="$1"
	local dep="$2"
	local package_dir="$3"
	violations=()
	check_generated_model_usage "${pkg}" "${dep}" "${package_dir}"
	if ((${#violations[@]} == 0)); then
		printf 'self-test expected generated transport fixture to fail: %s -> %s\n' "${pkg}" "${dep}" >&2
		return 1
	fi
}

run_self_tests() {
	expect_forbidden github.com/calypr/syfon/client/services github.com/calypr/syfon/common
	expect_allowed github.com/calypr/syfon/internal/storage/s3 github.com/aws/aws-sdk-go-v2/aws
	expect_allowed github.com/calypr/syfon/internal/persistence/sqlite github.com/mattn/go-sqlite3
	expect_allowed github.com/calypr/syfon/internal/persistence/sqlite github.com/calypr/syfon/internal/persistence/store
	expect_allowed github.com/calypr/syfon/internal/storage github.com/calypr/syfon/internal/storage/address
	expect_allowed github.com/calypr/syfon/internal/storage/address net/url
	expect_allowed github.com/calypr/syfon/cmd/server github.com/calypr/syfon/internal/persistence/credentialcipher
	expect_allowed github.com/calypr/syfon/internal/persistence/credentialcipher github.com/aws/aws-sdk-go-v2/service/kms
	expect_allowed github.com/calypr/syfon/internal/persistence/sqlite github.com/calypr/syfon/internal/persistence/credentialcipher
	expect_allowed github.com/calypr/syfon/internal/persistence/postgres github.com/calypr/syfon/internal/persistence/credentialcipher
	expect_forbidden github.com/calypr/syfon/internal/persistence/sqlite github.com/calypr/syfon/internal/persistence/postgres
	expect_forbidden github.com/calypr/syfon/internal/persistence/postgres github.com/aws/aws-sdk-go-v2/service/s3
	expect_forbidden github.com/calypr/syfon/internal/persistence/credentialcipher github.com/mattn/go-sqlite3
	expect_forbidden github.com/calypr/syfon/internal/persistence/credentialcipher github.com/calypr/syfon/internal/persistence/sqlite
	expect_allowed github.com/calypr/syfon/internal/transfers/lfs github.com/calypr/syfon/internal/transfers
	expect_forbidden github.com/calypr/syfon/internal/transfers github.com/calypr/syfon/internal/transfers/lfs
	expect_forbidden github.com/calypr/syfon/internal/transfers/lfs github.com/calypr/syfon/internal/persistence/sqlite
	expect_allowed github.com/calypr/syfon/internal/projects/storage github.com/calypr/syfon/internal/buckets
	expect_forbidden github.com/calypr/syfon/internal/projects/storage github.com/calypr/syfon/internal/persistence/sqlite
	expect_allowed github.com/calypr/syfon/internal/objects/scoperepair github.com/calypr/syfon/internal/objects
	expect_allowed github.com/calypr/syfon/internal/persistence/sqlite github.com/calypr/syfon/internal/objects
	expect_forbidden github.com/calypr/syfon/internal/objects github.com/mattn/go-sqlite3
	expect_allowed github.com/calypr/syfon/internal/objects github.com/calypr/syfon/internal/access
	expect_forbidden github.com/calypr/syfon/internal/objects github.com/calypr/syfon/internal/objects/scoperepair
	expect_forbidden github.com/calypr/syfon/internal/storage github.com/calypr/syfon/internal/storage/s3
	expect_forbidden github.com/calypr/syfon/internal/storage/address github.com/google/uuid
	expect_forbidden github.com/calypr/syfon/internal/buckets github.com/calypr/syfon/internal/storage
	expect_forbidden github.com/calypr/syfon/internal/usage github.com/calypr/syfon/internal/transfers
	expect_allowed github.com/calypr/syfon/internal/requestid context
	expect_allowed github.com/calypr/syfon/internal/objects github.com/calypr/syfon/apigen/errorapi
	expect_allowed github.com/calypr/syfon/internal/objects github.com/calypr/syfon/apigen/drs
	expect_forbidden github.com/calypr/syfon/internal/objects github.com/calypr/syfon/apigen/metricsapi
	expect_allowed github.com/calypr/syfon/internal/usage github.com/calypr/syfon/apigen/metricsapi
	expect_forbidden github.com/calypr/syfon/internal/usage github.com/calypr/syfon/apigen/drs
	expect_allowed github.com/calypr/syfon/internal/projects/storage github.com/calypr/syfon/apigen/internalapi
	expect_allowed github.com/calypr/syfon/internal/projects/storage github.com/calypr/syfon/apigen/drs
	expect_allowed github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/drs
	expect_allowed github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/lfsapi
	expect_allowed github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/metricsapi
	expect_allowed github.com/calypr/syfon/internal/transfers github.com/calypr/syfon/apigen/drs
	expect_allowed github.com/calypr/syfon/internal/transfers/lfs github.com/calypr/syfon/apigen/drs
	expect_allowed github.com/calypr/syfon/internal/transfers/lfs github.com/calypr/syfon/apigen/lfsapi
	expect_allowed_generated_usage github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/drs "${repo_root}/scripts/internal-import-policy/testdata/allowed-alias"
	expect_forbidden_generated_usage github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/drs "${repo_root}/scripts/internal-import-policy/testdata/transport-alias"
	expect_forbidden_generated_usage github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/drs "${repo_root}/scripts/internal-import-policy/testdata/dot-import"
	expect_forbidden_generated_usage github.com/calypr/syfon/internal/persistence/store github.com/calypr/syfon/apigen/drs "${repo_root}/scripts/internal-import-policy/testdata/blank-import"
	expect_forbidden github.com/calypr/syfon/internal/requestid github.com/calypr/syfon/internal/httpapi
	expect_forbidden github.com/calypr/syfon/internal/objects github.com/calypr/syfon/internal/testsupport/sqlite
	expect_forbidden github.com/calypr/syfon/internal/arbitrary github.com/calypr/syfon/internal/testsupport/sqlite
	expect_forbidden github.com/calypr/syfon/cmd/server github.com/calypr/syfon/internal/testsupport/sqlite
	for retired in api auth authz common core credentialcipher crypto db maintenance models repair requestmeta signer testutils urlmanager; do
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
		if is_canonical_generated_model "${pkg}" "${dep}"; then
			check_generated_model_usage "${pkg}" "${dep}"
		fi
	done
done <<<"${import_listing}"

if ((${#violations[@]} > 0)); then
	printf 'forbidden direct production imports:\n' >&2
	printf '  %s\n' "${violations[@]}" >&2
	exit 1
fi

echo "import policy passed: no forbidden direct production imports"
