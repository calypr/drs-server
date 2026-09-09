package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLoadConfig_NoDatabaseError(t *testing.T) {
	_, err := LoadConfig("")
	if err == nil {
		t.Error("expected error when no database is specified, got nil")
	}
}

func TestLoadConfig_MinimalValid(t *testing.T) {
	t.Setenv("DRS_DB_SQLITE_FILE", "drs.db")
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_BASIC_AUTH_USER", "drs-user")
	t.Setenv("DRS_BASIC_AUTH_PASSWORD", "drs-pass")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Port)
	}

	if cfg.Database.Sqlite == nil {
		t.Fatal("expected sqlite config")
	}
	if cfg.LFS.MaxBatchObjects != DefaultLFSMaxBatchObjects {
		t.Fatalf("expected default lfs.max_batch_objects=%d, got %d", DefaultLFSMaxBatchObjects, cfg.LFS.MaxBatchObjects)
	}
	if cfg.LFS.MaxBatchBodyBytes != DefaultLFSMaxBatchBodyBytes {
		t.Fatalf("expected default lfs.max_batch_body_bytes=%d, got %d", DefaultLFSMaxBatchBodyBytes, cfg.LFS.MaxBatchBodyBytes)
	}
	if cfg.LFS.RequestLimitPerMinute != DefaultLFSRequestLimitPerMinute {
		t.Fatalf("expected default lfs.request_limit_per_minute=%d, got %d", DefaultLFSRequestLimitPerMinute, cfg.LFS.RequestLimitPerMinute)
	}
	if !cfg.Routes.Ga4gh || !cfg.Routes.Internal || !cfg.Routes.LFS || !cfg.Routes.Metrics || !cfg.Routes.Docs {
		t.Fatalf("expected route modules to default enabled, got %+v", cfg.Routes)
	}
}

func TestLoadConfig_EnvOverrides(t *testing.T) {
	t.Setenv("DRS_PORT", "9090")
	t.Setenv("DRS_DB_SQLITE_FILE", "test_env.db")
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_BASIC_AUTH_USER", "drs-user")
	t.Setenv("DRS_BASIC_AUTH_PASSWORD", "drs-pass")
	t.Setenv("DRS_CREDENTIAL_LOCAL_KEY_FILE", "/tmp/test-env-kek")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Port)
	}

	if cfg.Database.Sqlite.File != "test_env.db" {
		t.Errorf("expected test_env.db, got %s", cfg.Database.Sqlite.File)
	}
	if cfg.CredentialEncryption.LocalKeyFile != "/tmp/test-env-kek" {
		t.Errorf("expected credential local key file override, got %s", cfg.CredentialEncryption.LocalKeyFile)
	}
}

func TestLoadConfig_CredentialEncryptionConfig(t *testing.T) {
	content := `
auth:
  mode: local
  basic:
    username: "drs-user"
    password: "drs-pass"
database:
  sqlite:
    file: "test.db"
credential_encryption:
  local_key_file: ".syfon-credential-kek"
  master_key: "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97"
`
	tmpfile, err := os.CreateTemp("", "config-credential-encryption-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.CredentialEncryption.LocalKeyFile != ".syfon-credential-kek" {
		t.Fatalf("expected configured local key file, got %q", cfg.CredentialEncryption.LocalKeyFile)
	}
	if cfg.CredentialEncryption.MasterKey != "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97" {
		t.Fatalf("expected configured master key, got %q", cfg.CredentialEncryption.MasterKey)
	}
}

func TestCredentialEncryptionConfigMarshalJSONRedactsMasterKey(t *testing.T) {
	cfg := CredentialEncryptionConfig{
		LocalKeyFile: ".syfon-credential-kek",
		MasterKey:    "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97",
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	got := string(b)
	if strings.Contains(got, "ee605db033f6992534def23f9594ffaa58142f8bd9b7ee8ae3de199aed435d97") {
		t.Fatalf("expected master key to be redacted, got %s", got)
	}
	if !strings.Contains(got, `"master_key":"***REDACTED***"`) {
		t.Fatalf("expected redacted master key, got %s", got)
	}
}

func TestLoadConfig_LocalAuthzCSV(t *testing.T) {
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", "")
	content := `
auth:
  mode: local
  local_authz_csv: "/tmp/local-authz.csv"
database:
  sqlite:
    file: "test.db"
`
	tmpfile, err := os.CreateTemp("", "config-local-authz-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Auth.LocalAuthzCSV != "/tmp/local-authz.csv" {
		t.Fatalf("expected local authz csv path, got %q", cfg.Auth.LocalAuthzCSV)
	}
	if got := os.Getenv("DRS_LOCAL_AUTHZ_CSV"); got != "" {
		t.Fatalf("LoadConfig must not export DRS_LOCAL_AUTHZ_CSV, got %q", got)
	}
}

func TestLoadConfig_AuthPrecedenceAndNoExport(t *testing.T) {
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_BASIC_AUTH_USER", "env-user")
	t.Setenv("DRS_BASIC_AUTH_PASSWORD", "env-pass")
	t.Setenv("DRS_LOCAL_AUTHZ_CSV", "env.csv")
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "false")
	t.Setenv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", "false")
	t.Setenv("DRS_AUTH_MOCK_RESOURCES", "/env-resource")
	t.Setenv("DRS_AUTH_MOCK_METHODS", "env-method")
	t.Setenv("SYFON_AUTHZ_PLUGIN_PATH", "/env/authz")
	t.Setenv("SYFON_AUTHN_PLUGIN_PATH", "/env/authn")
	t.Setenv("DRS_FENCE_URL", "https://env-fence.example")
	t.Setenv("DRS_DB_SQLITE_FILE", ":memory:")

	content := `
auth:
  mode: gen3
  basic:
    username: file-user
    password: file-pass
  local_authz_csv: file.csv
  mock:
    enabled: true
    require_auth_header: true
    resources: ["/file-resource"]
    methods: ["file-method"]
  plugin_paths:
    authz: /file/authz
    authn: /file/authn
  fence_url: https://file-fence.example
database:
  sqlite:
    file: ":memory:"
`
	cfg, err := LoadConfig(writeAuthTestFile(t, content))
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.Auth.Mode != AuthModeLocal {
		t.Fatalf("mode = %q, want environment override local", cfg.Auth.Mode)
	}
	if cfg.Auth.Basic.Username != "env-user" || cfg.Auth.Basic.Password != "env-pass" {
		t.Fatalf("basic credentials did not retain environment precedence: %+v", cfg.Auth.Basic)
	}
	if cfg.Auth.LocalAuthzCSV != "env.csv" {
		t.Fatalf("local CSV = %q, want env.csv", cfg.Auth.LocalAuthzCSV)
	}
	if !cfg.Auth.Mock.Enabled || !cfg.Auth.Mock.RequireAuthHeader || strings.Join(cfg.Auth.Mock.Resources, ",") != "/file-resource" || strings.Join(cfg.Auth.Mock.Methods, ",") != "file-method" {
		t.Fatalf("config mock values did not win: %+v", cfg.Auth.Mock)
	}
	if cfg.Auth.PluginPaths.Authz != "/file/authz" || cfg.Auth.PluginPaths.Authn != "/file/authn" || cfg.Auth.FenceURL != "https://file-fence.example" {
		t.Fatalf("config plugin/fence values did not win: %+v fence=%q", cfg.Auth.PluginPaths, cfg.Auth.FenceURL)
	}
	wantEnv := map[string]string{
		"DRS_AUTH_MOCK_ENABLED":             "false",
		"DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER": "false",
		"DRS_AUTH_MOCK_RESOURCES":           "/env-resource",
		"DRS_AUTH_MOCK_METHODS":             "env-method",
		"DRS_LOCAL_AUTHZ_CSV":               "env.csv",
		"SYFON_AUTHZ_PLUGIN_PATH":           "/env/authz",
		"SYFON_AUTHN_PLUGIN_PATH":           "/env/authn",
		"DRS_FENCE_URL":                     "https://env-fence.example",
	}
	for key, want := range wantEnv {
		if got := os.Getenv(key); got != want {
			t.Errorf("LoadConfig changed %s: got %q want %q", key, got, want)
		}
	}
}

func TestLoadConfig_InheritedAuthValuesStayTyped(t *testing.T) {
	t.Setenv("DRS_AUTH_MODE", "gen3")
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "yes")
	t.Setenv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", "on")
	t.Setenv("DRS_AUTH_MOCK_RESOURCES", " /inherited-resource, , /second ")
	t.Setenv("DRS_AUTH_MOCK_METHODS", " read, ,write ")
	t.Setenv("SYFON_AUTHZ_PLUGIN_PATH", "/inherited/authz")
	t.Setenv("SYFON_AUTHN_PLUGIN_PATH", "/inherited/authn")
	t.Setenv("DRS_FENCE_URL", "https://inherited-fence.example")
	t.Setenv("DRS_DB_HOST", "localhost")
	t.Setenv("DRS_DB_DATABASE", "testdb")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !cfg.Auth.Mock.Enabled || !cfg.Auth.Mock.RequireAuthHeader {
		t.Fatalf("inherited mock booleans not resolved: %+v", cfg.Auth.Mock)
	}
	if got := strings.Join(cfg.Auth.Mock.Resources, ","); got != "/inherited-resource,/second" {
		t.Fatalf("resources = %q", got)
	}
	if got := strings.Join(cfg.Auth.Mock.Methods, ","); got != "read,write" {
		t.Fatalf("methods = %q", got)
	}
	if cfg.Auth.PluginPaths.Authz != "/inherited/authz" || cfg.Auth.PluginPaths.Authn != "/inherited/authn" || cfg.Auth.FenceURL != "https://inherited-fence.example" {
		t.Fatalf("inherited paths/fence not resolved: %+v fence=%q", cfg.Auth.PluginPaths, cfg.Auth.FenceURL)
	}
}

func TestLoadConfig_ConfigOnlyMockDoesNotAffectSQLiteEligibility(t *testing.T) {
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "")
	content := `
auth:
  mode: gen3
  mock:
    enabled: true
database:
  sqlite:
    file: ":memory:"
`
	_, err := LoadConfig(writeAuthTestFile(t, content))
	if err == nil || !strings.Contains(err.Error(), `auth.mode "gen3" requires postgres database`) {
		t.Fatalf("expected inherited-mock validation failure, got %v", err)
	}
}

func TestLoadConfig_DoesNotRetainAuthFromPreviousLoad(t *testing.T) {
	for _, key := range []string{"DRS_AUTH_MOCK_ENABLED", "DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER", "DRS_AUTH_MOCK_RESOURCES", "DRS_AUTH_MOCK_METHODS", "SYFON_AUTHZ_PLUGIN_PATH", "SYFON_AUTHN_PLUGIN_PATH", "DRS_FENCE_URL"} {
		t.Setenv(key, "")
	}
	first := writeAuthTestFile(t, `
auth:
  mode: local
  allow_unauthenticated: true
  fence_url: https://first-fence.example
  mock:
    enabled: true
    resources: ["/first"]
database:
  sqlite:
    file: ":memory:"
`)
	second := writeAuthTestFile(t, `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: ":memory:"
`)
	if _, err := LoadConfig(first); err != nil {
		t.Fatalf("first LoadConfig failed: %v", err)
	}
	cfg, err := LoadConfig(second)
	if err != nil {
		t.Fatalf("second LoadConfig failed: %v", err)
	}
	if cfg.Auth.Mock.Enabled || cfg.Auth.FenceURL != "" || cfg.Auth.PluginPaths.Authz != "" || cfg.Auth.PluginPaths.Authn != "" {
		t.Fatalf("second config retained first auth values: %+v", cfg.Auth)
	}
}

func writeAuthTestFile(t *testing.T, content string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "config-auth-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func TestLoadConfig_BucketScopes(t *testing.T) {
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
bucket_scopes:
  - organization: calypr
    project_id: training
    path: s3://calypr/008b435e-c1da-58b8-80f1-3ad2882c43cd/nested/project/root
  - organization: calypr
    project_id: analysis
    bucket: calypr
    path_prefix: project/analysis
  - organization: calypr
    project_id: upload
    bucket: calypr
    organization_sub_path: organizations/calypr
    project_sub_path: projects/upload
`
	tmpfile, err := os.CreateTemp("", "config-bucket-scopes-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.BucketScopes) != 3 {
		t.Fatalf("expected 3 bucket scopes, got %d", len(cfg.BucketScopes))
	}
	if got := cfg.BucketScopes[0]; got.Organization != "calypr" || got.ProjectID != "training" || got.Bucket != "calypr" || got.PathPrefix != "008b435e-c1da-58b8-80f1-3ad2882c43cd/nested/project/root" {
		t.Fatalf("unexpected path-derived bucket scope: %+v", got)
	}
	if got := cfg.BucketScopes[1]; got.Organization != "calypr" || got.ProjectID != "analysis" || got.Bucket != "calypr" || got.PathPrefix != "project/analysis" {
		t.Fatalf("unexpected explicit bucket scope: %+v", got)
	}
	if got := cfg.BucketScopes[2]; got.Organization != "calypr" || got.ProjectID != "upload" || got.Bucket != "calypr" || got.PathPrefix != "organizations/calypr/projects/upload" {
		t.Fatalf("unexpected composed bucket scope: %+v", got)
	}
}

func TestLoadConfig_BucketsResources(t *testing.T) {
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
buckets:
  - bucket: calypr
    provider: s3
    region: us-east-1
    access_key: testing
    secret_key: testing-secret
    resources:
      - organization: calypr
        org_path: organizations/calypr
        projects:
          - project_id: training
            project_path: projects/training
          - project: analysis
            project_path: projects/analysis
      - organization: root_only
        org_path: roots/root_only
`
	tmpfile, err := os.CreateTemp("", "config-buckets-resources-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(tmpfile.Name())
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(cfg.Buckets))
	}
	if len(cfg.S3Credentials) != 1 {
		t.Fatalf("expected legacy s3_credentials alias to be populated, got %d", len(cfg.S3Credentials))
	}
	if len(cfg.BucketScopes) != 3 {
		t.Fatalf("expected 3 derived bucket scopes, got %d", len(cfg.BucketScopes))
	}
	if got := cfg.BucketScopes[0]; got.Organization != "calypr" || got.ProjectID != "training" || got.Bucket != "calypr" || got.PathPrefix != "organizations/calypr/projects/training" {
		t.Fatalf("unexpected nested training scope: %+v", got)
	}
	if got := cfg.BucketScopes[1]; got.Organization != "calypr" || got.ProjectID != "analysis" || got.Bucket != "calypr" || got.PathPrefix != "organizations/calypr/projects/analysis" {
		t.Fatalf("unexpected nested analysis scope: %+v", got)
	}
	if got := cfg.BucketScopes[2]; got.Organization != "root_only" || got.ProjectID != "" || got.Bucket != "calypr" || got.PathPrefix != "roots/root_only" {
		t.Fatalf("unexpected org-only scope: %+v", got)
	}
}

func TestLoadConfig_BucketScopePathBucketMismatch(t *testing.T) {
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
bucket_scopes:
  - organization: calypr
    project_id: training
    bucket: other
    path: s3://calypr/project
`
	tmpfile, err := os.CreateTemp("", "config-bucket-scope-mismatch-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = LoadConfig(tmpfile.Name())
	if err == nil {
		t.Fatal("expected bucket/path mismatch error")
	}
	if !strings.Contains(err.Error(), "does not match path bucket") {
		t.Fatalf("expected bucket mismatch error, got %v", err)
	}
}

func TestLoadConfig_BucketScopeRejectsPathLikeOrganization(t *testing.T) {
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
bucket_scopes:
  - organization: calypr/faliper
    project_id: training
    path: s3://calypr/calypr/faliper
`
	tmpfile, err := os.CreateTemp("", "config-bucket-scope-path-org-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = LoadConfig(tmpfile.Name())
	if err == nil {
		t.Fatal("expected path-like organization error")
	}
	if !strings.Contains(err.Error(), "organization must be a Gen3 program name") {
		t.Fatalf("expected path-like organization error, got %v", err)
	}
}

func TestLoadConfig_PostgresEnv(t *testing.T) {
	t.Setenv("DRS_DB_HOST", "myhost")
	t.Setenv("DRS_DB_DATABASE", "mydb")
	t.Setenv("DRS_AUTH_MODE", "gen3")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Database.Postgres == nil {
		t.Fatal("expected postgres config to be initialized by env vars")
	}

	if cfg.Database.Postgres.Host != "myhost" {
		t.Errorf("expected host myhost, got %s", cfg.Database.Postgres.Host)
	}

	// Sqlite should be nil if postgres env vars are set (per my logic in config.go)
	// Wait, let's verify if my logic actually nils it out or if the validation fails.
}

func TestLoadConfig_MutualExclusivity(t *testing.T) {
	// Creating a temp yaml file with both
	content := `
database:
  sqlite:
    file: "foo.db"
  postgres:
    host: "localhost"
`
	tmpfile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	tmpfile.Close()

	_, err = LoadConfig(tmpfile.Name())
	if err == nil {
		t.Error("expected error when both databases are specified, got nil")
	}
}

func TestLoadConfig_AuthModeRequired(t *testing.T) {
	t.Setenv("DRS_DB_SQLITE_FILE", "drs.db")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected error when auth.mode is not provided")
	}
}

func TestLoadConfig_InvalidAuthMode(t *testing.T) {
	t.Setenv("DRS_DB_SQLITE_FILE", "drs.db")
	t.Setenv("DRS_AUTH_MODE", "weird")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected error for invalid auth mode")
	}
}

func TestLoadConfig_Gen3RequiresPostgres(t *testing.T) {
	t.Setenv("DRS_DB_SQLITE_FILE", "drs.db")
	t.Setenv("DRS_AUTH_MODE", "gen3")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected error when auth.mode=gen3 and postgres is not configured")
	}
}

func TestLoadConfig_InvalidDBPortEnv(t *testing.T) {
	t.Setenv("DRS_DB_HOST", "localhost")
	t.Setenv("DRS_DB_DATABASE", "drs")
	t.Setenv("DRS_DB_PORT", "not-a-number")
	t.Setenv("DRS_AUTH_MODE", "gen3")

	if _, err := LoadConfig(""); err == nil {
		t.Fatal("expected invalid DRS_DB_PORT to return error")
	}
}

func TestLoadConfig_LFSEnvOverrides(t *testing.T) {
	t.Setenv("DRS_DB_SQLITE_FILE", "drs.db")
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_ALLOW_UNAUTHENTICATED_LOCAL", "true")
	t.Setenv("DRS_LFS_MAX_BATCH_OBJECTS", "200")
	t.Setenv("DRS_LFS_MAX_BATCH_BODY_BYTES", "123456")
	t.Setenv("DRS_LFS_REQUEST_LIMIT_PER_MINUTE", "33")
	t.Setenv("DRS_LFS_BANDWIDTH_LIMIT_BYTES_PER_MINUTE", "999")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.LFS.MaxBatchObjects != 200 {
		t.Fatalf("expected 200, got %d", cfg.LFS.MaxBatchObjects)
	}
	if cfg.LFS.MaxBatchBodyBytes != 123456 {
		t.Fatalf("expected 123456, got %d", cfg.LFS.MaxBatchBodyBytes)
	}
	if cfg.LFS.RequestLimitPerMinute != 33 {
		t.Fatalf("expected 33, got %d", cfg.LFS.RequestLimitPerMinute)
	}
	if cfg.LFS.BandwidthLimitBytesPerMinute != 999 {
		t.Fatalf("expected 999, got %d", cfg.LFS.BandwidthLimitBytesPerMinute)
	}
}

func TestLoadConfig_InvalidBucketNames(t *testing.T) {
	cases := []struct {
		bucket      string
		errContains string
	}{
		{"ab", "3-63 characters"},
		{strings.Repeat("a", 64), "3-63 characters"},
		{"MyBucket", "invalid"},
		{"my_bucket", "invalid"},
		{"my.bucket", "invalid"},
		{"-mybucket", "invalid"},
		{"mybucket-", "invalid"},
		{"192.168.1.1", "invalid"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.bucket, func(t *testing.T) {
			content := fmt.Sprintf(`
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
s3_credentials:
  - bucket: %q
    provider: s3
    region: "us-east-1"
    access_key: "test-key"
    secret_key: "test-secret"
`, tc.bucket)

			tmpfile, err := os.CreateTemp("", "config-invalid-bucket-*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpfile.Name())
			if _, err := tmpfile.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatal(err)
			}

			_, err = LoadConfig(tmpfile.Name())
			if err == nil {
				t.Fatalf("expected error for invalid bucket %q, got nil", tc.bucket)
			}
			if !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("bucket %q: expected error containing %q, got: %v", tc.bucket, tc.errContains, err)
			}
		})
	}
}

func TestLoadConfig_UnsupportedBucketProvider(t *testing.T) {
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
s3_credentials:
  - bucket: "local-bucket"
    provider: "bogus"
`

	tmpfile, err := os.CreateTemp("", "config-unsupported-provider-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = LoadConfig(tmpfile.Name())
	if err == nil {
		t.Fatal("expected error for unsupported provider bogus")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Fatalf("expected unsupported provider error, got %v", err)
	}
}

func TestLoadConfig_BucketProviderValidationRegression(t *testing.T) {
	cases := []struct {
		name         string
		provider     string
		bucket       string
		wantProvider string
		wantErr      bool
		errSubstring string
	}{
		{
			name:         "gcs alias accepted",
			provider:     "gs",
			bucket:       "my.gcs.bucket",
			wantProvider: "gcs",
		},
		{
			name:         "azure alias accepted",
			provider:     "azblob",
			bucket:       "my-azure-bucket",
			wantProvider: "azure",
		},
		{
			name:         "gcs alias accepts underscore",
			provider:     "gs",
			bucket:       "my_bucket",
			wantProvider: "gcs",
		},
		{
			name:         "file provider accepted",
			provider:     "file",
			bucket:       "local-bucket",
			wantProvider: "file",
		},
		{
			name:         "gcs invalid bucket rejected",
			provider:     "gcs",
			bucket:       "192.168.1.1",
			wantErr:      true,
			errSubstring: "cannot be an IP address",
		},
		{
			name:         "azure invalid bucket rejected",
			provider:     "azure",
			bucket:       "my.azure.bucket",
			wantErr:      true,
			errSubstring: "invalid",
		},
		{
			name:         "gcs reserved prefix rejected",
			provider:     "gcs",
			bucket:       "goog-bucket",
			wantErr:      true,
			errSubstring: "cannot begin with \"goog\"",
		},
		{
			name:         "azure consecutive hyphens rejected",
			provider:     "azure",
			bucket:       "my--bucket",
			wantErr:      true,
			errSubstring: "consecutive hyphens",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			content := fmt.Sprintf(`
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
s3_credentials:
  - bucket: %q
    provider: %q
`, tc.bucket, tc.provider)

			tmpfile, err := os.CreateTemp("", "config-bucket-regression-*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpfile.Name())
			if _, err := tmpfile.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadConfig(tmpfile.Name())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for provider=%q bucket=%q", tc.provider, tc.bucket)
				}
				if !strings.Contains(err.Error(), tc.errSubstring) {
					t.Fatalf("expected error containing %q, got %v", tc.errSubstring, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for provider=%q bucket=%q: %v", tc.provider, tc.bucket, err)
			}
			if len(cfg.S3Credentials) != 1 {
				t.Fatalf("expected one credential, got %d", len(cfg.S3Credentials))
			}
			if cfg.S3Credentials[0].Provider != tc.wantProvider {
				t.Fatalf("expected normalized provider %q, got %q", tc.wantProvider, cfg.S3Credentials[0].Provider)
			}
		})
	}
}

func TestLoadConfig_ValidBucketNames(t *testing.T) {
	validNames := []string{
		"abc",
		"my-bucket",
		"a1-b2-c3",
		"bucket123",
		"test-bucket-2026",
	}

	for _, bucket := range validNames {
		t.Run(bucket, func(t *testing.T) {
			content := fmt.Sprintf(`
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
s3_credentials:
  - bucket: %q
    provider: s3
    region: "us-east-1"
    access_key: "test-key"
    secret_key: "test-secret"
`, bucket)

			tmpfile, err := os.CreateTemp("", "config-valid-bucket-*.yaml")
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatal(err)
			}

			if _, err := LoadConfig(tmpfile.Name()); err != nil {
				t.Fatalf("expected valid bucket %q to pass validation, got error: %v", bucket, err)
			}
		})
	}
}

func TestLoadConfig_S3CompatibleCustomEndpointAllowsNonAWSDNSBucketNames(t *testing.T) {
	content := `
auth:
  mode: local
  allow_unauthenticated: true
database:
  sqlite:
    file: "test.db"
s3_credentials:
  - bucket: "EllrottLab"
    provider: "s3"
    endpoint: "https://rgw.ohsu.edu"
    region: "us-east-1"
    access_key: "test-key"
    secret_key: "test-secret"
`

	tmpfile, err := os.CreateTemp("", "config-s3-compatible-bucket-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())
	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(tmpfile.Name()); err != nil {
		t.Fatalf("expected custom-endpoint s3 bucket to pass validation, got error: %v", err)
	}
}

func TestLoadConfig_RouteEnvOverrides(t *testing.T) {
	t.Setenv("DRS_DB_SQLITE_FILE", "drs.db")
	t.Setenv("DRS_AUTH_MODE", "local")
	t.Setenv("DRS_ALLOW_UNAUTHENTICATED_LOCAL", "true")
	t.Setenv("DRS_ENABLE_GA4GH", "true")
	t.Setenv("DRS_ENABLE_INTERNAL", "1")
	t.Setenv("DRS_ENABLE_LFS", "true")
	t.Setenv("DRS_ENABLE_METRICS", "true")
	t.Setenv("DRS_ENABLE_DOCS", "true")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if !cfg.Routes.Ga4gh || !cfg.Routes.Internal || !cfg.Routes.LFS || !cfg.Routes.Metrics || !cfg.Routes.Docs {
		t.Fatalf("expected all route flags to be enabled, got %+v", cfg.Routes)
	}
}
