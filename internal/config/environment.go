package config

import (
	"fmt"
	"os"
	"strconv"
)

func applyEnvironmentOverrides(cfg *Config) error {
	// 3. Override with Environment Variables (if set)
	if v := os.Getenv("DRS_PROFILE"); v != "" {
		cfg.Profile = v
	}
	if portStr := os.Getenv("DRS_PORT"); portStr != "" {
		p, err := strconv.Atoi(portStr)
		if err != nil {
			return fmt.Errorf("invalid port: %s", portStr)
		}
		cfg.Port = p
	}
	if mode := os.Getenv("DRS_AUTH_MODE"); mode != "" {
		cfg.Auth.Mode = mode
	}
	if user := os.Getenv("DRS_BASIC_AUTH_USER"); user != "" {
		cfg.Auth.Basic.Username = user
	}
	if pass := os.Getenv("DRS_BASIC_AUTH_PASSWORD"); pass != "" {
		cfg.Auth.Basic.Password = pass
	}
	if v := os.Getenv("DRS_LOCAL_AUTHZ_CSV"); v != "" {
		cfg.Auth.LocalAuthzCSV = v
	}
	if v := os.Getenv("DRS_ALLOW_UNAUTHENTICATED_LOCAL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_ALLOW_UNAUTHENTICATED_LOCAL: %s", v)
		}
		cfg.Auth.AllowUnauthenticated = b
	}
	if v := os.Getenv("DRS_CREDENTIAL_LOCAL_KEY_FILE"); v != "" {
		cfg.CredentialEncryption.LocalKeyFile = v
	}
	if v := os.Getenv("DRS_LFS_MAX_BATCH_OBJECTS"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_LFS_MAX_BATCH_OBJECTS: %s", v)
		}
		cfg.LFS.MaxBatchObjects = i
	}
	if v := os.Getenv("DRS_LFS_MAX_BATCH_BODY_BYTES"); v != "" {
		i, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid DRS_LFS_MAX_BATCH_BODY_BYTES: %s", v)
		}
		cfg.LFS.MaxBatchBodyBytes = i
	}
	if v := os.Getenv("DRS_LFS_REQUEST_LIMIT_PER_MINUTE"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_LFS_REQUEST_LIMIT_PER_MINUTE: %s", v)
		}
		cfg.LFS.RequestLimitPerMinute = i
	}
	if v := os.Getenv("DRS_LFS_BANDWIDTH_LIMIT_BYTES_PER_MINUTE"); v != "" {
		i, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid DRS_LFS_BANDWIDTH_LIMIT_BYTES_PER_MINUTE: %s", v)
		}
		cfg.LFS.BandwidthLimitBytesPerMinute = i
	}
	if v := os.Getenv("DRS_SIGNING_DEFAULT_EXPIRY_SECONDS"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_SIGNING_DEFAULT_EXPIRY_SECONDS: %s", v)
		}
		cfg.Signing.DefaultExpirySeconds = i
	}
	if v := os.Getenv("DRS_MAX_BULK_REQUEST_LENGTH"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_MAX_BULK_REQUEST_LENGTH: %s", v)
		}
		cfg.DRS.MaxBulkRequestLength = i
	}
	if v := os.Getenv("DRS_MULTIPART_CLEANUP_INTERVAL_SECONDS"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_MULTIPART_CLEANUP_INTERVAL_SECONDS: %s", v)
		}
		cfg.Multipart.CleanupIntervalSeconds = i
	}
	if v := os.Getenv("DRS_MULTIPART_INACTIVE_TIMEOUT_SECONDS"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_MULTIPART_INACTIVE_TIMEOUT_SECONDS: %s", v)
		}
		cfg.Multipart.InactiveTimeoutSeconds = i
	}
	if v := os.Getenv("DRS_MULTIPART_COMPLETED_RETENTION_SECONDS"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_MULTIPART_COMPLETED_RETENTION_SECONDS: %s", v)
		}
		cfg.Multipart.CompletedRetentionSeconds = i
	}
	if v := os.Getenv("DRS_MULTIPART_BATCH_SIZE"); v != "" {
		i, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_MULTIPART_BATCH_SIZE: %s", v)
		}
		cfg.Multipart.BatchSize = i
	}
	if v := os.Getenv("DRS_ENABLE_DOCS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_ENABLE_DOCS: %s", v)
		}
		cfg.Routes.Docs = b
	}
	if v := os.Getenv("DRS_ENABLE_GA4GH"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_ENABLE_GA4GH: %s", v)
		}
		cfg.Routes.Ga4gh = b
	}
	if v := os.Getenv("DRS_ENABLE_METRICS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_ENABLE_METRICS: %s", v)
		}
		cfg.Routes.Metrics = b
	}
	if v := os.Getenv("DRS_ENABLE_INTERNAL"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_ENABLE_INTERNAL: %s", v)
		}
		cfg.Routes.Internal = b
	}
	if v := os.Getenv("DRS_ENABLE_LFS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("invalid DRS_ENABLE_LFS: %s", v)
		}
		cfg.Routes.LFS = b
	}

	postgresHost := os.Getenv("DRS_DB_HOST")
	postgresDatabase := os.Getenv("DRS_DB_DATABASE")
	postgresSelectedByEnv := postgresHost != "" || postgresDatabase != ""
	if cfg.Database.Postgres == nil && cfg.Database.Sqlite == nil && !postgresSelectedByEnv && os.Getenv("DRS_DB_SQLITE_FILE") == "" {
		for _, name := range []string{"DRS_DB_PORT", "DRS_DB_USER", "DRS_DB_PASSWORD", "DRS_DB_SSLMODE"} {
			if os.Getenv(name) != "" {
				return fmt.Errorf("%s is set, but PostgreSQL is not selected; set DRS_DB_HOST or DRS_DB_DATABASE to select PostgreSQL", name)
			}
		}
	}

	if postgresSelectedByEnv {
		if cfg.Database.Postgres == nil {
			cfg.Database.Postgres = &PostgresConfig{
				Host:    "localhost",
				Port:    5432,
				SSLMode: "require", // SECURITY FIX MED-2: Default to TLS required
			}
		}
		// If env vars specify postgres, we should probably disable the default sqlite if it was still active
		// But let's let the validation catch it if they are both set.
	}

	if cfg.Database.Postgres != nil {
		if v := os.Getenv("DRS_DB_HOST"); v != "" {
			cfg.Database.Postgres.Host = v
		}
		if v := os.Getenv("DRS_DB_PORT"); v != "" {
			p, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid DRS_DB_PORT: %s", v)
			}
			cfg.Database.Postgres.Port = p
		}
		if v := os.Getenv("DRS_DB_USER"); v != "" {
			cfg.Database.Postgres.User = v
		}
		if v := os.Getenv("DRS_DB_PASSWORD"); v != "" {
			cfg.Database.Postgres.Password = v
		}
		if v := os.Getenv("DRS_DB_DATABASE"); v != "" {
			cfg.Database.Postgres.Database = v
		}
		if v := os.Getenv("DRS_DB_SSLMODE"); v != "" {
			cfg.Database.Postgres.SSLMode = v
		}
		if v := os.Getenv("DRS_DB_ALLOW_INSECURE_TRANSPORT"); v != "" {
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid DRS_DB_ALLOW_INSECURE_TRANSPORT: %s", v)
			}
			cfg.Database.Postgres.AllowInsecureTransport = b
		}
		for _, setting := range []struct {
			env  string
			dest *int
		}{
			{env: "DRS_DB_MAX_OPEN_CONNECTIONS", dest: &cfg.Database.Postgres.MaxOpenConnections},
			{env: "DRS_DB_MAX_IDLE_CONNECTIONS", dest: &cfg.Database.Postgres.MaxIdleConnections},
			{env: "DRS_DB_CONNECTION_MAX_LIFETIME_SECONDS", dest: &cfg.Database.Postgres.ConnectionMaxLifetimeSeconds},
			{env: "DRS_DB_CONNECTION_MAX_IDLE_TIME_SECONDS", dest: &cfg.Database.Postgres.ConnectionMaxIdleTimeSeconds},
		} {
			if v := os.Getenv(setting.env); v != "" {
				i, err := strconv.Atoi(v)
				if err != nil {
					return fmt.Errorf("invalid %s: %s", setting.env, v)
				}
				*setting.dest = i
			}
		}
	}

	if v := os.Getenv("DRS_DB_SQLITE_FILE"); v != "" {
		if cfg.Database.Sqlite == nil {
			cfg.Database.Sqlite = &SqliteConfig{}
		}
		cfg.Database.Sqlite.File = v
	}
	return nil
}
