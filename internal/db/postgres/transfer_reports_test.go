package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/models"
)

func TestGetTransferAttributionReports_MapAggregatesAndBreakdown(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	mock.ExpectQuery("SELECT COUNT\\(\\*\\),").
		WithArgs("org").
		WillReturnRows(sqlmock.NewRows([]string{
			"event_count", "access_issued_count", "download_event_count", "upload_event_count",
			"bytes_requested", "bytes_downloaded", "bytes_uploaded",
		}).AddRow(int64(5), int64(3), int64(4), int64(1), int64(100), int64(80), int64(20)))

	summary, err := pg.GetTransferAttributionSummary(context.Background(), models.TransferAttributionFilter{Organization: "org"})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummary returned error: %v", err)
	}
	if summary.EventCount != 5 || summary.AccessIssuedCount != 3 || summary.BytesDownloaded != 80 || summary.BytesUploaded != 20 {
		t.Fatalf("unexpected transfer summary: %+v", summary)
	}

	when := time.Date(2026, time.April, 2, 3, 4, 5, 0, time.UTC)
	mock.ExpectQuery("SELECT provider").
		WithArgs("org").
		WillReturnRows(sqlmock.NewRows([]string{
			"key", "organization", "project", "provider", "bucket", "sha256", "actor_email", "actor_subject",
			"event_count", "bytes_requested", "bytes_downloaded", "bytes_uploaded", "last_transfer_time",
		}).AddRow("s3:bucket", "", "", "s3", "bucket", "", "", "", int64(2), int64(40), int64(30), int64(10), when))

	breakdown, err := pg.GetTransferAttributionBreakdown(context.Background(), models.TransferAttributionFilter{Organization: "org"}, "provider")
	if err != nil {
		t.Fatalf("GetTransferAttributionBreakdown returned error: %v", err)
	}
	if len(breakdown) != 1 || breakdown[0].Key != "s3:bucket" || breakdown[0].EventCount != 2 || breakdown[0].LastTransferTime == nil {
		t.Fatalf("unexpected transfer breakdown: %+v", breakdown)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
