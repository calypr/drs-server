package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/requestid"
)

func (db *Store) execContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.execOn(ctx, db.db, query, args...)
}

func defaultProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "s3"
	}
	return provider
}

func (db *Store) GetS3Credential(ctx context.Context, credentialID string) (*buckets.Credential, error) {
	var c buckets.Credential
	err := db.queryRowContext(ctx, `
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE credential_id = ?`, credentialID).Scan(
		&c.CredentialID, &c.Bucket, &c.Provider, &c.Region, &c.AccessKey, &c.SecretKey, &c.Endpoint,
	)
	if err == sql.ErrNoRows {
		fallback, fallbackErr := db.getS3CredentialByPhysicalBucket(ctx, credentialID)
		if fallbackErr == nil {
			auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "read", credentialID, nil)
			return fallback, nil
		}
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "read", credentialID, fallbackErr)
		return nil, fallbackErr
	}
	if err != nil {
		wrapped := fmt.Errorf("failed to fetch credential: %w", err)
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "read", credentialID, wrapped)
		return nil, wrapped
	}
	parsed, err := db.cipher.Parse(ctx, &c)
	if err != nil {
		wrapped := fmt.Errorf("failed to decrypt credential: %w", err)
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "read", credentialID, wrapped)
		return nil, wrapped
	}
	auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "read", credentialID, nil)
	return parsed, nil
}

func (db *Store) getS3CredentialByPhysicalBucket(ctx context.Context, bucket string) (*buckets.Credential, error) {
	rows, err := db.queryContext(ctx, `
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE bucket = ?`, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch credential by bucket: %w", err)
	}
	defer rows.Close()

	matches := make([]buckets.Credential, 0, 2)
	for rows.Next() {
		var c buckets.Credential
		if err := rows.Scan(&c.CredentialID, &c.Bucket, &c.Provider, &c.Region, &c.AccessKey, &c.SecretKey, &c.Endpoint); err != nil {
			return nil, err
		}
		matches = append(matches, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, errorapi.ErrStorageCredentialMissing
	case 1:
		parsed, err := db.cipher.Parse(ctx, &matches[0])
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt credential: %w", err)
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("multiple credentials use physical bucket %q; define the scope inline with the intended bucket credential", bucket)
	}
}

func (db *Store) SaveS3Credential(ctx context.Context, cred *buckets.Credential) error {
	bucket := ""
	if cred != nil {
		bucket = cred.Bucket
		if strings.TrimSpace(cred.CredentialID) == "" {
			cred.CredentialID = buckets.DeriveCredentialID(cred.Bucket, cred.Provider, cred.Region, cred.Endpoint, cred.AccessKey)
		}
	}
	stored, err := db.cipher.Prepare(ctx, cred)
	if err != nil {
		wrapped := fmt.Errorf("failed to prepare credential for storage: %w", err)
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", bucket, wrapped)
		return wrapped
	}
	if err := db.ensureUniquePhysicalBucket(ctx, db.db, stored.CredentialID, stored.Bucket); err != nil {
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", stored.Bucket, err)
		return err
	}

	// SQLite UPSERT syntax: INSERT INTO ... ON CONFLICT (...) DO UPDATE SET ...
	err = db.saveS3CredentialOn(ctx, db.db, stored)
	if err != nil {
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", stored.Bucket, err)
		return err
	}
	auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", stored.Bucket, nil)
	return nil
}

func (db *Store) saveS3CredentialOn(ctx context.Context, executor sqlExecutor, stored *buckets.Credential) error {
	_, err := db.execOn(ctx, executor, `
		INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (credential_id) DO UPDATE SET
			bucket = EXCLUDED.bucket,
			provider = EXCLUDED.provider,
			region = EXCLUDED.region,
			access_key = EXCLUDED.access_key,
			secret_key = EXCLUDED.secret_key,
			endpoint = EXCLUDED.endpoint`,
		stored.CredentialID, stored.Bucket, strings.ToLower(strings.TrimSpace(defaultProvider(stored.Provider))), stored.Region, stored.AccessKey, stored.SecretKey, stored.Endpoint,
	)
	if err != nil {
		return fmt.Errorf("failed to save credential: %w", err)
	}
	return nil
}

// SaveBucketConfiguration atomically persists a credential and its scope. A
// failure in either write leaves both rows unchanged.
func (db *Store) SaveBucketConfiguration(ctx context.Context, configuration buckets.BucketConfiguration) error {
	cred := configuration.Credential
	bucket := cred.Bucket
	if strings.TrimSpace(cred.CredentialID) == "" {
		cred.CredentialID = buckets.DeriveCredentialID(cred.Bucket, cred.Provider, cred.Region, cred.Endpoint, cred.AccessKey)
	}
	stored, err := db.cipher.Prepare(ctx, &cred)
	if err != nil {
		wrapped := fmt.Errorf("failed to prepare credential for storage: %w", err)
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", bucket, wrapped)
		return wrapped
	}
	preparedScope, err := normalizeBucketScope(&buckets.Scope{
		Organization: configuration.Organization,
		ProjectID:    configuration.ProjectID,
		CredentialID: stored.CredentialID,
		Bucket:       stored.Bucket,
		PathPrefix:   configuration.PathPrefix,
	})
	if err != nil {
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", bucket, err)
		return err
	}

	err = db.withWrite(ctx, func(tx *sql.Tx) error {
		if err := db.ensureUniquePhysicalBucket(ctx, tx, stored.CredentialID, stored.Bucket); err != nil {
			return err
		}
		if err := db.saveS3CredentialOn(ctx, tx, stored); err != nil {
			return err
		}
		return db.createBucketScopeOn(ctx, tx, preparedScope)
	})
	if err != nil {
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", stored.Bucket, err)
		return err
	}
	auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "write", stored.Bucket, nil)
	return nil
}

func normalizeBucketScope(scope *buckets.Scope) (buckets.Scope, error) {
	if scope == nil {
		return buckets.Scope{}, fmt.Errorf("scope is required")
	}
	normalized := buckets.Scope{
		Organization: strings.TrimSpace(scope.Organization),
		ProjectID:    strings.TrimSpace(scope.ProjectID),
		CredentialID: strings.TrimSpace(scope.CredentialID),
		Bucket:       strings.TrimSpace(scope.Bucket),
		PathPrefix:   strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"),
	}
	if normalized.CredentialID == "" {
		normalized.CredentialID = normalized.Bucket
	}
	if normalized.Organization == "" || normalized.Bucket == "" {
		return buckets.Scope{}, fmt.Errorf("organization and bucket are required")
	}
	return normalized, nil
}

func (db *Store) createBucketScopeOn(ctx context.Context, executor sqlExecutor, scope buckets.Scope) error {
	existing, err := db.getBucketScopeOn(ctx, executor, scope.Organization, scope.ProjectID)
	if err != nil && !errors.Is(err, errorapi.ErrBucketScopeNotFound) {
		return err
	}
	if err == nil && existing != nil {
		if strings.EqualFold(strings.TrimSpace(existing.CredentialID), scope.CredentialID) && strings.EqualFold(strings.TrimSpace(existing.Bucket), scope.Bucket) && strings.Trim(strings.TrimSpace(existing.PathPrefix), "/") == scope.PathPrefix {
			return nil
		}
		if _, err := db.execOn(ctx, executor, `
			UPDATE bucket_scope
			SET credential_id = ?, bucket = ?, path_prefix = ?
			WHERE organization = ? AND project_id = ?
		`, scope.CredentialID, scope.Bucket, scope.PathPrefix, scope.Organization, scope.ProjectID); err != nil {
			return fmt.Errorf("failed to update bucket scope: %w", err)
		}
		return nil
	}

	if _, err := db.execOn(ctx, executor, `
		INSERT INTO bucket_scope (organization, project_id, credential_id, bucket, path_prefix)
		VALUES (?, ?, ?, ?, ?)
	`, scope.Organization, scope.ProjectID, scope.CredentialID, scope.Bucket, scope.PathPrefix); err != nil {
		return fmt.Errorf("failed to create bucket scope: %w", err)
	}
	return nil
}

func (db *Store) getBucketScopeOn(ctx context.Context, executor sqlExecutor, organization, projectID string) (*buckets.Scope, error) {
	var scope buckets.Scope
	err := db.queryRowOn(ctx, executor, `
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
		WHERE organization = ? AND project_id = ?
	`, organization, projectID).Scan(
		&scope.Organization, &scope.ProjectID, &scope.CredentialID, &scope.Bucket, &scope.PathPrefix,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errorapi.ErrBucketScopeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket scope: %w", err)
	}
	return &scope, nil
}

func (db *Store) ensureUniquePhysicalBucket(ctx context.Context, executor sqlExecutor, credentialID, bucket string) error {
	credentialID = strings.TrimSpace(credentialID)
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil
	}

	var existingCredentialID string
	err := db.queryRowOn(ctx, executor, `
		SELECT credential_id
		FROM s3_credential
		WHERE bucket = ? AND credential_id <> ?
		LIMIT 1
	`, bucket, credentialID).Scan(&existingCredentialID)
	if err == nil {
		return fmt.Errorf("physical bucket %q is already configured under credential %q; reuse that credential and add a bucket scope instead", bucket, existingCredentialID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("failed to validate physical bucket uniqueness: %w", err)
}

func (db *Store) DeleteS3Credential(ctx context.Context, credentialID string) error {
	err := db.withWrite(ctx, func(tx *sql.Tx) error {
		resolvedID, _, found, err := db.resolveCredentialIdentityOn(ctx, tx, credentialID)
		if err != nil {
			return err
		}
		if !found {
			return errorapi.ErrStorageCredentialMissing
		}
		if err := db.lockCredentialOn(ctx, tx, resolvedID); err != nil {
			return err
		}
		if _, err := db.execOn(ctx, tx, "DELETE FROM bucket_scope WHERE credential_id = ?", resolvedID); err != nil {
			return fmt.Errorf("failed to delete bucket scopes for %s: %w", credentialID, err)
		}
		result, err := db.execOn(ctx, tx, "DELETE FROM s3_credential WHERE credential_id = ?", resolvedID)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return errorapi.ErrStorageCredentialMissing
		}
		return nil
	})
	if err != nil {
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "delete", credentialID, err)
		return err
	}
	auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "delete", credentialID, nil)
	return nil
}

func (db *Store) resolveCredentialIdentityOn(ctx context.Context, executor sqlExecutor, raw string) (string, string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false, nil
	}
	var credentialID, bucket string
	err := db.queryRowOn(ctx, executor, "SELECT credential_id, bucket FROM s3_credential WHERE credential_id = ?", raw).Scan(&credentialID, &bucket)
	if err == nil {
		return credentialID, bucket, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", false, err
	}
	var matches int
	if err := db.queryRowOn(ctx, executor, "SELECT COUNT(*) FROM s3_credential WHERE bucket = ?", raw).Scan(&matches); err != nil {
		return "", "", false, err
	}
	if matches > 1 {
		return "", "", false, fmt.Errorf("multiple credentials use physical bucket %q; define the scope inline with the intended bucket credential", raw)
	}
	if matches == 0 {
		return "", "", false, nil
	}
	err = db.queryRowOn(ctx, executor, "SELECT credential_id, bucket FROM s3_credential WHERE bucket = ?", raw).Scan(&credentialID, &bucket)
	if err == nil {
		return credentialID, bucket, true, nil
	}
	return "", "", false, err
}

func (db *Store) lockCredentialOn(ctx context.Context, executor sqlExecutor, credentialID string) error {
	result, err := db.execOn(ctx, executor, `
		UPDATE s3_credential
		SET credential_id = credential_id
		WHERE credential_id = ?
	`, credentialID)
	if err != nil {
		return fmt.Errorf("failed to lock storage credential: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect locked storage credential: %w", err)
	}
	if rows == 0 {
		return errorapi.ErrStorageCredentialMissing
	}
	return nil
}

// DeleteBucketScopeConfiguration atomically removes one scope and its unused
// credential. Returned aliases are safe to invalidate because commit succeeded.
func (db *Store) DeleteBucketScopeConfiguration(ctx context.Context, scope buckets.Scope) ([]string, error) {
	organization := strings.TrimSpace(scope.Organization)
	projectID := strings.TrimSpace(scope.ProjectID)
	requestedID := strings.TrimSpace(scope.CredentialID)
	pathPrefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
	if organization == "" || requestedID == "" {
		return nil, fmt.Errorf("organization and credential_id are required")
	}

	var aliases []string
	err := db.withWrite(ctx, func(tx *sql.Tx) error {
		canonicalID, physicalBucket, found, err := db.resolveCredentialIdentityOn(ctx, tx, requestedID)
		if err != nil {
			return err
		}
		if !found {
			canonicalID, physicalBucket = requestedID, requestedID
		} else if err := db.lockCredentialOn(ctx, tx, canonicalID); err != nil {
			return err
		}

		query := `DELETE FROM bucket_scope
			WHERE organization = ? AND project_id = ? AND COALESCE(path_prefix, '') = ?
			AND (credential_id = ? OR credential_id = ? OR bucket = ? OR bucket = ?)`
		result, err := db.execOn(ctx, tx, query, organization, projectID, pathPrefix, requestedID, canonicalID, requestedID, physicalBucket)
		if err != nil {
			return fmt.Errorf("failed to delete bucket scope: %w", err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("failed to inspect deleted bucket scope count: %w", err)
		}
		if rows == 0 {
			return errorapi.ErrBucketScopeNotFound
		}
		if !found {
			return nil
		}

		var remaining int
		if err := db.queryRowOn(ctx, tx, `SELECT COUNT(*) FROM bucket_scope
			WHERE credential_id = ? OR bucket = ?`, canonicalID, physicalBucket).Scan(&remaining); err != nil {
			return fmt.Errorf("failed to count remaining bucket scopes: %w", err)
		}
		if remaining > 0 {
			return nil
		}
		result, err = db.execOn(ctx, tx, "DELETE FROM s3_credential WHERE credential_id = ?", canonicalID)
		if err != nil {
			return fmt.Errorf("failed to delete unused credential: %w", err)
		}
		rows, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return errorapi.ErrStorageCredentialMissing
		}
		aliases = []string{requestedID, canonicalID, physicalBucket}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return aliases, nil
}

func (db *Store) ListS3Credentials(ctx context.Context) ([]buckets.Credential, error) {
	rows, err := db.queryContext(ctx, "SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint FROM s3_credential")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	creds := make([]buckets.Credential, 0)
	for rows.Next() {
		var c buckets.Credential
		if err := rows.Scan(&c.CredentialID, &c.Bucket, &c.Provider, &c.Region, &c.AccessKey, &c.SecretKey, &c.Endpoint); err != nil {
			auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "list", "", err)
			return nil, err
		}
		parsed, err := db.cipher.Parse(ctx, &c)
		if err != nil {
			wrapped := fmt.Errorf("failed to decrypt credential for bucket %s: %w", c.Bucket, err)
			auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "list", c.Bucket, wrapped)
			return nil, wrapped
		}
		creds = append(creds, *parsed)
	}
	if err := rows.Err(); err != nil {
		auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "list", "", err)
		return nil, err
	}
	auditCredentialAccess(ctx, requestid.GetRequestID(ctx), "list", "", nil)
	return creds, nil
}

func auditCredentialAccess(ctx context.Context, requestID, action, bucket string, err error) {
	mode := "local"
	if access.FromContext(ctx).Mode == "gen3" {
		mode = "gen3"
	}
	if err != nil {
		slog.Warn("s3 credential audit", "action", action, "bucket", bucket, "request_id", requestID, "mode", mode, "result", "error", "err", err)
		return
	}
	slog.Info("s3 credential audit", "action", action, "bucket", bucket, "request_id", requestID, "mode", mode, "result", "success")
}

func (db *Store) CreateBucketScope(ctx context.Context, scope *buckets.Scope) error {
	normalized, err := normalizeBucketScope(scope)
	if err != nil {
		return err
	}
	return db.createBucketScopeOn(ctx, db.db, normalized)
}

func (db *Store) GetBucketScope(ctx context.Context, organization, projectID string) (*buckets.Scope, error) {
	return db.getBucketScopeOn(ctx, db.db, strings.TrimSpace(organization), strings.TrimSpace(projectID))
}

func (db *Store) ListBucketScopes(ctx context.Context) ([]buckets.Scope, error) {
	rows, err := db.queryContext(ctx, `
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []buckets.Scope
	for rows.Next() {
		var s buckets.Scope
		if err := rows.Scan(&s.Organization, &s.ProjectID, &s.CredentialID, &s.Bucket, &s.PathPrefix); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
