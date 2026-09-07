package postgres

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/faults"
)

func TestDeleteObjectAlias_PreservesTransactionAndNotFoundIdentity(t *testing.T) {
	t.Run("success commits", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object_alias WHERE alias_id = $1")).
			WithArgs("alias-1").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		if err := pg.DeleteObjectAlias(context.Background(), "alias-1"); err != nil {
			t.Fatalf("DeleteObjectAlias returned error: %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})

	t.Run("missing alias rolls back with not found", func(t *testing.T) {
		pg, mock, rawDB := newMockPostgresDB(t)
		defer rawDB.Close()

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM drs_object_alias WHERE alias_id = $1")).
			WithArgs("missing").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectRollback()

		err := pg.DeleteObjectAlias(context.Background(), "missing")
		if !errors.Is(err, faults.ErrNotFound) {
			t.Fatalf("expected not found error, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet expectations: %v", err)
		}
	})
}

func TestCreateObjectAlias_ValidatesCanonicalAndAliasClaimsInTransaction(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('syfon-content-write', 0))")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
		WithArgs("object-1").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("object-1"))
	mock.ExpectQuery("SELECT DISTINCT replace\\(lower\\(trim\\(checksum\\)\\)").
		WithArgs("object-1").WillReturnRows(sqlmock.NewRows([]string{"checksum"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT resource FROM drs_object_controlled_access WHERE object_id = $1")).
		WithArgs("object-1").WillReturnRows(sqlmock.NewRows([]string{"resource"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id FROM drs_object WHERE id = $1")).
		WithArgs("alias-1").WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT object_id FROM drs_object_alias WHERE alias_id = $1")).
		WithArgs("alias-1").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO drs_object_alias").
		WithArgs("alias-1", "object-1").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := pg.CreateObjectAlias(context.Background(), " alias-1 ", " object-1 "); err != nil {
		t.Fatalf("CreateObjectAlias returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
