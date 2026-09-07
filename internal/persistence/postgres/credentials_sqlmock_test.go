package postgres

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/credentialcipher"
	"github.com/calypr/syfon/internal/faults"

	"github.com/DATA-DOG/go-sqlmock"
)

var (
	_ buckets.CredentialReader = (*PostgresDB)(nil)
	_ buckets.CredentialAdmin  = (*PostgresDB)(nil)
	_ buckets.ScopeStore       = (*PostgresDB)(nil)
	_ buckets.VisibilityQuery  = (*PostgresDB)(nil)
)

func newMockPostgresDB(t *testing.T) (*PostgresDB, sqlmock.Sqlmock, *sql.DB) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	return &PostgresDB{db: db}, mock, db
}

func TestGetS3Credential(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	rows := sqlmock.NewRows([]string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"}).
		AddRow("b1", "b1", "s3", "us-east-1", "ak", "sk", "https://s3.example")
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE credential_id = $1`)).
		WithArgs("b1").
		WillReturnRows(rows)

	got, err := pg.GetS3Credential(context.Background(), "b1")
	if err != nil {
		t.Fatalf("GetS3Credential returned error: %v", err)
	}
	if got.Bucket != "b1" || got.Provider != "s3" {
		t.Fatalf("unexpected credential: %#v", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestGetS3Credential_DecryptsEncryptedSecrets(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	encAK, err := credentialcipher.EncryptCredentialField("ak")
	if err != nil {
		t.Fatalf("encrypt access key: %v", err)
	}
	encSK, err := credentialcipher.EncryptCredentialField("sk")
	if err != nil {
		t.Fatalf("encrypt secret key: %v", err)
	}

	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	rows := sqlmock.NewRows([]string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"}).
		AddRow("b1", "b1", "s3", "us-east-1", encAK, encSK, "https://s3.example")
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE credential_id = $1`)).
		WithArgs("b1").
		WillReturnRows(rows)

	got, err := pg.GetS3Credential(context.Background(), "b1")
	if err != nil {
		t.Fatalf("GetS3Credential returned error: %v", err)
	}
	if got.AccessKey != "ak" || got.SecretKey != "sk" {
		t.Fatalf("expected decrypted keys, got %+v", got)
	}
}

func TestGetS3CredentialNotFound(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE credential_id = $1`)).
		WithArgs("missing").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE bucket = $1`)).
		WithArgs("missing").
		WillReturnRows(sqlmock.NewRows([]string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"}))

	_, err := pg.GetS3Credential(context.Background(), "missing")
	if err == nil || err.Error() != "credential not found" {
		t.Fatalf("expected credential not found error, got %v", err)
	}
}

func TestSaveS3Credential(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()
	derivedID := buckets.DeriveCredentialID("b1", "", "us-east-1", "https://s3.example", "ak")

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id
		FROM s3_credential
		WHERE bucket = $1 AND credential_id <> $2
		LIMIT 1
	`)).
		WithArgs("b1", derivedID).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (credential_id) DO UPDATE SET
			bucket = EXCLUDED.bucket,
			provider = EXCLUDED.provider,
			region = EXCLUDED.region,
			access_key = EXCLUDED.access_key,
			secret_key = EXCLUDED.secret_key,
			endpoint = EXCLUDED.endpoint`)).
		WithArgs(derivedID, "b1", "s3", "us-east-1", sqlmock.AnyArg(), sqlmock.AnyArg(), "https://s3.example").
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := pg.SaveS3Credential(context.Background(), &buckets.Credential{
		Bucket:    "b1",
		Provider:  "",
		Region:    "us-east-1",
		AccessKey: "ak",
		SecretKey: "sk",
		Endpoint:  "https://s3.example",
	})
	if err != nil {
		t.Fatalf("SaveS3Credential returned error: %v", err)
	}
}

func TestSaveS3CredentialRejectsDuplicatePhysicalBucket(t *testing.T) {
	t.Setenv(credentialcipher.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id
		FROM s3_credential
		WHERE bucket = $1 AND credential_id <> $2
		LIMIT 1
	`)).
		WithArgs("shared-bucket", "org-b/default").
		WillReturnRows(sqlmock.NewRows([]string{"credential_id"}).AddRow("org-a/default"))

	err := pg.SaveS3Credential(context.Background(), &buckets.Credential{
		CredentialID: "org-b/default",
		Bucket:       "shared-bucket",
		Provider:     "s3",
		Region:       "us-east-1",
		AccessKey:    "ak",
		SecretKey:    "sk",
	})
	if err == nil || err.Error() != `physical bucket "shared-bucket" is already configured under credential "org-a/default"; reuse that credential and add a bucket scope instead` {
		t.Fatalf("expected duplicate physical bucket error, got %v", err)
	}
}

func TestDeleteS3Credential(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT credential_id FROM s3_credential WHERE credential_id = $1")).
			WithArgs("b1").
			WillReturnRows(sqlmock.NewRows([]string{"credential_id"}).AddRow("b1"))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM bucket_scope WHERE credential_id = $1")).
			WithArgs("b1").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM s3_credential WHERE credential_id = $1")).
			WithArgs("b1").
			WillReturnResult(sqlmock.NewResult(0, 1))

		if err := pg.DeleteS3Credential(context.Background(), "b1"); err != nil {
			t.Fatalf("DeleteS3Credential returned error: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery(regexp.QuoteMeta("SELECT credential_id FROM s3_credential WHERE credential_id = $1")).
			WithArgs("missing").
			WillReturnRows(sqlmock.NewRows([]string{"credential_id"}))
		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint
		FROM s3_credential WHERE bucket = $1`)).
			WithArgs("missing").
			WillReturnRows(sqlmock.NewRows([]string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"}))

		err := pg.DeleteS3Credential(context.Background(), "missing")
		if err == nil || err.Error() != "credential not found" {
			t.Fatalf("expected credential not found, got %v", err)
		}
	})
}

func TestListS3Credentials(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	rows := sqlmock.NewRows([]string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"}).
		AddRow("b1", "b1", "s3", "us-east-1", "ak1", "sk1", "").
		AddRow("b2", "b2", "gcs", "us-central1", "ak2", "sk2", "https://example")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT credential_id, bucket, provider, region, access_key, secret_key, endpoint FROM s3_credential")).
		WillReturnRows(rows)

	got, err := pg.ListS3Credentials(context.Background())
	if err != nil {
		t.Fatalf("ListS3Credentials returned error: %v", err)
	}
	if len(got) != 2 || got[1].Bucket != "b2" {
		t.Fatalf("unexpected credentials: %#v", got)
	}
}

func TestCreateBucketScope(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		pg, _, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		if err := pg.CreateBucketScope(context.Background(), nil); err == nil {
			t.Fatal("expected nil scope validation error")
		}
		if err := pg.CreateBucketScope(context.Background(), &buckets.Scope{}); err == nil {
			t.Fatal("expected required field validation error")
		}
	})

	t.Run("idempotent existing scope", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		rows := sqlmock.NewRows([]string{"organization", "project_id", "credential_id", "bucket", "path_prefix"}).
			AddRow("org", "proj", "bucket-a", "bucket-a", "prefix")
		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
		WHERE organization = $1 AND project_id = $2
	`)).
			WithArgs("org", "proj").
			WillReturnRows(rows)

		err := pg.CreateBucketScope(context.Background(), &buckets.Scope{
			Organization: "org",
			ProjectID:    "proj",
			Bucket:       "bucket-a",
			PathPrefix:   "/prefix/",
		})
		if err != nil {
			t.Fatalf("expected idempotent success, got %v", err)
		}
	})

	t.Run("updates existing scope", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		rows := sqlmock.NewRows([]string{"organization", "project_id", "credential_id", "bucket", "path_prefix"}).
			AddRow("org", "proj", "bucket-a", "bucket-a", "prefix-a")
		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
		WHERE organization = $1 AND project_id = $2
	`)).
			WithArgs("org", "proj").
			WillReturnRows(rows)

		mock.ExpectExec(regexp.QuoteMeta(`
			UPDATE bucket_scope
			SET credential_id = $1, bucket = $2, path_prefix = $3
			WHERE organization = $4 AND project_id = $5
		`)).
			WithArgs("bucket-b", "bucket-b", "prefix-b", "org", "proj").
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := pg.CreateBucketScope(context.Background(), &buckets.Scope{
			Organization: "org",
			ProjectID:    "proj",
			Bucket:       "bucket-b",
			PathPrefix:   "prefix-b",
		})
		if err != nil {
			t.Fatalf("expected update success, got %v", err)
		}
	})

	t.Run("create new", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
		WHERE organization = $1 AND project_id = $2
	`)).
			WithArgs("org", "proj").
			WillReturnError(sql.ErrNoRows)

		mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO bucket_scope (organization, project_id, credential_id, bucket, path_prefix)
		VALUES ($1, $2, $3, $4, $5)
	`)).
			WithArgs("org", "proj", "bucket-a", "bucket-a", "nested/path").
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := pg.CreateBucketScope(context.Background(), &buckets.Scope{
			Organization: " org ",
			ProjectID:    " proj ",
			Bucket:       " bucket-a ",
			PathPrefix:   "/nested/path/",
		})
		if err != nil {
			t.Fatalf("expected create success, got %v", err)
		}
	})
}

func TestGetAndListBucketScopes(t *testing.T) {
	t.Run("get not found", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
		WHERE organization = $1 AND project_id = $2
	`)).
			WithArgs("org", "proj").
			WillReturnError(sql.ErrNoRows)

		_, err := pg.GetBucketScope(context.Background(), "org", "proj")
		if !errors.Is(err, faults.ErrNotFound) {
			t.Fatalf("expected not found error, got %v", err)
		}
	})

	t.Run("list", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		rows := sqlmock.NewRows([]string{"organization", "project_id", "credential_id", "bucket", "path_prefix"}).
			AddRow("org1", "proj1", "b1", "b1", "").
			AddRow("org2", "proj2", "b2", "b2", "x")
		mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT organization, project_id, credential_id, bucket, COALESCE(path_prefix, '')
		FROM bucket_scope
	`)).WillReturnRows(rows)

		got, err := pg.ListBucketScopes(context.Background())
		if err != nil {
			t.Fatalf("ListBucketScopes returned error: %v", err)
		}
		if len(got) != 2 || got[1].Bucket != "b2" {
			t.Fatalf("unexpected scopes: %#v", got)
		}
	})
}
