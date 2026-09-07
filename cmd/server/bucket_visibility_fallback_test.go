package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

type bucketFallbackScopeQuery struct {
	ids                   []string
	err                   error
	calls                 int
	organization, project string
}

func (q *bucketFallbackScopeQuery) ListObjectIDsByScope(_ context.Context, organization, project string) ([]string, error) {
	q.calls++
	q.organization = organization
	q.project = project
	return q.ids, q.err
}

type bucketFallbackRecordReader struct {
	records []objects.Record
	err     error
	calls   int
	ids     []string
}

func (r *bucketFallbackRecordReader) GetObject(context.Context, string) (*objects.Record, error) {
	return nil, nil
}

func (r *bucketFallbackRecordReader) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	r.calls++
	r.ids = append([]string(nil), ids...)
	return r.records, r.err
}

func TestBucketVisibilityFallbackScansAndProjectsRows(t *testing.T) {
	scope := &bucketFallbackScopeQuery{ids: []string{"obj-a", "obj-b"}}
	reader := &bucketFallbackRecordReader{records: []objects.Record{
		{
			Id:               "obj-a",
			Authorizations:   map[string][]string{"legacy": {"wrong"}},
			ControlledAccess: &[]string{"/programs/org/projects/project", "/organization/org/project/project", "/organization/org/project/other"},
			AccessMethods: &[]objects.AccessMethod{
				{Type: "s3", AccessUrl: nil},
				{Type: "s3", AccessUrl: &objects.AccessURL{Url: "  "}},
				{Type: "s3", AccessUrl: &objects.AccessURL{Url: " s3://bucket-a/key "}},
				{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket-a/key"}},
				{Type: "https", AccessUrl: &objects.AccessURL{Url: "https://example.test/object"}},
			},
		},
		{
			Id:               "obj-b",
			ControlledAccess: &[]string{"/organization/org/project/second"},
			AccessMethods:    &[]objects.AccessMethod{{Type: "file", AccessUrl: &objects.AccessURL{Url: "/data/b"}}},
		},
	}}

	rows, err := newBucketVisibilityFallback(scope, reader)(context.Background())
	if err != nil {
		t.Fatalf("fallback returned error: %v", err)
	}
	want := []buckets.VisibilityRow{
		{AccessURL: "s3://bucket-a/key", AccessType: "s3", Resource: "/organization/org/project/project"},
		{AccessURL: "s3://bucket-a/key", AccessType: "s3", Resource: "/organization/org/project/other"},
		{AccessURL: "s3://bucket-a/key", AccessType: "s3", Resource: "/organization/org/project/project"},
		{AccessURL: "s3://bucket-a/key", AccessType: "s3", Resource: "/organization/org/project/other"},
		{AccessURL: "https://example.test/object", AccessType: "https", Resource: "/organization/org/project/project"},
		{AccessURL: "https://example.test/object", AccessType: "https", Resource: "/organization/org/project/other"},
		{AccessURL: "/data/b", AccessType: "file", Resource: "/organization/org/project/second"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows = %#v, want %#v", rows, want)
	}
	if scope.calls != 1 || scope.organization != "" || scope.project != "" {
		t.Fatalf("scope query = calls %d, organization %q, project %q; want one raw scan", scope.calls, scope.organization, scope.project)
	}
	if reader.calls != 1 || !reflect.DeepEqual(reader.ids, []string{"obj-a", "obj-b"}) {
		t.Fatalf("bulk hydration = calls %d, ids %v; want one call with raw IDs", reader.calls, reader.ids)
	}
}

func TestBucketVisibilityFallbackFiltersRestrictedObjectsAndHonorsBroadAccess(t *testing.T) {
	resource := "/organization/org/project/allowed"
	scope := &bucketFallbackScopeQuery{ids: []string{"public", "allowed", "denied", "policy-denied"}}
	reader := &bucketFallbackRecordReader{records: []objects.Record{
		{Id: "public", PublicRead: true, ControlledAccess: &[]string{resource}, AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/public"}}}},
		{Id: "allowed", ControlledAccess: &[]string{resource}, AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/allowed"}}}},
		{Id: "denied", ControlledAccess: &[]string{"/organization/org/project/denied"}, AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/denied"}}}},
		{Id: "policy-denied", PublicReadPolicyKnown: true, AccessMethods: &[]objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://bucket/policy-denied"}}}},
	}}
	fallback := newBucketVisibilityFallback(scope, reader)

	restricted := access.NewSession("local")
	restricted.SetAuthorizations(nil, map[string]map[string]bool{resource: {"read": true}}, true)
	rows, err := fallback(access.WithSession(context.Background(), restricted))
	if err != nil {
		t.Fatalf("restricted fallback returned error: %v", err)
	}
	if got := visibilityRowURLs(rows); !reflect.DeepEqual(got, []string{"s3://bucket/public", "s3://bucket/allowed"}) {
		t.Fatalf("restricted URLs = %v, want public and authorized rows", got)
	}

	broad := access.NewSession("local")
	broad.SetAuthorizations(nil, map[string]map[string]bool{"/programs": {"read": true}}, true)
	rows, err = fallback(access.WithSession(context.Background(), broad))
	if err != nil {
		t.Fatalf("broad fallback returned error: %v", err)
	}
	if got := visibilityRowURLs(rows); !reflect.DeepEqual(got, []string{"s3://bucket/public", "s3://bucket/allowed", "s3://bucket/denied"}) {
		t.Fatalf("broad URLs = %v, want all rows with resources", got)
	}
}

func TestBucketVisibilityFallbackPropagatesScanAndHydrationErrors(t *testing.T) {
	scanErr := errors.New("scan failed")
	reader := &bucketFallbackRecordReader{}
	fallback := newBucketVisibilityFallback(&bucketFallbackScopeQuery{err: scanErr}, reader)
	if _, err := fallback(context.Background()); !errors.Is(err, scanErr) {
		t.Fatalf("scan error = %v, want %v", err, scanErr)
	}
	if reader.calls != 0 {
		t.Fatalf("hydration calls = %d after scan failure, want 0", reader.calls)
	}

	hydrateErr := errors.New("hydration failed")
	fallback = newBucketVisibilityFallback(&bucketFallbackScopeQuery{ids: []string{"obj"}}, &bucketFallbackRecordReader{err: hydrateErr})
	if _, err := fallback(context.Background()); !errors.Is(err, hydrateErr) {
		t.Fatalf("hydration error = %v, want %v", err, hydrateErr)
	}
}

func TestBucketVisibilityFallbackRejectsMissingObjectCapabilities(t *testing.T) {
	fallback := newBucketVisibilityFallback(nil, nil)
	if _, err := fallback(context.Background()); !errors.Is(err, errBucketVisibilityScopeQuery) {
		t.Fatalf("missing scope error = %v, want %v", err, errBucketVisibilityScopeQuery)
	}

	fallback = newBucketVisibilityFallback(&bucketFallbackScopeQuery{ids: []string{"obj"}}, nil)
	if _, err := fallback(context.Background()); !errors.Is(err, errBucketVisibilityRecordReader) {
		t.Fatalf("missing reader error = %v, want %v", err, errBucketVisibilityRecordReader)
	}
}

func visibilityRowURLs(rows []buckets.VisibilityRow) []string {
	urls := make([]string, 0, len(rows))
	for _, row := range rows {
		urls = append(urls, row.AccessURL)
	}
	return urls
}
