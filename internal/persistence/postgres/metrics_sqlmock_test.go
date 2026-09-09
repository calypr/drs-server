package postgres

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRecordFileUploadAndDownload(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectExec(regexp.MustCompile(`INSERT\s+INTO\s+object_usage_event\s*\(\s*object_id,\s*event_type,\s*event_time\s*\)\s+VALUES\s*\(\s*\$1,\s*'upload',\s*\$2\s*\)`).String()).
		WithArgs("obj-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := pg.RecordFileUpload(context.Background(), "obj-1"); err != nil {
		t.Fatalf("RecordFileUpload error: %v", err)
	}

	mock.ExpectExec(regexp.MustCompile(`INSERT\s+INTO\s+object_usage_event\s*\(\s*object_id,\s*event_type,\s*event_time\s*\)\s+VALUES\s*\(\s*\$1,\s*'download',\s*\$2\s*\)`).String()).
		WithArgs("obj-1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	if err := pg.RecordFileDownload(context.Background(), "obj-1"); err != nil {
		t.Fatalf("RecordFileDownload error: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
