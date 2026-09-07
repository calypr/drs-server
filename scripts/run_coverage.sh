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

mkdir -p "${OUT_DIR}"

case "${SCOPE}" in
  full)
    PKGS="$(cd "${ROOT_DIR}" && go list ./...)"
    ;;
  meaningful)
    PKGS="$(cd "${ROOT_DIR}" && go list ./... | grep -Ev '^github.com/calypr/syfon$|/apigen/|/tests/endpoints$|/testutils$|/cmd$|/cmd/openapi-remove-examples$')"
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
go tool cover -func="${OUT_FILE}" | tee "${OUT_DIR}/coverage.txt"
go tool cover -html="${OUT_FILE}" -o "${HTML_FILE}"

echo "coverage scope:    ${SCOPE}"
echo "coverage profile: ${OUT_FILE}"
echo "coverage report:  ${OUT_DIR}/coverage.txt"
echo "coverage html:    ${HTML_FILE}"
