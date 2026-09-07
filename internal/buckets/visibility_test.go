package buckets

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/syfon/internal/access"
)

func TestListVisibleBucketsUsesOptimizedQueryWithoutFallback(t *testing.T) {
	explicit := mustResource(t, "org", "explicit")
	other := mustResource(t, "org", "other")
	query := &fakeVisibilityQuery{rows: []VisibilityRow{
		{AccessURL: "s3://bucket-a/object", Resource: explicit},
		{AccessURL: "s3://bucket-b/object", Resource: explicit},
		{AccessURL: "gs://bucket-b/object", Resource: other},
	}}
	fallbackCalled := false
	service, _, _ := newFakeService(
		[]Credential{
			{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"},
			{CredentialID: "id-b", Bucket: "bucket-b", Provider: "gcs"},
		},
		[]Scope{{CredentialID: "id-a", Organization: "org", ProjectID: "explicit"}},
		query,
		func(context.Context) ([]VisibilityRow, error) {
			fallbackCalled = true
			return nil, errors.New("fallback must not run")
		},
		nil,
	)

	got, err := service.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListVisibleBuckets: %v", err)
	}
	want := map[string]VisibleBucket{
		"id-a": {Credential: Credential{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"}, Programs: []string{explicit}},
		"id-b": {Credential: Credential{CredentialID: "id-b", Bucket: "bucket-b", Provider: "gcs"}, Programs: []string{other}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("optimized visible buckets=%+v, want %+v", got, want)
	}
	if fallbackCalled || query.calls != 1 {
		t.Fatalf("dispatch query calls=%d fallbackCalled=%v", query.calls, fallbackCalled)
	}
	if !query.includeUnscoped || query.restrictToResources {
		t.Fatalf("query flags includeUnscoped=%v restrictToResources=%v", query.includeUnscoped, query.restrictToResources)
	}
}

func TestListVisibleBucketsUsesFallbackWhenOptimizationIsAbsent(t *testing.T) {
	explicit := mustResource(t, "org", "explicit")
	public := mustResource(t, "org", "public")
	fallbackCalls := 0
	service, _, _ := newFakeService(
		[]Credential{{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"}},
		[]Scope{{CredentialID: "id-a", Organization: "org", ProjectID: "explicit"}},
		nil,
		func(context.Context) ([]VisibilityRow, error) {
			fallbackCalls++
			return []VisibilityRow{
				{AccessURL: "s3://bucket-a/object", Resource: explicit},
				{AccessURL: "s3://bucket-a/other", Resource: public},
			}, nil
		},
		nil,
	)

	got, err := service.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListVisibleBuckets fallback: %v", err)
	}
	want := map[string]VisibleBucket{
		"id-a": {Credential: Credential{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"}, Programs: []string{
			explicit,
			public,
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback visible buckets=%+v, want %+v", got, want)
	}
	if fallbackCalls != 1 {
		t.Fatalf("fallback calls=%d, want 1", fallbackCalls)
	}
}

func TestListVisibleBucketsPreservesDistinctOptimizedAndFallbackContracts(t *testing.T) {
	explicit := mustResource(t, "org", "explicit")
	public := mustResource(t, "org", "public")
	credentials := []Credential{
		{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"},
		{CredentialID: "id-b", Bucket: "bucket-b", Provider: "gcs"},
	}
	scopes := []Scope{{CredentialID: "id-a", Organization: "org", ProjectID: "explicit"}}

	optimized, _, _ := newFakeService(credentials, scopes, &fakeVisibilityQuery{rows: []VisibilityRow{
		{AccessURL: "s3://bucket-a/object", Resource: explicit},
		{AccessURL: "gs://bucket-b/object", Resource: public},
	}}, nil, nil)
	optimizedGot, err := optimized.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("optimized visibility: %v", err)
	}
	optimizedWant := map[string][]string{
		"id-a": {explicit},
		"id-b": {public},
	}
	if got := visiblePrograms(optimizedGot); !reflect.DeepEqual(got, optimizedWant) {
		t.Fatalf("optimized programs=%v, want %v", got, optimizedWant)
	}

	fallback, _, _ := newFakeService(credentials, scopes, nil, func(context.Context) ([]VisibilityRow, error) {
		return []VisibilityRow{
			{AccessURL: "s3://bucket-a/object", Resource: explicit},
			{AccessURL: "s3://bucket-a/object", Resource: public},
		}, nil
	}, nil)
	fallbackGot, err := fallback.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("fallback visibility: %v", err)
	}
	fallbackWant := map[string][]string{
		"id-a": {explicit, public},
		"id-b": nil,
	}
	if got := visiblePrograms(fallbackGot); !reflect.DeepEqual(got, fallbackWant) {
		t.Fatalf("fallback programs=%v, want %v", got, fallbackWant)
	}
}

func TestListVisibleBucketsOptimizedAuthorizationArguments(t *testing.T) {
	query := &fakeVisibilityQuery{}
	service, _, _ := newFakeService([]Credential{{CredentialID: "id-a", Bucket: "bucket-a"}}, nil, query, nil, nil)

	local, err := service.ListVisibleBuckets(context.Background())
	if err != nil || len(local) != 1 {
		t.Fatalf("local visibility=(%v,%v)", local, err)
	}
	if query.restrictToResources {
		t.Fatal("local mode unexpectedly restricted optimized visibility")
	}

	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{"/programs": {"read": true}}, true)
	_, err = service.ListVisibleBuckets(access.WithSession(context.Background(), session))
	if err != nil {
		t.Fatalf("authorized Gen3 visibility: %v", err)
	}
	if query.restrictToResources {
		t.Fatal("broad /programs authorization should bypass optimized restriction")
	}

	session = access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{"/programs/other": {"read": true}}, true)
	_, err = service.ListVisibleBuckets(access.WithSession(context.Background(), session))
	if err != nil {
		t.Fatalf("restricted Gen3 visibility: %v", err)
	}
	if !query.restrictToResources {
		t.Fatal("narrow authorization should restrict optimized visibility")
	}
}

func TestListVisibleBucketsFiltersUnauthorizedExplicitScopes(t *testing.T) {
	allowed := mustResource(t, "org", "allowed")
	denied := mustResource(t, "org", "denied")
	query := &fakeVisibilityQuery{}
	service, _, _ := newFakeService(
		[]Credential{{CredentialID: "id-a", Bucket: "bucket-a"}},
		[]Scope{
			{CredentialID: "id-a", Organization: "org", ProjectID: "allowed"},
			{CredentialID: "id-a", Organization: "org", ProjectID: "denied"},
		},
		query, nil, nil,
	)
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{allowed: {"read": true}}, true)
	got, err := service.ListVisibleBuckets(access.WithSession(context.Background(), session))
	if err != nil {
		t.Fatalf("ListVisibleBuckets: %v", err)
	}
	if gotPrograms := got["id-a"].Programs; !reflect.DeepEqual(gotPrograms, []string{allowed}) {
		t.Fatalf("authorized programs=%v, want [%s]", gotPrograms, allowed)
	}
	if denied == allowed {
		t.Fatal("test resources must differ")
	}
}

func TestListVisibleBucketsPreservesBranchSpecificScopeAuthorization(t *testing.T) {
	explicit := mustResource(t, "org", "explicit")
	credentials := []Credential{{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"}}
	scopes := []Scope{{CredentialID: "id-a", Organization: "org", ProjectID: "explicit"}}

	for _, broadResource := range []string{"/programs", "/data_file"} {
		t.Run(broadResource, func(t *testing.T) {
			session := access.NewSession("gen3")
			session.AuthHeaderPresent = true
			session.SetAuthorizations(nil, map[string]map[string]bool{broadResource: {"read": true}}, true)
			ctx := access.WithSession(context.Background(), session)

			query := &fakeVisibilityQuery{}
			optimized, _, _ := newFakeService(credentials, scopes, query, nil, nil)
			optimizedGot, err := optimized.ListVisibleBuckets(ctx)
			if err != nil {
				t.Fatalf("optimized visibility: %v", err)
			}
			if got := optimizedGot["id-a"].Programs; !reflect.DeepEqual(got, []string{explicit}) {
				t.Fatalf("optimized programs=%v, want [%s]", got, explicit)
			}
			if query.restrictToResources {
				t.Fatal("broad authorization unexpectedly restricted optimized visibility")
			}

			fallback, _, _ := newFakeService(credentials, scopes, nil, func(context.Context) ([]VisibilityRow, error) {
				return nil, nil
			}, nil)
			fallbackGot, err := fallback.ListVisibleBuckets(ctx)
			if err != nil {
				t.Fatalf("fallback visibility: %v", err)
			}
			if got := fallbackGot["id-a"].Programs; got != nil {
				t.Fatalf("fallback programs=%v, want no programs", got)
			}
		})
	}
}

func TestListVisibleBucketsMapsProviderURLsAndFilePaths(t *testing.T) {
	s3Resource := mustResource(t, "org", "s3")
	gcsResource := mustResource(t, "org", "gcs")
	azureResource := mustResource(t, "org", "azure")
	fileResource := mustResource(t, "org", "file")
	credentials := []Credential{
		{CredentialID: "s3-id", Bucket: "s3-bucket", Provider: "s3"},
		{CredentialID: "gcs-id", Bucket: "gcs-bucket", Provider: "gcs"},
		{CredentialID: "azure-id", Bucket: "azure-bucket", Provider: "azure"},
		{CredentialID: "file-id", Bucket: "file-bucket", Provider: "file", Endpoint: "/tmp/syfon-file-root"},
	}
	query := &fakeVisibilityQuery{rows: []VisibilityRow{
		{AccessURL: "s3://s3-bucket/object", Resource: s3Resource},
		{AccessURL: "gs://gcs-bucket/object", Resource: gcsResource},
		{AccessURL: "azblob://azure-bucket/object", Resource: azureResource},
		{AccessURL: "/tmp/syfon-file-root/path/object", Resource: fileResource},
	}}
	service, _, _ := newFakeService(credentials, nil, query, nil, nil)
	got, err := service.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListVisibleBuckets: %v", err)
	}
	for id, resource := range map[string]string{
		"s3-id":    s3Resource,
		"gcs-id":   gcsResource,
		"azure-id": azureResource,
		"file-id":  fileResource,
	} {
		if got[id].Programs == nil || !reflect.DeepEqual(got[id].Programs, []string{resource}) {
			t.Fatalf("%s programs=%v, want [%s]", id, got[id].Programs, resource)
		}
	}
}

func TestListVisibleBucketsPropagatesVisibilitySourceErrors(t *testing.T) {
	want := errors.New("visibility unavailable")
	query := &fakeVisibilityQuery{err: want}
	service, _, _ := newFakeService([]Credential{{Bucket: "bucket-a"}}, nil, query, nil, nil)
	if _, err := service.ListVisibleBuckets(context.Background()); !errors.Is(err, want) {
		t.Fatalf("optimized error=%v, want %v", err, want)
	}

	service, _, _ = newFakeService([]Credential{{Bucket: "bucket-a"}}, nil, nil, func(context.Context) ([]VisibilityRow, error) {
		return nil, want
	}, nil)
	if _, err := service.ListVisibleBuckets(context.Background()); !errors.Is(err, want) {
		t.Fatalf("fallback error=%v, want %v", err, want)
	}
}

func TestListVisibleBucketsSortsProgramsWithinEachCredential(t *testing.T) {
	service, _, _ := newFakeService(
		[]Credential{{CredentialID: "id-a", Bucket: "bucket-a"}},
		nil,
		&fakeVisibilityQuery{rows: []VisibilityRow{
			{AccessURL: "s3://bucket-a/z", Resource: "/programs/org/project/z"},
			{AccessURL: "s3://bucket-a/a", Resource: "/programs/org/project/a"},
		}},
		nil,
		nil,
	)
	got, err := service.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("ListVisibleBuckets: %v", err)
	}
	want := []string{"/programs/org/project/a", "/programs/org/project/z"}
	if !reflect.DeepEqual(got["id-a"].Programs, want) {
		t.Fatalf("programs=%v, want %v", got["id-a"].Programs, want)
	}
}

func TestVisibleToCallerMatchesPhysicalAndCredentialAliases(t *testing.T) {
	visible := map[string]VisibleBucket{
		"credential-id": {Credential: Credential{CredentialID: "credential-id", Bucket: "physical-bucket"}},
	}
	for _, tc := range []struct {
		name         string
		bucket       string
		credentialID string
		want         bool
	}{
		{name: "physical bucket", bucket: "PHYSICAL-BUCKET", want: true},
		{name: "map credential key", credentialID: "CREDENTIAL-ID", want: true},
		{name: "credential field", credentialID: "credential-id", want: true},
		{name: "unknown", bucket: "other", credentialID: "other", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := VisibleToCaller(visible, tc.bucket, tc.credentialID); got != tc.want {
				t.Fatalf("VisibleToCaller()=%v, want %v", got, tc.want)
			}
		})
	}
}

func visiblePrograms(visible map[string]VisibleBucket) map[string][]string {
	result := make(map[string][]string, len(visible))
	for key, bucket := range visible {
		result[key] = append([]string(nil), bucket.Programs...)
		sort.Strings(result[key])
	}
	return result
}

func mustResource(t *testing.T, organization, project string) string {
	t.Helper()
	resource, err := access.ResourcePath(organization, project)
	if err != nil {
		t.Fatalf("ResourcePath(%q,%q): %v", organization, project, err)
	}
	return resource
}
