package config

import (
	"fmt"
	"os"
	"strconv"
)

func applyEnvironmentOverrides(cfg *Config) error {
	// 3. Override with Environment Variables (if set)
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

	// DB Env Vars overrides
	// If Postgres env vars are provided, we assume Postgres.
	if os.Getenv("DRS_DB_HOST") != "" || os.Getenv("DRS_DB_DATABASE") != "" {
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
	}

	if v := os.Getenv("DRS_DB_SQLITE_FILE"); v != "" {
		if cfg.Database.Sqlite == nil {
			cfg.Database.Sqlite = &SqliteConfig{}
		}
		cfg.Database.Sqlite.File = v
	}
	return nil
}
