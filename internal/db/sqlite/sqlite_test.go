package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/crypto"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
)

func TestSqliteDB_CRUD(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	obj := &drs.DrsObject{
		Id:          "abc",
		Size:        123,
		CreatedTime: time.Now(),
		UpdatedTime: func() *time.Time { t := time.Now(); return &t }(),
		Version:     common.Ptr("1.0"),
		Name:        common.Ptr("testing"),
		AccessMethods: &[]drs.AccessMethod{
			{
				Type: drs.AccessMethodTypeS3,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/key"},
			},
		},
		Checksums: []drs.Checksum{
			{Type: "sha256", Checksum: "abc"},
		},
	}

	// Create
	if err := db.CreateObject(ctx, &models.InternalObject{DrsObject: *obj}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	// Get
	fetched, err := db.GetObject(ctx, "abc")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if fetched.Size != obj.Size {
		t.Errorf("expected size %d, got %d", obj.Size, fetched.Size)
	}
	if fetched.AccessMethods == nil || len(*fetched.AccessMethods) != 1 {
		t.Errorf("expected 1 access method, got %v", fetched.AccessMethods)
	}

	// Get by Checksum
	objs, err := db.GetObjectsByChecksum(ctx, "abc")
	if err != nil {
		t.Fatalf("GetObjectsByChecksum failed: %v", err)
	}
	if len(objs) != 1 || objs[0].Id != "abc" {
		t.Errorf("expected 1 object with id abc, got %v", objs)
	}

	// Delete
	if err := db.DeleteObject(ctx, "abc"); err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}

	// Verify Deleted
	_, err = db.GetObject(ctx, "abc")
	if err == nil {
		t.Fatal("expected error getting deleted object, got nil")
	}
}

func TestSqliteDB_InitializesControlledAccessTable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE drs_object_access_method (
		object_id TEXT,
		url TEXT,
		type TEXT
	)`); err != nil {
		t.Fatalf("create legacy access method table: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	db, err := NewSqliteDB(dbPath)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	defer db.db.Close()

	rows, err := db.db.Query(`PRAGMA table_info(drs_object_controlled_access)`)
	if err != nil {
		t.Fatalf("inspect controlled access table: %v", err)
	}
	defer rows.Close()

	columns := make([]string, 0)
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read table_info rows: %v", err)
	}
	if !slices.Contains(columns, "object_id") {
		t.Fatalf("expected object_id column, got %v", columns)
	}
	if !slices.Contains(columns, "resource") {
		t.Fatalf("expected resource column, got %v", columns)
	}
}

func TestSqliteDB_BackfillsAccessGrantsFromExistingAccessEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-access-events.db")
	raw, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE transfer_attribution_event (
		event_id TEXT PRIMARY KEY,
		event_type TEXT NOT NULL,
		event_time TIMESTAMP NOT NULL,
		request_id TEXT NOT NULL DEFAULT '',
		object_id TEXT NOT NULL DEFAULT '',
		sha256 TEXT NOT NULL DEFAULT '',
		object_size INTEGER NOT NULL DEFAULT 0,
		organization TEXT NOT NULL DEFAULT '',
		project TEXT NOT NULL DEFAULT '',
		access_id TEXT NOT NULL DEFAULT '',
		provider TEXT NOT NULL DEFAULT '',
		bucket TEXT NOT NULL DEFAULT '',
		storage_url TEXT NOT NULL DEFAULT '',
		range_start INTEGER NULL,
		range_end INTEGER NULL,
		bytes_requested INTEGER NOT NULL DEFAULT 0,
		bytes_completed INTEGER NOT NULL DEFAULT 0,
		actor_email TEXT NOT NULL DEFAULT '',
		actor_subject TEXT NOT NULL DEFAULT '',
		auth_mode TEXT NOT NULL DEFAULT '',
		client_name TEXT NOT NULL DEFAULT '',
		client_version TEXT NOT NULL DEFAULT '',
		transfer_session_id TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatalf("create legacy transfer table: %v", err)
	}
	now := time.Now().UTC()
	for _, eventID := range []string{"legacy-access-1", "legacy-access-2"} {
		if _, err := raw.Exec(`INSERT INTO transfer_attribution_event (
			event_id, event_type, event_time, object_id, sha256, object_size,
			organization, project, access_id, provider, bucket, storage_url, bytes_requested
		) VALUES (?, 'access_issued', ?, 'did-1', 'sha-1', 42, 'calypr', 'proj-a', 's3', 's3', 'bucket-a', 's3://bucket-a/root/sha-1', 42)`, eventID, now); err != nil {
			t.Fatalf("insert legacy access event: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy db: %v", err)
	}

	db, err := NewSqliteDB(dbPath)
	if err != nil {
		t.Fatalf("open migrated db: %v", err)
	}
	defer db.db.Close()

	var grants, issueCount, distinctEventGrants int
	if err := db.db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(issue_count), 0) FROM access_grant`).Scan(&grants, &issueCount); err != nil {
		t.Fatalf("inspect access_grant rows: %v", err)
	}
	if err := db.db.QueryRow(`SELECT COUNT(DISTINCT access_grant_id) FROM transfer_attribution_event`).Scan(&distinctEventGrants); err != nil {
		t.Fatalf("inspect migrated event grant ids: %v", err)
	}
	if grants != 1 || issueCount != 2 || distinctEventGrants != 1 {
		t.Fatalf("expected legacy events to backfill one canonical grant, got grants=%d issue_count=%d event_grants=%d", grants, issueCount, distinctEventGrants)
	}
}

func TestSqliteDB_GetObjectsByChecksum_WhenIDDiffers(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	checksum := "47454ac45ec9e9d88d76ba2dc8dff527ba6899a0f4189eb67dfcb2da0aa7d125"

	obj := &drs.DrsObject{
		Id:          "did-123",
		Size:        10,
		CreatedTime: time.Now(),
		UpdatedTime: func() *time.Time { t := time.Now(); return &t }(),
		Version:     common.Ptr("1.0"),
		Name:        common.Ptr("oid-object"),
		AccessMethods: &[]drs.AccessMethod{
			{
				Type: drs.AccessMethodTypeS3,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/cbds/end_to_end_test/" + checksum},
			},
		},
		Checksums: []drs.Checksum{
			{Type: "sha256", Checksum: checksum},
		},
	}
	if err := db.CreateObject(ctx, &models.InternalObject{DrsObject: *obj}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	objs, err := db.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		t.Fatalf("GetObjectsByChecksum failed: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("expected 1 object, got %d", len(objs))
	}
	if objs[0].Id != "did-123" {
		t.Fatalf("expected object id did-123, got %s", objs[0].Id)
	}
}

func TestSqliteDB_GetObjectPreservesStoredName(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	now := time.Now().UTC()
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO drs_object (id, size, created_time, updated_time, name, version, description)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"legacy-1", int64(42), now, now, "file.txt", "v1", "desc",
	); err != nil {
		t.Fatalf("insert object: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO drs_object_access_method (object_id, url, type)
		VALUES (?, ?, ?)`,
		"legacy-1", "s3://bucket/key", "s3",
	); err != nil {
		t.Fatalf("insert access method: %v", err)
	}

	obj, err := db.GetObject(ctx, "legacy-1")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got := common.StringVal(obj.Name); got != "file.txt" {
		t.Fatalf("expected stored name, got %q", got)
	}
}

func TestSqliteDB_NormalizeNameToBasenameOnInsert(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	// 1. Test CreateObject with Unix and Windows paths
	now := time.Now().UTC()
	objUnix := &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          "unix-1",
			Size:        100,
			CreatedTime: now,
			Name:        common.Ptr("/path/to/some/unix_file.txt"),
		},
	}
	if err := db.CreateObject(ctx, objUnix); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	gotUnix, err := db.GetObject(ctx, "unix-1")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got := common.StringVal(gotUnix.Name); got != "unix_file.txt" {
		t.Fatalf("expected name to be normalized to unix_file.txt, got %q", got)
	}

	objWin := &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          "win-1",
			Size:        200,
			CreatedTime: now,
			Name:        common.Ptr(`C:\Windows\System32\win_file.txt`),
		},
	}
	if err := db.CreateObject(ctx, objWin); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	gotWin, err := db.GetObject(ctx, "win-1")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got := common.StringVal(gotWin.Name); got != "win_file.txt" {
		t.Fatalf("expected name to be normalized to win_file.txt, got %q", got)
	}

	// 2. Test RegisterObjects with paths
	bulkObjs := []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:          "bulk-unix",
				Size:        300,
				CreatedTime: now,
				Name:        common.Ptr("/var/log/syslog.log"),
			},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "bulk-win",
				Size:        400,
				CreatedTime: now,
				Name:        common.Ptr(`D:\Data\config.json`),
			},
		},
	}

	if err := db.RegisterObjects(ctx, bulkObjs); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	gotBulkUnix, err := db.GetObject(ctx, "bulk-unix")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got := common.StringVal(gotBulkUnix.Name); got != "syslog.log" {
		t.Fatalf("expected name syslog.log, got %q", got)
	}

	gotBulkWin, err := db.GetObject(ctx, "bulk-win")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if got := common.StringVal(gotBulkWin.Name); got != "config.json" {
		t.Fatalf("expected name config.json, got %q", got)
	}
}

func TestSqliteDB_ObjectAliasLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	canonicalID := "11111111-1111-4111-8111-111111111111"
	aliasID := "22222222-2222-4222-8222-222222222222"
	checksum := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	now := time.Now().UTC()

	if err := db.CreateObject(ctx, &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          canonicalID,
			CreatedTime: now,
			UpdatedTime: &now,
			Checksums:   []drs.Checksum{{Type: "sha256", Checksum: checksum}},
			AccessMethods: &[]drs.AccessMethod{
				{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/path/object"}},
			},
		},
		Authorizations: map[string][]string{"a": {"b"}},
	}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	if err := db.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
		t.Fatalf("CreateObjectAlias failed: %v", err)
	}

	resolved, err := db.ResolveObjectAlias(ctx, aliasID)
	if err != nil {
		t.Fatalf("ResolveObjectAlias failed: %v", err)
	}
	if resolved != canonicalID {
		t.Fatalf("expected canonical id %s, got %s", canonicalID, resolved)
	}

	aliased, err := db.GetObject(ctx, aliasID)
	if err != nil {
		t.Fatalf("GetObject(alias) failed: %v", err)
	}
	if aliased.Id != canonicalID {
		t.Fatalf("expected canonical id %s, got %s", canonicalID, aliased.Id)
	}
	if len(aliased.Checksums) != 1 || aliased.Checksums[0].Checksum != checksum {
		t.Fatalf("expected checksum to resolve through alias, got %+v", aliased.Checksums)
	}

	byChecksum, err := db.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		t.Fatalf("GetObjectsByChecksum failed: %v", err)
	}
	if len(byChecksum) != 1 || byChecksum[0].Id != canonicalID {
		t.Fatalf("expected exactly one canonical record for checksum, got %+v", byChecksum)
	}

	if err := db.DeleteObjectAlias(ctx, aliasID); err != nil {
		t.Fatalf("DeleteObjectAlias(alias) failed: %v", err)
	}
	if _, err := db.ResolveObjectAlias(ctx, aliasID); err == nil {
		t.Fatal("expected alias to be deleted")
	}
	if _, err := db.GetObject(ctx, canonicalID); err != nil {
		t.Fatalf("expected canonical object to remain, got error: %v", err)
	}
}

func TestSqliteDB_ObjectReadsIgnoreAuthContext(t *testing.T) {
	buildCtx := map[string]func([]string) context.Context{
		"gen3": func(resources []string) context.Context {
			session := access.NewSession("gen3")
			session.AuthHeaderPresent = true
			session.SetAuthorizations(resources, nil, true)
			return access.WithSession(context.Background(), session)
		},
		"local-authz": func(resources []string) context.Context {
			session := access.NewSession("local")
			session.AuthzEnforced = true
			session.SetAuthorizations(resources, nil, true)
			return access.WithSession(context.Background(), session)
		},
	}

	for mode, makeCtx := range buildCtx {
		t.Run(mode, func(t *testing.T) {
			db, err := NewSqliteDB(":memory:")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			now := time.Now().UTC()
			checksum := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
			if err := db.CreateObject(context.Background(), &models.InternalObject{
				DrsObject: drs.DrsObject{
					Id:          "obj-authz",
					CreatedTime: now,
					UpdatedTime: &now,
					Checksums:   []drs.Checksum{{Type: "sha256", Checksum: checksum}},
					AccessMethods: &[]drs.AccessMethod{
						{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://bucket/path/object"}},
					},
				},
				Authorizations: map[string][]string{"org": {"project"}},
			}); err != nil {
				t.Fatalf("CreateObject failed: %v", err)
			}

			allowedCtx := makeCtx([]string{"/programs/org/projects/project"})
			if _, err := db.GetObject(allowedCtx, "obj-authz"); err != nil {
				t.Fatalf("expected GetObject to ignore auth context: %v", err)
			}
			byChecksum, err := db.GetObjectsByChecksum(allowedCtx, checksum)
			if err != nil {
				t.Fatalf("expected checksum lookup to ignore auth context: %v", err)
			}
			if len(byChecksum) != 1 || byChecksum[0].Id != "obj-authz" {
				t.Fatalf("expected checksum lookup to return object, got %+v", byChecksum)
			}

			deniedCtx := makeCtx([]string{"/programs/org/projects/other"})
			if _, err := db.GetObject(deniedCtx, "obj-authz"); err != nil {
				t.Fatalf("expected GetObject to ignore denied auth context, got %v", err)
			}
			byChecksum, err = db.GetObjectsByChecksum(deniedCtx, checksum)
			if err != nil {
				t.Fatalf("expected denied checksum lookup to still return object: %v", err)
			}
			if len(byChecksum) != 1 || byChecksum[0].Id != "obj-authz" {
				t.Fatalf("expected denied checksum lookup to return object, got %+v", byChecksum)
			}
		})
	}
}

func TestSqliteDB_DeleteObjectByAliasRemovesCanonicalObject(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	canonicalID := "11111111-1111-4111-8111-111111111111"
	aliasID := "22222222-2222-4222-8222-222222222222"
	now := time.Now().UTC()

	if err := db.CreateObject(ctx, &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          canonicalID,
			CreatedTime: now,
			UpdatedTime: &now,
			Name:        common.Ptr("object.txt"),
		},
		Authorizations: map[string][]string{"a": {"b"}},
	}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}
	if err := db.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
		t.Fatalf("CreateObjectAlias failed: %v", err)
	}

	if err := db.DeleteObject(ctx, aliasID); err != nil {
		t.Fatalf("DeleteObject(alias) failed: %v", err)
	}
	if _, err := db.GetObject(ctx, canonicalID); err == nil {
		t.Fatal("expected canonical object to be deleted")
	}
	if _, err := db.ResolveObjectAlias(ctx, aliasID); err == nil {
		t.Fatal("expected alias mapping to be deleted")
	}
	ids, err := db.ListObjectIDsByScope(ctx, "a", "b")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope failed: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("expected no listed ids after delete, got %v", ids)
	}
}

func TestSqliteDB_S3Credentials(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	cred := &models.S3Credential{
		Bucket:    "test-bucket",
		Region:    "us-east-1",
		AccessKey: "key",
		SecretKey: "secret",
		Endpoint:  "http://localhost:9000",
	}

	if err := db.SaveS3Credential(ctx, cred); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}

	fetched, err := db.GetS3Credential(ctx, "test-bucket")
	if err != nil {
		t.Fatalf("GetS3Credential failed: %v", err)
	}
	if fetched.AccessKey != cred.AccessKey {
		t.Errorf("expected key %s, got %s", cred.AccessKey, fetched.AccessKey)
	}

	list, err := db.ListS3Credentials(ctx)
	if err != nil {
		t.Fatalf("ListS3Credentials failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 cred, got %d", len(list))
	}

	if err := db.DeleteS3Credential(ctx, "test-bucket"); err != nil {
		t.Fatalf("DeleteS3Credential failed: %v", err)
	}
}

func TestSqliteDB_SaveS3CredentialRejectsDuplicatePhysicalBucket(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	first := models.S3Credential{
		CredentialID: "org-a/default",
		Bucket:       "shared-bucket",
		Region:       "us-east-1",
		AccessKey:    "key-a",
		SecretKey:    "secret-a",
	}
	if err := db.SaveS3Credential(ctx, &first); err != nil {
		t.Fatalf("SaveS3Credential(first) failed: %v", err)
	}

	second := models.S3Credential{
		CredentialID: "org-b/default",
		Bucket:       "shared-bucket",
		Region:       "us-east-1",
		AccessKey:    "key-b",
		SecretKey:    "secret-b",
	}
	err = db.SaveS3Credential(ctx, &second)
	if err == nil || !strings.Contains(err.Error(), `physical bucket "shared-bucket" is already configured under credential "org-a/default"`) {
		t.Fatalf("expected duplicate physical bucket error, got %v", err)
	}
}

func TestSqliteDB_GetS3CredentialRejectsAmbiguousLegacyPhysicalBucket(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS s3_credential_unique_bucket_insert`); err != nil {
		t.Fatalf("drop insert trigger: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `DROP TRIGGER IF EXISTS s3_credential_unique_bucket_update`); err != nil {
		t.Fatalf("drop update trigger: %v", err)
	}

	for _, cred := range []models.S3Credential{
		{CredentialID: "org-a/default", Bucket: "shared-bucket", Region: "us-east-1", AccessKey: "key-a", SecretKey: "secret-a"},
		{CredentialID: "org-b/default", Bucket: "shared-bucket", Region: "us-east-1", AccessKey: "key-b", SecretKey: "secret-b"},
	} {
		stored, err := crypto.PrepareS3CredentialForStorage(&cred)
		if err != nil {
			t.Fatalf("PrepareS3CredentialForStorage(%s) failed: %v", cred.CredentialID, err)
		}
		if _, err := db.db.ExecContext(ctx, `
			INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, stored.CredentialID, stored.Bucket, "s3", stored.Region, stored.AccessKey, stored.SecretKey, stored.Endpoint); err != nil {
			t.Fatalf("legacy insert(%s) failed: %v", cred.CredentialID, err)
		}
	}

	if _, err := db.GetS3Credential(ctx, "shared-bucket"); err == nil || !strings.Contains(err.Error(), "multiple credentials") {
		t.Fatalf("expected ambiguous physical bucket lookup error, got %v", err)
	}
}

func TestSqliteDB_DirectInsertRejectsDuplicatePhysicalBucket(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	first, err := crypto.PrepareS3CredentialForStorage(&models.S3Credential{
		CredentialID: "org-a/default",
		Bucket:       "shared-bucket",
		Provider:     "s3",
		Region:       "us-east-1",
		AccessKey:    "key-a",
		SecretKey:    "secret-a",
	})
	if err != nil {
		t.Fatalf("PrepareS3CredentialForStorage(first) failed: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, first.CredentialID, first.Bucket, "s3", first.Region, first.AccessKey, first.SecretKey, first.Endpoint); err != nil {
		t.Fatalf("raw first insert failed: %v", err)
	}

	second, err := crypto.PrepareS3CredentialForStorage(&models.S3Credential{
		CredentialID: "org-b/default",
		Bucket:       "shared-bucket",
		Provider:     "s3",
		Region:       "us-east-1",
		AccessKey:    "key-b",
		SecretKey:    "secret-b",
	})
	if err != nil {
		t.Fatalf("PrepareS3CredentialForStorage(second) failed: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `
		INSERT INTO s3_credential (credential_id, bucket, provider, region, access_key, secret_key, endpoint)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, second.CredentialID, second.Bucket, "s3", second.Region, second.AccessKey, second.SecretKey, second.Endpoint)
	if err == nil || !strings.Contains(err.Error(), "physical bucket is already configured under another credential") {
		t.Fatalf("expected trigger rejection, got %v", err)
	}
}

func TestSqliteDB_S3Credentials_EncryptedAtRest(t *testing.T) {
	t.Setenv(crypto.CredentialMasterKeyEnv, "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	cred := &models.S3Credential{
		Bucket:    "enc-bucket",
		Region:    "us-east-1",
		AccessKey: "plain-ak",
		SecretKey: "plain-sk",
		Endpoint:  "http://localhost:9000",
	}
	if err := db.SaveS3Credential(ctx, cred); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}

	var storedAK, storedSK string
	if err := db.db.QueryRowContext(ctx, "SELECT access_key, secret_key FROM s3_credential WHERE bucket = ?", "enc-bucket").Scan(&storedAK, &storedSK); err != nil {
		t.Fatalf("raw select failed: %v", err)
	}
	if storedAK == "plain-ak" || storedSK == "plain-sk" {
		t.Fatalf("expected encrypted values at rest, got access=%q secret=%q", storedAK, storedSK)
	}

	got, err := db.GetS3Credential(ctx, "enc-bucket")
	if err != nil {
		t.Fatalf("GetS3Credential failed: %v", err)
	}
	if got.AccessKey != "plain-ak" || got.SecretKey != "plain-sk" {
		t.Fatalf("expected decrypted values, got %+v", got)
	}
}

func TestSqliteDB_BulkOperations(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")

	objects := []models.InternalObject{
		{DrsObject: drs.DrsObject{Id: "bulk-1", Size: 10}, Authorizations: map[string][]string{"org": {"p1"}}},
		{DrsObject: drs.DrsObject{Id: "bulk-2", Size: 20}, Authorizations: map[string][]string{"org": {"p2"}}},
	}

	if err := db.RegisterObjects(ctx, objects); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	fetched, _ := db.GetBulkObjects(ctx, []string{"bulk-1", "bulk-2"})
	if len(fetched) != 2 {
		t.Errorf("expected 2 objects, got %d", len(fetched))
	}
	for _, obj := range fetched {
		if obj.ControlledAccess == nil || len(*obj.ControlledAccess) != 1 {
			t.Fatalf("expected controlled access on %s, got %+v", obj.Id, obj.ControlledAccess)
		}
	}

	if err := db.BulkDeleteObjects(ctx, []string{"bulk-1", "bulk-2"}); err != nil {
		t.Fatalf("BulkDeleteObjects failed: %v", err)
	}
}

func TestSqliteDB_RegisterObjectsChunksUsageFlushParameters(t *testing.T) {
	ctx := context.Background()
	database, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	objects := make([]models.InternalObject, sqliteMaxParams+1)
	for i := range objects {
		objects[i] = models.InternalObject{DrsObject: drs.DrsObject{Id: fmt.Sprintf("chunk-%d", i)}}
	}
	if err := database.RegisterObjects(ctx, objects); err != nil {
		t.Fatalf("RegisterObjects should chunk usage flush parameters: %v", err)
	}
	got, err := database.GetBulkObjects(ctx, []string{"chunk-0", fmt.Sprintf("chunk-%d", len(objects)-1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected first and last records, got %d", len(got))
	}
}

func TestSqliteDB_GetBulkObjects_SplitHydrationPreservesOrderAndDedupes(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")

	objects := []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:   "bulk-a",
				Size: 10,
				AccessMethods: &[]drs.AccessMethod{
					{
						Type: drs.AccessMethodTypeS3,
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://bucket/a"},
					},
					{
						Type: drs.AccessMethodTypeS3,
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://bucket/a"},
					},
				},
				Checksums: []drs.Checksum{
					{Type: "sha256", Checksum: "aaa"},
					{Type: "sha256", Checksum: "aaa"},
				},
			},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:   "bulk-b",
				Size: 20,
				AccessMethods: &[]drs.AccessMethod{
					{
						Type: drs.AccessMethodTypeGs,
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "gs://bucket/b"},
					},
				},
				Checksums: []drs.Checksum{
					{Type: "md5", Checksum: "bbb"},
				},
			},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
	}

	if err := db.RegisterObjects(ctx, objects); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	fetched, err := db.GetBulkObjects(ctx, []string{"bulk-b", "bulk-a", "bulk-b"})
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	if len(fetched) != 2 || fetched[0].Id != "bulk-b" || fetched[1].Id != "bulk-a" {
		t.Fatalf("expected input order with deduplicated ids, got %+v", fetched)
	}
	if fetched[1].AccessMethods == nil || len(*fetched[1].AccessMethods) != 1 {
		t.Fatalf("expected deduplicated access methods, got %+v", fetched[1].AccessMethods)
	}
	if len(fetched[1].Checksums) != 1 || fetched[1].Checksums[0].Checksum != "aaa" {
		t.Fatalf("expected deduplicated checksums, got %+v", fetched[1].Checksums)
	}
}

func TestSqliteDB_UpdateAccessMethods(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")

	obj := &drs.DrsObject{Id: "update-me"}
	if err := db.CreateObject(ctx, &models.InternalObject{DrsObject: *obj}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	newMethods := []drs.AccessMethod{
		{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: "s3://new/path"}},
	}

	if err := db.UpdateObjectAccessMethods(ctx, "update-me", newMethods); err != nil {
		t.Fatalf("UpdateObjectAccessMethods failed: %v", err)
	}

	fetched, err := db.GetObject(ctx, "update-me")
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	if fetched.AccessMethods == nil || len(*fetched.AccessMethods) != 1 || (*fetched.AccessMethods)[0].AccessUrl == nil || (*fetched.AccessMethods)[0].AccessUrl.Url != "s3://new/path" {
		t.Errorf("expected updated access method, got %v", fetched.AccessMethods)
	}
}

func TestSqliteDB_GetObjectsByChecksumsAndListByPrefix(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")

	now := time.Now()
	objects := []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:          "sha-x",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-x"}},
				AccessMethods: &[]drs.AccessMethod{
					testAccessMethod("s3://bucket/programs/a/projects/b/sha-x"),
				},
			},
			Authorizations: map[string][]string{"a": {"b"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "sha-y",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-y"}},
				AccessMethods: &[]drs.AccessMethod{
					testAccessMethod("s3://bucket/programs/a/projects/c/sha-y"),
				},
			},
			Authorizations: map[string][]string{"a": {"c"}},
		},
	}
	if err := db.RegisterObjects(ctx, objects); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	byChecksums, err := db.GetObjectsByChecksums(ctx, []string{"sha-x", "sha-y", "missing"})
	if err != nil {
		t.Fatalf("GetObjectsByChecksums failed: %v", err)
	}
	if len(byChecksums["sha-x"]) != 1 || byChecksums["sha-x"][0].Id != "sha-x" {
		t.Fatalf("unexpected checksum result for sha-x: %+v", byChecksums["sha-x"])
	}
	if len(byChecksums["missing"]) != 0 {
		t.Fatalf("expected empty results for missing checksum")
	}

	ids, err := db.ListObjectIDsByScope(ctx, "a", "b")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != "sha-x" {
		t.Fatalf("unexpected ids for prefix query: %+v", ids)
	}
}

func TestSqliteDB_ListScopedObjectIDsByChecksums(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")
	now := time.Now()
	objects := []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:          "proj-a-1",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-a"}},
			},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "proj-a-2",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-a"}},
			},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "other-project",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-a"}},
			},
			Authorizations: map[string][]string{"org": {"p2"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "other-org",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-a"}},
			},
			Authorizations: map[string][]string{"other": {"p1"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "proj-b",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-b"}},
			},
			Authorizations: map[string][]string{"org": {"p1"}},
		},
	}
	if err := db.RegisterObjects(ctx, objects); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	res, err := db.ListScopedObjectIDsByChecksums(ctx, "org", "p1", []string{"sha-a", "sha-a", "sha-b", "missing", ""})
	if err != nil {
		t.Fatalf("ListScopedObjectIDsByChecksums failed: %v", err)
	}
	if got := res["sha-a"]; len(got) != 2 || got[0] != "proj-a-1" || got[1] != "proj-a-2" {
		t.Fatalf("unexpected scoped ids for sha-a: %+v", got)
	}
	if got := res["sha-b"]; len(got) != 1 || got[0] != "proj-b" {
		t.Fatalf("unexpected scoped ids for sha-b: %+v", got)
	}
	if got := res["missing"]; len(got) != 0 {
		t.Fatalf("expected empty ids for missing checksum, got %+v", got)
	}
	emptyRes, err := db.ListScopedObjectIDsByChecksums(ctx, "org", "p1", nil)
	if err != nil {
		t.Fatalf("ListScopedObjectIDsByChecksums empty failed: %v", err)
	}
	if len(emptyRes) != 0 {
		t.Fatalf("expected empty map for empty checksum input, got %+v", emptyRes)
	}

	large := make([]string, sqliteMaxParams)
	for i := range large {
		large[i] = fmt.Sprintf("large-%d", i)
	}
	large[len(large)-1] = "sha-b"
	largeRes, err := db.ListScopedObjectIDsByChecksums(ctx, "org", "p1", large)
	if err != nil {
		t.Fatalf("ListScopedObjectIDsByChecksums large input failed: %v", err)
	}
	if got := largeRes["sha-b"]; len(got) != 1 || got[0] != "proj-b" {
		t.Fatalf("unexpected large-input result for sha-b: %+v", got)
	}
}

func TestSqliteDB_ListObjectIDsByScopeRootIncludesUnscoped(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")
	now := time.Now()

	if err := db.RegisterObjects(ctx, []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:          "scoped",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "scoped"}},
				AccessMethods: &[]drs.AccessMethod{
					testAccessMethod("s3://bucket/programs/a/projects/b/scoped"),
				},
			},
			Authorizations: map[string][]string{"a": {"b"}},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "unscoped",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "unscoped"}},
			},
		},
	}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	ids, err := db.ListObjectIDsByScope(ctx, "", "")
	if err != nil {
		t.Fatalf("ListObjectIDsByScope root failed: %v", err)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["scoped"] || !seen["unscoped"] {
		t.Fatalf("expected scoped and unscoped ids, got %+v", ids)
	}
}

func testAccessMethod(url string) drs.AccessMethod {
	return drs.AccessMethod{
		Type: drs.AccessMethodTypeS3,
		AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: url},
	}
}

func newLegacyDuplicateSHAFixture(t *testing.T) (*SqliteDB, string, string) {
	t.Helper()
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	now := time.Now()
	objectA := "3f5b5dac-f07d-5fdb-998d-532a95dd42d1"
	objectB := "f9be6500-ea29-5427-843f-eb44dcdc6fb5"
	if err := db.RegisterObjects(ctx, []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:            objectA,
				CreatedTime:   now,
				UpdatedTime:   &now,
				AccessMethods: &[]drs.AccessMethod{testAccessMethod("s3://bucket/original-a")},
			},
		},
		{
			DrsObject: drs.DrsObject{
				Id:            objectB,
				CreatedTime:   now,
				UpdatedTime:   &now,
				AccessMethods: &[]drs.AccessMethod{testAccessMethod("s3://bucket/original-b")},
			},
		},
	}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	const sha = "faec17cafc7af76bbdbe96a499545ff00ce2ef0ff4c65e05571dbbe0f17435ce"
	if _, err := db.db.ExecContext(ctx, `
		INSERT INTO drs_object_checksum (object_id, type, checksum)
		VALUES (?, ?, ?), (?, ?, ?)
	`, objectA, "sha256", sha, objectB, "sha256", sha); err != nil {
		t.Fatalf("insert duplicate SHA fixture: %v", err)
	}
	return db, objectA, objectB
}

func assertAccessMethodURL(t *testing.T, db *SqliteDB, objectID, wantURL string) {
	t.Helper()
	obj, err := db.GetObject(context.Background(), objectID)
	if err != nil {
		t.Fatalf("GetObject(%q) failed: %v", objectID, err)
	}
	if obj.AccessMethods == nil || len(*obj.AccessMethods) != 1 || (*obj.AccessMethods)[0].AccessUrl.Url != wantURL {
		t.Fatalf("expected access method %q for %q, got %+v", wantURL, objectID, obj.AccessMethods)
	}
}

func TestSqliteDB_UpdateObjectAccessMethodsTargetsPhysicalRowWithLegacyDuplicateSHA(t *testing.T) {
	db, objectA, objectB := newLegacyDuplicateSHAFixture(t)

	if err := db.UpdateObjectAccessMethods(context.Background(), objectA, []drs.AccessMethod{
		testAccessMethod("s3://bucket/repaired-a"),
	}); err != nil {
		t.Fatalf("UpdateObjectAccessMethods failed: %v", err)
	}

	assertAccessMethodURL(t, db, objectA, "s3://bucket/repaired-a")
	assertAccessMethodURL(t, db, objectB, "s3://bucket/original-b")
}

func TestSqliteDB_BulkUpdateAccessMethods(t *testing.T) {
	ctx := context.Background()
	db, _ := NewSqliteDB(":memory:")

	now := time.Now()
	if err := db.RegisterObjects(ctx, []models.InternalObject{
		{
			DrsObject: drs.DrsObject{
				Id:          "obj-a",
				CreatedTime: now,
				UpdatedTime: &now,
			},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "obj-b",
				CreatedTime: now,
				UpdatedTime: &now,
			},
		},
	}); err != nil {
		t.Fatalf("RegisterObjects failed: %v", err)
	}

	err := db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{
		"obj-a": {
			{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/a"}},
		},
		"obj-b": {
			{Type: drs.AccessMethodTypeS3, AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/b"}},
		},
	})
	if err != nil {
		t.Fatalf("BulkUpdateAccessMethods failed: %v", err)
	}

	a, _ := db.GetObject(ctx, "obj-a")
	if a.AccessMethods == nil || len(*a.AccessMethods) != 1 || (*a.AccessMethods)[0].AccessUrl.Url != "s3://bucket/a" {
		t.Fatalf("unexpected access methods for obj-a: %+v", a.AccessMethods)
	}
}

func TestSqliteDB_BulkUpdateAccessMethodsTargetsPhysicalRowWithLegacyDuplicateSHA(t *testing.T) {
	ctx := context.Background()
	db, objectA, objectB := newLegacyDuplicateSHAFixture(t)

	if err := db.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{
		objectA: {testAccessMethod("s3://bucket/repaired-a")},
	}); err != nil {
		t.Fatalf("BulkUpdateAccessMethods failed: %v", err)
	}

	assertAccessMethodURL(t, db, objectA, "s3://bucket/repaired-a")
	assertAccessMethodURL(t, db, objectB, "s3://bucket/original-b")
}

func TestSqliteDB_GetServiceInfo(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	info, err := db.GetServiceInfo(ctx)
	if err != nil {
		t.Fatalf("GetServiceInfo failed: %v", err)
	}
	if info == nil || info.Name == "" {
		t.Fatalf("expected non-empty service info, got %+v", info)
	}
}

func TestSqliteDB_PendingLFSMetaLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	candidate := drs.DrsObjectCandidate{
		Name: common.Ptr("candidate"),
		Size: 123,
		Checksums: []drs.Checksum{
			{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		},
	}

	if err := db.SavePendingLFSMeta(ctx, []models.PendingLFSMeta{
		{
			OID:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Candidate: candidate,
			CreatedAt: now,
			ExpiresAt: now.Add(5 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("SavePendingLFSMeta failed: %v", err)
	}

	entry, err := db.PopPendingLFSMeta(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("PopPendingLFSMeta failed: %v", err)
	}
	if common.StringVal(entry.Candidate.Name) != "candidate" {
		t.Fatalf("unexpected candidate payload: %+v", entry.Candidate)
	}

	if _, err := db.PopPendingLFSMeta(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatalf("expected not found after pop")
	}
}

func TestSqliteDB_PendingLFSMetaPrunesExpired(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	oid := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	candidate := drs.DrsObjectCandidate{
		Name: common.Ptr("expired"),
		Checksums: []drs.Checksum{
			{Type: "sha256", Checksum: oid},
		},
	}

	if err := db.SavePendingLFSMeta(ctx, []models.PendingLFSMeta{
		{
			OID:       oid,
			Candidate: candidate,
			CreatedAt: now.Add(-2 * time.Hour),
			ExpiresAt: now.Add(-1 * time.Hour),
		},
	}); err != nil {
		t.Fatalf("SavePendingLFSMeta failed: %v", err)
	}

	if _, err := db.PopPendingLFSMeta(ctx, oid); err == nil {
		t.Fatalf("expected not found for expired metadata")
	}
}

func TestSqliteDB_FileUsageMetrics(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	oid := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := db.CreateObject(ctx, &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          oid,
			Name:        common.Ptr("metrics-object"),
			Size:        42,
			CreatedTime: now,
			UpdatedTime: &now,
			Version:     common.Ptr("1"),
		},
	}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	if err := db.RecordFileUpload(ctx, oid); err != nil {
		t.Fatalf("RecordFileUpload failed: %v", err)
	}
	if err := db.RecordFileDownload(ctx, oid); err != nil {
		t.Fatalf("RecordFileDownload failed: %v", err)
	}
	if err := db.RecordFileDownload(ctx, oid); err != nil {
		t.Fatalf("RecordFileDownload failed: %v", err)
	}

	usage, err := db.GetFileUsage(ctx, oid)
	if err != nil {
		t.Fatalf("GetFileUsage failed: %v", err)
	}
	if usage.UploadCount != 1 || usage.DownloadCount != 2 {
		t.Fatalf("unexpected usage counters: %+v", usage)
	}
	if usage.LastAccessTime == nil {
		t.Fatalf("expected last access time to be set")
	}

	rows, err := db.ListFileUsage(ctx, 10, 0, nil)
	if err != nil {
		t.Fatalf("ListFileUsage failed: %v", err)
	}
	if len(rows) == 0 {
		t.Fatalf("expected at least one usage row")
	}

	summary, err := db.GetFileUsageSummary(ctx, nil)
	if err != nil {
		t.Fatalf("GetFileUsageSummary failed: %v", err)
	}
	if summary.TotalFiles == 0 || summary.TotalUploads == 0 || summary.TotalDownloads == 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestSqliteDB_FileUsageMetrics_MissingObjectQueuedAndFlushedOnCreate(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	oid := "missing-object"
	if err := db.RecordFileUpload(ctx, oid); err != nil {
		t.Fatalf("RecordFileUpload should queue for missing object, got: %v", err)
	}
	if err := db.RecordFileDownload(ctx, oid); err != nil {
		t.Fatalf("RecordFileDownload should queue for missing object, got: %v", err)
	}
	if _, err := db.GetFileUsage(ctx, oid); err == nil {
		t.Fatalf("expected not found for missing object usage")
	}

	now := time.Now().UTC()
	if err := db.CreateObject(ctx, &models.InternalObject{
		DrsObject: drs.DrsObject{
			Id:          oid,
			Name:        common.Ptr("later-created"),
			Size:        11,
			CreatedTime: now,
			UpdatedTime: &now,
			Version:     common.Ptr("1"),
		},
	}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}
	usage, err := db.GetFileUsage(ctx, oid)
	if err != nil {
		t.Fatalf("GetFileUsage failed after create: %v", err)
	}
	if usage.UploadCount != 1 || usage.DownloadCount != 1 {
		t.Fatalf("expected queued usage to flush on create, got: %+v", usage)
	}
}

func TestSqliteDB_ListObjectIDsPageByChecksum(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	for _, obj := range []models.InternalObject{
		{
			Authorizations: map[string][]string{"org": {"p1"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-a",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "same"}},
			},
		},
		{
			Authorizations: map[string][]string{"org": {"p1"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-b",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "same"}},
			},
		},
		{
			Authorizations: map[string][]string{"org": {"p2"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-c",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "md5", Checksum: "same"}},
			},
		},
	} {
		if err := db.CreateObject(ctx, &obj); err != nil {
			t.Fatalf("CreateObject failed: %v", err)
		}
	}

	ids, err := db.ListObjectIDsPageByChecksum(ctx, "same", "sha256", "org", "p1", "obj-a", 10, 0, nil, false, false)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByChecksum failed: %v", err)
	}
	if !slices.Equal(ids, []string{"obj-b"}) {
		t.Fatalf("unexpected checksum page: %v", ids)
	}
}

func TestSqliteDB_ListObjectIDsPageByURL(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	objectWithURL := func(id, rawURL string, authz map[string][]string) models.InternalObject {
		return models.InternalObject{
			Authorizations: authz,
			DrsObject: drs.DrsObject{
				Id:          id,
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: id + "-hash"}},
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: rawURL},
				}},
			},
		}
	}
	targetURL := "s3://bucket/path/image.offsets.json"
	for _, obj := range []models.InternalObject{
		objectWithURL("obj-a", targetURL, map[string][]string{"org": {"p1"}}),
		objectWithURL("obj-b", targetURL, map[string][]string{"org": {"p2"}}),
		objectWithURL("obj-c", targetURL, nil),
		objectWithURL("obj-d", "s3://bucket/path/other.offsets.json", map[string][]string{"org": {"p1"}}),
		objectWithURL("obj-e", targetURL, map[string][]string{"org": {"p1"}}),
	} {
		if err := db.CreateObject(ctx, &obj); err != nil {
			t.Fatalf("CreateObject failed: %v", err)
		}
	}

	ids, err := db.ListObjectIDsPageByURL(ctx, targetURL, "org", "p1", "", 10, 0, nil, false, false)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByURL scope query failed: %v", err)
	}
	if !slices.Equal(ids, []string{"obj-a", "obj-e"}) {
		t.Fatalf("unexpected scoped URL IDs: %v", ids)
	}

	ids, err = db.ListObjectIDsPageByURL(ctx, targetURL, "", "", "", 10, 0, []string{"/programs/org/projects/p2"}, false, true)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByURL resource query failed: %v", err)
	}
	if !slices.Equal(ids, []string{"obj-b"}) {
		t.Fatalf("unexpected resource-filtered URL IDs: %v", ids)
	}

	ids, err = db.ListObjectIDsPageByURL(ctx, targetURL, "", "", "", 10, 0, []string{"/programs/org/projects/p2"}, true, true)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByURL unscoped resource query failed: %v", err)
	}
	if !slices.Equal(ids, []string{"obj-b", "obj-c"}) {
		t.Fatalf("unexpected resource-filtered URL IDs with unscoped: %v", ids)
	}

	ids, err = db.ListObjectIDsPageByURL(ctx, targetURL, "", "", "obj-a", 1, 0, nil, false, false)
	if err != nil {
		t.Fatalf("ListObjectIDsPageByURL paged query failed: %v", err)
	}
	if !slices.Equal(ids, []string{"obj-b"}) {
		t.Fatalf("unexpected paged URL IDs: %v", ids)
	}
}

func TestSqliteDB_AuthorizedObjectLookupQueries(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	for _, obj := range []models.InternalObject{
		{
			Authorizations: map[string][]string{"org": {"p1"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-a",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "same"}},
			},
		},
		{
			Authorizations: map[string][]string{"org": {"p2"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-b",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "same"}},
			},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "obj-public",
				CreatedTime: now,
				UpdatedTime: &now,
				Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "same"}},
			},
		},
	} {
		if err := db.CreateObject(ctx, &obj); err != nil {
			t.Fatalf("CreateObject failed: %v", err)
		}
	}

	scopeIDs, err := db.ListObjectIDsByScopeAndResources(ctx, "org", "p1", []string{"/programs/org/projects/p1"}, true)
	if err != nil {
		t.Fatalf("ListObjectIDsByScopeAndResources failed: %v", err)
	}
	if !slices.Equal(scopeIDs, []string{"obj-a"}) {
		t.Fatalf("unexpected scoped ids: %v", scopeIDs)
	}

	byChecksum, err := db.ListObjectIDsByChecksumsAndResources(ctx, []string{"same"}, []string{"/programs/org/projects/p1"}, true, true)
	if err != nil {
		t.Fatalf("ListObjectIDsByChecksumsAndResources failed: %v", err)
	}
	if !slices.Equal(byChecksum["same"], []string{"obj-a", "obj-public"}) {
		t.Fatalf("unexpected checksum auth ids: %+v", byChecksum)
	}
}

func TestSqliteDB_ScopedFileUsageQueries(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	for _, obj := range []models.InternalObject{
		{
			Authorizations: map[string][]string{"org": {"p1"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-1",
				Name:        common.Ptr("one"),
				Size:        1,
				CreatedTime: now,
				UpdatedTime: &now,
			},
		},
		{
			Authorizations: map[string][]string{"org": {"p1"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-2",
				Name:        common.Ptr("two"),
				Size:        2,
				CreatedTime: now,
				UpdatedTime: &now,
			},
		},
		{
			Authorizations: map[string][]string{"org": {"p2"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-3",
				Name:        common.Ptr("three"),
				Size:        3,
				CreatedTime: now,
				UpdatedTime: &now,
			},
		},
	} {
		if err := db.CreateObject(ctx, &obj); err != nil {
			t.Fatalf("CreateObject failed: %v", err)
		}
	}
	if err := db.RecordFileUpload(ctx, "obj-2"); err != nil {
		t.Fatalf("RecordFileUpload failed: %v", err)
	}
	if err := db.RecordFileDownload(ctx, "obj-2"); err != nil {
		t.Fatalf("RecordFileDownload failed: %v", err)
	}

	rows, err := db.ListFileUsagePageByScope(ctx, "org", "p1", 1, 1, nil)
	if err != nil {
		t.Fatalf("ListFileUsagePageByScope failed: %v", err)
	}
	if len(rows) != 1 || rows[0].ObjectID != "obj-2" {
		t.Fatalf("unexpected scoped usage page: %+v", rows)
	}

	summary, err := db.GetFileUsageSummaryByScope(ctx, "org", "p1", nil)
	if err != nil {
		t.Fatalf("GetFileUsageSummaryByScope failed: %v", err)
	}
	if summary.TotalFiles != 2 || summary.TotalUploads != 1 || summary.TotalDownloads != 1 || summary.InactiveFileCount != 0 {
		t.Fatalf("unexpected scoped summary: %+v", summary)
	}

	recordSummary, err := db.GetProjectRecordSummaryByScope(ctx, "org", "p1")
	if err != nil {
		t.Fatalf("GetProjectRecordSummaryByScope failed: %v", err)
	}
	if recordSummary.RecordCount != 2 {
		t.Fatalf("unexpected scoped record count: %+v", recordSummary)
	}
	if recordSummary.RecordLatestUpdatedTime == nil || !recordSummary.RecordLatestUpdatedTime.Equal(now) {
		t.Fatalf("unexpected scoped record latest updated time: %+v", recordSummary.RecordLatestUpdatedTime)
	}
}

func TestSqliteDB_TransferAttributionByResources(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	events := []models.TransferAttributionEvent{
		{
			EventID:        "ev-a",
			AccessGrantID:  "grant-a",
			EventType:      models.TransferEventAccessIssued,
			Direction:      models.ProviderTransferDirectionDownload,
			EventTime:      now,
			Organization:   "org",
			Project:        "p1",
			Provider:       "s3",
			Bucket:         "bucket-a",
			BytesRequested: 11,
			BytesCompleted: 11,
			ActorEmail:     "user-a@example.com",
			StorageURL:     "s3://bucket-a/a",
		},
		{
			EventID:        "ev-b",
			AccessGrantID:  "grant-b",
			EventType:      models.TransferEventAccessIssued,
			Direction:      models.ProviderTransferDirectionDownload,
			EventTime:      now.Add(time.Minute),
			Organization:   "org",
			Project:        "p2",
			Provider:       "s3",
			Bucket:         "bucket-b",
			BytesRequested: 29,
			BytesCompleted: 29,
			ActorEmail:     "user-b@example.com",
			StorageURL:     "s3://bucket-b/b",
		},
	}
	if err := db.RecordTransferAttributionEvents(ctx, events); err != nil {
		t.Fatalf("RecordTransferAttributionEvents failed: %v", err)
	}

	summary, err := db.GetTransferAttributionSummaryByResources(ctx, models.TransferAttributionFilter{}, []string{"/programs/org/projects/p1"})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummaryByResources failed: %v", err)
	}
	if summary.EventCount != 1 || summary.BytesDownloaded != 11 {
		t.Fatalf("unexpected scoped transfer summary: %+v", summary)
	}

	breakdown, err := db.GetTransferAttributionBreakdownByResources(ctx, models.TransferAttributionFilter{}, "user", []string{"/programs/org/projects/p1"})
	if err != nil {
		t.Fatalf("GetTransferAttributionBreakdownByResources failed: %v", err)
	}
	if len(breakdown) != 1 || breakdown[0].ActorEmail != "user-a@example.com" || breakdown[0].BytesDownloaded != 11 {
		t.Fatalf("unexpected scoped transfer breakdown: %+v", breakdown)
	}
}

func TestSqliteDB_ListBucketVisibilityRows(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	for _, obj := range []models.InternalObject{
		{
			Authorizations: map[string][]string{"org": {"p1"}},
			DrsObject: drs.DrsObject{
				Id:          "obj-scoped",
				CreatedTime: now,
				UpdatedTime: &now,
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket-a/scoped"},
				}},
			},
		},
		{
			DrsObject: drs.DrsObject{
				Id:          "obj-public",
				CreatedTime: now,
				UpdatedTime: &now,
				AccessMethods: &[]drs.AccessMethod{{
					Type: drs.AccessMethodTypeS3,
					AccessUrl: &struct {
						Headers *[]string `json:"headers,omitempty"`
						Url     string    `json:"url"`
					}{Url: "s3://bucket-b/public"},
				}},
			},
		},
	} {
		if err := db.CreateObject(ctx, &obj); err != nil {
			t.Fatalf("CreateObject failed: %v", err)
		}
	}

	rows, err := db.ListBucketVisibilityRows(ctx, []string{"/programs/org/projects/p1"}, true, true)
	if err != nil {
		t.Fatalf("ListBucketVisibilityRows failed: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 visibility rows, got %d (%+v)", len(rows), rows)
	}
}

func TestSqliteDB_TransferAttributionMetrics(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	oid := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if err := db.CreateObject(ctx, &models.InternalObject{
		Authorizations: map[string][]string{"calypr": {"proj-a"}},
		DrsObject: drs.DrsObject{
			Id:          "did-1",
			Name:        common.Ptr("transfer-object"),
			Size:        42,
			CreatedTime: now,
			UpdatedTime: &now,
			Version:     common.Ptr("1"),
			Checksums: []drs.Checksum{
				{Type: "sha256", Checksum: oid},
			},
		},
	}); err != nil {
		t.Fatalf("CreateObject failed: %v", err)
	}

	rangeStart := int64(0)
	rangeEnd := int64(41)
	grants := []models.TransferAttributionEvent{
		{
			EventID:           "grant-download-1",
			AccessGrantID:     "grant-download-1",
			EventType:         models.TransferEventAccessIssued,
			Direction:         models.ProviderTransferDirectionDownload,
			EventTime:         now,
			RequestID:         "request-1",
			ObjectID:          "did-1",
			SHA256:            oid,
			ObjectSize:        42,
			Organization:      "calypr",
			Project:           "proj-a",
			AccessID:          "s3",
			Provider:          "s3",
			Bucket:            "bucket-a",
			StorageURL:        "s3://bucket-a/program/proj/" + oid,
			RangeStart:        &rangeStart,
			RangeEnd:          &rangeEnd,
			BytesRequested:    42,
			BytesCompleted:    42,
			ActorEmail:        "user@example.com",
			ActorSubject:      "user@example.com",
			AuthMode:          "gen3",
			ClientName:        "git-drs",
			ClientVersion:     "test",
			TransferSessionID: "session-1",
		},
		{
			EventID:           "grant-upload-1",
			AccessGrantID:     "grant-upload-1",
			EventType:         models.TransferEventAccessIssued,
			Direction:         models.ProviderTransferDirectionUpload,
			EventTime:         now.Add(time.Second),
			RequestID:         "request-2",
			ObjectID:          "did-1",
			SHA256:            oid,
			ObjectSize:        42,
			Organization:      "calypr",
			Project:           "proj-a",
			AccessID:          "s3",
			Provider:          "s3",
			Bucket:            "bucket-a",
			StorageURL:        "s3://bucket-a/program/proj/" + oid,
			BytesRequested:    42,
			BytesCompleted:    42,
			ActorSubject:      "local-user",
			AuthMode:          "local",
			ClientName:        "git-drs",
			ClientVersion:     "test",
			TransferSessionID: "session-2",
		},
	}
	if err := db.RecordTransferAttributionEvents(ctx, grants); err != nil {
		t.Fatalf("RecordTransferAttributionEvents failed: %v", err)
	}
	if err := db.RecordTransferAttributionEvents(ctx, grants[:1]); err != nil {
		t.Fatalf("duplicate RecordTransferAttributionEvents failed: %v", err)
	}
	providerEvents := []models.ProviderTransferEvent{
		{
			ProviderEventID:  "provider-download-1",
			Direction:        models.ProviderTransferDirectionDownload,
			EventTime:        now.Add(2 * time.Second),
			Provider:         "s3",
			Bucket:           "bucket-a",
			ObjectKey:        "program/proj/" + oid,
			StorageURL:       "s3://bucket-a/program/proj/" + oid,
			RangeStart:       &rangeStart,
			RangeEnd:         &rangeEnd,
			BytesTransferred: 42,
			HTTPMethod:       "GET",
			HTTPStatus:       200,
		},
		{
			ProviderEventID:  "provider-upload-1",
			Direction:        models.ProviderTransferDirectionUpload,
			EventTime:        now.Add(3 * time.Second),
			Provider:         "s3",
			Bucket:           "bucket-a",
			ObjectKey:        "program/proj/" + oid,
			StorageURL:       "s3://bucket-a/program/proj/" + oid,
			BytesTransferred: 42,
			HTTPMethod:       "PUT",
			HTTPStatus:       200,
			ActorSubject:     "local-user",
		},
	}
	if err := db.RecordProviderTransferEvents(ctx, providerEvents); err != nil {
		t.Fatalf("RecordProviderTransferEvents failed: %v", err)
	}
	if err := db.RecordProviderTransferEvents(ctx, providerEvents[:1]); err != nil {
		t.Fatalf("duplicate RecordProviderTransferEvents failed: %v", err)
	}

	summary, err := db.GetTransferAttributionSummary(ctx, models.TransferAttributionFilter{
		Organization: "calypr",
		Project:      "proj-a",
	})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummary failed: %v", err)
	}
	if summary.EventCount != 2 || summary.DownloadEventCount != 1 || summary.UploadEventCount != 1 || summary.BytesDownloaded != 42 || summary.BytesUploaded != 42 {
		t.Fatalf("unexpected transfer summary: %+v", summary)
	}

	userBreakdown, err := db.GetTransferAttributionBreakdown(ctx, models.TransferAttributionFilter{Organization: "calypr"}, "user")
	if err != nil {
		t.Fatalf("GetTransferAttributionBreakdown(user) failed: %v", err)
	}
	if len(userBreakdown) != 2 {
		t.Fatalf("expected two user breakdown rows, got %+v", userBreakdown)
	}
	providerBreakdown, err := db.GetTransferAttributionBreakdown(ctx, models.TransferAttributionFilter{Provider: "s3", Bucket: "bucket-a"}, "provider")
	if err != nil {
		t.Fatalf("GetTransferAttributionBreakdown(provider) failed: %v", err)
	}
	if len(providerBreakdown) != 1 || providerBreakdown[0].BytesDownloaded != 42 || providerBreakdown[0].BytesUploaded != 42 {
		t.Fatalf("unexpected provider breakdown: %+v", providerBreakdown)
	}
	objectBreakdown, err := db.GetTransferAttributionBreakdown(ctx, models.TransferAttributionFilter{SHA256: oid}, "object")
	if err != nil {
		t.Fatalf("GetTransferAttributionBreakdown(object) failed: %v", err)
	}
	if len(objectBreakdown) != 1 || objectBreakdown[0].SHA256 != oid {
		t.Fatalf("unexpected object breakdown: %+v", objectBreakdown)
	}

	if err := db.DeleteObject(ctx, "did-1"); err != nil {
		t.Fatalf("DeleteObject failed: %v", err)
	}
	afterDelete, err := db.GetTransferAttributionSummary(ctx, models.TransferAttributionFilter{SHA256: oid})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummary after delete failed: %v", err)
	}
	if afterDelete.EventCount != 2 || afterDelete.BytesDownloaded != 42 || afterDelete.BytesUploaded != 42 {
		t.Fatalf("expected transfer events to survive object deletion, got %+v", afterDelete)
	}
	unmatched := models.ProviderTransferEvent{
		ProviderEventID:  "provider-unmatched-1",
		Direction:        models.ProviderTransferDirectionDownload,
		EventTime:        now,
		Provider:         "s3",
		Bucket:           "bucket-a",
		ObjectKey:        "missing",
		BytesTransferred: 10,
		HTTPMethod:       "GET",
		HTTPStatus:       200,
	}
	if err := db.RecordProviderTransferEvents(ctx, []models.ProviderTransferEvent{unmatched}); err != nil {
		t.Fatalf("RecordProviderTransferEvents unmatched failed: %v", err)
	}
	defaultSummary, err := db.GetTransferAttributionSummary(ctx, models.TransferAttributionFilter{Bucket: "bucket-a"})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummary default failed: %v", err)
	}
	if defaultSummary.EventCount != 2 {
		t.Fatalf("unmatched provider event should not be billed by default: %+v", defaultSummary)
	}
	allSummary, err := db.GetTransferAttributionSummary(ctx, models.TransferAttributionFilter{Bucket: "bucket-a", ReconciliationStatus: "all"})
	if err != nil {
		t.Fatalf("GetTransferAttributionSummary all failed: %v", err)
	}
	if allSummary.EventCount != 2 {
		t.Fatalf("expected all reconciliation states when requested, got %+v", allSummary)
	}

	var grantCount, issueCount int
	if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(MAX(issue_count), 0) FROM access_grant`).Scan(&grantCount, &issueCount); err != nil {
		t.Fatalf("count access grants: %v", err)
	}
	if grantCount != 1 || issueCount != 2 {
		t.Fatalf("expected one canonical access grant with two issues, got count=%d issue_count=%d", grantCount, issueCount)
	}
}

func TestSqliteDB_AccessGrantReconciliationAmbiguousOnlyForDifferentGrants(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}
	now := time.Now().UTC()
	events := []models.TransferAttributionEvent{
		{
			EventID:        "access-root",
			EventType:      models.TransferEventAccessIssued,
			EventTime:      now,
			ObjectID:       "did-root",
			SHA256:         "sha-root",
			ObjectSize:     10,
			Organization:   "calypr",
			Project:        "proj-a",
			AccessID:       "s3",
			Provider:       "s3",
			Bucket:         "bucket-a",
			StorageURL:     "s3://bucket-a/root/shared-key",
			BytesRequested: 10,
		},
		{
			EventID:        "access-other",
			EventType:      models.TransferEventAccessIssued,
			EventTime:      now.Add(time.Second),
			ObjectID:       "did-other",
			SHA256:         "sha-other",
			ObjectSize:     10,
			Organization:   "calypr",
			Project:        "proj-b",
			AccessID:       "s3",
			Provider:       "s3",
			Bucket:         "bucket-a",
			StorageURL:     "s3://bucket-a/other/shared-key",
			BytesRequested: 10,
		},
	}
	if err := db.RecordTransferAttributionEvents(ctx, events); err != nil {
		t.Fatalf("RecordTransferAttributionEvents failed: %v", err)
	}
	if err := db.RecordProviderTransferEvents(ctx, []models.ProviderTransferEvent{{
		ProviderEventID:  "ambiguous-provider-event",
		Direction:        models.ProviderTransferDirectionDownload,
		EventTime:        now.Add(2 * time.Second),
		Provider:         "s3",
		Bucket:           "bucket-a",
		ObjectKey:        "shared-key",
		BytesTransferred: 10,
		HTTPMethod:       "GET",
		HTTPStatus:       200,
	}}); err != nil {
		t.Fatalf("RecordProviderTransferEvents failed: %v", err)
	}
	var status string
	if err := db.db.QueryRowContext(ctx, `SELECT reconciliation_status FROM provider_transfer_event WHERE provider_event_id = 'ambiguous-provider-event'`).Scan(&status); err != nil {
		t.Fatalf("read provider event status: %v", err)
	}
	if status != models.ProviderTransferAmbiguous {
		t.Fatalf("expected ambiguous provider event, got %q", status)
	}
}

func TestSqliteDB_BucketScopeLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	if err := db.CreateBucketScope(ctx, nil); err == nil {
		t.Fatalf("expected validation error for nil scope")
	}

	scope := &models.BucketScope{
		Organization: "calypr",
		ProjectID:    "proj-a",
		Bucket:       "bucket-a",
		PathPrefix:   "/data/a/",
	}
	if err := db.CreateBucketScope(ctx, scope); err != nil {
		t.Fatalf("CreateBucketScope failed: %v", err)
	}

	got, err := db.GetBucketScope(ctx, "calypr", "proj-a")
	if err != nil {
		t.Fatalf("GetBucketScope failed: %v", err)
	}
	if got.Bucket != "bucket-a" || got.PathPrefix != "data/a" {
		t.Fatalf("unexpected scope: %+v", got)
	}

	if err := db.CreateBucketScope(ctx, &models.BucketScope{
		Organization: "calypr",
		ProjectID:    "proj-a",
		Bucket:       "bucket-a",
		PathPrefix:   "data/a",
	}); err != nil {
		t.Fatalf("idempotent create should succeed, got: %v", err)
	}

	if err := db.CreateBucketScope(ctx, &models.BucketScope{
		Organization: "calypr",
		ProjectID:    "proj-a",
		Bucket:       "bucket-b",
		PathPrefix:   "data/b",
	}); err != nil {
		t.Fatalf("expected remap update to succeed, got: %v", err)
	}
	got, err = db.GetBucketScope(ctx, "calypr", "proj-a")
	if err != nil {
		t.Fatalf("GetBucketScope after remap failed: %v", err)
	}
	if got.Bucket != "bucket-b" || got.PathPrefix != "data/b" {
		t.Fatalf("unexpected remapped scope: %+v", got)
	}

	all, err := db.ListBucketScopes(ctx)
	if err != nil {
		t.Fatalf("ListBucketScopes failed: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 scope, got %d", len(all))
	}

	if err := db.CreateBucketScope(ctx, &models.BucketScope{
		Organization: "calypr",
		Bucket:       "bucket-root",
		PathPrefix:   "",
	}); err != nil {
		t.Fatalf("program-level CreateBucketScope failed: %v", err)
	}
	root, err := db.GetBucketScope(ctx, "calypr", "")
	if err != nil {
		t.Fatalf("program-level GetBucketScope failed: %v", err)
	}
	if root.Bucket != "bucket-root" || root.PathPrefix != "" {
		t.Fatalf("unexpected program-level scope: %+v", root)
	}

	_, err = db.GetBucketScope(ctx, "calypr", "missing")
	if !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing scope, got: %v", err)
	}
}

func TestSqliteDB_GetPendingLFSMeta(t *testing.T) {
	ctx := context.Background()
	db, err := NewSqliteDB(":memory:")
	if err != nil {
		t.Fatalf("failed to create db: %v", err)
	}

	now := time.Now().UTC()
	oid := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	candidate := drs.DrsObjectCandidate{
		Name: common.Ptr("candidate-get"),
		Checksums: []drs.Checksum{
			{Type: "sha256", Checksum: oid},
		},
	}

	if err := db.SavePendingLFSMeta(ctx, []models.PendingLFSMeta{
		{
			OID:       oid,
			Candidate: candidate,
			CreatedAt: now,
			ExpiresAt: now.Add(10 * time.Minute),
		},
	}); err != nil {
		t.Fatalf("SavePendingLFSMeta failed: %v", err)
	}

	got, err := db.GetPendingLFSMeta(ctx, oid)
	if err != nil {
		t.Fatalf("GetPendingLFSMeta failed: %v", err)
	}
	if common.StringVal(got.Candidate.Name) != "candidate-get" {
		t.Fatalf("unexpected pending metadata: %+v", got)
	}

	_, err = db.GetPendingLFSMeta(ctx, "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee")
	if !errors.Is(err, faults.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing pending metadata, got: %v", err)
	}
}
