package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"

	"github.com/calypr/syfon/internal/crypto"
)

func (db *PostgresDB) GetS3Credential(ctx context.Context, credentialID string) (*models.S3Credential, error) {
	var c models.S3Credential
	err := db.db.QueryRowContext(ctx, `
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE credential_id = $1`, credentialID).Scan(
		&c.CredentialID, &c.Bucket, &c.Provider, &c.Region, &c.AccessKey, &c.SecretKey, &c.Endpoint,
	)
	if err == sql.ErrNoRows {
		fallback, fallbackErr := db.getS3CredentialByPhysicalBucket(ctx, credentialID)
		if fallbackErr == nil {
			common.AuditS3CredentialAccess(ctx, "read", credentialID, nil)
			return fallback, nil
		}
		common.AuditS3CredentialAccess(ctx, "read", credentialID, fallbackErr)
		return nil, fallbackErr
	}
	if err != nil {
		wrapped := fmt.Errorf("failed to fetch credential: %w", err)
		common.AuditS3CredentialAccess(ctx, "read", credentialID, wrapped)
		return nil, wrapped
	}
	parsed, err := crypto.ParseS3CredentialFromStorage(&c)
	if err != nil {
		wrapped := fmt.Errorf("failed to decrypt credential: %w", err)
		common.AuditS3CredentialAccess(ctx, "read", credentialID, wrapped)
		return nil, wrapped
	}
	common.AuditS3CredentialAccess(ctx, "read", credentialID, nil)
	return parsed, nil
}

func (db *PostgresDB) getS3CredentialByPhysicalBucket(ctx context.Context, bucket string) (*models.S3Credential, error) {
	rows, err := db.db.QueryContext(ctx, `
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE bucket = $1`, bucket)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch credential by bucket: %w", err)
	}
	defer rows.Close()

	matches := make([]models.S3Credential, 0, 2)
	for rows.Next() {
		var c models.S3Credential
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
		return nil, fmt.Errorf("credential not found")
	case 1:
		parsed, err := crypto.ParseS3CredentialFromStorage(&matches[0])
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt credential: %w", err)
		}
		return parsed, nil
	default:
		return nil, fmt.Errorf("multiple credentials use physical bucket %q; define the scope inline with the intended bucket credential", bucket)
	}
}

func (db *PostgresDB) SaveS3Credential(ctx context.Context, cred *models.S3Credential) error {
	bucket := ""
	if cred != nil {
		bucket = cred.Bucket
		if strings.TrimSpace(cred.CredentialID) == "" {
			cred.CredentialID = common.DeriveCredentialID(cred.Bucket, cred.Provider, cred.Region, cred.Endpoint, cred.AccessKey)
		}
	}
	stored, err := crypto.PrepareS3CredentialForStorage(cred)
	if err != nil {
		wrapped := fmt.Errorf("failed to prepare credential for storage: %w", err)
		common.AuditS3CredentialAccess(ctx, "write", bucket, wrapped)
		return wrapped
	}
	if err := db.ensureUniquePhysicalBucket(ctx, stored.CredentialID, stored.Bucket); err != nil {
		common.AuditS3CredentialAccess(ctx, "write", stored.Bucket, err)
		return err
	}

	_, err = db.db.ExecContext(ctx, `
		INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
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
		wrapped := fmt.Errorf("failed to save credential: %w", err)
		common.AuditS3CredentialAccess(ctx, "write", stored.Bucket, wrapped)
		return wrapped
	}
	common.AuditS3CredentialAccess(ctx, "write", stored.Bucket, nil)
	return nil
}

func (db *PostgresDB) ensureUniquePhysicalBucket(ctx context.Context, credentialID, bucket string) error {
	credentialID = strings.TrimSpace(credentialID)
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil
	}

	var existingCredentialID string
	err := db.db.QueryRowContext(ctx, `
		SELECT credential_id
		FROM s3_credential
		WHERE bucket = $1 AND credential_id <> $2
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

func (db *PostgresDB) DeleteS3Credential(ctx context.Context, credentialID string) error {
	resolvedID, err := db.resolveCredentialID(ctx, credentialID)
	if err != nil {
		common.AuditS3CredentialAccess(ctx, "delete", credentialID, err)
		return err
	}
	// 1. Delete bucket scopes first (cascade delete is on object_id, but bucket_scope is manual link)
	if _, err := db.db.ExecContext(ctx, "DELETE FROM bucket_scope WHERE credential_id = $1", resolvedID); err != nil {
		common.AuditS3CredentialAccess(ctx, "delete", credentialID, err)
		return fmt.Errorf("failed to delete bucket scopes for %s: %w", credentialID, err)
	}

	result, err := db.db.ExecContext(ctx, "DELETE FROM s3_credential WHERE credential_id = $1", resolvedID)
	if err != nil {
		common.AuditS3CredentialAccess(ctx, "delete", credentialID, err)
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		common.AuditS3CredentialAccess(ctx, "delete", credentialID, err)
		return err
	}
	if rows == 0 {
		notFoundErr := fmt.Errorf("credential not found")
		common.AuditS3CredentialAccess(ctx, "delete", credentialID, notFoundErr)
		return notFoundErr
	}
	common.AuditS3CredentialAccess(ctx, "delete", credentialID, nil)
	return nil
}

func (db *PostgresDB) resolveCredentialID(ctx context.Context, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("credential not found")
	}
	var exact string
	err := db.db.QueryRowContext(ctx, "SELECT credential_id FROM s3_credential WHERE credential_id = $1", raw).Scan(&exact)
	if err == nil {
		return exact, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	cred, err := db.getS3CredentialByPhysicalBucket(ctx, raw)
	if err != nil {
		return "", err
	}
	return cred.CredentialID, nil
}

func (db *PostgresDB) ListS3Credentials(ctx context.Context) ([]models.S3Credential, error) {
	rows, err := db.db.QueryContext(ctx, "SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint FROM s3_credential")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var creds []models.S3Credential
	for rows.Next() {
		var c models.S3Credential
		if err := rows.Scan(&c.CredentialID, &c.Bucket, &c.Provider, &c.Region, &c.AccessKey, &c.SecretKey, &c.Endpoint); err != nil {
			common.AuditS3CredentialAccess(ctx, "list", "", err)
			return nil, err
		}
		parsed, err := crypto.ParseS3CredentialFromStorage(&c)
		if err != nil {
			wrapped := fmt.Errorf("failed to decrypt credential for bucket %s: %w", c.Bucket, err)
			common.AuditS3CredentialAccess(ctx, "list", c.Bucket, wrapped)
			return nil, wrapped
		}
		creds = append(creds, *parsed)
	}
	common.AuditS3CredentialAccess(ctx, "list", "", nil)
	return creds, nil
}

func (db *PostgresDB) CreateBucketScope(ctx context.Context, scope *models.BucketScope) error {
	if scope == nil {
		return fmt.Errorf("scope is required")
	}
	org := strings.TrimSpace(scope.Organization)
	project := strings.TrimSpace(scope.ProjectID)
	credentialID := strings.TrimSpace(scope.CredentialID)
	bucket := strings.TrimSpace(scope.Bucket)
	prefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
	if credentialID == "" {
		credentialID = bucket
	}

	if org == "" || bucket == "" {
		return fmt.Errorf("organization and bucket are required")
	}

	existing, err := db.GetBucketScope(ctx, org, project)
	if err != nil && !errors.Is(err, faults.ErrNotFound) {
		return err
	}
	if err == nil && existing != nil {
		if strings.EqualFold(strings.TrimSpace(existing.CredentialID), credentialID) && strings.EqualFold(strings.TrimSpace(existing.Bucket), bucket) && strings.Trim(strings.TrimSpace(existing.PathPrefix), "/") == prefix {
			return nil
		}
		_, err = db.db.ExecContext(ctx, `
			UPDATE bucket_scope
			SET credential_id = $1, bucket = $2, path_prefix = $3
			WHERE organization = $4 AND project_id = $5
		`, credentialID, bucket, prefix, org, project)
		if err != nil {
			return fmt.Errorf("failed to update bucket scope: %w", err)
		}
		return nil
	}

	_, err = db.db.ExecContext(ctx, `
		INSERT INTO bucket_scope (organization, project_id, credential_id, bucket, path_prefix)
		VALUES ($1, $2, $3, $4, $5)
	`, org, project, credentialID, bucket, prefix)
	if err != nil {
		return fmt.Errorf("failed to create bucket scope: %w", err)
	}
	return nil
}

func (db *PostgresDB) GetBucketScope(ctx context.Context, organization, projectID string) (*models.BucketScope, error) {
	var s models.BucketScope
	err := db.db.QueryRowContext(ctx, `
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
		WHERE organization = $1 AND project_id = $2
	`, strings.TrimSpace(organization), strings.TrimSpace(projectID)).Scan(
		&s.Organization, &s.ProjectID, &s.CredentialID, &s.Bucket, &s.PathPrefix,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: bucket scope not found", faults.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket scope: %w", err)
	}
	return &s, nil
}

func (db *PostgresDB) DeleteBucketScope(ctx context.Context, organization, projectID, credentialID, pathPrefix string) error {
	org := strings.TrimSpace(organization)
	project := strings.TrimSpace(projectID)
	credentialID = strings.TrimSpace(credentialID)
	pathPrefix = strings.Trim(strings.TrimSpace(pathPrefix), "/")
	if org == "" || credentialID == "" {
		return fmt.Errorf("organization and credential_id are required")
	}

	query := `
		DELETE FROM bucket_scope
		WHERE organization = $1
		AND (credential_id = $2 OR bucket = $2)
	`
	args := []any{org, credentialID}
	if project != "" {
		query += `
		AND project_id = $3
		`
		args = append(args, project)
	} else {
		query += `
		AND project_id = ''
		`
	}
	query += `
	AND COALESCE(path_prefix, '') = $` + fmt.Sprint(len(args)+1)
	args = append(args, pathPrefix)

	result, err := db.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to delete bucket scope: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to inspect deleted bucket scope count: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: bucket scope not found", faults.ErrNotFound)
	}
	return nil
}

func (db *PostgresDB) ListBucketScopes(ctx context.Context) ([]models.BucketScope, error) {
	rows, err := db.db.QueryContext(ctx, `
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.BucketScope
	for rows.Next() {
		var s models.BucketScope
		if err := rows.Scan(&s.Organization, &s.ProjectID, &s.CredentialID, &s.Bucket, &s.PathPrefix); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}
