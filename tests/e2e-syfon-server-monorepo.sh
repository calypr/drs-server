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
MONO_TOP_LEVELS="${MONO_TOP_LEVELS:-apps,services,libs}"
MONO_SUBDIRS="${MONO_SUBDIRS:-2}"
MONO_FILES_PER_SUBDIR="${MONO_FILES_PER_SUBDIR:-5}"
MONO_VERIFY_ALL="${MONO_VERIFY_ALL:-false}"
MONO_UPLOAD_BATCH_SIZE="${MONO_UPLOAD_BATCH_SIZE:-10}"
TEST_BUCKET_NAME="${TEST_BUCKET_NAME:-${SYFON_E2E_BUCKET_NAME:-${BUCKET_NAME:-syfon-e2e-bucket}}}"
TEST_BUCKET_PROVIDER="${TEST_BUCKET_PROVIDER:-${SYFON_E2E_BUCKET_PROVIDER:-${BUCKET_PROVIDER:-${SYFON_BUCKET_PROVIDER:-s3}}}}"
TEST_BUCKET_ACCESS_KEY="${TEST_BUCKET_ACCESS_KEY:-${SYFON_E2E_BUCKET_ACCESS_KEY:-${BUCKET_ACCESS_KEY:-${AWS_ACCESS_KEY_ID:-}}}}"
TEST_BUCKET_SECRET_KEY="${TEST_BUCKET_SECRET_KEY:-${SYFON_E2E_BUCKET_SECRET_KEY:-${BUCKET_SECRET_KEY:-${AWS_SECRET_ACCESS_KEY:-}}}}"
TEST_BUCKET_ENDPOINT="${TEST_BUCKET_ENDPOINT:-${SYFON_E2E_BUCKET_ENDPOINT:-${BUCKET_ENDPOINT:-${AWS_ENDPOINT_URL_S3:-${S3_ENDPOINT:-}}}}}"
TEST_BUCKET_REGION="${TEST_BUCKET_REGION:-${SYFON_E2E_BUCKET_REGION:-${BUCKET_REGION:-${AWS_REGION:-${AWS_DEFAULT_REGION:-us-east-1}}}}}"
TEST_ORGANIZATION="${TEST_ORGANIZATION:-syfon}"
TEST_PROJECT_ID="${TEST_PROJECT_ID:-e2e}"

log() { printf '[syfon-e2e-monorepo] %s\n' "$*"; }
log_warn() { printf '[syfon-e2e-monorepo][warn] %s\n' "$*" >&2; }
fail() { printf '[syfon-e2e-monorepo] ERROR: %s\n' "$*" >&2; exit 1; }
phase() { log "PHASE: $1"; }

CURRENT_PHASE="init"
TEST_OUTCOME="FAIL"
FAIL_LINE=""
FAIL_CMD=""
SYFON_BIN=""
MONO_ROOT=""
VERIFY_ROOT=""
REQ_ITEMS_FILE=""
RESP_FILE=""
INDEX_ITEMS_FILE=""

on_error() {
  local line="${BASH_LINENO[0]:-}"
  local cmd="${BASH_COMMAND:-unknown}"
  FAIL_LINE="${FAIL_LINE:-$line}"
  FAIL_CMD="${FAIL_CMD:-$cmd}"
}

cleanup() {
  local exit_code=$?
  rm -f "${SYFON_BIN:-}" /tmp/syfon-monorepo-bucket-create.out /tmp/syfon-monorepo-upload.out \
    "${REQ_ITEMS_FILE:-}" "${RESP_FILE:-}" "${INDEX_ITEMS_FILE:-}" /tmp/syfon-monorepo-index.out
  rm -rf "${MONO_ROOT:-}" "${VERIFY_ROOT:-}"
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
  status="$(curl -sS -o /tmp/syfon-monorepo-bucket-create.out -w '%{http_code}' \
    -X PUT -H 'Content-Type: application/json' \
    --data "$payload" \
    "${TEST_DRS_URL%/}/data/buckets")"
  if [[ "$status" != "200" && "$status" != "201" ]]; then
    cat /tmp/syfon-monorepo-bucket-create.out >&2 || true
    fail "failed to create/ensure bucket config (status=$status)"
  fi
  log "bucket config ensured: $TEST_BUCKET_NAME ($TEST_BUCKET_PROVIDER)"
}

build_syfon() {
  local out
  out="$(mktemp /tmp/syfon-e2e-monorepo-bin.XXXXXX)"
  go build -o "$out" "$DRS_SERVER_ROOT"
  chmod +x "$out"
  printf '%s' "$out"
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
  status="$(curl -sS -o /tmp/syfon-monorepo-upload.out -w '%{http_code}' \
    -X PUT --upload-file "$src" "$signed_url")"
  if [[ ! "$status" =~ ^2 ]]; then
    cat /tmp/syfon-monorepo-upload.out >&2 || true
    fail "signed URL upload failed (status=$status) src=$src"
  fi
}

should_verify() {
  local idx="$1"
  local total="$2"
  if [[ "$MONO_VERIFY_ALL" == "true" ]]; then
    return 0
  fi
  [[ "$idx" == "1" || "$idx" == "$total" ]]
}

main() {
  CURRENT_PHASE="validation"
  log "using server: ${TEST_DRS_URL%/}"
  health_check
  CURRENT_PHASE="auth-setup"
  ensure_bucket_config

  local syfon_bin mono_root verify_root
  syfon_bin="$(build_syfon)"
  mono_root="$(mktemp -d /tmp/syfon-e2e-monorepo-src.XXXXXX)"
  verify_root="$(mktemp -d /tmp/syfon-e2e-monorepo-dst.XXXXXX)"
  SYFON_BIN="$syfon_bin"
  MONO_ROOT="$mono_root"
  VERIFY_ROOT="$verify_root"

  local IFS=',' top_levels_arr=()
  read -r -a top_levels_arr <<<"$MONO_TOP_LEVELS"
  [[ "${#top_levels_arr[@]}" -gt 0 ]] || fail "MONO_TOP_LEVELS is empty"

  local -a rel_paths=()
  local -a src_paths=()
  local -a dids=()
  local -a expected_hashes=()

  local count=0
  local top sub f rel src did expected
  for top in "${top_levels_arr[@]}"; do
    top="$(printf '%s' "$top" | xargs)"
    [[ -n "$top" ]] || continue
    for ((sub=1; sub<=MONO_SUBDIRS; sub++)); do
      for ((f=1; f<=MONO_FILES_PER_SUBDIR; f++)); do
        count=$((count + 1))
        rel="$top/sub-${sub}/file-$(printf '%04d' "$f").txt"
        src="$mono_root/$rel"
        mkdir -p "$(dirname "$src")"
        printf 'monorepo file %s #%d at %s\n' "$rel" "$count" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$src"
        expected="$(sha256sum "$src" | awk '{print $1}')"
        did="$(new_uuid)"
        rel_paths+=("$rel")
        src_paths+=("$src")
        dids+=("$did")
        expected_hashes+=("$expected")
      done
    done
  done

  local total="${#src_paths[@]}"
  [[ "$total" -gt 0 ]] || fail "no files generated for upload"
  log "generated monorepo fixture files: $total"

  local batch_size="$MONO_UPLOAD_BATCH_SIZE"
  if ! [[ "$batch_size" =~ ^[0-9]+$ ]] || [[ "$batch_size" -lt 1 ]]; then
    fail "MONO_UPLOAD_BATCH_SIZE must be a positive integer"
  fi

  local start=0 end i batch_num=0
  local req_items_file req_payload resp_file status did rel src expected size
  local sign_bucket sign_name sign_url sign_status sign_err object_url
  local index_items_file index_payload index_status
  req_items_file="$(mktemp /tmp/syfon-monorepo-req-items.XXXXXX)"
  resp_file="$(mktemp /tmp/syfon-monorepo-sign-resp.XXXXXX)"
  index_items_file="$(mktemp /tmp/syfon-monorepo-index-items.XXXXXX)"
  REQ_ITEMS_FILE="$req_items_file"
  RESP_FILE="$resp_file"
  INDEX_ITEMS_FILE="$index_items_file"

  CURRENT_PHASE="bulk-upload-register"
  while [[ "$start" -lt "$total" ]]; do
    end=$((start + batch_size))
    if [[ "$end" -gt "$total" ]]; then
      end="$total"
    fi
    batch_num=$((batch_num + 1))
    log "upload batch #$batch_num items=$((end - start)) range=$((start + 1))-$end"

    : >"$req_items_file"
    for ((i=start; i<end; i++)); do
      did="${dids[$i]}"
      rel="${rel_paths[$i]}"
      jq -nc \
        --arg file_id "$did" \
        --arg bucket "$TEST_BUCKET_NAME" \
        --arg file_name "$rel" \
        '{file_id:$file_id,bucket:$bucket,file_name:$file_name}' >>"$req_items_file"
    done
    req_payload="$(jq -sc '{requests:.}' "$req_items_file")"

    status="$(curl -sS -o "$resp_file" -w '%{http_code}' \
      -X POST -H 'Content-Type: application/json' \
      --data "$req_payload" \
      "${TEST_DRS_URL%/}/data/upload/bulk")"
    if [[ "$status" != "200" && "$status" != "207" ]]; then
      cat "$resp_file" >&2 || true
      fail "bulk upload signing failed (status=$status)"
    fi

    if [[ "$(jq '.results | length' "$resp_file")" -ne "$((end - start))" ]]; then
      cat "$resp_file" >&2 || true
      fail "bulk upload signing returned unexpected result count"
    fi

    : >"$index_items_file"
    for ((i=start; i<end; i++)); do
      did="${dids[$i]}"
      rel="${rel_paths[$i]}"
      src="${src_paths[$i]}"
      expected="${expected_hashes[$i]}"
      size="$(wc -c <"$src" | tr -d ' ')"

      sign_status="$(jq -r --arg did "$did" '.results[] | select(.file_id==$did) | .status' "$resp_file" | head -n1)"
      sign_bucket="$(jq -r --arg did "$did" '.results[] | select(.file_id==$did) | (.bucket // "")' "$resp_file" | head -n1)"
      sign_name="$(jq -r --arg did "$did" '.results[] | select(.file_id==$did) | (.file_name // "")' "$resp_file" | head -n1)"
      sign_url="$(jq -r --arg did "$did" '.results[] | select(.file_id==$did) | (.url // "")' "$resp_file" | head -n1)"
      sign_err="$(jq -r --arg did "$did" '.results[] | select(.file_id==$did) | (.error // "")' "$resp_file" | head -n1)"

      [[ "$sign_status" == "200" ]] || fail "bulk signing item failed did=$did status=$sign_status err=$sign_err"
      [[ -n "$sign_url" ]] || fail "bulk signing item missing URL did=$did"

      upload_to_signed_url "$sign_url" "$src"

      if [[ "$sign_url" != *"://"* ]]; then
        object_url="$sign_url"
      else
        object_url="s3://${sign_bucket}/${sign_name}"
      fi
      jq -nc \
        --arg did "$did" \
        --arg file_name "$rel" \
        --arg object_url "$object_url" \
        --arg sha "$expected" \
        --arg organization "$TEST_ORGANIZATION" \
        --arg project "$TEST_PROJECT_ID" \
        --argjson size "$size" \
        '{did:$did,file_name:$file_name,size:$size,urls:[$object_url],hashes:{sha256:$sha},organization:$organization,project:$project}' >>"$index_items_file"
    done

    index_payload="$(jq -sc '{records:.}' "$index_items_file")"
    index_status="$(curl -sS -o /tmp/syfon-monorepo-index.out -w '%{http_code}' \
      -X POST -H 'Content-Type: application/json' \
      --data "$index_payload" \
      "${TEST_DRS_URL%/}/index/bulk")"
    if [[ "$index_status" != "201" ]]; then
      cat /tmp/syfon-monorepo-index.out >&2 || true
      fail "bulk index submit failed (status=$index_status)"
    fi
    start="$end"
  done

  local dst sum_out verify_idx
  CURRENT_PHASE="download-verify"
  for ((i=0; i<total; i++)); do
    verify_idx=$((i + 1))
    if ! should_verify "$verify_idx" "$total"; then
      continue
    fi
    dst="$verify_root/${rel_paths[$i]}"
    mkdir -p "$(dirname "$dst")"
    "$syfon_bin" --server "${TEST_DRS_URL%/}" download --did "${dids[$i]}" --out "$dst" >/dev/null
    cmp "${src_paths[$i]}" "$dst" || fail "download verification failed for did=${dids[$i]} rel=${rel_paths[$i]}"

    sum_out="$("$syfon_bin" --server "${TEST_DRS_URL%/}" sha256sum --did "${dids[$i]}")"
    if ! printf '%s\n' "$sum_out" | grep -q "${expected_hashes[$i]}"; then
      fail "sha256 verification failed for did=${dids[$i]} rel=${rel_paths[$i]}"
    fi
  done

  log "PASS objects=$count verified_mode=$MONO_VERIFY_ALL top_levels=${MONO_TOP_LEVELS}"
  TEST_OUTCOME="PASS"
}

main "$@"
