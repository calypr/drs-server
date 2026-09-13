package store_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	postgresdb "github.com/calypr/syfon/internal/persistence/postgres"
	sqlitedb "github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	"github.com/google/uuid"
)

func TestQueryConstructionCharacterization(t *testing.T) {
	backends := []struct {
		name string
		open func(*testing.T) *store.Store
	}{
		{
			name: "sqlite",
			open: func(t *testing.T) *store.Store {
				db, err := sqlitedb.NewSqliteDB(":memory:", nil)
				if err != nil {
					t.Fatalf("open sqlite test store: %v", err)
				}
				t.Cleanup(func() { _ = db.Close() })
				return db
			},
		},
		{
			name: "postgres",
			open: func(t *testing.T) *store.Store {
				dsn := os.Getenv("SYFON_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("SYFON_TEST_POSTGRES_DSN is not configured")
				}
				db, err := postgresdb.NewPostgresDB(dsn, nil)
				if err != nil {
					t.Fatalf("open postgres test store: %v", err)
				}
				t.Cleanup(func() { _ = db.Close() })
				return db
			},
		},
	}

	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.open(t)
			ctx := context.Background()
			suffix := uuid.NewString()
			sha := fmt.Sprintf("%x", sha256.Sum256([]byte(suffix)))
			shaQuery := "SHA256:" + strings.ToUpper(sha)
			objectA, objectB, objectC := "bulk-a-"+suffix, "bulk-b-"+suffix, "bulk-c-"+suffix
			url := "s3://bucket/query-characterization/" + suffix
			const p1, p2 = "/programs/org/projects/p1", "/programs/org/projects/p2"
			generic := "ETAG-" + suffix
			records := []drs.DrsObject{
				{Id: objectA, ControlledAccess: &[]string{p1}, Checksums: []drs.Checksum{{Type: "sha256", Checksum: sha}}},
				{Id: objectB, ControlledAccess: &[]string{p2}, Checksums: []drs.Checksum{{Type: "etag", Checksum: generic}}},
				{Id: objectC},
			}
			now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			for i := range records {
				records[i].CreatedTime, records[i].UpdatedTime = now, &now
				records[i].AccessMethods = &[]drs.AccessMethod{{Type: drs.AccessMethodTypeS3, AccessUrl: &drs.AccessURL{Url: url}}}
			}
			if err := db.RegisterObjects(ctx, records); err != nil {
				t.Fatalf("RegisterObjects: %v", err)
			}
			t.Cleanup(func() {
				for _, id := range []string{objectA, objectB, objectC} {
					_ = db.DeleteObject(ctx, id)
				}
			})
			matches, err := db.GetObjectsByChecksums(ctx, []string{shaQuery, generic, strings.ToLower(generic), "missing"})
			if err != nil {
				t.Fatalf("GetObjectsByChecksums: %v", err)
			}
			if got := objectIDs(matches[shaQuery]); !slices.Equal(got, []string{objectA}) {
				t.Fatalf("SHA256 lookup = %v, want [%s]", got, objectA)
			}
			if got := objectIDs(matches[generic]); !slices.Equal(got, []string{objectB}) {
				t.Fatalf("generic lookup = %v, want [%s]", got, objectB)
			}
			if _, ok := matches[strings.ToLower(generic)]; ok {
				t.Fatalf("generic lookup changed case sensitivity: %#v", matches[strings.ToLower(generic)])
			}

			object, err := db.GetObject(ctx, objectA)
			if err != nil {
				t.Fatalf("GetObject by ID: %v", err)
			}
			if object.Id != objectA || object.Checksums == nil || len(object.Checksums) != 1 || object.Checksums[0].Checksum != sha {
				t.Fatalf("GetObject by ID = %#v", object)
			}

			bulk, err := db.GetBulkObjects(ctx, []string{objectB, objectA, objectB, "missing"})
			if err != nil {
				t.Fatalf("GetBulkObjects: %v", err)
			}
			if got := objectIDs(bulk); !slices.Equal(got, []string{objectB, objectA}) {
				t.Fatalf("bulk ID lookup = %v, want [%s %s]", got, objectB, objectA)
			}

			urlCases := []struct {
				name                         string
				organization, project, after string
				resources                    []string
				includeUnscoped, restrict    bool
				limit, offset                int
				want                         []string
			}{
				{name: "scoped", organization: "org", project: "p1", limit: 10, want: []string{objectA}},
				{name: "resource", resources: []string{p1}, restrict: true, limit: 10, want: []string{objectA}},
				{name: "resource-plus-unscoped", resources: []string{p1}, includeUnscoped: true, restrict: true, limit: 10, want: []string{objectA, objectC}},
				{name: "resource-plus-unscoped-page", resources: []string{p1}, includeUnscoped: true, restrict: true, limit: 1, offset: 1, want: []string{objectC}},
				{name: "cursor", after: objectA, limit: 1, want: []string{objectB}},
				{name: "unscoped-only", includeUnscoped: true, restrict: true, limit: 10, want: []string{objectC}},
				{name: "no-readable-resources", restrict: true, limit: 10, want: []string{}},
			}
			for _, testCase := range urlCases {
				t.Run("URL/"+testCase.name, func(t *testing.T) {
					ids, err := db.ListObjectIDsPageByURL(ctx, url, testCase.organization, testCase.project, testCase.after, testCase.limit, testCase.offset, testCase.resources, testCase.includeUnscoped, testCase.restrict)
					if err != nil {
						t.Fatalf("ListObjectIDsPageByURL: %v", err)
					}
					if !slices.Equal(ids, testCase.want) {
						t.Fatalf("ListObjectIDsPageByURL = %v, want %v", ids, testCase.want)
					}
				})
			}
		})
	}
}

func objectIDs(objects []drs.DrsObject) []string {
	ids := make([]string, 0, len(objects))
	for _, object := range objects {
		ids = append(ids, object.Id)
	}
	return ids
}
