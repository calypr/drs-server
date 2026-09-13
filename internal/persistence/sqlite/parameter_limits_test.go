package sqlite

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/usage"
)

func TestLargeListQueriesPreserveBehavior(t *testing.T) {
	database, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	const itemCount = 905
	ids := make([]string, itemCount)
	checksums := make([]string, itemCount)
	resources := make([]string, itemCount)
	for i := range itemCount {
		ids[i] = fmt.Sprintf("obj-%04d", i)
		checksums[i] = fmt.Sprintf("%064x", i+1)
		resources[i], err = clientaccess.ResourcePath(fmt.Sprintf("org-%04d", i), "project")
		if err != nil {
			t.Fatal(err)
		}
	}
	targetResource := resources[len(resources)-1]
	deniedResource, err := clientaccess.ResourcePath("denied", "project")
	if err != nil {
		t.Fatal(err)
	}
	const sharedURL = "s3://bucket/shared"

	tx, err := database.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object (id, size, created_time, updated_time, name) VALUES (?, ?, ?, ?, ?)`, id, i, now, now, id); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert object %d: %v", i, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_checksum (object_id, type, checksum) VALUES (?, 'sha-256', ?)`, id, checksums[i]); err != nil {
			_ = tx.Rollback()
			t.Fatalf("insert checksum %d: %v", i, err)
		}
		if i == 0 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_controlled_access (object_id, resource) VALUES (?, ?)`, id, targetResource); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		} else if i > 1 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_controlled_access (object_id, resource) VALUES (?, ?)`, id, deniedResource); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
		if i < 2 {
			if _, err := tx.ExecContext(ctx, `INSERT INTO drs_object_access_method (object_id, url, type) VALUES (?, ?, 's3')`, id, sharedURL); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO object_usage_event (object_id, event_type, event_time) VALUES (?, 'download', ?)`, id, now); err != nil {
				_ = tx.Rollback()
				t.Fatal(err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO object_usage_event (object_id, event_type, event_time) VALUES (?, 'upload', ?)`, id, now); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	requestIDs := slices.Clone(ids)
	slices.Reverse(requestIDs)
	requestIDs = append(requestIDs, requestIDs[0])
	bulk, err := database.GetBulkObjects(ctx, requestIDs)
	if err != nil {
		t.Fatalf("GetBulkObjects: %v", err)
	}
	if len(bulk) != itemCount || bulk[0].Id != requestIDs[0] || bulk[len(bulk)-1].Id != requestIDs[itemCount-1] {
		t.Fatalf("GetBulkObjects returned %d objects from %s through %s", len(bulk), bulk[0].Id, bulk[len(bulk)-1].Id)
	}
	usageByID, err := database.ListFileUsageByObjectIDs(ctx, requestIDs)
	if err != nil {
		t.Fatalf("ListFileUsageByObjectIDs: %v", err)
	}
	if len(usageByID) != itemCount || usageByID[0].ObjectId == nil || *usageByID[0].ObjectId != ids[0] ||
		usageByID[len(usageByID)-1].ObjectId == nil || *usageByID[len(usageByID)-1].ObjectId != ids[itemCount-1] {
		t.Fatalf("ListFileUsageByObjectIDs returned %d rows without global ordering or deduplication", len(usageByID))
	}

	byChecksum, err := database.GetObjectsByChecksums(ctx, checksums)
	if err != nil {
		t.Fatalf("GetObjectsByChecksums: %v", err)
	}
	if len(byChecksum) != itemCount || len(byChecksum[checksums[itemCount-1]]) != 1 {
		t.Fatalf("GetObjectsByChecksums returned %d keys", len(byChecksum))
	}

	organizationResource, err := clientaccess.ResourcePath("unmatched", "")
	if err != nil {
		t.Fatal(err)
	}
	resources = append(resources, targetResource, organizationResource)
	wantVisibleIDs := []string{ids[0], ids[1]}
	visibleIDs, err := database.ListObjectIDsByResources(ctx, resources, true)
	if err != nil {
		t.Fatalf("ListObjectIDsByResources: %v", err)
	}
	if !slices.Equal(visibleIDs, wantVisibleIDs) {
		t.Fatalf("visible object IDs = %v, want %v", visibleIDs, wantVisibleIDs)
	}

	page, err := database.ListObjectIDsPageByURL(ctx, sharedURL, "", "", "", 1, 1, resources, true, true)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByURL: %v", err)
	}
	if !slices.Equal(page, []string{ids[1]}) {
		t.Fatalf("second object page = %v, want %s", page, ids[1])
	}

	visibility, err := database.ListBucketVisibilityRows(ctx, resources, true, true)
	if err != nil {
		t.Fatalf("ListBucketVisibilityRows: %v", err)
	}
	if len(visibility) != 2 {
		t.Fatalf("visibility rows = %d, want 2", len(visibility))
	}

	usagePage, err := database.ListFileUsagePageByResources(ctx, resources, true, 1, 1, nil)
	if err != nil {
		t.Fatalf("ListFileUsagePageByResources: %v", err)
	}
	if len(usagePage) != 1 || usagePage[0].ObjectId == nil || *usagePage[0].ObjectId != ids[1] {
		t.Fatalf("second usage page = %+v, want %s", usagePage, ids[1])
	}
	usageSummary, err := database.GetFileUsageSummaryByResources(ctx, resources, true, nil)
	if err != nil {
		t.Fatalf("GetFileUsageSummaryByResources: %v", err)
	}
	if usageSummary.TotalFiles == nil || *usageSummary.TotalFiles != 2 ||
		usageSummary.TotalUploads == nil || *usageSummary.TotalUploads != 2 ||
		usageSummary.TotalDownloads == nil || *usageSummary.TotalDownloads != 2 {
		t.Fatalf("usage summary = %+v", usageSummary)
	}

	if err := database.RecordTransferAttributionEvents(ctx, []usage.Event{{
		EventID:        "large-resource-event",
		EventType:      usage.TransferEventAccessIssued,
		EventTime:      now,
		Organization:   fmt.Sprintf("org-%04d", itemCount-1),
		Project:        "project",
		BytesRequested: 7,
	}}); err != nil {
		t.Fatalf("RecordTransferAttributionEvents: %v", err)
	}
	transferSummary, err := database.QueryTransferSummary(ctx, usage.Filter{}, resources)
	if err != nil {
		t.Fatalf("QueryTransferSummary: %v", err)
	}
	if transferSummary.EventCount == nil || *transferSummary.EventCount != 1 ||
		transferSummary.BytesRequested == nil || *transferSummary.BytesRequested != 7 {
		t.Fatalf("transfer summary = %+v", transferSummary)
	}
	breakdown, err := database.QueryTransferBreakdown(ctx, usage.Filter{}, "project", resources)
	if err != nil {
		t.Fatalf("QueryTransferBreakdown: %v", err)
	}
	if len(breakdown) != 1 || breakdown[0].EventCount == nil || *breakdown[0].EventCount != 1 {
		t.Fatalf("transfer breakdown = %+v", breakdown)
	}
}
