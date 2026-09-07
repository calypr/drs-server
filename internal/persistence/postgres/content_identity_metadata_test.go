package postgres

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/objects"
)

func TestPostgresMergeContentRowTxUsesMergedMetadataAndAlias(t *testing.T) {
	_, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()
	oldTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := oldTime.Add(time.Hour)

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`
		INSERT INTO drs_object_name_alias (object_id, name_alias) VALUES ($1, $2)
		ON CONFLICT (object_id, name_alias) DO NOTHING`)).
		WithArgs("object-1", "old.txt").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(`
		UPDATE drs_object SET size = $1, updated_time = $2, name = $3,
		version = $4, description = $5 WHERE id = $6`)).
		WithArgs(int64(9), newTime, "new.txt", "2", "new", "object-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	tx, err := rawDB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	name, version, description := "dir/new.txt", "2", "new"
	if err := postgresMergeContentRowTx(context.Background(), tx, postgresContentRow{
		id: "object-1", name: "old.txt", updated: oldTime,
	}, &objects.Record{
		Name: &name, Version: &version, Description: &description, Size: 9, UpdatedTime: &newTime,
	}, []string{"/organization/org/project/p"}, []string{"/organization/org/project/p"}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
