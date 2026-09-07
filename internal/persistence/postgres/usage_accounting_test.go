package postgres

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
)

func TestGetFileUsageFlushesEventsAndReturnsLatestAccess(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	uploaded := time.Date(2026, time.March, 1, 10, 0, 0, 0, time.UTC)
	downloaded := uploaded.Add(time.Hour)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT e.object_id
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
	`)).
		WillReturnRows(sqlmock.NewRows([]string{"object_id"}).AddRow("object-1"))
	mock.ExpectExec("INSERT INTO object_usage").
		WithArgs(pq.Array([]string{"object-1"}), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM object_usage_event e").
		WithArgs(pq.Array([]string{"object-1"})).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE o.id = $1
	`)).
		WithArgs("object-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "size", "upload_count", "download_count", "last_upload_time", "last_download_time",
		}).AddRow("object-1", "object.dat", int64(42), int64(2), int64(3), uploaded, downloaded))

	usage, err := pg.GetFileUsage(context.Background(), "object-1")
	if err != nil {
		t.Fatalf("GetFileUsage returned error: %v", err)
	}
	if usage.ObjectID != "object-1" || usage.Name != "object.dat" || usage.Size != 42 {
		t.Fatalf("unexpected object usage identity: %+v", usage)
	}
	if usage.UploadCount != 2 || usage.DownloadCount != 3 {
		t.Fatalf("unexpected object usage counts: %+v", usage)
	}
	if usage.LastAccessTime == nil || !usage.LastAccessTime.Equal(downloaded) {
		t.Fatalf("expected latest access %v, got %+v", downloaded, usage.LastAccessTime)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestListFileUsageByObjectIDs_ReturnsCountsAndLatestTimes(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	uploaded := time.Date(2026, time.May, 1, 10, 0, 0, 0, time.UTC)
	downloaded := uploaded.Add(2 * time.Hour)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT DISTINCT e.object_id
		FROM object_usage_event e
		JOIN drs_object o ON o.id = e.object_id
	`)).
		WillReturnRows(sqlmock.NewRows([]string{"object_id"}))
	mock.ExpectCommit()
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT o.id, o.name, o.size,
			COALESCE(u.upload_count, 0),
			COALESCE(u.download_count, 0),
			u.last_upload_time,
			u.last_download_time
		FROM drs_object o
		LEFT JOIN object_usage u ON u.object_id = o.id
		WHERE o.id = ANY($1)
		ORDER BY o.id
	`)).
		WithArgs(pq.Array([]string{"object-1", "object-2"})).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "size", "upload_count", "download_count", "last_upload_time", "last_download_time",
		}).
			AddRow("object-1", "one.dat", int64(10), int64(1), int64(0), uploaded, nil).
			AddRow("object-2", "two.dat", int64(20), int64(0), int64(2), nil, downloaded))

	usage, err := pg.ListFileUsageByObjectIDs(context.Background(), []string{"object-1", "object-2"})
	if err != nil {
		t.Fatalf("ListFileUsageByObjectIDs returned error: %v", err)
	}
	if len(usage) != 2 || usage[0].UploadCount != 1 || usage[1].DownloadCount != 2 {
		t.Fatalf("unexpected usage rows: %+v", usage)
	}
	if usage[0].LastAccessTime == nil || !usage[0].LastAccessTime.Equal(uploaded) || usage[1].LastAccessTime == nil || !usage[1].LastAccessTime.Equal(downloaded) {
		t.Fatalf("unexpected latest access times: %+v", usage)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
