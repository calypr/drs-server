package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/internal/usage"
)

func TestRecordTransferAttributionEvents_PersistsGrantAndCommits(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	when := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	start := int64(10)
	event := usage.Event{
		EventID:           "event-1",
		EventType:         usage.TransferEventAccessIssued,
		Direction:         "upload",
		EventTime:         when,
		RequestID:         "request-1",
		ObjectID:          "object-1",
		SHA256:            "sha-1",
		ObjectSize:        42,
		Organization:      "org",
		Project:           "project",
		AccessID:          "access-1",
		Provider:          "s3",
		Bucket:            "bucket",
		StorageURL:        "s3://bucket/object-1",
		RangeStart:        &start,
		BytesRequested:    20,
		BytesCompleted:    20,
		ActorEmail:        "actor@example.com",
		ActorSubject:      "subject-1",
		AuthMode:          "session",
		ClientName:        "client",
		ClientVersion:     "1.0",
		TransferSessionID: "session-1",
	}
	grantID := usage.GrantID(event)

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO transfer_attribution_event").ExpectExec().
		WithArgs(
			event.EventID, grantID, event.EventType, event.Direction, when, event.RequestID, event.ObjectID, event.SHA256, event.ObjectSize,
			event.Organization, event.Project, event.AccessID, event.Provider, event.Bucket, event.StorageURL,
			start, nil, event.BytesRequested, event.BytesCompleted,
			event.ActorEmail, event.ActorSubject, event.AuthMode, event.ClientName, event.ClientVersion, event.TransferSessionID,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO access_grant").
		WithArgs(grantID, when, when, event.ObjectID, event.SHA256, event.ObjectSize,
			event.Organization, event.Project, event.AccessID, event.Provider, event.Bucket, event.StorageURL,
			event.ActorEmail, event.ActorSubject, event.AuthMode).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := pg.RecordTransferAttributionEvents(context.Background(), []usage.Event{
		{},
		{EventID: "ignored", EventType: "transfer_started"},
		event,
	}); err != nil {
		t.Fatalf("RecordTransferAttributionEvents returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestRecordProviderTransferEvents_NormalizesAndRecordsUnmatchedEvent(t *testing.T) {
	pg, mock, rawDB := newMockPostgresDB(t)
	defer rawDB.Close()

	when := time.Date(2026, time.February, 3, 4, 5, 6, 0, time.UTC)
	event := usage.ProviderEvent{
		ProviderEventID:    "provider-event-1",
		Direction:          "GET",
		EventTime:          when,
		RequestID:          "request-1",
		ProviderRequestID:  "provider-request-1",
		ObjectID:           "object-1",
		SHA256:             "sha-1",
		ObjectSize:         42,
		Organization:       "org",
		Project:            "project",
		AccessID:           "access-1",
		Provider:           " s3 ",
		Bucket:             " bucket ",
		ObjectKey:          " /object-1 ",
		StorageURL:         " s3://bucket/object-1 ",
		BytesTransferred:   42,
		HTTPMethod:         "GET",
		HTTPStatus:         206,
		RequesterPrincipal: "principal",
		SourceIP:           "127.0.0.1",
		UserAgent:          "client",
		RawEventRef:        "raw-1",
		ActorEmail:         "actor@example.com",
		ActorSubject:       "subject-1",
		AuthMode:           "session",
	}

	mock.ExpectBegin()
	prepared := mock.ExpectPrepare("INSERT INTO provider_transfer_event")
	mock.ExpectQuery("SELECT access_grant_id, first_issued_at").
		WithArgs("s3", "bucket", when.Add(15*time.Minute), when.Add(-24*time.Hour), "s3://bucket/object-1").
		WillReturnRows(sqlmock.NewRows([]string{
			"access_grant_id", "first_issued_at", "last_issued_at", "issue_count", "object_id", "sha256", "object_size",
			"organization", "project", "access_id", "provider", "bucket", "storage_url", "actor_email", "actor_subject", "auth_mode",
		}))
	prepared.ExpectExec().
		WithArgs(
			event.ProviderEventID, "", usage.ProviderTransferDirectionDownload, when, event.RequestID, event.ProviderRequestID,
			event.ObjectID, event.SHA256, event.ObjectSize, event.Organization, event.Project, event.AccessID, "s3", "bucket",
			"object-1", "s3://bucket/object-1", nil, nil, event.BytesTransferred, event.HTTPMethod, event.HTTPStatus,
			event.RequesterPrincipal, event.SourceIP, event.UserAgent, event.RawEventRef, event.ActorEmail, event.ActorSubject, event.AuthMode,
			usage.ProviderTransferUnmatched,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := pg.RecordProviderTransferEvents(context.Background(), []usage.ProviderEvent{event}); err != nil {
		t.Fatalf("RecordProviderTransferEvents returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
