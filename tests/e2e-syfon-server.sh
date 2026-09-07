#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DRS_SERVER_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"

ENV_FILE="${ENV_FILE:-$DRS_SERVER_ROOT/.env}"
if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a
  source "$ENV_FILE"
  set +a
fi

TEST_DRS_URL="${TEST_DRS_URL:-${SYFON_E2E_SERVER_URL:-${DRS_URL:-http://127.0.01}}}"
TEST_BUCKET_NAME="${TEST_BUCKET_NAME:-${SYFON_E2E_BUCKET_NAME:-${BUCKET_NAME:-syfon-e2e-bucket}}}"
TEST_BUCKET_PROVIDER="${TEST_BUCKET_PROVIDER:-${SYFON_E2E_BUCKET_PROVIDER:-${BUCKET_PROVIDER:-${SYFON_BUCKET_PROVIDER:-s3}}}}"
TEST_BUCKET_ACCESS_KEY="${TEST_BUCKET_ACCESS_KEY:-${SYFON_E2E_BUCKET_ACCESS_KEY:-${BUCKET_ACCESS_KEY:-${AWS_ACCESS_KEY_ID:-}}}}"
TEST_BUCKET_SECRET_KEY="${TEST_BUCKET_SECRET_KEY:-${SYFON_E2E_BUCKET_SECRET_KEY:-${BUCKET_SECRET_KEY:-${AWS_SECRET_ACCESS_KEY:-}}}}"
TEST_BUCKET_ENDPOINT="${TEST_BUCKET_ENDPOINT:-${SYFON_E2E_BUCKET_ENDPOINT:-${BUCKET_ENDPOINT:-${AWS_ENDPOINT_URL_S3:-${S3_ENDPOINT:-}}}}}"
TEST_BUCKET_REGION="${TEST_BUCKET_REGION:-${SYFON_E2E_BUCKET_REGION:-${BUCKET_REGION:-${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}}}}"
TEST_ORGANIZATION="${TEST_ORGANIZATION:-syfon}"
TEST_PROJECT_ID="${TEST_PROJECT_ID:-e2e}"

log() { printf '[syfon-e2e] %s\n' "$*"; }
log_warn() { printf '[syfon-e2e][warn] %s\n' "$*" >&2; }
fail() { printf '[syfon-e2e] ERROR: %s\n' "$*" >&2; exit 1; }
phase() { log "PHASE: $1"; }

CURRENT_PHASE="init"
TEST_OUTCOME="FAIL"
FAIL_LINE=""
FAIL_CMD=""
SYFON_BIN=""
SRC_FILE=""
DST_FILE=""

on_error() {
  local line="${BASH_LINENO[0]:-}"
  local cmd="${BASH_COMMAND:-unknown}"
  FAIL_LINE="${FAIL_LINE:-$line}"
  FAIL_CMD="${FAIL_CMD:-$cmd}"
}

cleanup() {
  local exit_code=$?
  rm -f "${SYFON_BIN:-}" /tmp/syfon-bucket-create.out "${SRC_FILE:-}" "${DST_FILE:-}"
  if [[ "$exit_code" -eq 0 && "$TEST_OUTCOME" == "PASS" ]]; then
    log "RESULT: PASS"
  else
    log_warn "RESULT: FAIL (phase=${CURRENT_PHASE}, line=${FAIL_LINE:-unknown}, exit_code=$exit_code)"
    if [[ -n "${FAIL_CMD:-}" ]]; then
      log_warn "Failed command: ${FAIL_CMD}"
    fi
  fi
  exit "$exit_code"
}

trap on_error ERR
trap cleanup EXIT

request() {
  local method="$1"
  local url="$2"
  local body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -sS -X "$method" -H 'Content-Type: application/json' --data "$body" "$url"
  else
    curl -sS -X "$method" "$url"
  fi
}

health_check() {
  local status
  status="$(curl -sS -o /dev/null -w '%{http_code}' "${TEST_DRS_URL%/}/healthz")"
  [[ "$status" == "200" ]] || fail "health check failed: ${TEST_DRS_URL%/}/healthz returned $status"
}

ensure_bucket_config() {
  local payload
  if [[ "$TEST_BUCKET_PROVIDER" == "file" ]]; then
    [[ -n "$TEST_BUCKET_ENDPOINT" ]] || fail "TEST_BUCKET_ENDPOINT is required when TEST_BUCKET_PROVIDER uses a custom endpoint"
    mkdir -p "$TEST_BUCKET_ENDPOINT"
    payload="$(jq -n \
      --arg bucket "$TEST_BUCKET_NAME" \
      --arg provider "$TEST_BUCKET_PROVIDER" \
      --arg region "$TEST_BUCKET_REGION" \
      --arg endpoint "$TEST_BUCKET_ENDPOINT" \
      --arg organization "$TEST_ORGANIZATION" \
      --arg project_id "$TEST_PROJECT_ID" \
      '{bucket:$bucket, provider:$provider, region:$region}
      + (if $endpoint == "" then {} else {endpoint:$endpoint} end)
      + (if $organization == "" then {} else {organization:$organization} end)
      + (if $project_id == "" then {} else {project_id:$project_id} end)')"
  else
    [[ -n "$TEST_BUCKET_ACCESS_KEY" ]] || fail "TEST_BUCKET_ACCESS_KEY is required when TEST_BUCKET_PROVIDER=$TEST_BUCKET_PROVIDER"
    [[ -n "$TEST_BUCKET_SECRET_KEY" ]] || fail "TEST_BUCKET_SECRET_KEY is required when TEST_BUCKET_PROVIDER=$TEST_BUCKET_PROVIDER"
    payload="$(jq -n \
      --arg bucket "$TEST_BUCKET_NAME" \
      --arg provider "$TEST_BUCKET_PROVIDER" \
      --arg region "$TEST_BUCKET_REGION" \
      --arg access_key "$TEST_BUCKET_ACCESS_KEY" \
      --arg secret_key "$TEST_BUCKET_SECRET_KEY" \
      --arg endpoint "$TEST_BUCKET_ENDPOINT" \
      --arg organization "$TEST_ORGANIZATION" \
      --arg project_id "$TEST_PROJECT_ID" \
      '{bucket:$bucket, provider:$provider, region:$region, access_key:$access_key, secret_key:$secret_key}
      + (if $endpoint == "" then {} else {endpoint:$endpoint} end)
      + (if $organization == "" then {} else {organization:$organization} end)
      + (if $project_id == "" then {} else {project_id:$project_id} end)')"
  fi
  local status
  status="$(curl -sS -o /tmp/syfon-bucket-create.out -w '%{http_code}' \
    -X PUT -H 'Content-Type: application/json' \
    --data "$payload" \
    "${TEST_DRS_URL%/}/data/buckets")"
  if [[ "$status" != "200" && "$status" != "201" ]]; then
    cat /tmp/syfon-bucket-create.out >&2 || true
    fail "failed to create/ensure bucket config (status=$status)"
  fi
  log "bucket config ensured: $TEST_BUCKET_NAME ($TEST_BUCKET_PROVIDER)"
}

build_syfon() {
  local out
  out="$(mktemp /tmp/syfon-e2e-bin.XXXXXX)"
  go build -o "$out" "$DRS_SERVER_ROOT"
  chmod +x "$out"
  printf '%s' "$out"
}

main() {
  CURRENT_PHASE="validation"
  log "using server: ${TEST_DRS_URL%/}"
  health_check

  CURRENT_PHASE="auth-setup"
  ensure_bucket_config

  CURRENT_PHASE="build-cli"
  SYFON_BIN="$(build_syfon)"

  local src dst did object_id sum expected upload_out
  src="$(mktemp /tmp/syfon-e2e-src.XXXXXX)"
  dst="$(mktemp /tmp/syfon-e2e-dst.XXXXXX)"
  SRC_FILE="$src"
  DST_FILE="$dst"
  did="syfon-e2e-$(date +%s)"
  printf 'syfon external e2e %s\n' "$did" >"$src"

  CURRENT_PHASE="ping"
  log "ping"
  "$SYFON_BIN" --server "${TEST_DRS_URL%/}" ping

  CURRENT_PHASE="upload"
  log "upload"
  upload_out="$("$SYFON_BIN" --server "${TEST_DRS_URL%/}" upload --file "$src")"
  printf '%s\n' "$upload_out"
  object_id="$(printf '%s\n' "$upload_out" | awk '/^uploaded /{print $NF}' | tail -n1)"
  [[ -n "$object_id" ]] || fail "unable to parse uploaded object id from syfon upload output"

  CURRENT_PHASE="download"
  log "download"
  "$SYFON_BIN" --server "${TEST_DRS_URL%/}" download --did "$object_id" --out "$dst"
  cmp "$src" "$dst" || fail "downloaded file differs from uploaded file"

  CURRENT_PHASE="hash-verify"
  log "sha256sum"
  sum="$("$SYFON_BIN" --server "${TEST_DRS_URL%/}" sha256sum --did "$object_id" | tail -n1 | tr -d '\r\n')"
  expected="$(sha256sum "$src" | awk '{print $1}')"
  [[ "$sum" == "$expected" ]] || fail "sha256 mismatch: expected=$expected got=$sum"

  log "PASS requested_did=$did object_id=$object_id sha256=$sum"
  TEST_OUTCOME="PASS"
}

main "$@"
