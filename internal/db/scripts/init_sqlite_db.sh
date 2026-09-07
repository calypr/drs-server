#!/usr/bin/env bash
set -euo pipefail

DB_PATH="${1:-drs_local.db}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SCHEMA_FILE="${SCRIPT_DIR}/init_sqlite.sql"

if ! command -v sqlite3 >/dev/null 2>&1; then
  echo "sqlite3 command not found. Please install sqlite3."
  exit 1
fi

sqlite3 "${DB_PATH}" < "${SCHEMA_FILE}"
echo "Initialized SQLite schema at ${DB_PATH}"

