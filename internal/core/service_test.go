package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/calypr/syfon/internal/urlmanager"
)

type coreTestDB struct {
	*testutils.MockDatabase
	aliases                map[string]string
	creds                  []models.S3Credential
	getS3CredentialCalls   int
	listS3CredentialsCalls int
}

func (d *coreTestDB) ResolveObjectAlias(ctx context.Context, aliasID string) (string, error) {
	if d.aliases != nil {
		if canonical, ok := d.aliases[aliasID]; ok {
			return canonical, nil
		}
	}
	return "", fmt.Errorf("%w: object not found", faults.ErrNotFound)
}

func (d *coreTestDB) ListS3Credentials(ctx context.Context) ([]models.S3Credential, error) {
	d.listS3CredentialsCalls++
	if d.creds != nil {
		out := make([]models.S3Credential, len(d.creds))
		copy(out, d.creds)
		return out, nil
	}
	if d.MockDatabase == nil {
		return nil, nil
	}
	return d.MockDatabase.ListS3Credentials(ctx)
}

func (d *coreTestDB) GetS3Credential(ctx context.Context, bucket string) (*models.S3Credential, error) {
	d.getS3CredentialCalls++
	if d.MockDatabase == nil {
		return nil, nil
	}
	return d.MockDatabase.GetS3Credential(ctx, bucket)
}

type capturingURLManager struct {
	signURLBucket       string
	signURLAccessURL    string
	signUploadBucket    string
	signUploadAccessURL string
	signDownloadBucket  string
	signDownloadURL     string
	initBucket          string
	initKey             string
	partBucket          string
	partKey             string
	partUploadID        string
	partNumber          int32
	completeBucket      string
	completeKey         string
	completeUploadID    string
	completeParts       []urlmanager.MultipartPart
	invalidatedBuckets  []string
}

func (m *capturingURLManager) SignURL(ctx context.Context, accessId string, url string, opts urlmanager.SignOptions) (string, error) {
	m.signURLBucket = accessId
	m.signURLAccessURL = url
	return "signed:" + url, nil
}

func (m *capturingURLManager) SignUploadURL(ctx context.Context, accessId string, url string, opts urlmanager.SignOptions) (string, error) {
	m.signUploadBucket = accessId
	m.signUploadAccessURL = url
	return "upload:" + url, nil
}

func (m *capturingURLManager) SignDownloadPart(ctx context.Context, bucket string, url string, start int64, end int64, opts urlmanager.SignOptions) (string, error) {
	m.signDownloadBucket = bucket
	m.signDownloadURL = url
	return "download:" + url, nil
}

func (m *capturingURLManager) InitMultipartUpload(ctx context.Context, bucket string, key string) (string, error) {
	m.initBucket = bucket
	m.initKey = key
	return "upload-id", nil
}

func (m *capturingURLManager) SignMultipartPart(ctx context.Context, bucket string, key string, uploadId string, partNumber int32) (string, error) {
	m.partBucket = bucket
	m.partKey = key
	m.partUploadID = uploadId
	m.partNumber = partNumber
	return "part:" + key, nil
}

func (m *capturingURLManager) CompleteMultipartUpload(ctx context.Context, bucket string, key string, uploadId string, parts []urlmanager.MultipartPart) error {
	m.completeBucket = bucket
	m.completeKey = key
	m.completeUploadID = uploadId
	m.completeParts = append([]urlmanager.MultipartPart(nil), parts...)
	return nil
}

func (m *capturingURLManager) InvalidateBucket(bucket string) {
	m.invalidatedBuckets = append(m.invalidatedBuckets, strings.TrimSpace(bucket))
}

func buildGen3Context(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}

func buildLocalAuthzContext(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}

func TestObjectManagerGetObjectLookupPaths(t *testing.T) {
	cases := []struct {
		name    string
		db      *coreTestDB
		ident   string
		method  string
		ctx     context.Context
		wantID  string
		wantURI string
		wantErr error
	}{
		{
			name: "checksum lookup",
			db: &coreTestDB{
				MockDatabase: &testutils.MockDatabase{
					Objects: map[string]*drs.DrsObject{
						"obj-1": {
							Id:      "obj-1",
							SelfUri: "drs://obj-1",
							Checksums: []drs.Checksum{
								{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
							},
						},
					},
					ObjectAuthz: map[string]map[string][]string{
						"obj-1": {"data_file": {}},
					},
				},
			},
			ident:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			method:  "read",
			ctx:     buildGen3Context(map[string]map[string]bool{"/programs/data_file": {"read": true}}),
			wantID:  "obj-1",
			wantURI: "drs://obj-1",
		},
		{
			name: "direct id lookup",
			db: &coreTestDB{
				MockDatabase: &testutils.MockDatabase{
					Objects: map[string]*drs.DrsObject{
						"obj-2": {Id: "obj-2", SelfUri: "drs://obj-2"},
					},
					ObjectAuthz: map[string]map[string][]string{
						"obj-2": {"data_file": {}},
					},
				},
			},
			ident:   "obj-2",
			method:  "read",
			ctx:     buildGen3Context(map[string]map[string]bool{"/programs/data_file": {"read": true}}),
			wantID:  "obj-2",
			wantURI: "drs://obj-2",
		},
		{
			name: "alias fallback",
			db: &coreTestDB{
				MockDatabase: &testutils.MockDatabase{
					Objects: map[string]*drs.DrsObject{
						"canonical-1": {Id: "canonical-1", SelfUri: "drs://canonical-1"},
					},
					ObjectAuthz: map[string]map[string][]string{
						"canonical-1": {"data_file": {}},
					},
				},
				aliases: map[string]string{
					"alias-1": "canonical-1",
				},
			},
			ident:   "alias-1",
			method:  "read",
			ctx:     buildGen3Context(map[string]map[string]bool{"/programs/data_file": {"read": true}}),
			wantID:  "canonical-1",
			wantURI: "drs://canonical-1",
		},
		{
			name: "access denied",
			db: &coreTestDB{
				MockDatabase: &testutils.MockDatabase{
					Objects: map[string]*drs.DrsObject{
						"obj-3": {Id: "obj-3"},
					},
					ObjectAuthz: map[string]map[string][]string{
						"obj-3": {"a": {}},
					},
				},
			},
			ident:   "obj-3",
			method:  "delete",
			ctx:     buildGen3Context(map[string]map[string]bool{"/programs/b": {"delete": true}}),
			wantErr: faults.ErrUnauthorized,
		},
		{
			name: "not found",
			db: &coreTestDB{
				MockDatabase: &testutils.MockDatabase{
					Objects: map[string]*drs.DrsObject{},
				},
			},
			ident:   "missing",
			method:  "read",
			ctx:     buildGen3Context(map[string]map[string]bool{"/data_file": {"read": true}}),
			wantErr: faults.ErrNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			om := NewObjectManager(tc.db, &capturingURLManager{})
			obj, err := om.GetObject(tc.ctx, tc.ident, tc.method)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected error %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if obj.Id != tc.wantID {
				t.Fatalf("expected id %q, got %q", tc.wantID, obj.Id)
			}
			if obj.SelfUri != tc.wantURI {
				t.Fatalf("expected self uri %q, got %q", tc.wantURI, obj.SelfUri)
			}
		})
	}
}

func TestObjectManagerGetObjectAuthzParity(t *testing.T) {
	builders := map[string]func(map[string]map[string]bool) context.Context{
		"gen3":        buildGen3Context,
		"local-authz": buildLocalAuthzContext,
	}
	for mode, buildCtx := range builders {
		t.Run(mode, func(t *testing.T) {
			db := &coreTestDB{
				MockDatabase: &testutils.MockDatabase{
					Objects: map[string]*drs.DrsObject{
						"obj-1": {Id: "obj-1", SelfUri: "drs://obj-1"},
					},
					ObjectAuthz: map[string]map[string][]string{
						"obj-1": {"org": {"project"}},
					},
				},
			}
			om := NewObjectManager(db, &capturingURLManager{})
			resource := "/programs/org/projects/project"

			if _, err := om.GetObject(buildCtx(map[string]map[string]bool{
				resource: {"read": true},
			}), "obj-1", "read"); err != nil {
				t.Fatalf("expected read to succeed: %v", err)
			}

			_, err := om.GetObject(buildCtx(map[string]map[string]bool{
				resource: {"create": true},
			}), "obj-1", "read")
			if !errors.Is(err, faults.ErrUnauthorized) {
				t.Fatalf("expected missing read privilege to be unauthorized, got %v", err)
			}
		})
	}
}

func TestObjectManagerCredentialWritesInvalidateSignerCache(t *testing.T) {
	ctx := context.Background()
	db := &coreTestDB{MockDatabase: &testutils.MockDatabase{}}
	um := &capturingURLManager{}
	om := NewObjectManager(db, um)

	cred := &models.S3Credential{Bucket: "b1", Provider: "s3", Region: "us-east-1", AccessKey: "a", SecretKey: "s"}
	if err := om.SaveS3Credential(ctx, cred); err != nil {
		t.Fatalf("SaveS3Credential failed: %v", err)
	}
	if got, want := um.invalidatedBuckets, []string{"b1"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("unexpected invalidated buckets after save: got %v want %v", got, want)
	}

	if err := om.DeleteS3Credential(ctx, "b1"); err != nil {
		t.Fatalf("DeleteS3Credential failed: %v", err)
	}
	if got, want := um.invalidatedBuckets, []string{"b1", "b1"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("unexpected invalidated buckets after delete: got %v want %v", got, want)
	}
}

func TestBucketCatalogCachesMissingScopeLookup(t *testing.T) {
	ctx := context.Background()
	mockDB := &testutils.MockDatabase{}
	om := NewObjectManager(&coreTestDB{MockDatabase: mockDB}, nil)

	if scope, found, err := om.bucketCatalog.lookupBucketScope(ctx, "missing-org", "missing-project"); err != nil {
		t.Fatalf("first scope lookup failed: %v", err)
	} else if found || scope != (models.BucketScope{}) {
		t.Fatalf("expected missing scope, got scope=%+v found=%t", scope, found)
	}
	if scope, found, err := om.bucketCatalog.lookupBucketScope(ctx, "missing-org", "missing-project"); err != nil {
		t.Fatalf("cached scope lookup failed: %v", err)
	} else if found || scope.Organization != "missing-org" || scope.ProjectID != "missing-project" {
		t.Fatalf("expected cached missing scope key, got scope=%+v found=%t", scope, found)
	}
	if mockDB.GetBucketScopeCalls != 1 {
		t.Fatalf("expected one database lookup for a cached miss, got %d", mockDB.GetBucketScopeCalls)
	}
}

func TestBucketCatalogDeleteScopeInvalidatesLookupCache(t *testing.T) {
	ctx := context.Background()
	mockDB := &testutils.MockDatabase{
		BucketScopes: map[string]models.BucketScope{
			"org|project": {
				Organization: "org",
				ProjectID:    "project",
				Bucket:       "old-bucket",
				PathPrefix:   "old-prefix",
			},
		},
	}
	om := NewObjectManager(&coreTestDB{MockDatabase: mockDB}, nil)

	if _, found, err := om.bucketCatalog.lookupBucketScope(ctx, "org", "project"); err != nil || !found {
		t.Fatalf("expected initial scope lookup to succeed, found=%t err=%v", found, err)
	}
	if err := om.DeleteBucketScope(ctx, "org", "project", "old-bucket", "old-prefix"); err != nil {
		t.Fatalf("delete bucket scope failed: %v", err)
	}
	mockDB.BucketScopes["org|project"] = models.BucketScope{
		Organization: "org",
		ProjectID:    "project",
		Bucket:       "new-bucket",
		PathPrefix:   "new-prefix",
	}

	scope, found, err := om.bucketCatalog.lookupBucketScope(ctx, "org", "project")
	if err != nil {
		t.Fatalf("lookup after invalidation failed: %v", err)
	}
	if !found || scope.Bucket != "new-bucket" || scope.PathPrefix != "new-prefix" {
		t.Fatalf("expected lookup to observe replacement scope, got scope=%+v found=%t", scope, found)
	}
	if mockDB.GetBucketScopeCalls != 2 {
		t.Fatalf("expected cache invalidation to force a second database lookup, got %d", mockDB.GetBucketScopeCalls)
	}
}

func TestObjectManagerBulkReadFiltering(t *testing.T) {
	db := &coreTestDB{
		MockDatabase: &testutils.MockDatabase{
			Objects: map[string]*drs.DrsObject{
				"obj-1": {
					Id: "obj-1",
					Checksums: []drs.Checksum{
						{Type: "sha256", Checksum: "sha-1"},
					},
				},
				"obj-2": {
					Id: "obj-2",
					Checksums: []drs.Checksum{
						{Type: "sha256", Checksum: "sha-2"},
					},
				},
			},
			ObjectAuthz: map[string]map[string][]string{
				"obj-1": {"org": {"one"}},
				"obj-2": {"org": {"two"}},
			},
		},
	}
	om := NewObjectManager(db, &capturingURLManager{})
	ctx := buildGen3Context(map[string]map[string]bool{
		"/programs/org/projects/one": {"read": true},
	})

	objects, err := om.GetBulkObjects(ctx, []string{"obj-1", "obj-2"}, "read")
	if err != nil {
		t.Fatalf("GetBulkObjects failed: %v", err)
	}
	if len(objects) != 1 || objects[0].Id != "obj-1" {
		t.Fatalf("expected only obj-1 from bulk read, got %+v", objects)
	}

	byChecksum, err := om.GetObjectsByChecksums(ctx, []string{"sha-1", "sha-2"}, "read")
	if err != nil {
		t.Fatalf("GetObjectsByChecksums failed: %v", err)
	}
	if len(byChecksum["sha-1"]) != 1 || byChecksum["sha-1"][0].Id != "obj-1" {
		t.Fatalf("expected checksum sha-1 to resolve obj-1, got %+v", byChecksum["sha-1"])
	}
	if got := byChecksum["sha-2"]; len(got) != 0 {
		t.Fatalf("expected checksum sha-2 to be filtered, got %+v", got)
	}
}

func TestObjectManagerLifecycleAuthorization(t *testing.T) {
	t.Run("register enforces create on candidate resources", func(t *testing.T) {
		db := &coreTestDB{MockDatabase: &testutils.MockDatabase{}}
		om := NewObjectManager(db, &capturingURLManager{})
		obj := models.InternalObject{
			DrsObject:      drs.DrsObject{Id: "new-object"},
			Authorizations: map[string][]string{"org": {"project"}},
		}

		deniedCtx := buildGen3Context(map[string]map[string]bool{
			"/programs/org/projects/project": {"read": true},
		})
		err := om.RegisterObjects(deniedCtx, []models.InternalObject{obj})
		if !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected register without create privilege to be unauthorized, got %v", err)
		}
		if !strings.Contains(err.Error(), "new-object") || !strings.Contains(err.Error(), "org/project") {
			t.Fatalf("expected denied register error to include object id and scope, got %v", err)
		}
		if _, ok := db.Objects["new-object"]; ok {
			t.Fatalf("unauthorized register wrote object")
		}

		allowedCtx := buildGen3Context(map[string]map[string]bool{
			"/programs/org/projects/project": {"create": true},
		})
		if err := om.RegisterObjects(allowedCtx, []models.InternalObject{obj}); err != nil {
			t.Fatalf("expected register with create privilege to succeed: %v", err)
		}
		if _, ok := db.Objects["new-object"]; !ok {
			t.Fatalf("authorized register did not write object")
		}
	})

	t.Run("replace requires current update and new grant create with read", func(t *testing.T) {
		database := testutils.NewInMemoryDB()
		om := NewObjectManager(database, &capturingURLManager{})
		if err := om.RegisterObjects(context.Background(), []models.InternalObject{{
			DrsObject:      drs.DrsObject{Id: "obj"},
			Authorizations: map[string][]string{"old": {"scope"}},
		}}); err != nil {
			t.Fatal(err)
		}
		replacement := models.InternalObject{
			DrsObject:      drs.DrsObject{Id: "obj", Name: common.Ptr("updated")},
			Authorizations: map[string][]string{"new": {"scope"}},
		}
		err := om.ReplaceObjects(buildGen3Context(map[string]map[string]bool{
			"/programs/old/projects/scope": {"update": true},
		}), []models.InternalObject{replacement})
		if !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected unauthorized grant replacement, got %v", err)
		}
		err = om.ReplaceObjects(buildGen3Context(map[string]map[string]bool{
			"/programs/old/projects/scope": {"update": true, "read": true},
			"/programs/new/projects/scope": {"create": true},
		}), []models.InternalObject{replacement})
		if err != nil {
			t.Fatalf("authorized replacement: %v", err)
		}
		object, err := database.GetObject(context.Background(), "obj")
		if err != nil {
			t.Fatal(err)
		}
		if got := common.StringVal(object.Name); got != "updated" {
			t.Fatalf("replacement name = %q", got)
		}
	})

	t.Run("delete by checksum uses delete privilege without requiring read", func(t *testing.T) {
		db := &coreTestDB{MockDatabase: &testutils.MockDatabase{
			Objects: map[string]*drs.DrsObject{
				"delete-me": {
					Id:        "delete-me",
					Checksums: []drs.Checksum{{Type: "sha256", Checksum: "sha-delete"}},
				},
				"keep-me": {
					Id:        "keep-me",
					Checksums: []drs.Checksum{{Type: "sha256", Checksum: "sha-keep"}},
				},
			},
			ObjectAuthz: map[string]map[string][]string{
				"delete-me": {"org": {"delete"}},
				"keep-me":   {"org": {"read"}},
			},
		}}
		om := NewObjectManager(db, &capturingURLManager{})
		ctx := buildGen3Context(map[string]map[string]bool{
			"/programs/org/projects/delete": {"delete": true},
		})

		count, err := om.DeleteObjectsByChecksums(ctx, []string{"sha-delete", "sha-keep"})
		if err != nil {
			t.Fatalf("DeleteObjectsByChecksums failed: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected one delete, got %d", count)
		}
		if _, ok := db.Objects["delete-me"]; ok {
			t.Fatalf("expected delete-me to be removed")
		}
		if _, ok := db.Objects["keep-me"]; !ok {
			t.Fatalf("expected keep-me to remain")
		}
	})

	t.Run("single mutations reject unauthorized access", func(t *testing.T) {
		accessMethods := []drs.AccessMethod{{Type: drs.AccessMethodTypeHttps}}
		db := &coreTestDB{MockDatabase: &testutils.MockDatabase{
			Objects: map[string]*drs.DrsObject{
				"obj": {Id: "obj"},
			},
			ObjectAuthz: map[string]map[string][]string{
				"obj": {"org": {"project"}},
			},
		}}
		om := NewObjectManager(db, &capturingURLManager{})
		deniedCtx := buildGen3Context(map[string]map[string]bool{
			"/programs/org/projects/project": {"read": true},
		})

		if err := om.DeleteObject(deniedCtx, "obj"); !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected delete to reject missing privilege, got %v", err)
		}
		if err := om.UpdateObjectAccessMethods(deniedCtx, "obj", accessMethods); !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected access method update to reject missing privilege, got %v", err)
		}
		if err := om.CreateObjectAlias(deniedCtx, "alias", "obj"); !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected alias create to reject missing privilege, got %v", err)
		}

		allowedCtx := buildGen3Context(map[string]map[string]bool{
			"/programs/org/projects/project": {"delete": true, "update": true},
		})
		if err := om.UpdateObjectAccessMethods(allowedCtx, "obj", accessMethods); err != nil {
			t.Fatalf("expected access method update to succeed: %v", err)
		}
		if err := om.CreateObjectAlias(allowedCtx, "alias", "obj"); err != nil {
			t.Fatalf("expected alias create to succeed: %v", err)
		}
		if err := om.DeleteObject(allowedCtx, "obj"); err != nil {
			t.Fatalf("expected delete to succeed: %v", err)
		}
	})

	t.Run("scope list and single checksum lookup filter reads", func(t *testing.T) {
		db := &coreTestDB{MockDatabase: &testutils.MockDatabase{
			Objects: map[string]*drs.DrsObject{
				"obj-1": {Id: "obj-1", Checksums: []drs.Checksum{{Type: "sha256", Checksum: "shared"}}},
				"obj-2": {Id: "obj-2", Checksums: []drs.Checksum{{Type: "sha256", Checksum: "shared"}}},
			},
			ObjectAuthz: map[string]map[string][]string{
				"obj-1": {"org": {"one"}},
				"obj-2": {"org": {"two"}},
			},
		}}
		om := NewObjectManager(db, &capturingURLManager{})
		ctx := buildGen3Context(map[string]map[string]bool{
			"/programs/org/projects/one": {"read": true},
		})

		ids, err := om.ListObjectIDsByScope(ctx, "org", "", "read")
		if err != nil {
			t.Fatalf("ListObjectIDsByScope failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != "obj-1" {
			t.Fatalf("expected only readable obj-1 id, got %+v", ids)
		}

		objects, err := om.GetObjectsByChecksum(ctx, "shared", "read")
		if err != nil {
			t.Fatalf("GetObjectsByChecksum failed: %v", err)
		}
		if len(objects) != 1 || objects[0].Id != "obj-1" {
			t.Fatalf("expected only readable obj-1 checksum match, got %+v", objects)
		}
	})
}

func TestObjectManagerDeleteResolveAndSignDelegation(t *testing.T) {
	t.Run("delete by scope filters unauthorized objects", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{
				Objects: map[string]*drs.DrsObject{
					"obj-a": {Id: "obj-a"},
					"obj-b": {Id: "obj-b"},
				},
				ObjectAuthz: map[string]map[string][]string{
					"obj-a": {"a": {"one"}},
					"obj-b": {"a": {"two"}},
				},
			},
		}
		om := NewObjectManager(db, &capturingURLManager{})
		ctx := buildGen3Context(map[string]map[string]bool{
			"/programs/a":              {"delete": true},
			"/programs/a/projects/one": {"delete": true},
		})

		count, err := om.DeleteBulkByScope(ctx, "a", "")
		if err != nil {
			t.Fatalf("DeleteBulkByScope failed: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected 1 deletion, got %d", count)
		}
		if _, ok := db.Objects["obj-a"]; !ok {
			t.Fatalf("grant removal must retain obj-a content")
		}
		if _, ok := db.Objects["obj-b"]; !ok {
			t.Fatalf("expected obj-b to remain")
		}
	})

	t.Run("bulk update access methods checks authorization in one bulk read", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{
				Objects: map[string]*drs.DrsObject{
					"obj-a": {Id: "obj-a"},
					"obj-b": {Id: "obj-b"},
				},
				ObjectAuthz: map[string]map[string][]string{
					"obj-a": {"a": {"one"}},
					"obj-b": {"a": {"two"}},
				},
			},
		}
		om := NewObjectManager(db, &capturingURLManager{})
		ctx := buildGen3Context(map[string]map[string]bool{
			"/programs/a/projects/one": {"update": true},
		})

		err := om.BulkUpdateAccessMethods(ctx, map[string][]drs.AccessMethod{
			"obj-a": {{
				Type: drs.AccessMethodTypeS3,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/a"},
			}},
			"obj-b": {{
				Type: drs.AccessMethodTypeS3,
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/b"},
			}},
		})
		if !errors.Is(err, faults.ErrUnauthorized) {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
	})

	t.Run("resolve bucket uses configured credentials", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{},
			creds: []models.S3Credential{
				{Bucket: "default-bucket", Provider: "s3"},
				{Bucket: "secondary", Provider: "s3"},
			},
		}
		om := NewObjectManager(db, &capturingURLManager{})

		got, err := om.ResolveBucket(context.Background(), "")
		if err != nil {
			t.Fatalf("ResolveBucket failed: %v", err)
		}
		if got != "default-bucket" {
			t.Fatalf("expected default bucket, got %q", got)
		}

		got, err = om.ResolveBucket(context.Background(), "secondary")
		if err != nil {
			t.Fatalf("ResolveBucket explicit bucket failed: %v", err)
		}
		if got != "secondary" {
			t.Fatalf("expected secondary bucket, got %q", got)
		}

		if _, err := om.ResolveBucket(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), `bucket "missing" not configured`) {
			t.Fatalf("expected error for unknown bucket")
		}
	})

	t.Run("signing delegates resolved bucket", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{},
		}
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)

		signed, err := om.SignURL(context.Background(), "s3://bucket-a/path/to/object", urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignURL failed: %v", err)
		}
		if signed != "signed:s3://bucket-a/path/to/object" {
			t.Fatalf("unexpected signed url: %s", signed)
		}
		if um.signURLBucket != "bucket-a" {
			t.Fatalf("expected bucket-a, got %q", um.signURLBucket)
		}

		partURL, err := om.SignDownloadPart(context.Background(), "bucket-b", "s3://bucket-b/path/to/object", 10, 20, urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignDownloadPart failed: %v", err)
		}
		if partURL != "download:s3://bucket-b/path/to/object" {
			t.Fatalf("unexpected download part url: %s", partURL)
		}
		if um.signDownloadBucket != "bucket-b" {
			t.Fatalf("expected download bucket bucket-b, got %q", um.signDownloadBucket)
		}

		uploadID, err := om.InitMultipartUpload(context.Background(), "bucket-c", "path/to/object")
		if err != nil {
			t.Fatalf("InitMultipartUpload failed: %v", err)
		}
		if uploadID != "upload-id" {
			t.Fatalf("unexpected upload id: %s", uploadID)
		}
		part, err := om.SignMultipartPart(context.Background(), "bucket-c", "path/to/object", uploadID, 3)
		if err != nil {
			t.Fatalf("SignMultipartPart failed: %v", err)
		}
		if part != "part:path/to/object" {
			t.Fatalf("unexpected part url: %s", part)
		}
		if err := om.CompleteMultipartUpload(context.Background(), "bucket-c", "path/to/object", uploadID, []urlmanager.MultipartPart{{PartNumber: 3, ETag: "etag"}}); err != nil {
			t.Fatalf("CompleteMultipartUpload failed: %v", err)
		}
		if um.partBucket != "bucket-c" || um.completeBucket != "bucket-c" {
			t.Fatalf("expected multipart delegation to bucket-c, got part=%q complete=%q", um.partBucket, um.completeBucket)
		}
	})

	t.Run("object download preserves imported physical key", func(t *testing.T) {
		mockDB := &testutils.MockDatabase{
			BucketScopes: map[string]models.BucketScope{
				"calypr|training": {
					Organization: "calypr",
					ProjectID:    "training",
					Bucket:       "calypr",
					PathPrefix:   "org-root/project-root",
				},
			},
		}
		db := &coreTestDB{
			MockDatabase: mockDB,
		}
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		obj := &models.InternalObject{
			Authorizations: map[string][]string{"calypr": {"training"}},
		}

		signed, err := om.SignObjectURL(context.Background(), obj, "s3://calypr/008b435e-c1da-58b8-80f1-3ad2882c43cd/542504", urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		wantURL := "s3://calypr/008b435e-c1da-58b8-80f1-3ad2882c43cd/542504"
		if signed != "signed:"+wantURL {
			t.Fatalf("unexpected signed url: %s", signed)
		}
		if um.signURLAccessURL != wantURL {
			t.Fatalf("expected scoped storage url %q, got %q", wantURL, um.signURLAccessURL)
		}
		if _, err := om.SignObjectURL(context.Background(), obj, "s3://calypr/another-object", urlmanager.SignOptions{}); err != nil {
			t.Fatalf("second SignObjectURL failed: %v", err)
		}
		if mockDB.GetBucketScopeCalls != 2 {
			t.Fatalf("download compatibility lookup should consult the organization and project scopes once, got %d db lookups", mockDB.GetBucketScopeCalls)
		}
	})

	t.Run("object download preserves stored bucket despite project scope", func(t *testing.T) {
		mockDB := &testutils.MockDatabase{
			BucketScopes: map[string]models.BucketScope{
				"gdc_mirror|gdc_mirror": {
					Organization: "gdc_mirror",
					ProjectID:    "gdc_mirror",
					Bucket:       "bforepc-prod",
					PathPrefix:   "bforepc",
				},
			},
		}
		db := &coreTestDB{MockDatabase: mockDB}
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		obj := &models.InternalObject{
			DrsObject: drs.DrsObject{
				ControlledAccess: &[]string{"/programs/gdc_mirror/projects/gdc_mirror"},
			},
		}

		_, err := om.SignObjectURL(context.Background(), obj, "s3://calypr/223bebff-debb-555c-bd59-5372f106c76c/4413832f86f331fc270de6d2263e13ac865d4524eef701ec8f4a342feb2f4300", urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		wantURL := "s3://calypr/223bebff-debb-555c-bd59-5372f106c76c/4413832f86f331fc270de6d2263e13ac865d4524eef701ec8f4a342feb2f4300"
		if um.signURLAccessURL != wantURL {
			t.Fatalf("expected scoped storage url %q, got %q", wantURL, um.signURLAccessURL)
		}
	})

	t.Run("object signing does not double prepend existing bucket scope prefix", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{
				BucketScopes: map[string]models.BucketScope{
					"calypr|training": {
						Organization: "calypr",
						ProjectID:    "training",
						Bucket:       "calypr",
						PathPrefix:   "org-root/project-root",
					},
				},
			},
		}
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		obj := &models.InternalObject{
			Authorizations: map[string][]string{"calypr": {"training"}},
		}

		input := "s3://calypr/org-root/project-root/008b435e-c1da-58b8-80f1-3ad2882c43cd/542504"
		if _, err := om.SignObjectURL(context.Background(), obj, input, urlmanager.SignOptions{}); err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if um.signURLAccessURL != input {
			t.Fatalf("expected already-scoped storage url to be unchanged, got %q", um.signURLAccessURL)
		}
	})

	t.Run("object signing composes organization and project prefixes and repairs malformed s3 access urls", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{
				BucketScopes: map[string]models.BucketScope{
					"syfon|": {
						Organization: "syfon",
						Bucket:       "syfon-e2e-bucket",
						PathPrefix:   "program-root",
					},
					"syfon|e2e": {
						Organization: "syfon",
						ProjectID:    "e2e",
						Bucket:       "syfon-e2e-bucket",
						PathPrefix:   "project-subpath",
					},
				},
			},
		}
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		obj := &models.InternalObject{
			DrsObject: drs.DrsObject{
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: "412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47",
				}},
				ControlledAccess: &[]string{"/organization/syfon/project/e2e"},
			},
		}

		input := "s3://f781273b-52eb-5ac2-a484-775235eef303"
		if _, err := om.SignObjectURL(context.Background(), obj, input, urlmanager.SignOptions{Method: "PUT"}); err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		wantURL := "s3://syfon-e2e-bucket/program-root/project-subpath/412f8568bfb0e62937ee40c6fcdeaa1cf55910c558c0152250340356c8829a47"
		if um.signURLBucket != "syfon-e2e-bucket" {
			t.Fatalf("expected signer bucket syfon-e2e-bucket, got %q", um.signURLBucket)
		}
		if um.signURLAccessURL != wantURL {
			t.Fatalf("expected repaired scoped upload URL %q, got %q", wantURL, um.signURLAccessURL)
		}
	})

	t.Run("object storage target preserves stored key", func(t *testing.T) {
		mockDB := &testutils.MockDatabase{
			Objects: map[string]*drs.DrsObject{
				"obj-delete": {
					Id:               "obj-delete",
					ControlledAccess: &[]string{"/organization/cbds/project/git_drs_test"},
					Checksums: []drs.Checksum{{
						Type:     "sha256",
						Checksum: "6d1bf6c2-917d-545e-b44d-8e28f96ec170",
					}},
					AccessMethods: &[]drs.AccessMethod{{
						Type: drs.AccessMethodTypeS3,
						AccessUrl: &struct {
							Headers *[]string `json:"headers,omitempty"`
							Url     string    `json:"url"`
						}{Url: "s3://cbds-minio/6d1bf6c2-917d-545e-b44d-8e28f96ec170"},
					}},
				},
			},
			BucketScopes: map[string]models.BucketScope{
				"cbds|git_drs_test": {
					Organization: "cbds",
					ProjectID:    "git_drs_test",
					Bucket:       "cbds-minio",
					PathPrefix:   "cbds",
				},
			},
		}
		om := NewObjectManager(mockDB, &capturingURLManager{})
		obj, err := mockDB.GetObject(context.Background(), "obj-delete")
		if err != nil {
			t.Fatalf("GetObject failed: %v", err)
		}

		targets, err := om.storageTargetsForObject(context.Background(), obj)
		if err != nil {
			t.Fatalf("storageTargetsForObject failed: %v", err)
		}
		if len(targets) != 1 {
			t.Fatalf("expected one storage target, got %+v", targets)
		}
		if got, want := targets[0].bucket, "cbds-minio"; got != want {
			t.Fatalf("unexpected target bucket: got %q want %q", got, want)
		}
		if got, want := targets[0].key, "6d1bf6c2-917d-545e-b44d-8e28f96ec170"; got != want {
			t.Fatalf("unexpected target key: got %q want %q", got, want)
		}
	})

	t.Run("object signing scopes uploads and repairs legacy download replicas", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{
				BucketScopes: map[string]models.BucketScope{
					"HTAN_INT|BForePC": {
						Organization: "HTAN_INT",
						ProjectID:    "BForePC",
						Bucket:       "bforepc",
						PathPrefix:   "bforepc-prod",
					},
				},
			},
		}
		obj := &models.InternalObject{
			DrsObject: drs.DrsObject{
				ControlledAccess: &[]string{"/organization/HTAN_INT/project/BForePC"},
			},
		}
		sourceURL := "s3://bforepc-prod/OHSU/koei_chin/slide.ome.tiff"
		wantURL := "s3://bforepc/bforepc-prod/OHSU/koei_chin/slide.ome.tiff"

		t.Run("download", func(t *testing.T) {
			um := &capturingURLManager{}
			om := NewObjectManager(db, um)
			signed, err := om.SignObjectURL(context.Background(), obj, sourceURL, urlmanager.SignOptions{})
			if err != nil {
				t.Fatalf("SignObjectURL download failed: %v", err)
			}
			if signed != "signed:"+wantURL {
				t.Fatalf("unexpected signed download URL: got %q want %q", signed, "signed:"+wantURL)
			}
			if um.signURLBucket != "bforepc" {
				t.Fatalf("expected download signer bucket bforepc, got %q", um.signURLBucket)
			}
			if um.signURLAccessURL != wantURL {
				t.Fatalf("expected download storage URL %q, got %q", wantURL, um.signURLAccessURL)
			}
		})

		t.Run("upload", func(t *testing.T) {
			um := &capturingURLManager{}
			om := NewObjectManager(db, um)
			signed, err := om.SignObjectURL(context.Background(), obj, sourceURL, urlmanager.SignOptions{Method: "PUT"})
			if err != nil {
				t.Fatalf("SignObjectURL upload failed: %v", err)
			}
			if signed != "signed:"+wantURL {
				t.Fatalf("unexpected signed upload URL: got %q want %q", signed, "signed:"+wantURL)
			}
			if um.signURLBucket != "bforepc" {
				t.Fatalf("expected upload signer bucket bforepc, got %q", um.signURLBucket)
			}
			if um.signURLAccessURL != wantURL {
				t.Fatalf("expected upload storage URL %q, got %q", wantURL, um.signURLAccessURL)
			}
		})

		t.Run("download part", func(t *testing.T) {
			um := &capturingURLManager{}
			om := NewObjectManager(db, um)
			partURL, err := om.SignObjectDownloadPart(context.Background(), obj, "bforepc-prod", sourceURL, 0, 1023, urlmanager.SignOptions{})
			if err != nil {
				t.Fatalf("SignObjectDownloadPart failed: %v", err)
			}
			if partURL != "download:"+wantURL {
				t.Fatalf("unexpected signed part URL: got %q want %q", partURL, "download:"+wantURL)
			}
			if um.signDownloadBucket != "bforepc" {
				t.Fatalf("expected part signer bucket bforepc, got %q", um.signDownloadBucket)
			}
			if um.signDownloadURL != wantURL {
				t.Fatalf("expected part storage URL %q, got %q", wantURL, um.signDownloadURL)
			}
		})
	})

	t.Run("root bucket scope preserves existing physical storage key", func(t *testing.T) {
		db := &coreTestDB{
			MockDatabase: &testutils.MockDatabase{
				BucketScopes: map[string]models.BucketScope{
					"gdc_mirror|gdc_mirror": {
						Organization: "gdc_mirror",
						ProjectID:    "gdc_mirror",
						Bucket:       "gdcdata",
						PathPrefix:   "",
					},
				},
			},
		}
		om := NewObjectManager(db, &capturingURLManager{})
		obj := &models.InternalObject{
			DrsObject: drs.DrsObject{
				Id: "00664eeb-830c-5fe4-b48c-054cd9c8e02f",
				Checksums: []drs.Checksum{{
					Type:     "sha256",
					Checksum: "239f8402efd37b62bfb892aa4becb0692b3ca5f58015083d8567e8d7fbdd1843",
				}},
				ControlledAccess: &[]string{"/organization/gdc_mirror/project/gdc_mirror"},
			},
		}
		sourceURL := "s3://gdcdata/00664eeb-830c-5fe4-b48c-054cd9c8e02f/239f8402efd37b62bfb892aa4becb0692b3ca5f58015083d8567e8d7fbdd1843"

		target, err := om.ResolveCanonicalStorageTarget(context.Background(), CanonicalStorageTargetRequest{
			Object:         obj,
			AccessURL:      sourceURL,
			PreferChecksum: true,
		})
		if err != nil {
			t.Fatalf("ResolveCanonicalStorageTarget failed: %v", err)
		}
		if target.URL != sourceURL {
			t.Fatalf("expected root scoped physical URL to remain %q, got %q", sourceURL, target.URL)
		}
		if target.Bucket != "gdcdata" {
			t.Fatalf("expected bucket gdcdata, got %q", target.Bucket)
		}
		if target.Key != "00664eeb-830c-5fe4-b48c-054cd9c8e02f/239f8402efd37b62bfb892aa4becb0692b3ca5f58015083d8567e8d7fbdd1843" {
			t.Fatalf("unexpected target key: %q", target.Key)
		}
	})

	t.Run("create bucket scope updates signing cache", func(t *testing.T) {
		mockDB := &testutils.MockDatabase{}
		db := &coreTestDB{MockDatabase: mockDB}
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		if err := om.CreateBucketScope(context.Background(), &models.BucketScope{
			Organization: "calypr",
			ProjectID:    "training",
			Bucket:       "calypr",
			PathPrefix:   "org-root/project-root",
		}); err != nil {
			t.Fatalf("CreateBucketScope failed: %v", err)
		}
		obj := &models.InternalObject{
			Authorizations: map[string][]string{"calypr": {"training"}},
		}
		if _, err := om.SignObjectURL(context.Background(), obj, "s3://calypr/relative-key", urlmanager.SignOptions{Method: "PUT"}); err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if mockDB.GetBucketScopeCalls != 1 {
			t.Fatalf("expected create to populate signing cache without db lookup, got %d lookups", mockDB.GetBucketScopeCalls)
		}
		if want := "s3://calypr/org-root/project-root/relative-key"; um.signURLAccessURL != want {
			t.Fatalf("expected scoped storage url %q, got %q", want, um.signURLAccessURL)
		}
	})

}
