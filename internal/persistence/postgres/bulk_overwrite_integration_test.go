package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	postgresdb "github.com/calypr/syfon/internal/persistence/postgres"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/calypr/syfon/internal/usage"
	"github.com/google/uuid"
)

func openPostgresTestStore(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
	}
	t.Setenv(credentialcipher.CredentialLocalKeyFileEnv, filepath.Join(t.TempDir(), "credential.key"))
	db, err := postgresdb.NewPostgresDB(dsn, nil)
	if err != nil {
		t.Fatalf("open postgres test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPostgresSchemaContract(t *testing.T) {
	db := openPostgresTestStore(t)
	contracts := map[string]struct {
		columns    []string
		primaryKey []string
	}{
		"drs_object":                 {[]string{"id", "size", "created_time", "updated_time", "name", "version", "description"}, []string{"id"}},
		"drs_object_alias":           {[]string{"alias_id", "object_id"}, []string{"alias_id"}},
		"drs_object_access_method":   {[]string{"object_id", "url", "type", "access_method_json"}, nil},
		"s3_credential":              {[]string{"credential_id", "bucket", "provider", "region", "access_key", "secret_key", "endpoint"}, []string{"credential_id"}},
		"bucket_scope":               {[]string{"organization", "project_id", "credential_id", "bucket", "path_prefix"}, []string{"organization", "project_id"}},
		"object_usage":               {[]string{"object_id", "upload_count", "download_count", "last_upload_time", "last_download_time", "updated_time"}, []string{"object_id"}},
		"object_usage_event":         {[]string{"id", "object_id", "event_type", "event_time"}, []string{"id"}},
		"multipart_upload_session":   {[]string{"upload_id", "completion_id", "target_json", "authorization_json", "state", "completion_token", "parts_fingerprint", "completed_location", "created_time", "updated_time"}, []string{"upload_id"}},
		"transfer_attribution_event": {[]string{"event_id", "access_grant_id", "event_type", "direction", "event_time", "request_id", "object_id", "sha256", "object_size", "organization", "project", "access_id", "provider", "bucket", "storage_url", "range_start", "range_end", "bytes_requested", "bytes_completed", "actor_email", "actor_subject", "auth_mode", "client_name", "client_version", "transfer_session_id"}, []string{"event_id"}},
		"access_grant":               {[]string{"access_grant_id", "first_issued_at", "last_issued_at", "issue_count", "object_id", "sha256", "object_size", "organization", "project", "access_id", "provider", "bucket", "storage_url", "actor_email", "actor_subject", "auth_mode"}, []string{"access_grant_id"}},
	}

	for table, contract := range contracts {
		rows, err := db.DB().QueryContext(context.Background(), `
			SELECT column_name
			FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = $1
			ORDER BY ordinal_position`, table)
		if err != nil {
			t.Fatalf("read %s columns: %v", table, err)
		}
		var columns []string
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				rows.Close()
				t.Fatalf("scan %s column: %v", table, err)
			}
			columns = append(columns, column)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close %s columns: %v", table, err)
		}
		if !slices.Equal(columns, contract.columns) {
			t.Fatalf("%s columns = %v, want %v", table, columns, contract.columns)
		}

		keyRows, err := db.DB().QueryContext(context.Background(), `
			SELECT kcu.column_name
			FROM information_schema.table_constraints tc
			JOIN information_schema.key_column_usage kcu
			  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
			WHERE tc.table_schema = current_schema() AND tc.table_name = $1 AND tc.constraint_type = 'PRIMARY KEY'
			ORDER BY kcu.ordinal_position`, table)
		if err != nil {
			t.Fatalf("read %s primary key: %v", table, err)
		}
		var primaryKey []string
		for keyRows.Next() {
			var column string
			if err := keyRows.Scan(&column); err != nil {
				keyRows.Close()
				t.Fatalf("scan %s primary key: %v", table, err)
			}
			primaryKey = append(primaryKey, column)
		}
		if err := keyRows.Close(); err != nil {
			t.Fatalf("close %s primary key: %v", table, err)
		}
		if !slices.Equal(primaryKey, contract.primaryKey) {
			t.Fatalf("%s primary key = %v, want %v", table, primaryKey, contract.primaryKey)
		}
	}
}

func TestPostgresPersistenceRoundTrips(t *testing.T) {
	db := openPostgresTestStore(t)
	ctx := context.Background()
	suffix := uuid.NewString()
	organization := "ci-" + suffix
	project := "project"
	credentialID := "credential-" + suffix
	bucket := "bucket-" + suffix
	objectID := "object-" + suffix
	aliasID := "alias-" + suffix
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(suffix)))
	now := time.Now().UTC()

	credential := &buckets.Credential{CredentialID: credentialID, Bucket: bucket, Provider: "s3", Region: "us-east-1", AccessKey: "access", SecretKey: "secret", Endpoint: "https://s3.example"}
	if err := db.SaveS3Credential(ctx, credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteS3Credential(ctx, credentialID) })
	gotCredential, err := db.GetS3Credential(ctx, credentialID)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if *gotCredential != *credential {
		t.Fatalf("credential = %#v, want %#v", gotCredential, credential)
	}

	scope := &buckets.Scope{Organization: organization, ProjectID: project, CredentialID: credentialID, Bucket: bucket, PathPrefix: "prefix"}
	if err := db.CreateBucketScope(ctx, scope); err != nil {
		t.Fatalf("create bucket scope: %v", err)
	}
	gotScope, err := db.GetBucketScope(ctx, organization, project)
	if err != nil {
		t.Fatalf("get bucket scope: %v", err)
	}
	if *gotScope != *scope {
		t.Fatalf("bucket scope = %#v, want %#v", gotScope, scope)
	}

	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		t.Fatal(err)
	}
	name := "object.dat"
	if err := db.RegisterObjects(ctx, []drs.DrsObject{{
		Id: objectID, Name: &name, Size: 42, CreatedTime: now, UpdatedTime: &now,
		Checksums: []drs.Checksum{{Type: "sha256", Checksum: checksum}}, ControlledAccess: &[]string{resource},
	}}); err != nil {
		t.Fatalf("register object: %v", err)
	}
	t.Cleanup(func() { _ = db.DeleteObject(ctx, objectID) })
	if err := db.RecordFileUpload(ctx, objectID); err != nil {
		t.Fatalf("record upload: %v", err)
	}
	if err := db.RecordFileDownload(ctx, objectID); err != nil {
		t.Fatalf("record download: %v", err)
	}
	fileUsage, err := db.GetFileUsage(ctx, objectID)
	if err != nil {
		t.Fatalf("get file usage: %v", err)
	}
	if fileUsage.UploadCount == nil || *fileUsage.UploadCount != 1 || fileUsage.DownloadCount == nil || *fileUsage.DownloadCount != 1 {
		t.Fatalf("file usage = %#v, want one upload and one download", fileUsage)
	}

	if err := db.CreateObjectAlias(ctx, aliasID, objectID); err != nil {
		t.Fatalf("create alias: %v", err)
	}
	aliased, err := db.GetObject(ctx, aliasID)
	if err != nil || aliased.Id != objectID {
		t.Fatalf("alias lookup = %#v, %v", aliased, err)
	}

	event := usage.Event{
		EventID: "event-" + suffix, EventType: usage.TransferEventAccessIssued, Direction: usage.ProviderTransferDirectionDownload,
		EventTime: now, ObjectID: objectID, SHA256: checksum, ObjectSize: 42, Organization: organization, Project: project,
		AccessID: "s3", Provider: "s3", Bucket: bucket, StorageURL: "s3://" + bucket + "/" + objectID, BytesRequested: 42, BytesCompleted: 42,
	}
	if err := db.RecordTransferAttributionEvents(ctx, []usage.Event{event}); err != nil {
		t.Fatalf("record transfer attribution: %v", err)
	}
	summary, err := db.QueryTransferSummary(ctx, usage.Filter{Organization: organization, Project: project}, nil)
	if err != nil {
		t.Fatalf("query transfer summary: %v", err)
	}
	if summary.EventCount == nil || *summary.EventCount != 1 || summary.BytesDownloaded == nil || *summary.BytesDownloaded != 42 {
		t.Fatalf("transfer summary = %#v, want one 42-byte download", summary)
	}
}

func TestPostgresBulkOverwriteObjects(t *testing.T) {
	db := openPostgresTestStore(t)
	suffix := uuid.NewString()
	organization := "ci-overwrite-" + suffix
	targetID := "target-" + suffix
	sourceID := "source-" + suffix
	sha := fmt.Sprintf("%x", sha256.Sum256([]byte("overwrite-"+suffix)))

	resource, err := clientaccess.ResourcePath(organization, "project")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldName := "old"
	if err := db.RegisterObjects(context.Background(), []drs.DrsObject{{
		Id:               targetID,
		Name:             &oldName,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}}); err != nil {
		t.Fatalf("seed target record: %v", err)
	}

	newName := "new"
	service := objects.NewService(db)
	result, err := service.BulkOverwriteObjects(context.Background(), organization, "project", []drs.DrsObject{{
		Id:               sourceID,
		Name:             &newName,
		CreatedTime:      now,
		UpdatedTime:      &now,
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{resource},
	}})
	if err != nil {
		t.Fatalf("bulk overwrite: %v", err)
	}
	if result.Replaced != 1 || result.ChecksumMatched != 1 {
		t.Fatalf("unexpected overwrite result: %+v", result)
	}
	got, err := db.GetObject(context.Background(), targetID)
	if err != nil {
		t.Fatalf("read overwritten target: %v", err)
	}
	if got.Name == nil || *got.Name != newName {
		t.Fatalf("source metadata did not replace target: %+v", got.Name)
	}
	if _, err := db.GetObject(context.Background(), sourceID); err == nil {
		t.Fatal("checksum sibling should retain target DID")
	}
}
