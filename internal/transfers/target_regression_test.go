package transfers

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
)

type targetAccessSpy struct {
	requests []storage.AccessRequest
}

func (s *targetAccessSpy) Access(_ context.Context, request storage.AccessRequest) (storage.Access, error) {
	s.requests = append(s.requests, request)
	if request.Range != nil {
		return storage.Access{Location: "download:" + request.Target.Location}, nil
	}
	return storage.Access{Location: "signed:" + request.Target.Location}, nil
}

func targetService(scopes map[string]buckets.Scope, credentials []buckets.Credential) (*Service, *targetAccessSpy) {
	access := &targetAccessSpy{}
	scopeReader := scopeFake{scopes: scopes}
	return NewService(Dependencies{
		Access:      access,
		Scopes:      scopeReader,
		Credentials: credentialFake{credentials: credentials},
	}), access
}

func targetObject(resources ...string) *objects.Record {
	return &objects.Record{ControlledAccess: &resources}
}

func TestLegacyS3DownloadCompatibility(t *testing.T) {
	const (
		resource = "/organization/HTAN_INT/project/BForePC"
		legacy   = "s3://bforepc-prod/OHSU/koei_chin/slide.ome.tiff"
		physical = "s3://bforepc/bforepc-prod/OHSU/koei_chin/slide.ome.tiff"
	)
	scope := buckets.Scope{Organization: "HTAN_INT", ProjectID: "BForePC", Bucket: "bforepc", PathPrefix: "bforepc-prod"}

	t.Run("maps full and ranged downloads", func(t *testing.T) {
		service, access := targetService(map[string]buckets.Scope{"HTAN_INT|BForePC": scope}, nil)
		obj := targetObject(resource)
		signed, err := service.SignObjectURL(context.Background(), obj, legacy, storage.AccessOptions{})
		if err != nil || signed != "signed:"+physical {
			t.Fatalf("unexpected full download: signed=%q err=%v", signed, err)
		}
		part, err := service.SignObjectDownloadPart(context.Background(), obj, "bforepc-prod", legacy, 0, 1023, storage.AccessOptions{})
		if err != nil || part != "download:"+physical {
			t.Fatalf("unexpected ranged download: signed=%q err=%v", part, err)
		}
		if len(access.requests) != 2 || access.requests[0].Target.AccessID != "bforepc" || access.requests[1].Target.AccessID != "bforepc" {
			t.Fatalf("unexpected access requests: %+v", access.requests)
		}
	})

	t.Run("preserves exact configured physical bucket", func(t *testing.T) {
		service, _ := targetService(map[string]buckets.Scope{"HTAN_INT|BForePC": scope}, []buckets.Credential{{CredentialID: "bforepc-prod", Bucket: "bforepc-prod", Provider: "s3"}})
		signed, err := service.SignObjectURL(context.Background(), targetObject(resource), legacy, storage.AccessOptions{})
		if err != nil || signed != "signed:"+legacy {
			t.Fatalf("configured physical bucket must remain unchanged: signed=%q err=%v", signed, err)
		}
	})

	t.Run("maps credential identifier that is not physical bucket", func(t *testing.T) {
		service, _ := targetService(map[string]buckets.Scope{"HTAN_INT|BForePC": scope}, []buckets.Credential{{CredentialID: "bforepc-prod", Bucket: "bforepc", Provider: "s3"}})
		signed, err := service.SignObjectURL(context.Background(), targetObject(resource), legacy, storage.AccessOptions{})
		if err != nil || signed != "signed:"+physical {
			t.Fatalf("credential identifier mapping changed: signed=%q err=%v", signed, err)
		}
	})

	t.Run("preserves physical bucket when name equals scope prefix", func(t *testing.T) {
		physicalScope := buckets.Scope{Organization: "HTAN_INT", ProjectID: "BForePC", Bucket: "bforepc", PathPrefix: "bforepc"}
		physicalURL := "s3://bforepc/OHSU/koei_chin/slide.ome.tiff"
		service, _ := targetService(map[string]buckets.Scope{"HTAN_INT|BForePC": physicalScope}, []buckets.Credential{{CredentialID: "physical", Bucket: "bforepc", Provider: "s3"}})
		signed, err := service.SignObjectURL(context.Background(), targetObject(resource), physicalURL, storage.AccessOptions{})
		if err != nil || signed != "signed:"+physicalURL {
			t.Fatalf("physical URL changed: signed=%q err=%v", signed, err)
		}
	})

	for _, tc := range []struct {
		name  string
		url   string
		scope buckets.Scope
		want  string
	}{
		{name: "physical target bucket", url: physical, scope: scope, want: physical},
		{name: "partial prefix host", url: "s3://bforepc-pro/OHSU/file", scope: scope, want: "s3://bforepc-pro/OHSU/file"},
		{name: "empty prefix", url: legacy, scope: buckets.Scope{Organization: "HTAN_INT", ProjectID: "BForePC", Bucket: "bforepc", PathPrefix: ""}, want: legacy},
		{name: "preserves object key segments", url: "s3://bforepc-prod/OHSU/./slide//raw.tiff", scope: scope, want: "s3://bforepc/bforepc-prod/OHSU/./slide//raw.tiff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, _ := targetService(map[string]buckets.Scope{"HTAN_INT|BForePC": tc.scope}, nil)
			signed, err := service.SignObjectURL(context.Background(), targetObject(resource), tc.url, storage.AccessOptions{})
			if err != nil || signed != "signed:"+tc.want {
				t.Fatalf("unexpected download: signed=%q err=%v want=%q", signed, err, "signed:"+tc.want)
			}
		})
	}

	t.Run("rejects conflicting physical mappings", func(t *testing.T) {
		service, _ := targetService(map[string]buckets.Scope{
			"HTAN_INT|one": {Organization: "HTAN_INT", ProjectID: "one", Bucket: "bforepc-a", PathPrefix: "bforepc-prod"},
			"HTAN_INT|two": {Organization: "HTAN_INT", ProjectID: "two", Bucket: "bforepc-b", PathPrefix: "bforepc-prod"},
		}, nil)
		if _, err := service.SignObjectURL(context.Background(), targetObject("/organization/HTAN_INT/project/one", "/organization/HTAN_INT/project/two"), legacy, storage.AccessOptions{}); !errors.Is(err, faults.ErrConflict) {
			t.Fatalf("expected conflicting legacy mapping error, got %v", err)
		}
	})

	t.Run("leaves PUT behavior on canonical target path", func(t *testing.T) {
		service, _ := targetService(map[string]buckets.Scope{"HTAN_INT|BForePC": scope}, nil)
		signed, err := service.SignObjectURL(context.Background(), targetObject(resource), legacy, storage.AccessOptions{Method: "PUT"})
		if err != nil || signed != "signed:"+physical {
			t.Fatalf("unexpected PUT target: signed=%q err=%v", signed, err)
		}
	})
}

func TestLegacyS3DownloadCredentialErrorsPropagate(t *testing.T) {
	service := NewService(Dependencies{
		Access:      &targetAccessSpy{},
		Scopes:      scopeFake{scopes: map[string]buckets.Scope{"HTAN_INT|BForePC": {Organization: "HTAN_INT", ProjectID: "BForePC", Bucket: "physical", PathPrefix: "legacy"}}},
		Credentials: credentialErrorFake{err: errors.New("credential list unavailable")},
	})
	_, err := service.SignObjectURL(context.Background(), targetObject("/organization/HTAN_INT/project/BForePC"), "s3://legacy/key", storage.AccessOptions{})
	if err == nil || err.Error() != "credential list unavailable" {
		t.Fatalf("expected credential lookup error to propagate, got %v", err)
	}
}

type credentialErrorFake struct{ err error }

func (f credentialErrorFake) ListS3Credentials(context.Context) ([]buckets.Credential, error) {
	return nil, f.err
}

func TestScopedLogicalDownloadSigning(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	logical := "s3://syfon-ci/" + sha
	physical := "s3://syfon-ci/project-a/" + sha
	obj := &objects.Record{Checksums: []objects.Checksum{{Type: "sha256", Checksum: sha}}, ControlledAccess: &[]string{"/organization/ci/project/a"}}
	service, access := targetService(map[string]buckets.Scope{"ci|a": {Organization: "ci", ProjectID: "a", Bucket: "syfon-ci", PathPrefix: "project-a"}}, nil)
	if _, err := service.SignObjectURL(context.Background(), obj, logical, storage.AccessOptions{}); err != nil {
		t.Fatal(err)
	}
	if access.requests[0].Target.Location != physical || access.requests[0].Target.AccessID != "syfon-ci" {
		t.Fatalf("logical URL did not resolve: %+v", access.requests[0])
	}
	if _, err := service.SignObjectDownloadPart(context.Background(), obj, "syfon-ci", logical, 0, 1023, storage.AccessOptions{}); err != nil {
		t.Fatal(err)
	}
	if access.requests[1].Target.Location != physical || access.requests[1].Target.AccessID != "syfon-ci" {
		t.Fatalf("logical ranged URL did not resolve: %+v", access.requests[1])
	}
	for _, raw := range []string{"s3://syfon-ci/imported/legacy-object", physical} {
		service, access = targetService(map[string]buckets.Scope{"ci|a": {Organization: "ci", ProjectID: "a", Bucket: "syfon-ci", PathPrefix: "project-a"}}, nil)
		if _, err := service.SignObjectURL(context.Background(), obj, raw, storage.AccessOptions{}); err != nil {
			t.Fatal(err)
		}
		if access.requests[0].Target.Location != raw {
			t.Fatalf("imported/scoped URL changed from %q to %q", raw, access.requests[0].Target.Location)
		}
	}
}

func TestTargetHelperContracts(t *testing.T) {
	bucket, key, ok := parseS3Location("s3://bucket-name/path/to/object")
	if !ok || bucket != "bucket-name" || key != "path/to/object" {
		t.Fatalf("unexpected parsed location: bucket=%q key=%q ok=%v", bucket, key, ok)
	}
	scopes := []buckets.Scope{{PathPrefix: "org"}, {PathPrefix: "project"}}
	if got := normalizeScopedStorageKey("org/project/object.txt", scopes); got != "org/project/object.txt" {
		t.Fatalf("already-prefixed key changed to %q", got)
	}
	if got := normalizeScopedStorageKey("", scopes); got != "org/project" {
		t.Fatalf("expected joined scope prefix, got %q", got)
	}
}

type staticContentReader struct{ records []objects.Record }

func (r staticContentReader) GetObjectsByChecksum(context.Context, string) ([]objects.Record, error) {
	return r.records, nil
}

func (r staticContentReader) GetObjectsByChecksums(_ context.Context, checksums []string) (map[string][]objects.Record, error) {
	result := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		result[checksum] = r.records
	}
	return result, nil
}

func TestMergedContentPreservesReplicaLocation(t *testing.T) {
	scopes := map[string]buckets.Scope{
		"org|a": {Organization: "org", ProjectID: "a", Bucket: "bucket-a", PathPrefix: "a"},
		"org|b": {Organization: "org", ProjectID: "b", Bucket: "bucket-b", PathPrefix: "b"},
	}
	objs := []objects.Record{
		{Id: "uuid-a", Checksums: []objects.Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, ControlledAccess: &[]string{"/organization/org/project/a"}},
		{Id: "uuid-b", Checksums: []objects.Checksum{{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, ControlledAccess: &[]string{"/organization/org/project/b"}},
	}
	original := "s3://bucket-a/a/file"
	service, _ := targetService(scopes, nil)
	single, err := service.SignObjectURL(context.Background(), &objs[0], original, storage.AccessOptions{})
	if err != nil || single != "signed:"+original {
		t.Fatalf("single project changed replica: %q (%v)", single, err)
	}
	objectService := objects.NewService(objects.Dependencies{Content: staticContentReader{records: objs}})
	view, err := objectService.GetCanonicalContent(context.Background(), objs[0].Checksums[0].Checksum, "")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := service.SignObjectURL(context.Background(), &view.Record, original, storage.AccessOptions{})
	if err != nil || signed != "signed:"+original {
		t.Fatalf("merged read changed replica: got %q (%v)", signed, err)
	}
}
