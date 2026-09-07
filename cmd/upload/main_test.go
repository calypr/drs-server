package upload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	drsapi "github.com/calypr/syfon/apigen/drs"
	syfonclient "github.com/calypr/syfon/client/services"
)

type didLookupStub struct {
	obj              drsapi.DrsObject
	err              error
	deletedObjectID  string
	deleteStorageArg *bool
	deleteErr        error
}

func (s didLookupStub) GetObject(context.Context, string) (drsapi.DrsObject, error) {
	if s.err != nil {
		return drsapi.DrsObject{}, s.err
	}
	return s.obj, nil
}

func (s *didLookupStub) DeleteObject(_ context.Context, objectID string, deleteStorageData bool) error {
	s.deletedObjectID = objectID
	s.deleteStorageArg = &deleteStorageData
	return s.deleteErr
}

func TestResolveUploadBucketForScopePrefersExactProjectMatch(t *testing.T) {
	bucket, err := resolveUploadBucketForScope(bucketapi.BucketsResponse{
		S3BUCKETS: map[string]bucketapi.BucketMetadata{
			"org-bucket":     {Programs: &[]string{"/organization/cbds"}},
			"project-bucket": {Programs: &[]string{"/organization/cbds/project/proj1"}},
		},
	}, "cbds", "proj1")
	if err != nil {
		t.Fatalf("resolveUploadBucketForScope returned error: %v", err)
	}
	if bucket != "project-bucket" {
		t.Fatalf("expected project-bucket, got %q", bucket)
	}
}

func TestResolveUploadBucketForScopeFallsBackToOrganizationScope(t *testing.T) {
	bucket, err := resolveUploadBucketForScope(bucketapi.BucketsResponse{
		S3BUCKETS: map[string]bucketapi.BucketMetadata{
			"org-bucket": {Programs: &[]string{"/organization/cbds"}},
		},
	}, "cbds", "proj1")
	if err != nil {
		t.Fatalf("resolveUploadBucketForScope returned error: %v", err)
	}
	if bucket != "org-bucket" {
		t.Fatalf("expected org-bucket, got %q", bucket)
	}
}

func TestResolveUploadBucketForScopeRejectsAmbiguousOrganizationScope(t *testing.T) {
	_, err := resolveUploadBucketForScope(bucketapi.BucketsResponse{
		S3BUCKETS: map[string]bucketapi.BucketMetadata{
			"bucket-a": {Programs: &[]string{"/organization/cbds"}},
			"bucket-b": {Programs: &[]string{"/organization/cbds"}},
		},
	}, "cbds", "")
	if err == nil || !strings.Contains(err.Error(), "maps to multiple buckets") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestResolveUploadBucketForScopeRejectsMissingScope(t *testing.T) {
	_, err := resolveUploadBucketForScope(bucketapi.BucketsResponse{
		S3BUCKETS: map[string]bucketapi.BucketMetadata{
			"other-bucket": {Programs: &[]string{"/organization/other"}},
		},
	}, "cbds", "proj1")
	if err == nil || !strings.Contains(err.Error(), "no bucket configured") {
		t.Fatalf("expected missing scope error, got %v", err)
	}
}

func TestEnsureWritableDIDRequiresOverwriteForExistingObject(t *testing.T) {
	stub := &didLookupStub{obj: drsapi.DrsObject{Id: "did-1"}}
	_, err := ensureWritableDID(context.Background(), stub, "did-1", false)
	if err == nil || !strings.Contains(err.Error(), "--overwrite") {
		t.Fatalf("expected overwrite guidance, got %v", err)
	}
}

func TestEnsureWritableDIDAllowsOverwriteForExistingObject(t *testing.T) {
	stub := &didLookupStub{obj: drsapi.DrsObject{Id: "did-1"}}
	warning, err := ensureWritableDID(context.Background(), stub, "did-1", true)
	if err != nil {
		t.Fatalf("expected overwrite to allow existing DID, got %v", err)
	}
	if !strings.Contains(warning, "already existed") {
		t.Fatalf("expected overwrite warning, got %q", warning)
	}
	if stub.deletedObjectID != "did-1" {
		t.Fatalf("expected overwrite to delete did-1 first, got %q", stub.deletedObjectID)
	}
	if stub.deleteStorageArg == nil || *stub.deleteStorageArg {
		t.Fatalf("expected overwrite delete to preserve storage bytes, got deleteStorageData=%v", stub.deleteStorageArg)
	}
}

func TestEnsureWritableDIDAllowsMissingObject(t *testing.T) {
	warning, err := ensureWritableDID(context.Background(), &didLookupStub{err: syfonclient.ErrObjectNotFound}, "did-1", false)
	if err != nil || warning != "" {
		t.Fatalf("expected missing DID to be writable without warning, got warning=%q err=%v", warning, err)
	}
}

func TestEnsureWritableDIDPropagatesLookupFailure(t *testing.T) {
	_, err := ensureWritableDID(context.Background(), &didLookupStub{err: errors.New("boom")}, "did-1", false)
	if err == nil || !strings.Contains(err.Error(), "check existing DID") {
		t.Fatalf("expected lookup failure, got %v", err)
	}
}

func TestEnsureWritableDIDPropagatesOverwriteDeleteFailure(t *testing.T) {
	stub := &didLookupStub{obj: drsapi.DrsObject{Id: "did-1"}, deleteErr: errors.New("delete boom")}
	_, err := ensureWritableDID(context.Background(), stub, "did-1", true)
	if err == nil || !strings.Contains(err.Error(), "delete existing DID") {
		t.Fatalf("expected overwrite delete failure, got %v", err)
	}
}

func TestUploadRecordPathPreservesRelativeHierarchy(t *testing.T) {
	got, err := uploadRecordPath("data/tcga.tumor.hugo.tsv")
	if err != nil {
		t.Fatalf("uploadRecordPath returned error: %v", err)
	}
	if got != "data/tcga.tumor.hugo.tsv" {
		t.Fatalf("expected relative hierarchy, got %q", got)
	}
}

func TestUploadRecordPathMakesAbsolutePathRelativeToCWD(t *testing.T) {
	tmpDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	target := filepath.Join(tmpDir, "data", "nested", "file.tsv")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdirall failed: %v", err)
	}
	if err := os.WriteFile(target, []byte("payload"), 0o644); err != nil {
		t.Fatalf("writefile failed: %v", err)
	}
	got, err := uploadRecordPath(target)
	if err != nil {
		t.Fatalf("uploadRecordPath returned error: %v", err)
	}
	if got != "data/nested/file.tsv" {
		t.Fatalf("expected cwd-relative path, got %q", got)
	}
}

func TestUploadRecordPathFallsBackToBaseNameOutsideCWD(t *testing.T) {
	tmpDir := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	outside := filepath.Join(string(filepath.Separator), "tmp", "outside", "file.tsv")
	got, err := uploadRecordPath(outside)
	if err != nil {
		t.Fatalf("uploadRecordPath returned error: %v", err)
	}
	if got != "file.tsv" {
		t.Fatalf("expected basename fallback, got %q", got)
	}
}
