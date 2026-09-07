package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/calypr/syfon/internal/common"
)

func validateConfig(cfg *Config) error {
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
	for i, cred := range cfg.Buckets {
		bucketProvider, err := common.ParseBucketProvider(cred.Provider)
		if err != nil {
			return fmt.Errorf("buckets[%d]: %w", i, err)
		}
		cfg.Buckets[i].Provider = bucketProvider
		cfg.Buckets[i].CredentialID = common.DeriveCredentialID(
			cred.Bucket,
			bucketProvider,
			cred.Region,
			cred.Endpoint,
			cred.AccessKey,
		)
		if err := common.ValidateBucketNameWithEndpoint(bucketProvider, cred.Bucket, cred.Endpoint); err != nil {
			return fmt.Errorf("buckets[%d]: %w", i, err)
		}
		if bucketProvider == common.S3Provider {
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
	cfg.BucketScopes = append(append([]BucketScopeConfig(nil), cfg.BucketScopes...), derivedScopes...)
	credentialIDsByBucket := credentialIDsByPhysicalBucket(cfg.Buckets)

	for i := range cfg.BucketScopes {
		scope := &cfg.BucketScopes[i]
		scope.Organization = strings.TrimSpace(scope.Organization)
		scope.ProjectID = strings.TrimSpace(scope.ProjectID)
		scope.CredentialID = strings.TrimSpace(scope.CredentialID)
		scope.Bucket = strings.TrimSpace(scope.Bucket)
		scope.Path = strings.TrimSpace(scope.Path)
		scope.PathPrefix = strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
		scope.OrganizationSubPath = cleanBucketScopeSubPath(scope.OrganizationSubPath)
		scope.ProjectSubPath = cleanBucketScopeSubPath(scope.ProjectSubPath)

		if scope.Organization == "" {
			return fmt.Errorf("bucket_scopes[%d]: organization is required", i)
		}
		if strings.Contains(scope.Organization, "/") {
			return fmt.Errorf("bucket_scopes[%d]: organization must be a Gen3 program name, not a storage path", i)
		}
		if strings.Contains(scope.ProjectID, "/") {
			return fmt.Errorf("bucket_scopes[%d]: project_id must be a Gen3 project id, not a storage path", i)
		}
		hasComposedSubPaths := scope.OrganizationSubPath != "" || scope.ProjectSubPath != ""
		if hasComposedSubPaths && scope.Path != "" {
			return fmt.Errorf("bucket_scopes[%d]: path cannot be combined with organization_sub_path or project_sub_path", i)
		}
		if hasComposedSubPaths && scope.PathPrefix != "" {
			return fmt.Errorf("bucket_scopes[%d]: path_prefix cannot be combined with organization_sub_path or project_sub_path", i)
		}
		if scope.Path != "" {
			u, err := url.Parse(scope.Path)
			if err != nil {
				return fmt.Errorf("bucket_scopes[%d]: invalid path: %w", i, err)
			}
			if common.ProviderFromScheme(u.Scheme) == "" {
				return fmt.Errorf("bucket_scopes[%d]: unsupported storage scheme: %s", i, u.Scheme)
			}
			pathBucket := strings.TrimSpace(u.Host)
			if pathBucket == "" {
				return fmt.Errorf("bucket_scopes[%d]: path must include a bucket", i)
			}
			if scope.Bucket != "" && !strings.EqualFold(scope.Bucket, pathBucket) {
				return fmt.Errorf("bucket_scopes[%d]: bucket %q does not match path bucket %q", i, scope.Bucket, pathBucket)
			}
			prefix, err := common.NormalizeStoragePath(scope.Path, pathBucket)
			if err != nil {
				return fmt.Errorf("bucket_scopes[%d]: %w", i, err)
			}
			if scope.PathPrefix != "" && scope.PathPrefix != prefix {
				return fmt.Errorf("bucket_scopes[%d]: path_prefix %q does not match path prefix %q", i, scope.PathPrefix, prefix)
			}
			scope.Bucket = pathBucket
			scope.PathPrefix = prefix
		}
		if scope.CredentialID == "" {
			scope.CredentialID, err = resolveScopeCredentialID(scope.Bucket, credentialIDsByBucket)
			if err != nil {
				return fmt.Errorf("bucket_scopes[%d]: %w", i, err)
			}
		}
		if hasComposedSubPaths {
			if scope.Bucket == "" && scope.CredentialID == "" {
				return fmt.Errorf("bucket_scopes[%d]: bucket is required when organization_sub_path or project_sub_path is set", i)
			}
			scope.PathPrefix = joinBucketScopeSubPaths(scope.OrganizationSubPath, scope.ProjectSubPath)
		}
		if scope.Bucket == "" && scope.CredentialID == "" {
			return fmt.Errorf("bucket_scopes[%d]: bucket or path is required", i)
		}
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
	if cfg.Auth.Mode == AuthModeGen3 && cfg.Database.Postgres == nil && !isMockAuthEnabledFromEnv() {
		return fmt.Errorf("auth.mode %q requires postgres database", cfg.Auth.Mode)
	}
	if (cfg.Auth.Basic.Username == "") != (cfg.Auth.Basic.Password == "") {
		return fmt.Errorf("both auth.basic.username and auth.basic.password must be set together")
	}

	if cfg.Auth.Mode == AuthModeLocal && cfg.Auth.LocalAuthzCSV == "" && (cfg.Auth.Basic.Username == "" || cfg.Auth.Basic.Password == "") && !cfg.Auth.AllowUnauthenticated {
		return fmt.Errorf("auth.mode %q requires auth.basic.username/password or auth.local_authz_csv; set auth.allow_unauthenticated=true only for development/testing", AuthModeLocal)
	}

	// Gen3 mock auth is the supported local integration-testing path for Gen3 mode.
	if isMockAuthEnabledFromEnv() && cfg.Auth.Mode != AuthModeGen3 {
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
	return nil
}
