package buckets

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
)

func TestListVisibleBucketsUsesVisibilityQuery(t *testing.T) {
	explicit := mustResource(t, "org", "explicit")
	other := mustResource(t, "org", "other")
	query := &fakeVisibilityQuery{rows: []VisibilityRow{
		{AccessURL: "s3://bucket-a/object", Resource: explicit},
		{AccessURL: "s3://bucket-b/object", Resource: explicit},
		{AccessURL: "gs://bucket-b/object", Resource: other},
	}}
	service, _, _ := newFakeService(
		[]Credential{
			{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"},
			{CredentialID: "id-b", Bucket: "bucket-b", Provider: "gcs"},
		},
		[]Scope{{CredentialID: "id-a", Organization: "org", ProjectID: "explicit"}},
		query, nil,
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
		t.Fatalf("visible buckets=%+v, want %+v", got, want)
	}
	if query.calls != 1 {
		t.Fatalf("visibility query calls=%d, want 1", query.calls)
	}
	if !query.includeUnscoped || query.restrictToResources {
		t.Fatalf("query flags includeUnscoped=%v restrictToResources=%v", query.includeUnscoped, query.restrictToResources)
	}
}

func TestListVisibleBucketsPreservesCredentialContracts(t *testing.T) {
	explicit := mustResource(t, "org", "explicit")
	public := mustResource(t, "org", "public")
	credentials := []Credential{
		{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"},
		{CredentialID: "id-b", Bucket: "bucket-b", Provider: "gcs"},
	}
	scopes := []Scope{{CredentialID: "id-a", Organization: "org", ProjectID: "explicit"}}

	service, _, _ := newFakeService(credentials, scopes, &fakeVisibilityQuery{rows: []VisibilityRow{
		{AccessURL: "s3://bucket-a/object", Resource: explicit},
		{AccessURL: "gs://bucket-b/object", Resource: public},
	}}, nil)
	got, err := service.ListVisibleBuckets(context.Background())
	if err != nil {
		t.Fatalf("visibility: %v", err)
	}
	want := map[string][]string{
		"id-a": {explicit},
		"id-b": {public},
	}
	if programs := visiblePrograms(got); !reflect.DeepEqual(programs, want) {
		t.Fatalf("programs=%v, want %v", programs, want)
	}
}

func TestListVisibleBucketsAuthorizationArguments(t *testing.T) {
	query := &fakeVisibilityQuery{}
	service, _, _ := newFakeService([]Credential{
		{CredentialID: "id-a", Bucket: "bucket-a"},
		{CredentialID: "id-b", Bucket: "bucket-b"},
	}, nil, query, nil)

	local, err := service.ListVisibleBuckets(context.Background())
	if err != nil || len(local) != 2 {
		t.Fatalf("local visibility=(%v,%v)", local, err)
	}
	if query.restrictToResources {
		t.Fatal("local mode unexpectedly restricted visibility")
	}

	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{"/programs": {"read": true}}, true)
	broad, err := service.ListVisibleBuckets(access.WithSession(context.Background(), session))
	if err != nil {
		t.Fatalf("authorized Gen3 visibility: %v", err)
	}
	if got := len(broad); got != 2 {
		t.Fatalf("broad authorization should preserve configured credentials, got %d", got)
	}
	if query.restrictToResources {
		t.Fatal("broad /programs authorization should bypass visibility restriction")
	}

	session = access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{"/programs/other": {"read": true}}, true)
	narrow, err := service.ListVisibleBuckets(access.WithSession(context.Background(), session))
	if err != nil {
		t.Fatalf("restricted Gen3 visibility: %v", err)
	}
	if len(narrow) != 0 {
		t.Fatalf("restricted authorization should omit credentials without programs, got %d", len(narrow))
	}
	if !query.restrictToResources {
		t.Fatal("narrow authorization should restrict visibility")
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
		query, nil,
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

func TestListVisibleBucketsRestrictedDropsCredentialsWithoutAuthorizedPrograms(t *testing.T) {
	allowed := mustResource(t, "org", "allowed")
	query := &fakeVisibilityQuery{rows: []VisibilityRow{{AccessURL: "s3://bucket-a/object", Resource: allowed}}}
	service, _, _ := newFakeService(
		[]Credential{
			{CredentialID: "id-a", Bucket: "bucket-a", Provider: "s3"},
			{CredentialID: "id-b", Bucket: "bucket-b", Provider: "s3"},
		},
		nil,
		query,
		nil,
	)
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{allowed: {"read": true}}, true)

	got, err := service.ListVisibleBuckets(access.WithSession(context.Background(), session))
	if err != nil {
		t.Fatalf("ListVisibleBuckets: %v", err)
	}
	if _, ok := got["id-b"]; ok {
		t.Fatalf("restricted visibility exposed unauthorized credential: %+v", got["id-b"])
	}
	if gotPrograms := got["id-a"].Programs; !reflect.DeepEqual(gotPrograms, []string{allowed}) {
		t.Fatalf("authorized programs=%v, want [%s]", gotPrograms, allowed)
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
			service, _, _ := newFakeService(credentials, scopes, query, nil)
			got, err := service.ListVisibleBuckets(ctx)
			if err != nil {
				t.Fatalf("visibility: %v", err)
			}
			if programs := got["id-a"].Programs; !reflect.DeepEqual(programs, []string{explicit}) {
				t.Fatalf("programs=%v, want [%s]", programs, explicit)
			}
			if query.restrictToResources {
				t.Fatal("broad authorization unexpectedly restricted visibility")
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
	service, _, _ := newFakeService(credentials, nil, query, nil)
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
	service, _, _ := newFakeService([]Credential{{Bucket: "bucket-a"}}, nil, query, nil)
	if _, err := service.ListVisibleBuckets(context.Background()); !errors.Is(err, want) {
		t.Fatalf("visibility error=%v, want %v", err, want)
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

func TestVisibleScopeOperationsPreserveTheirDistinctReadPolicies(t *testing.T) {
	allowed := mustResource(t, "Org", "Project")
	root := mustResource(t, "Org", "")
	service, _, _ := newFakeService(
		[]Credential{
			{CredentialID: "gcs-id", Bucket: "physical-gcs", Provider: "gcs"},
			{CredentialID: "s3-id", Bucket: "physical-s3", Provider: "s3"},
		},
		[]Scope{
			{CredentialID: "gcs-id", Bucket: "physical-gcs", Organization: "Org", ProjectID: "Project", PathPrefix: "data"},
			{CredentialID: "s3-id", Bucket: "physical-s3", Organization: "Org", ProjectID: "", PathPrefix: ""},
			{CredentialID: "gcs-id", Bucket: "physical-gcs", Organization: "Other", ProjectID: "Project", PathPrefix: "secret"},
		},
		&fakeVisibilityQuery{}, nil,
	)
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, map[string]map[string]bool{
		allowed: {"read": true},
		root:    {"read": true},
	}, true)
	ctx := access.WithSession(context.Background(), session)

	scopes, err := service.ListVisibleScopes(ctx, "PHYSICAL-GCS")
	if err != nil {
		t.Fatalf("ListVisibleScopes: %v", err)
	}
	if !reflect.DeepEqual(scopes, []VisibleScope{{Organization: "Org", ProjectID: "Project", Path: "gs://physical-gcs/data"}}) {
		t.Fatalf("visible bucket scopes=%+v", scopes)
	}

	projectScopes, err := service.ListVisibleProjectScopes(ctx, "org", "project")
	if err != nil {
		t.Fatalf("ListVisibleProjectScopes: %v", err)
	}
	want := []VisibleProjectScope{
		{Bucket: "physical-gcs", Organization: "org", ProjectID: "Project", Path: "gs://physical-gcs/data"},
		{Bucket: "physical-s3", Organization: "org", ProjectID: "", Path: "s3://physical-s3"},
	}
	if !reflect.DeepEqual(projectScopes, want) {
		t.Fatalf("visible project scopes=%+v, want %+v", projectScopes, want)
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
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		t.Fatalf("ResourcePath(%q,%q): %v", organization, project, err)
	}
	return resource
}
