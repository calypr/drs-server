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

TEST_DRS_URL="${TEST_DRS_URL:-${SYFON_E2E_SERVER_URL:-${DRS_URL:-http://127.0.0.1:8080}}}"
TEST_BUCKET_NAME="${TEST_BUCKET_NAME:-${SYFON_E2E_BUCKET_NAME:-${BUCKET_NAME:-syfon-e2e-bucket}}}"
TEST_BUCKET_PROVIDER="${TEST_BUCKET_PROVIDER:-${SYFON_E2E_BUCKET_PROVIDER:-${BUCKET_PROVIDER:-${SYFON_BUCKET_PROVIDER:-s3}}}}"
TEST_BUCKET_ACCESS_KEY="${TEST_BUCKET_ACCESS_KEY:-${SYFON_E2E_BUCKET_ACCESS_KEY:-${BUCKET_ACCESS_KEY:-${AWS_ACCESS_KEY_ID:-}}}}"
TEST_BUCKET_SECRET_KEY="${TEST_BUCKET_SECRET_KEY:-${SYFON_E2E_BUCKET_SECRET_KEY:-${BUCKET_SECRET_KEY:-${AWS_SECRET_ACCESS_KEY:-}}}}"
TEST_BUCKET_ENDPOINT="${TEST_BUCKET_ENDPOINT:-${SYFON_E2E_BUCKET_ENDPOINT:-${BUCKET_ENDPOINT:-${AWS_ENDPOINT_URL_S3:-${S3_ENDPOINT:-}}}}}"
TEST_BUCKET_REGION="${TEST_BUCKET_REGION:-${SYFON_E2E_BUCKET_REGION:-${BUCKET_REGION:-${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}}}}"
TEST_ORGANIZATION="${TEST_ORGANIZATION:-syfon}"
TEST_PROJECT_ID="${TEST_PROJECT_ID:-e2e}"

log() { printf '[syfon-e2e-addurl] %s\n' "$*"; }
log_warn() { printf '[syfon-e2e-addurl][warn] %s\n' "$*" >&2; }
fail() { printf '[syfon-e2e-addurl] ERROR: %s\n' "$*" >&2; exit 1; }
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
  rm -f "${SYFON_BIN:-}" "${SRC_FILE:-}" "${DST_FILE:-}" \
    /tmp/syfon-addurl-bucket-create.out /tmp/syfon-addurl-sign.out /tmp/syfon-addurl-upload.out
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

new_uuid() {
  if command -v uuidgen >/dev/null 2>&1; then
    uuidgen | tr '[:upper:]' '[:lower:]'
    return
  fi
  # Fallback shape; primary path should be uuidgen.
  printf '%s-%s-%s-%s-%s\n' \
    "$(openssl rand -hex 4)" \
    "$(openssl rand -hex 2)" \
    "$(openssl rand -hex 2)" \
    "$(openssl rand -hex 2)" \
    "$(openssl rand -hex 6)"
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
  status="$(curl -sS -o /tmp/syfon-addurl-bucket-create.out -w '%{http_code}' \
    -X PUT -H 'Content-Type: application/json' \
    --data "$payload" \
    "${TEST_DRS_URL%/}/data/buckets")"
  if [[ "$status" != "200" && "$status" != "201" ]]; then
    cat /tmp/syfon-addurl-bucket-create.out >&2 || true
    fail "failed to create/ensure bucket config (status=$status)"
  fi
  log "bucket config ensured: $TEST_BUCKET_NAME ($TEST_BUCKET_PROVIDER)"
}

upload_to_signed_url() {
  local signed_url="$1"
  local src="$2"
  if [[ "$signed_url" != *"://"* ]]; then
    local target_path="$signed_url"
    mkdir -p "$(dirname "$target_path")"
    cp "$src" "$target_path"
    return
  fi
  local status
  status="$(curl -sS -o /tmp/syfon-addurl-upload.out -w '%{http_code}' \
    -X PUT --upload-file "$src" "$signed_url")"
  if [[ ! "$status" =~ ^2 ]]; then
    cat /tmp/syfon-addurl-upload.out >&2 || true
    fail "signed URL upload failed (status=$status)"
  fi
}

build_syfon() {
  local out
  out="$(mktemp /tmp/syfon-e2e-addurl-bin.XXXXXX)"
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

  local syfon_bin src dst did src_size expected_sum sum_out key req_payload sign_status signed_url sign_name sign_bucket object_url
  CURRENT_PHASE="build-cli"
  syfon_bin="$(build_syfon)"
  SYFON_BIN="$syfon_bin"
  src="$(mktemp /tmp/syfon-e2e-addurl-src.XXXXXX)"
  dst="$(mktemp /tmp/syfon-e2e-addurl-dst.XXXXXX)"
  SRC_FILE="$src"
  DST_FILE="$dst"
  did="$(new_uuid)"
  key="syfon-e2e/addurl/${did}.txt"

  printf 'syfon add-url e2e payload %s\n' "$did" >"$src"
  src_size="$(wc -c <"$src" | tr -d ' ')"
  expected_sum="$(sha256sum "$src" | awk '{print $1}')"

  CURRENT_PHASE="ping"
  log "ping"
  "$syfon_bin" --server "${TEST_DRS_URL%/}" ping

  CURRENT_PHASE="seed-upload"
  log "pre-upload to bucket via bulk signed URL"
  req_payload="$(jq -nc --arg did "$did" --arg bucket "$TEST_BUCKET_NAME" --arg key "$key" '{requests:[{file_id:$did,bucket:$bucket,file_name:$key}]}')"
  sign_status="$(curl -sS -o /tmp/syfon-addurl-sign.out -w '%{http_code}' \
    -X POST -H 'Content-Type: application/json' \
    --data "$req_payload" \
    "${TEST_DRS_URL%/}/data/upload/bulk")"
  if [[ "$sign_status" != "200" && "$sign_status" != "207" ]]; then
    cat /tmp/syfon-addurl-sign.out >&2 || true
    fail "failed to request bulk upload signed URL (status=$sign_status)"
  fi
  signed_url="$(jq -r '.results[0].url // ""' /tmp/syfon-addurl-sign.out)"
  sign_name="$(jq -r '.results[0].file_name // ""' /tmp/syfon-addurl-sign.out)"
  sign_bucket="$(jq -r '.results[0].bucket // ""' /tmp/syfon-addurl-sign.out)"
  [[ -n "$signed_url" ]] || fail "bulk upload signing returned empty URL"
  upload_to_signed_url "$signed_url" "$src"
  if [[ "$signed_url" != *"://"* ]]; then
    object_url="$signed_url"
  else
    object_url="s3://${sign_bucket}/${sign_name}"
  fi

  CURRENT_PHASE="add-url"
  log "add-url"
  "$syfon_bin" --server "${TEST_DRS_URL%/}" add-url \
    --did "$did" \
    --url "$object_url" \
    --name "syfon-addurl-e2e.txt" \
    --size "$src_size" \
    --sha256 "$expected_sum"

  CURRENT_PHASE="download"
  log "download"
  "$syfon_bin" --server "${TEST_DRS_URL%/}" download --did "$did" --out "$dst"
  cmp "$src" "$dst" || fail "downloaded file differs from source file"

  CURRENT_PHASE="hash-verify"
  log "sha256sum"
  sum_out="$("$syfon_bin" --server "${TEST_DRS_URL%/}" sha256sum --did "$did")"
  printf '%s\n' "$sum_out"
  if ! printf '%s\n' "$sum_out" | grep -q "$expected_sum"; then
    fail "sha256 mismatch: expected=$expected_sum output=$sum_out"
  fi

  log "PASS did=$did sha256=$expected_sum"
  TEST_OUTCOME="PASS"
}

main "$@"
