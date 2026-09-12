#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCOPE="${COVERAGE_SCOPE:-meaningful}"

WORK_DIR="${ROOT_DIR}"
case "${SCOPE}" in
  full|meaningful)
    WORK_DIR="${ROOT_DIR}"
    ;;
  client)
    WORK_DIR="${ROOT_DIR}/client"
    ;;
  *)
    echo "unknown COVERAGE_SCOPE='${SCOPE}'. valid: full, meaningful, client"
    exit 1
    ;;
esac

OUT_DIR="${WORK_DIR}/coverage"
OUT_FILE="${OUT_DIR}/coverage.out"
HTML_FILE="${OUT_DIR}/coverage.html"
REPORT_FILE="${OUT_FILE}"

mkdir -p "${OUT_DIR}"

case "${SCOPE}" in
  full)
    PKGS="$(cd "${ROOT_DIR}" && go list ./...)"
    ;;
  meaningful)
    PKGS="$(cd "${ROOT_DIR}" && go list ./... | grep -Ev '/tests/|/internal/testsupport/|/cmd/openapi-remove-examples$')"
    ;;
  client)
    PKGS="./..."
    ;;
esac

if [[ -z "${PKGS}" ]]; then
  echo "no packages selected for coverage run"
  exit 1
fi

cd "${WORK_DIR}"
echo "coverage scope:   ${SCOPE}"
echo "coverage workdir: ${WORK_DIR}"
echo "coverage output:  ${OUT_FILE}"
echo "packages:"
printf '  %s\n' ${PKGS}

GO_TEST_FLAGS=(-count=1 -covermode=atomic -coverprofile "${OUT_FILE}")
if [[ "${SCOPE}" == "client" ]]; then
  GO_TEST_FLAGS=(-v "${GO_TEST_FLAGS[@]}")
fi

CGO_ENABLED=1 go test "${GO_TEST_FLAGS[@]}" ${PKGS}

if [[ "${SCOPE}" == "meaningful" ]]; then
  CGO_ENABLED=1 go test -count=1 -covermode=atomic \
    -coverpkg=./internal/... \
    -coverprofile "${OUT_DIR}/cross-package.out" \
    ./cmd ./cmd/server ./internal/persistence/sqlite ./internal/persistence/postgres

  CGO_ENABLED=1 go test -count=1 -covermode=atomic \
    -coverpkg=./internal/... \
    -coverprofile "${OUT_DIR}/cross-domain.out" \
    ./internal/buckets/... \
    ./internal/httpapi/... \
    ./internal/objects/... \
    ./internal/projects/... \
    ./internal/transfers/... \
    ./internal/usage/...

  REPORT_FILE="${OUT_DIR}/combined.out"
  awk -f "${ROOT_DIR}/scripts/merge_coverprofiles.awk" \
    "${OUT_FILE}" \
    "${OUT_DIR}/cross-package.out" \
    "${OUT_DIR}/cross-domain.out" >"${REPORT_FILE}"
fi

go tool cover -func="${REPORT_FILE}" | tee "${OUT_DIR}/coverage.txt"
go tool cover -html="${REPORT_FILE}" -o "${HTML_FILE}"

minimum="${COVERAGE_MIN:-}"
if [[ -n "${minimum}" ]]; then
  actual="$(awk '/^total:/ { gsub(/%/, "", $3); print $3 }' "${OUT_DIR}/coverage.txt")"
  awk -v actual="${actual}" -v minimum="${minimum}" 'BEGIN {
    if (actual + 0 < minimum + 0) {
      printf "coverage %.1f%% is below required %.1f%%\n", actual, minimum > "/dev/stderr"
      exit 1
    }
    printf "coverage %.1f%% meets required %.1f%%\n", actual, minimum
  }'
fi

echo "coverage scope:    ${SCOPE}"
echo "coverage profile: ${REPORT_FILE}"
echo "coverage report:  ${OUT_DIR}/coverage.txt"
echo "coverage html:    ${HTML_FILE}"
