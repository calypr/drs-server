package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
)

func TestGetS3CredentialMissingHasTypedIdentity(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSqliteDB: %v", err)
	}
	defer db.DB().Close()

	_, err = db.GetS3Credential(context.Background(), "missing")
	if !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		t.Fatalf("GetS3Credential error=%v, want storage credential missing", err)
	}
}

func TestGetS3CredentialDatabaseFailureIsNotMissing(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSqliteDB: %v", err)
	}
	db.DB().Close()

	_, err = db.GetS3Credential(context.Background(), "missing")
	if err == nil || errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		t.Fatalf("GetS3Credential error=%v, want non-missing database failure", err)
	}
}

func TestDeleteS3CredentialMissingHasTypedIdentity(t *testing.T) {
	db, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("NewSqliteDB: %v", err)
	}
	defer db.DB().Close()

	err = db.DeleteS3Credential(context.Background(), "missing")
	if !errors.Is(err, errorapi.ErrStorageCredentialMissing) {
		t.Fatalf("DeleteS3Credential error=%v, want storage credential missing", err)
	}
}
