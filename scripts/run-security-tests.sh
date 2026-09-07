#!/bin/bash
# Security fixes test suite
# Tests all security fixes from the security audit

set -e

echo "=== Syfon Security Fixes Test Suite ==="
echo ""

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Test authentication/authorization fixes (CRIT-1, HIGH-1, HIGH-2)
echo "[1/5] Testing Authentication & Authorization Fixes (CRIT-1, HIGH-1, HIGH-2)..."
go test -v ./internal/access/authentication -run TestParseToken_IssuerAllowlistValidation
go test -v ./internal/access/authentication -run TestIsIssuerAllowed
go test -v ./internal/config -run TestLoadConfig_LocalModeWithoutBasicAuthRejected
go test -v ./internal/config -run TestLoadConfig_LocalModeAllowsExplicitUnauthenticatedDevMode
go test -v ./internal/config -run TestLoadConfig_MockAuthAllowsGen3Mode
echo "✓ CRIT-1: JWT issuer validation (SSRF prevention)"
echo "✓ HIGH-1: unauthenticated local mode rejected unless explicitly opted in"
echo "✓ HIGH-2: Mock auth supported only for Gen3 integration testing"
echo ""

# Test configuration and secrets (MED-1, MED-2, MED-3)
echo "[2/5] Testing Configuration & Secrets Fixes (MED-1, MED-2, MED-3)..."
go test -v ./internal/config -run TestLoadConfig_PostgresSSLModeDefault
go test -v ./internal/config -run TestSecretRedaction_BasicAuthConfig
go test -v ./internal/config -run TestSecretRedaction_PostgresConfig
go test -v ./internal/config -run TestSecretRedaction_S3Config
go test -v ./client/config -run TestEnsureExists_DirectoryPermissions
go test -v ./client/config -run TestEnsureExists_ConfigFileCreation
go test -v ./client/config -run TestConfigPath_NestedDirectoryStructure
echo "✓ MED-1: Secrets redacted in JSON marshaling"
echo "✓ MED-2: Postgres SSL defaults to 'require'"
echo "✓ MED-3: Client config directory 0700 permissions"
echo ""

# Test cryptography fixes (HIGH-3, LOW-2)
echo "[3/5] Testing Cryptography Fixes (HIGH-3, LOW-2)..."
go test -v ./internal/credentialcipher -run TestLocalCredentialKeyPath_DefaultPath
go test -v ./internal/credentialcipher -run TestLocalCredentialKeyPath_ExplicitEnvVar
go test -v ./internal/credentialcipher -run TestLocalCredentialKeyPath_SQLiteDir
go test -v ./internal/credentialcipher -run TestLocalKeyManager_KeyIDLength
echo "✓ HIGH-3: KEK default path moved from /tmp to /app"
echo "✓ LOW-2: KEK fingerprint extended to 128-bit"
echo ""

# Test client fixes (INFO-3)
echo "[4/5] Testing Client Library Fixes (INFO-3)..."
go test -v ./client -run TestDefaultConfig_HasTimeout
go test -v ./client -run TestNew_ClientHasTimeout
go test -v ./client -run TestNewClient_WithCustomConfig
go test -v ./client -run TestNewClient_WithNilConfig
echo "✓ INFO-3: Client HTTP timeout set to 10 minutes"
echo ""

# Summary
echo "[5/5] Summary"
echo ""
echo "=== All Security Fixes Verified ==="
echo ""
echo "Critical Fixes (CRIT-1):"
echo "  ✓ JWT signature verification with issuer allowlist"
echo "  ✓ SSRF protection via scheme enforcement (HTTPS only)"
echo ""
echo "High-Severity Fixes (HIGH-1, HIGH-2, HIGH-3):"
echo "  ✓ Authorization bypass prevention in local mode"
echo "  ✓ Mock auth supported only for Gen3 integration testing"
echo "  ✓ KEK default path moved from /tmp to /app (0700)"
echo ""
echo "Medium-Severity Fixes (MED-1, MED-2, MED-3):"
echo "  ✓ Config secrets redacted in JSON output"
echo "  ✓ Postgres SSL defaults to 'require'"
echo "  ✓ Client config directory permissions set to 0700"
echo ""
echo "Low/Info Fixes (LOW-2, INFO-3):"
echo "  ✓ KEK fingerprint extended from 64 to 128 bits"
echo "  ✓ Client HTTP timeout set to 10 minutes"
echo ""
echo "Test suite completed successfully!"
