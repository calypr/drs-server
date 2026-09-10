package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/calypr/syfon/apigen/metricsapi"
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
	direction := metricsapi.ProviderTransferDirection("GET")
	requestID := "request-1"
	providerRequestID := "provider-request-1"
	objectID := "object-1"
	sha256 := "sha-1"
	objectSize := int64(42)
	organization := "org"
	project := "project"
	accessID := "access-1"
	objectKey := " /object-1 "
	storageURL := " s3://bucket/object-1 "
	httpMethod := "GET"
	httpStatus := 206
	requesterPrincipal := "principal"
	sourceIP := "127.0.0.1"
	userAgent := "client"
	rawEventRef := "raw-1"
	actorEmail := "actor@example.com"
	actorSubject := "subject-1"
	authMode := "session"
	event := metricsapi.ProviderTransferEvent{
		ProviderEventId:    "provider-event-1",
		Direction:          direction,
		EventTime:          &when,
		RequestId:          &requestID,
		ProviderRequestId:  &providerRequestID,
		ObjectId:           &objectID,
		Sha256:             &sha256,
		ObjectSize:         &objectSize,
		Organization:       &organization,
		Project:            &project,
		AccessId:           &accessID,
		Provider:           " s3 ",
		Bucket:             " bucket ",
		ObjectKey:          &objectKey,
		StorageUrl:         &storageURL,
		BytesTransferred:   42,
		HttpMethod:         &httpMethod,
		HttpStatus:         &httpStatus,
		RequesterPrincipal: &requesterPrincipal,
		SourceIp:           &sourceIP,
		UserAgent:          &userAgent,
		RawEventRef:        &rawEventRef,
		ActorEmail:         &actorEmail,
		ActorSubject:       &actorSubject,
		AuthMode:           &authMode,
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
			event.ProviderEventId, "", usage.ProviderTransferDirectionDownload, when, requestID, providerRequestID,
			objectID, sha256, objectSize, organization, project, accessID, "s3", "bucket",
			"object-1", "s3://bucket/object-1", nil, nil, event.BytesTransferred, httpMethod, httpStatus,
			requesterPrincipal, sourceIP, userAgent, rawEventRef, actorEmail, actorSubject, authMode,
			usage.ProviderTransferUnmatched,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := pg.RecordProviderTransferEvents(context.Background(), []metricsapi.ProviderTransferEvent{event}); err != nil {
		t.Fatalf("RecordProviderTransferEvents returned error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
