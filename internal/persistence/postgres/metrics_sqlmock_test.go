package postgres

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/usage"
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

func TestTransferAttributionWhereHelpers(t *testing.T) {
	from := time.Now().UTC().Add(-time.Hour)
	to := time.Now().UTC()
	where, args := transferAttributionWhere(usage.Filter{
		Organization: "org",
		Project:      "proj",
		Direction:    "download",
		From:         &from,
		To:           &to,
		Provider:     "s3",
		Bucket:       "bucket-a",
		SHA256:       "sha-1",
		User:         "user@example.com",
	})
	if !strings.Contains(where, "organization = $1") || !strings.Contains(where, "(actor_email =") {
		t.Fatalf("unexpected transferAttributionWhere clause: %q", where)
	}
	if len(args) < 9 {
		t.Fatalf("expected populated args, got %d (%+v)", len(args), args)
	}

	whereRes, argsRes := transferAttributionWhereByResources(usage.Filter{}, []string{"/organization/org/project/proj"})
	if !strings.Contains(whereRes, "organization") {
		t.Fatalf("expected resource clause in whereByResources, got %q", whereRes)
	}
	if len(argsRes) == 0 {
		t.Fatalf("expected args for whereByResources")
	}

	whereNone, _ := transferAttributionWhereByResources(usage.Filter{}, nil)
	if !strings.Contains(whereNone, "1 = 0") {
		t.Fatalf("expected 1=0 guard for empty resources, got %q", whereNone)
	}
}

func TestProviderTransferHelpers(t *testing.T) {
	if key, _ := transferAttributionGroupExpr("provider"); !strings.Contains(key, "provider") {
		t.Fatalf("unexpected attribution group expression for provider: %q", key)
	}

	if got := normalizeTransferDirection("upload"); got != usage.ProviderTransferDirectionUpload {
		t.Fatalf("expected upload direction to remain upload, got %q", got)
	}
	if got := normalizeTransferDirection("unknown"); got != usage.ProviderTransferDirectionDownload {
		t.Fatalf("expected unknown => download, got %q", got)
	}
}

func TestRecordTransferAttributionEvents_EmptyInput(t *testing.T) {
	pg, _, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()
	if err := pg.RecordTransferAttributionEvents(context.Background(), nil); err != nil {
		t.Fatalf("expected nil for empty transfer attribution events, got %v", err)
	}
}

func TestRecordProviderTransferEvents_EmptyInput(t *testing.T) {
	pg, _, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()
	if err := pg.RecordProviderTransferEvents(context.Background(), nil); err != nil {
		t.Fatalf("expected nil for empty provider transfer events, got %v", err)
	}
}
