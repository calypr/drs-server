package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/usage"
)

func TestRecordTransferAttributionEvents_PersistsEmptyScopeArguments(t *testing.T) {
	db, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

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
	grantID := usage.GrantID(event)

	mock.ExpectBegin()
	prepared := mock.ExpectPrepare("INSERT INTO transfer_attribution_event")
	prepared.ExpectExec().WithArgs(
		event.EventID, grantID, event.EventType, event.Direction, event.EventTime,
		event.RequestID, event.ObjectID, event.SHA256, event.ObjectSize,
		"", "", event.AccessID, event.Provider, event.Bucket, event.StorageURL,
		nil, nil, event.BytesRequested, event.BytesCompleted,
		event.ActorEmail, event.ActorSubject, event.AuthMode, event.ClientName, event.ClientVersion, event.TransferSessionID,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO access_grant").WithArgs(
		grantID, event.EventTime, event.EventTime, event.ObjectID, event.SHA256, event.ObjectSize,
		"", "", event.AccessID, event.Provider, event.Bucket, event.StorageURL,
		event.ActorEmail, event.ActorSubject, event.AuthMode,
	).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := db.RecordTransferAttributionEvents(context.Background(), []usage.Event{event}); err != nil {
		t.Fatalf("RecordTransferAttributionEvents returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
