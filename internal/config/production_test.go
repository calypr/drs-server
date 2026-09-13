package config

import "testing"

func productionTestConfig() *Config {
	cfg := defaultConfig()
	cfg.Profile = ProfileProduction
	cfg.Database = DatabaseConfig{Postgres: &PostgresConfig{
		Host:               "postgres",
		Database:           "drs",
		SSLMode:            "verify-full",
		MaxOpenConnections: 4,
	}}
	cfg.Routes.Docs = false
	cfg.Auth.Mode = AuthModeLocal
	cfg.Auth.Basic.Username = "drs"
	cfg.Auth.Basic.Password = "secret"
	cfg.CredentialEncryption.MasterKey = "stable-key-reference"
	return cfg
}

func TestValidateConfigProductionRequiresStableOperationalSettings(t *testing.T) {
	for _, name := range []string{"DRS_CREDENTIAL_MASTER_KEY", "DRS_CREDENTIAL_LOCAL_KEY_FILE", "DRS_CREDENTIAL_KMS_KEY_ID", "DRS_CREDENTIAL_KEY_MANAGER"} {
		t.Setenv(name, "")
	}
	t.Run("accepts valid profile", func(t *testing.T) {
		if err := validateConfig(productionTestConfig()); err != nil {
			t.Fatalf("validateConfig() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "sqlite", mutate: func(cfg *Config) {
			cfg.Database.Postgres = nil
			cfg.Database.Sqlite = &SqliteConfig{File: ":memory:"}
		}},
		{name: "docs", mutate: func(cfg *Config) { cfg.Routes.Docs = true }},
		{name: "ephemeral encryption", mutate: func(cfg *Config) { cfg.CredentialEncryption.MasterKey = "" }},
		{name: "pool", mutate: func(cfg *Config) { cfg.Database.Postgres.MaxOpenConnections = 0 }},
		{name: "insecure transport", mutate: func(cfg *Config) { cfg.Database.Postgres.SSLMode = "disable" }},
		{name: "identity", mutate: func(cfg *Config) { cfg.Service.OrganizationURL = "" }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := productionTestConfig()
			testCase.mutate(cfg)
			if err := validateConfig(cfg); err == nil {
				t.Fatal("validateConfig() succeeded, want production profile error")
			}
		})
	}
}

func TestValidateConfigProductionAllowsExplicitInsecureTransport(t *testing.T) {
	cfg := productionTestConfig()
	cfg.Database.Postgres.SSLMode = "disable"
	cfg.Database.Postgres.AllowInsecureTransport = true
	if err := validateConfig(cfg); err != nil {
		t.Fatalf("validateConfig() error = %v", err)
	}
}

func TestValidateConfigProductionRejectsInheritedMockAuth(t *testing.T) {
	t.Setenv("DRS_AUTH_MOCK_ENABLED", "true")
	cfg := productionTestConfig()
	cfg.Auth.Mode = AuthModeGen3
	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig() succeeded with inherited mock authentication enabled")
	}
}

func TestValidateConfigRejectsImmediateMultipartReaping(t *testing.T) {
	cfg := productionTestConfig()
	cfg.Multipart.CleanupIntervalSeconds = 60
	cfg.Multipart.InactiveTimeoutSeconds = 0
	if err := validateConfig(cfg); err == nil {
		t.Fatal("validateConfig() succeeded with immediate multipart reaping enabled")
	}
}

func TestApplyEnvironmentOverridesProductionRuntimeSettings(t *testing.T) {
	cfg := defaultConfig()
	cfg.Database.Postgres = &PostgresConfig{}
	values := map[string]string{
		"DRS_PROFILE":                               ProfileProduction,
		"DRS_MAX_BULK_REQUEST_LENGTH":               "75",
		"DRS_MULTIPART_CLEANUP_INTERVAL_SECONDS":    "90",
		"DRS_MULTIPART_INACTIVE_TIMEOUT_SECONDS":    "3600",
		"DRS_MULTIPART_COMPLETED_RETENTION_SECONDS": "7200",
		"DRS_MULTIPART_BATCH_SIZE":                  "125",
		"DRS_DB_ALLOW_INSECURE_TRANSPORT":           "true",
		"DRS_DB_MAX_OPEN_CONNECTIONS":               "30",
		"DRS_DB_MAX_IDLE_CONNECTIONS":               "12",
		"DRS_DB_CONNECTION_MAX_LIFETIME_SECONDS":    "1800",
		"DRS_DB_CONNECTION_MAX_IDLE_TIME_SECONDS":   "300",
	}
	for name, value := range values {
		t.Setenv(name, value)
	}
	if err := applyEnvironmentOverrides(cfg); err != nil {
		t.Fatalf("applyEnvironmentOverrides() error = %v", err)
	}
	if cfg.Profile != ProfileProduction || cfg.DRS.MaxBulkRequestLength != 75 {
		t.Fatalf("profile or DRS settings were not applied: %+v", cfg)
	}
	if cfg.Multipart != (MultipartConfig{CleanupIntervalSeconds: 90, InactiveTimeoutSeconds: 3600, CompletedRetentionSeconds: 7200, BatchSize: 125}) {
		t.Fatalf("multipart settings = %+v", cfg.Multipart)
	}
	if got := cfg.Database.Postgres; !got.AllowInsecureTransport || got.MaxOpenConnections != 30 || got.MaxIdleConnections != 12 || got.ConnectionMaxLifetimeSeconds != 1800 || got.ConnectionMaxIdleTimeSeconds != 300 {
		t.Fatalf("PostgreSQL runtime settings = %+v", got)
	}
}
