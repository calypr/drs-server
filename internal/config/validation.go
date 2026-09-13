package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage/address"
)

func validateConfig(cfg *Config) error {
	cfg.Profile = strings.ToLower(strings.TrimSpace(cfg.Profile))
	if cfg.Profile == "" {
		cfg.Profile = ProfileDevelopment
	}
	if cfg.Profile != ProfileDevelopment && cfg.Profile != ProfileProduction {
		return fmt.Errorf("invalid profile %q: expected %q or %q", cfg.Profile, ProfileDevelopment, ProfileProduction)
	}
	if cfg.DRS.MaxBulkRequestLength < 1 {
		return fmt.Errorf("drs.max_bulk_request_length must be >= 1")
	}
	if cfg.Multipart.CleanupIntervalSeconds < 0 || cfg.Multipart.InactiveTimeoutSeconds < 0 || cfg.Multipart.CompletedRetentionSeconds < 0 || cfg.Multipart.BatchSize < 0 {
		return fmt.Errorf("multipart cleanup values must be >= 0")
	}
	if cfg.Multipart.CleanupIntervalSeconds > 0 {
		if cfg.Multipart.InactiveTimeoutSeconds == 0 {
			return fmt.Errorf("multipart.inactive_timeout_seconds must be >= 1 when cleanup is enabled")
		}
		if cfg.Multipart.InactiveTimeoutSeconds < cfg.Multipart.CleanupIntervalSeconds {
			return fmt.Errorf("multipart.inactive_timeout_seconds must be at least cleanup_interval_seconds")
		}
		if cfg.Multipart.BatchSize == 0 {
			return fmt.Errorf("multipart.batch_size must be >= 1 when cleanup is enabled")
		}
	}
	// Final Validation: Exactly one DB must be specified
	if cfg.Database.Sqlite != nil && cfg.Database.Postgres != nil {
		// If both are set, but one is the default "drs.db" and the other was explicitly set by user,
		// we can try to be smart, but user asked to "raise an error".
		// Actually, if I load a file that has `postgres:`, the `sqlite:` default from line 52 is still there.
		// So I must clear it if postgres is detected.

		// If postgres was explicitly defined (either in file or via env), we clear the default sqlite.
		// A better way is to check if it's the "default" value.
		if cfg.Database.Sqlite.File == "drs.db" && (cfg.Database.Postgres.Host != "localhost" || cfg.Database.Postgres.Database != "") {
			// This is risky. Let's just follow the user instruction: if both present, error.
			// This means my LoadConfig must be careful not to leave defaults if others are set.
		}
	}

	if cfg.Database.Sqlite != nil && cfg.Database.Postgres != nil {
		return fmt.Errorf("multiple databases specified in config; only one of 'sqlite' or 'postgres' allowed")
	}
	if cfg.Database.Sqlite == nil && cfg.Database.Postgres == nil {
		return fmt.Errorf("no database specified in config")
	}

	if len(cfg.Buckets) > 0 && len(cfg.S3Credentials) > 0 {
		return fmt.Errorf("config may specify only one of 'buckets' or legacy 's3_credentials'")
	}
	if len(cfg.Buckets) == 0 && len(cfg.S3Credentials) > 0 {
		cfg.Buckets = append([]BucketConfig(nil), cfg.S3Credentials...)
	}

	// Validate configured bucket credentials.
	for i := range cfg.Buckets {
		cred := &cfg.Buckets[i]
		cred.Bucket = strings.TrimSpace(cred.Bucket)
		cred.Region = strings.ToLower(strings.TrimSpace(cred.Region))
		cred.Endpoint = strings.TrimRight(strings.TrimSpace(cred.Endpoint), "/")
		bucketProvider, err := address.ParseBucketProvider(cred.Provider)
		if err != nil {
			return fmt.Errorf("buckets[%d]: %w", i, err)
		}
		cred.Provider = bucketProvider
		cred.CredentialID = buckets.DeriveCredentialID(
			cred.Bucket,
			bucketProvider,
			cred.Region,
			cred.Endpoint,
			cred.AccessKey,
		)
		if err := address.ValidateBucketNameWithEndpoint(bucketProvider, cred.Bucket, cred.Endpoint); err != nil {
			return fmt.Errorf("buckets[%d]: %w", i, err)
		}
		if bucketProvider == address.S3Provider {
			if cred.Region == "" {
				return fmt.Errorf("buckets[%d]: region is required for provider=%s", i, bucketProvider)
			}
			if cred.AccessKey == "" {
				return fmt.Errorf("buckets[%d]: access_key is required for provider=%s", i, bucketProvider)
			}
			if cred.SecretKey == "" {
				return fmt.Errorf("buckets[%d]: secret_key is required for provider=%s", i, bucketProvider)
			}
		}
	}

	derivedScopes, err := deriveBucketScopesFromBuckets(cfg.Buckets)
	if err != nil {
		return err
	}
	definitions := make([]bucketScopeDefinition, 0, len(cfg.BucketScopes)+len(derivedScopes))
	for i, scope := range cfg.BucketScopes {
		definitions = append(definitions, bucketScopeDefinition{scope: scope, source: fmt.Sprintf("bucket_scopes[%d]", i)})
	}
	definitions = append(definitions, derivedScopes...)
	credentialIDsByBucket := credentialIDsByPhysicalBucket(cfg.Buckets)
	accepted := make([]bucketScopeDefinition, 0, len(definitions))
	byKey := make(map[[2]string]int, len(definitions))
	for _, definition := range definitions {
		normalized, err := normalizeBucketScope(definition.scope, definition.source, credentialIDsByBucket)
		if err != nil {
			return err
		}
		definition.scope = normalized
		key := [2]string{normalized.Organization, normalized.ProjectID}
		if previousIndex, exists := byKey[key]; exists {
			previous := accepted[previousIndex]
			if previous.scope == normalized {
				continue
			}
			return fmt.Errorf("%s conflicts with %s for organization %q project %q", definition.source, previous.source, normalized.Organization, normalized.ProjectID)
		}
		byKey[key] = len(accepted)
		accepted = append(accepted, definition)
	}
	cfg.BucketScopes = make([]BucketScopeConfig, len(accepted))
	for i, definition := range accepted {
		cfg.BucketScopes[i] = definition.scope
	}
	// Keep the legacy field populated for older call sites and tests.
	cfg.S3Credentials = append([]BucketConfig(nil), cfg.Buckets...)

	cfg.Auth.Mode = strings.ToLower(strings.TrimSpace(cfg.Auth.Mode))
	if cfg.Auth.Mode == "" {
		return fmt.Errorf("auth.mode is required and must be one of %q or %q", AuthModeLocal, AuthModeGen3)
	}
	if cfg.Auth.Mode != AuthModeLocal && cfg.Auth.Mode != AuthModeGen3 {
		return fmt.Errorf("invalid auth.mode %q: expected %q or %q", cfg.Auth.Mode, AuthModeLocal, AuthModeGen3)
	}
	if cfg.Auth.Mode == AuthModeGen3 && cfg.Database.Postgres == nil && !inheritedMockAuthEnabled() {
		return fmt.Errorf("auth.mode %q requires postgres database", cfg.Auth.Mode)
	}
	if (cfg.Auth.Basic.Username == "") != (cfg.Auth.Basic.Password == "") {
		return fmt.Errorf("both auth.basic.username and auth.basic.password must be set together")
	}

	if cfg.Auth.Mode == AuthModeLocal && cfg.Auth.LocalAuthzCSV == "" && (cfg.Auth.Basic.Username == "" || cfg.Auth.Basic.Password == "") && !cfg.Auth.AllowUnauthenticated {
		return fmt.Errorf("auth.mode %q requires auth.basic.username/password or auth.local_authz_csv; set auth.allow_unauthenticated=true only for development/testing", AuthModeLocal)
	}

	// Gen3 mock auth is the supported local integration-testing path for Gen3 mode.
	if inheritedMockAuthEnabled() && cfg.Auth.Mode != AuthModeGen3 {
		return fmt.Errorf("mock auth (DRS_AUTH_MOCK_ENABLED) is only allowed in gen3 auth mode, not in %q", cfg.Auth.Mode)
	}
	if cfg.LFS.MaxBatchObjects < 0 {
		return fmt.Errorf("lfs.max_batch_objects must be >= 0")
	}
	if cfg.LFS.MaxBatchBodyBytes < 0 {
		return fmt.Errorf("lfs.max_batch_body_bytes must be >= 0")
	}
	if cfg.LFS.RequestLimitPerMinute < 0 {
		return fmt.Errorf("lfs.request_limit_per_minute must be >= 0")
	}
	if cfg.LFS.BandwidthLimitBytesPerMinute < 0 {
		return fmt.Errorf("lfs.bandwidth_limit_bytes_per_minute must be >= 0")
	}
	if cfg.Database.Postgres != nil {
		pg := cfg.Database.Postgres
		if pg.MaxOpenConnections < 0 || pg.MaxIdleConnections < 0 || pg.ConnectionMaxLifetimeSeconds < 0 || pg.ConnectionMaxIdleTimeSeconds < 0 {
			return fmt.Errorf("postgres connection pool values must be >= 0")
		}
		if pg.MaxOpenConnections > 0 && pg.MaxIdleConnections > pg.MaxOpenConnections {
			return fmt.Errorf("postgres.max_idle_connections cannot exceed max_open_connections")
		}
	}
	if cfg.Profile == ProfileProduction {
		if cfg.Database.Sqlite != nil {
			return fmt.Errorf("production profile requires PostgreSQL; SQLite is not supported")
		}
		if cfg.Auth.AllowUnauthenticated || cfg.Auth.Mock.Enabled || inheritedMockAuthEnabled() {
			return fmt.Errorf("production profile forbids unauthenticated or mock authentication")
		}
		if cfg.Routes.Docs {
			return fmt.Errorf("production profile requires routes.docs=false")
		}
		if cfg.Database.Postgres == nil {
			return fmt.Errorf("production profile requires PostgreSQL")
		}
		pg := cfg.Database.Postgres
		if strings.EqualFold(strings.TrimSpace(pg.SSLMode), "disable") || strings.TrimSpace(pg.SSLMode) == "" {
			if !pg.AllowInsecureTransport {
				return fmt.Errorf("production profile requires PostgreSQL TLS; set database.postgres.allow_insecure_transport=true only for an explicit trusted network")
			}
		}
		if pg.MaxOpenConnections < 1 {
			return fmt.Errorf("production profile requires postgres.max_open_connections >= 1")
		}
		if !stableCredentialEncryptionConfigured(cfg) {
			return fmt.Errorf("production profile requires stable credential encryption configuration")
		}
		if strings.TrimSpace(cfg.Service.ID) == "" || strings.TrimSpace(cfg.Service.Name) == "" || strings.TrimSpace(cfg.Service.Organization) == "" || strings.TrimSpace(cfg.Service.OrganizationURL) == "" {
			return fmt.Errorf("production profile requires service id, name, organization, and organization_url")
		}
	}
	return nil
}

func stableCredentialEncryptionConfigured(cfg *Config) bool {
	if strings.TrimSpace(cfg.CredentialEncryption.MasterKey) != "" || strings.TrimSpace(cfg.CredentialEncryption.LocalKeyFile) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("DRS_CREDENTIAL_MASTER_KEY")) != "" || strings.TrimSpace(os.Getenv("DRS_CREDENTIAL_LOCAL_KEY_FILE")) != "" || strings.TrimSpace(os.Getenv("DRS_CREDENTIAL_KMS_KEY_ID")) != "" {
		return true
	}
	manager := strings.ToLower(strings.TrimSpace(os.Getenv("DRS_CREDENTIAL_KEY_MANAGER")))
	return manager != "" && manager != "local" && manager != "file"
}

func normalizeBucketScope(scope BucketScopeConfig, source string, credentialIDsByBucket map[string][]string) (BucketScopeConfig, error) {
	scope.Organization = strings.TrimSpace(scope.Organization)
	scope.ProjectID = strings.TrimSpace(scope.ProjectID)
	scope.CredentialID = strings.TrimSpace(scope.CredentialID)
	scope.Bucket = strings.TrimSpace(scope.Bucket)
	scope.Path = strings.TrimSpace(scope.Path)
	scope.PathPrefix = strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
	scope.OrganizationSubPath = cleanBucketScopeSubPath(scope.OrganizationSubPath)
	scope.ProjectSubPath = cleanBucketScopeSubPath(scope.ProjectSubPath)

	if scope.Organization == "" {
		return BucketScopeConfig{}, fmt.Errorf("%s: organization is required", source)
	}
	if strings.Contains(scope.Organization, "/") {
		return BucketScopeConfig{}, fmt.Errorf("%s: organization must be a Gen3 program name, not a storage path", source)
	}
	if strings.Contains(scope.ProjectID, "/") {
		return BucketScopeConfig{}, fmt.Errorf("%s: project_id must be a Gen3 project id, not a storage path", source)
	}
	hasComposedSubPaths := scope.OrganizationSubPath != "" || scope.ProjectSubPath != ""
	if hasComposedSubPaths && scope.Path != "" {
		return BucketScopeConfig{}, fmt.Errorf("%s: path cannot be combined with organization_sub_path or project_sub_path", source)
	}
	if hasComposedSubPaths && scope.PathPrefix != "" {
		return BucketScopeConfig{}, fmt.Errorf("%s: path_prefix cannot be combined with organization_sub_path or project_sub_path", source)
	}
	if scope.Path != "" {
		u, err := url.Parse(scope.Path)
		if err != nil {
			return BucketScopeConfig{}, fmt.Errorf("%s: invalid path: %w", source, err)
		}
		if address.ProviderFromScheme(u.Scheme) == "" {
			return BucketScopeConfig{}, fmt.Errorf("%s: unsupported storage scheme: %s", source, u.Scheme)
		}
		pathBucket := strings.TrimSpace(u.Host)
		if pathBucket == "" {
			return BucketScopeConfig{}, fmt.Errorf("%s: path must include a bucket", source)
		}
		if scope.Bucket != "" && !strings.EqualFold(scope.Bucket, pathBucket) {
			return BucketScopeConfig{}, fmt.Errorf("%s: bucket %q does not match path bucket %q", source, scope.Bucket, pathBucket)
		}
		prefix, err := address.NormalizeStoragePath(scope.Path, pathBucket)
		if err != nil {
			return BucketScopeConfig{}, fmt.Errorf("%s: %w", source, err)
		}
		if scope.PathPrefix != "" && scope.PathPrefix != prefix {
			return BucketScopeConfig{}, fmt.Errorf("%s: path_prefix %q does not match path prefix %q", source, scope.PathPrefix, prefix)
		}
		scope.Bucket = pathBucket
		scope.PathPrefix = prefix
	}
	if scope.CredentialID == "" {
		credentialID, err := resolveScopeCredentialID(scope.Bucket, credentialIDsByBucket)
		if err != nil {
			return BucketScopeConfig{}, fmt.Errorf("%s: %w", source, err)
		}
		scope.CredentialID = credentialID
	}
	if hasComposedSubPaths {
		if scope.Bucket == "" && scope.CredentialID == "" {
			return BucketScopeConfig{}, fmt.Errorf("%s: bucket is required when organization_sub_path or project_sub_path is set", source)
		}
		scope.PathPrefix = joinBucketScopeSubPaths(scope.OrganizationSubPath, scope.ProjectSubPath)
	}
	if scope.Bucket == "" && scope.CredentialID == "" {
		return BucketScopeConfig{}, fmt.Errorf("%s: bucket or path is required", source)
	}
	scope.Path = ""
	scope.OrganizationSubPath = ""
	scope.ProjectSubPath = ""
	return scope, nil
}
