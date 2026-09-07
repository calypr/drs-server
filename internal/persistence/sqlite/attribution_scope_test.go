package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/usage"
)

func TestSqliteDB_RetainsEmptyScopeEventsOutsideProjectReports(t *testing.T) {
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	event := usage.Event{
		EventID:        "ambiguous-event",
		EventType:      usage.TransferEventAccessIssued,
		Direction:      usage.ProviderTransferDirectionDownload,
		EventTime:      time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		RequestID:      "request-1",
		ObjectID:       "object-1",
		AccessID:       "s3",
		Provider:       "s3",
		Bucket:         "bucket",
		StorageURL:     "s3://bucket/object-1",
		BytesRequested: 42,
	}
	ctx := context.Background()
	if err := db.RecordTransferAttributionEvents(ctx, []usage.Event{event}); err != nil {
		t.Fatalf("RecordTransferAttributionEvents failed: %v", err)
	}

	all, err := db.GetTransferAttributionSummary(ctx, usage.Filter{})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummary failed: %v", err)
	}
	if all.EventCount != 1 || all.BytesDownloaded != 42 {
		t.Fatalf("empty-scope event was not retained: %+v", all)
	}

	project, err := db.GetTransferAttributionSummaryByResources(ctx, usage.Filter{}, []string{"/organization/org/project/project"})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummaryByResources failed: %v", err)
	}
	if project.EventCount != 0 || project.BytesDownloaded != 0 {
		t.Fatalf("empty-scope event entered project report: %+v", project)
	}
}
