package copyproject

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/cmd/projectcopy"
)

func TestStoragePathRemapping(t *testing.T) {
	pathValue := func(value string) *string { return &value }

	t.Run("organization replaces matching suffix", func(t *testing.T) {
		got, ok := remapOrgScopePath(&bucketapi.BucketScopeResponse{Path: pathValue("s3://source/base/source-org")}, "source-org", "target", "target-org")
		if !ok || got != "s3://target/base/target-org" {
			t.Fatalf("remapOrgScopePath = %q, %v", got, ok)
		}
	})

	t.Run("organization appends when suffix differs", func(t *testing.T) {
		got, ok := remapOrgScopePath(&bucketapi.BucketScopeResponse{Path: pathValue("s3://source/base")}, "source-org", "target", "target-org")
		if !ok || got != "s3://target/base/target-org" {
			t.Fatalf("remapOrgScopePath = %q, %v", got, ok)
		}
	})

	for _, tc := range []struct {
		name  string
		scope *bucketapi.BucketScopeResponse
		want  bool
	}{
		{name: "nil scope", scope: nil},
		{name: "nil path", scope: &bucketapi.BucketScopeResponse{}},
		{name: "blank path", scope: &bucketapi.BucketScopeResponse{Path: pathValue("  ")}},
		{name: "unknown provider", scope: &bucketapi.BucketScopeResponse{Path: pathValue("ftp://source/base")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := remapOrgScopePath(tc.scope, "source-org", "target", "target-org")
			if ok != tc.want || got != "" {
				t.Fatalf("remapOrgScopePath = %q, %v; want empty, %v", got, ok, tc.want)
			}
		})
	}

	srcScope := projectcopy.Scope{Organization: "source-org", Project: "source-project"}
	dstScope := projectcopy.Scope{Organization: "target-org", Project: "target-project"}
	srcOrg := &bucketapi.BucketScopeResponse{Organization: srcScope.Organization, Path: pathValue("s3://source/organizations/source-org")}
	srcProject := &bucketapi.BucketScopeResponse{Organization: srcScope.Organization, ProjectId: srcScope.Project, Path: pathValue("s3://source/organizations/source-org/projects/source-project")}

	t.Run("project preserves relative path under destination organization", func(t *testing.T) {
		dstOrg := &bucketapi.BucketScopeResponse{Organization: dstScope.Organization, Path: pathValue("s3://existing/organizations/target-org")}
		got, ok := remapProjectScopePath(srcProject, srcOrg, dstOrg, srcScope, dstScope, "target")
		if !ok || got != "s3://target/organizations/target-org/projects/source-project" {
			t.Fatalf("remapProjectScopePath = %q, %v", got, ok)
		}
	})

	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "adjacent organization and project suffix", path: "s3://source/source-org/source-project", want: "s3://target/target-org/target-project"},
		{name: "project suffix only", path: "s3://source/custom/source-project", want: "s3://target/custom/target-project"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := &bucketapi.BucketScopeResponse{Path: pathValue(tc.path)}
			got, ok := remapProjectScopePath(project, nil, nil, srcScope, dstScope, "target")
			if !ok || got != tc.want {
				t.Fatalf("remapProjectScopePath = %q, %v; want %q, true", got, ok, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		name    string
		project *bucketapi.BucketScopeResponse
		want    bool
	}{
		{name: "nil project", project: nil},
		{name: "blank project path", project: &bucketapi.BucketScopeResponse{Path: pathValue(" ")}},
		{name: "invalid project path", project: &bucketapi.BucketScopeResponse{Path: pathValue("ftp://source/organizations/source-org/projects/source-project")}},
		{name: "unrelated project path", project: &bucketapi.BucketScopeResponse{Path: pathValue("s3://source/other/project")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := remapProjectScopePath(tc.project, nil, nil, srcScope, dstScope, "target")
			if ok != tc.want || got != "" {
				t.Fatalf("remapProjectScopePath = %q, %v; want empty, %v", got, ok, tc.want)
			}
		})
	}
}

func TestStoragePathHelpers(t *testing.T) {
	pathValue := func(value string) *string { return &value }

	if got := defaultOrgScopePath("bucket", "org"); got != "s3://bucket/organizations/org" {
		t.Fatalf("defaultOrgScopePath = %q", got)
	}
	if got := defaultProjectScopePath("bucket", "org", "project"); got != "s3://bucket/organizations/org/projects/project" {
		t.Fatalf("defaultProjectScopePath = %q", got)
	}
	if got := pathOrEmpty(&bucketapi.BucketScopeResponse{Path: pathValue("  s3://bucket/path  ")}); got != "s3://bucket/path" {
		t.Fatalf("pathOrEmpty = %q", got)
	}
	if got := pathOrEmpty(nil); got != "" {
		t.Fatalf("pathOrEmpty(nil) = %q", got)
	}

	parsed, segments, ok := parseStorageURL(" s3://bucket/one/two/ ")
	if !ok || parsed.Host != "bucket" || !reflect.DeepEqual(segments, []string{"one", "two"}) {
		t.Fatalf("parseStorageURL = %#v, %#v, %v", parsed, segments, ok)
	}
	if parsed, segments, ok := parseStorageURL("s3://bucket"); !ok || parsed.Host != "bucket" || segments != nil {
		t.Fatalf("parseStorageURL root = %#v, %#v, %v", parsed, segments, ok)
	}
	for _, raw := range []string{"", "relative/path", "ftp://bucket/path", "://bad"} {
		if _, _, ok := parseStorageURL(raw); ok {
			t.Fatalf("parseStorageURL(%q) unexpectedly succeeded", raw)
		}
	}

	left, _ := url.Parse("S3://Bucket/path")
	right, _ := url.Parse("s3://bucket/other")
	if !sameURLRoot(left, right) || sameURLRoot(left, nil) {
		t.Fatalf("sameURLRoot did not compare scheme and host as expected")
	}
	if !hasPathPrefix([]string{"one", "two"}, []string{"one"}) || hasPathPrefix([]string{"one"}, []string{"one", "two"}) || hasPathPrefix([]string{"one", "different"}, []string{"one", "two"}) {
		t.Fatalf("hasPathPrefix returned an unexpected result")
	}

	if got := storageSchemeFromURL("https://bucket/path"); got != "https" {
		t.Fatalf("storageSchemeFromURL = %q", got)
	}
	if got := storageSchemeFromURL("relative"); got != "s3" {
		t.Fatalf("storageSchemeFromURL fallback = %q", got)
	}
	for _, tc := range []struct {
		name, projectPath, bucket, key, want string
	}{
		{name: "empty project path", bucket: "bucket", key: "key", want: "s3://bucket/key"},
		{name: "invalid project path", projectPath: "not-a-url", bucket: "bucket", key: "key", want: "s3://bucket/key"},
		{name: "scoped key", projectPath: "gs://bucket/prefix", bucket: "target", key: "/key/", want: "gs://bucket/prefix/key"},
		{name: "empty key", projectPath: "s3://bucket/prefix", bucket: "target", want: "s3://bucket/prefix"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopedObjectURL(tc.projectPath, tc.bucket, tc.key); got != tc.want {
				t.Fatalf("scopedObjectURL = %q; want %q", got, tc.want)
			}
		})
	}

	if org, project := pathScope("/organization/org/project/project"); org != "org" || project != "project" {
		t.Fatalf("pathScope = %q/%q", org, project)
	}
	if org, project := pathScope("invalid"); org != "" || project != "" {
		t.Fatalf("pathScope invalid = %q/%q", org, project)
	}
	if !sameServerURL(" https://example/ ", "https://example") || sameServerURL("https://one", "https://two") {
		t.Fatalf("sameServerURL returned an unexpected result")
	}
}

func TestPreferredUploadKey(t *testing.T) {
	accessMethods := []drsapi.AccessMethod{
		{AccessUrl: nil},
		{AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: " "}},
		{AccessUrl: &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Url: "s3://bucket/from-access-method"}},
	}
	if got := preferredUploadKey(&accessMethods, " sha256-value ", "name.txt", "/tmp/temp"); got != "sha256-value" {
		t.Fatalf("checksum upload key = %q", got)
	}
	if got := preferredUploadKey(&accessMethods, "", "", "/tmp/temp"); got != "from-access-method" {
		t.Fatalf("access method upload key = %q", got)
	}
	if got := preferredUploadKey(nil, "", "name.txt", "/tmp/temp"); got != "name.txt" {
		t.Fatalf("filename upload key = %q", got)
	}
	if got := preferredUploadKey(nil, "", "", "/tmp/temp"); got != "temp" {
		t.Fatalf("temporary path upload key = %q", got)
	}
}
