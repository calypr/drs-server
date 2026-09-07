package internaldrs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/repair"
	"github.com/calypr/syfon/internal/testutils"
)

func TestStorageRepairInspectorReturnsCanonicalURL(t *testing.T) {
	db := storageRepairTestDatabase()
	om := core.NewObjectManager(db, &testutils.MockUrlManager{})
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket, key string) (*core.StorageObjectMetadata, error) {
		return &core.StorageObjectMetadata{Bucket: bucket, Key: key}, nil
	})

	got, err := (storageRepairInspector{om: om}).Inspect(context.Background(), repair.StorageInspectRequest{
		Organization: "org",
		Project:      "proj",
		ObjectURL:    " s3://b1/prefix/object.bin ",
	})
	if err != nil {
		t.Fatalf("inspect failed: %v", err)
	}
	if got.ObjectURL != "s3://b1/prefix/object.bin" {
		t.Fatalf("unexpected canonical object URL: %q", got.ObjectURL)
	}
}

func TestStorageRepairInspectorClassifiesMissingObject(t *testing.T) {
	om := core.NewObjectManager(storageRepairTestDatabase(), &testutils.MockUrlManager{})
	om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket, key string) (*core.StorageObjectMetadata, error) {
		return nil, &core.StorageInspectError{Kind: core.StorageInspectObjectNotFound, Message: "provider could not find object"}
	})

	_, err := (storageRepairInspector{om: om}).Inspect(context.Background(), repair.StorageInspectRequest{ObjectURL: "s3://b1/prefix/object.bin"})
	if !errors.Is(err, repair.ErrStorageObjectNotFound) {
		t.Fatalf("expected repair missing-object error, got %v", err)
	}
}

func TestStorageRepairInspectorPreservesAuthorizationAndCredentialFailures(t *testing.T) {
	t.Run("authorization", func(t *testing.T) {
		om := core.NewObjectManager(storageRepairTestDatabase(), &testutils.MockUrlManager{})
		om.SetS3ObjectInspector(func(ctx context.Context, cred models.S3Credential, bucket, key string) (*core.StorageObjectMetadata, error) {
			return nil, &core.StorageInspectError{Kind: core.StorageInspectPermissionDenied, Message: "provider denied access"}
		})
		_, err := (storageRepairInspector{om: om}).Inspect(policyTestContext("gen3", true, nil), repair.StorageInspectRequest{ObjectURL: "s3://b1/prefix/object.bin"})
		var inspectErr *core.StorageInspectError
		if !errors.As(err, &inspectErr) || inspectErr.Kind != core.StorageInspectPermissionDenied {
			t.Fatalf("expected permission-denied inspection error, got %v", err)
		}
	})

	t.Run("credential", func(t *testing.T) {
		db := &testutils.MockDatabase{NoDefaultCreds: true}
		om := core.NewObjectManager(db, &testutils.MockUrlManager{})
		_, err := (storageRepairInspector{om: om}).Inspect(context.Background(), repair.StorageInspectRequest{ObjectURL: "s3://missing/prefix/object.bin"})
		var inspectErr *core.StorageInspectError
		if !errors.As(err, &inspectErr) || inspectErr.Kind != core.StorageInspectCredentialMissing {
			t.Fatalf("expected credential-missing inspection error, got %v", err)
		}
	})
}

func TestStorageRepairInspectorRejectsMalformedTarget(t *testing.T) {
	om := core.NewObjectManager(storageRepairTestDatabase(), &testutils.MockUrlManager{})
	_, err := (storageRepairInspector{om: om}).Inspect(context.Background(), repair.StorageInspectRequest{ObjectURL: "https://example.com/object.bin"})
	var inspectErr *core.StorageInspectError
	if !errors.As(err, &inspectErr) || inspectErr.Kind != core.StorageInspectInvalidInput {
		t.Fatalf("expected invalid-target inspection error, got %v", err)
	}
}

func storageRepairTestDatabase() *testutils.MockDatabase {
	return &testutils.MockDatabase{
		Credentials: map[string]models.S3Credential{
			"b1": {CredentialID: "b1", Bucket: "b1", Provider: "s3"},
		},
		BucketScopes: map[string]models.BucketScope{
			"org|proj": {Organization: "org", ProjectID: "proj", Bucket: "b1"},
		},
	}
}

func TestScopeRepairApplyRejectsReadOnlyProjectUser(t *testing.T) {
	resource, err := sycommon.ResourcePath("org", "proj")
	if err != nil {
		t.Fatalf("resource path: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/data/repair/project-scope/apply", strings.NewReader(`{"organization":"org","project":"proj"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(policyTestContext("gen3", true, map[string]map[string]bool{
		resource: {"read": true},
	}))

	rr := doInternalDRSTestRequest(req, core.NewObjectManager(&testutils.MockDatabase{}, &testutils.MockUrlManager{}))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read-only caller, got %d body=%s", rr.Code, rr.Body.String())
	}
}
