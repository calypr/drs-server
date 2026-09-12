package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/usage"
)

func TestTransferAttributionCanonicalizesSHAWritesAndFilters(t *testing.T) {
	ctx := context.Background()
	database, err := NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	canonical := strings.Repeat("a", 64)
	event := usage.Event{
		EventID:      "canonical-sha-event",
		EventType:    usage.TransferEventAccessIssued,
		EventTime:    time.Now().UTC(),
		ObjectID:     "object-1",
		SHA256:       " SHA256:" + strings.ToUpper(canonical) + " ",
		Provider:     "s3",
		Bucket:       "bucket",
		StorageURL:   "s3://bucket/object-1",
		Organization: "org",
		Project:      "project",
	}
	if err := database.RecordTransferAttributionEvents(ctx, []usage.Event{event}); err != nil {
		t.Fatal(err)
	}

	var eventSHA, grantSHA string
	if err := database.DB().QueryRowContext(ctx, `SELECT sha256 FROM transfer_attribution_event WHERE event_id = ?`, event.EventID).Scan(&eventSHA); err != nil {
		t.Fatal(err)
	}
	if err := database.DB().QueryRowContext(ctx, `SELECT sha256 FROM access_grant`).Scan(&grantSHA); err != nil {
		t.Fatal(err)
	}
	if eventSHA != canonical || grantSHA != canonical {
		t.Fatalf("persisted SHA values = event:%q grant:%q, want %q", eventSHA, grantSHA, canonical)
	}

	for _, filter := range []string{canonical, " SHA256:" + strings.ToUpper(canonical) + " "} {
		summary, err := database.QueryTransferSummary(ctx, usage.Filter{SHA256: filter}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if sqliteTestInt64Val(summary.EventCount) != 1 {
			t.Fatalf("filter %q matched %v events, want 1", filter, summary.EventCount)
		}
	}

	breakdown, err := database.QueryTransferBreakdown(ctx, usage.Filter{SHA256: canonical}, "object", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown) != 1 || sqliteTestStringVal(breakdown[0].Sha256) != canonical {
		t.Fatalf("object breakdown = %+v, want canonical SHA", breakdown)
	}
}
