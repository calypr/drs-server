package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	sqlitedb "github.com/calypr/syfon/internal/persistence/sqlite"
)

func TestScopedUsageQueriesBindSummaryCutoffBeforeResources(t *testing.T) {
	db, err := sqlitedb.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("open sqlite test store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	now := time.Now().UTC()
	objectID := "usage-query-bind-order"
	resource := "/organization/acme/project/research"
	if err := db.RegisterObjects(ctx, []drs.DrsObject{{
		Id:               objectID,
		CreatedTime:      now,
		UpdatedTime:      &now,
		ControlledAccess: &[]string{resource},
	}}); err != nil {
		t.Fatalf("RegisterObjects: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteObject(ctx, objectID) })
	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatalf("RecordFileUpload: %v", err)
	}

	cutoff := now.Add(-time.Hour)
	summary, err := db.GetFileUsageSummaryByResources(ctx, []string{resource}, false, &cutoff)
	if err != nil {
		t.Fatalf("GetFileUsageSummaryByResources: %v", err)
	}
	if summary.TotalFiles == nil || *summary.TotalFiles != 1 {
		t.Fatalf("summary total files = %#v, want 1", summary.TotalFiles)
	}
	if summary.TotalUploads == nil || *summary.TotalUploads != 1 {
		t.Fatalf("summary total uploads = %#v, want 1", summary.TotalUploads)
	}
	if summary.TotalDownloads == nil || *summary.TotalDownloads != 0 {
		t.Fatalf("summary total downloads = %#v, want 0", summary.TotalDownloads)
	}
	if summary.InactiveFileCount == nil || *summary.InactiveFileCount != 1 {
		t.Fatalf("summary inactive files = %#v, want 1", summary.InactiveFileCount)
	}

	page, err := db.ListFileUsagePageByResources(ctx, []string{resource}, false, 10, 0, &cutoff)
	if err != nil {
		t.Fatalf("ListFileUsagePageByResources: %v", err)
	}
	if len(page) != 1 || page[0].ObjectId == nil || *page[0].ObjectId != objectID {
		t.Fatalf("usage page = %#v, want object %q", page, objectID)
	}
}
