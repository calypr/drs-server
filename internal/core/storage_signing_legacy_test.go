package core

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/calypr/syfon/internal/urlmanager"
)

func TestObjectManagerLegacyS3DownloadCompatibility(t *testing.T) {
	const (
		resource = "/organization/HTAN_INT/project/BForePC"
		legacy   = "s3://bforepc-prod/OHSU/koei_chin/slide.ome.tiff"
		physical = "s3://bforepc/bforepc-prod/OHSU/koei_chin/slide.ome.tiff"
	)
	scope := models.BucketScope{
		Organization: "HTAN_INT",
		ProjectID:    "BForePC",
		Bucket:       "bforepc",
		PathPrefix:   "bforepc-prod",
	}
	newObject := func(resources ...string) *models.InternalObject {
		return &models.InternalObject{DrsObject: drs.DrsObject{ControlledAccess: &resources}}
	}
	newManager := func(scopes map[string]models.BucketScope, credentials map[string]models.S3Credential) (*ObjectManager, *capturingURLManager) {
		db := &coreTestDB{MockDatabase: &testutils.MockDatabase{BucketScopes: scopes, Credentials: credentials}}
		um := &capturingURLManager{}
		return NewObjectManager(db, um), um
	}

	t.Run("maps full and ranged downloads", func(t *testing.T) {
		om, um := newManager(map[string]models.BucketScope{"HTAN_INT|BForePC": scope}, nil)
		obj := newObject(resource)

		signed, err := om.SignObjectURL(context.Background(), obj, legacy, urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if signed != "signed:"+physical || um.signURLBucket != "bforepc" || um.signURLAccessURL != physical {
			t.Fatalf("unexpected mapped full download: signed=%q bucket=%q url=%q", signed, um.signURLBucket, um.signURLAccessURL)
		}

		part, err := om.SignObjectDownloadPart(context.Background(), obj, "bforepc-prod", legacy, 0, 1023, urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectDownloadPart failed: %v", err)
		}
		if part != "download:"+physical || um.signDownloadBucket != "bforepc" || um.signDownloadURL != physical {
			t.Fatalf("unexpected mapped ranged download: signed=%q bucket=%q url=%q", part, um.signDownloadBucket, um.signDownloadURL)
		}
	})

	t.Run("preserves exact configured physical bucket", func(t *testing.T) {
		om, um := newManager(map[string]models.BucketScope{"HTAN_INT|BForePC": scope}, map[string]models.S3Credential{
			"bforepc-prod": {CredentialID: "bforepc-prod", Bucket: "bforepc-prod", Provider: "s3"},
		})
		signed, err := om.SignObjectURL(context.Background(), newObject(resource), legacy, urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if signed != "signed:"+legacy || um.signURLBucket != "bforepc-prod" || um.signURLAccessURL != legacy {
			t.Fatalf("configured physical bucket must remain unchanged: signed=%q bucket=%q url=%q", signed, um.signURLBucket, um.signURLAccessURL)
		}
	})

	t.Run("maps credential identifier that is not the physical bucket", func(t *testing.T) {
		om, um := newManager(map[string]models.BucketScope{"HTAN_INT|BForePC": scope}, map[string]models.S3Credential{
			"bforepc-prod": {CredentialID: "bforepc-prod", Bucket: "bforepc", Provider: "s3"},
		})
		signed, err := om.SignObjectURL(context.Background(), newObject(resource), legacy, urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if signed != "signed:"+physical || um.signURLBucket != "bforepc" || um.signURLAccessURL != physical {
			t.Fatalf("credential identifier must map to its physical bucket: signed=%q bucket=%q url=%q", signed, um.signURLBucket, um.signURLAccessURL)
		}
	})

	t.Run("preserves physical bucket when its name equals the scope prefix", func(t *testing.T) {
		physicalScope := models.BucketScope{
			Organization: "HTAN_INT",
			ProjectID:    "BForePC",
			Bucket:       "bforepc",
			PathPrefix:   "bforepc",
		}
		physicalURL := "s3://bforepc/OHSU/koei_chin/slide.ome.tiff"
		om, um := newManager(map[string]models.BucketScope{"HTAN_INT|BForePC": physicalScope}, map[string]models.S3Credential{
			"physical": {CredentialID: "physical", Bucket: "bforepc", Provider: "s3"},
		})
		signed, err := om.SignObjectURL(context.Background(), newObject(resource), physicalURL, urlmanager.SignOptions{})
		if err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if signed != "signed:"+physicalURL || um.signURLBucket != "bforepc" || um.signURLAccessURL != physicalURL {
			t.Fatalf("physical URL must remain unchanged: signed=%q bucket=%q url=%q", signed, um.signURLBucket, um.signURLAccessURL)
		}
	})

	for _, tc := range []struct {
		name  string
		url   string
		scope models.BucketScope
		want  string
	}{
		{name: "physical target bucket", url: physical, scope: scope, want: physical},
		{name: "partial prefix host", url: "s3://bforepc-pro/OHSU/file", scope: scope, want: "s3://bforepc-pro/OHSU/file"},
		{name: "empty prefix", url: legacy, scope: models.BucketScope{Organization: "HTAN_INT", ProjectID: "BForePC", Bucket: "bforepc", PathPrefix: ""}, want: legacy},
		{name: "preserves object key segments", url: "s3://bforepc-prod/OHSU/./slide//raw.tiff", scope: scope, want: "s3://bforepc/bforepc-prod/OHSU/./slide//raw.tiff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			om, um := newManager(map[string]models.BucketScope{"HTAN_INT|BForePC": tc.scope}, nil)
			signed, err := om.SignObjectURL(context.Background(), newObject(resource), tc.url, urlmanager.SignOptions{})
			if err != nil {
				t.Fatalf("SignObjectURL failed: %v", err)
			}
			if signed != "signed:"+tc.want || um.signURLAccessURL != tc.want {
				t.Fatalf("unexpected unchanged download: signed=%q url=%q want=%q", signed, um.signURLAccessURL, tc.want)
			}
		})
	}

	t.Run("rejects conflicting physical mappings", func(t *testing.T) {
		obj := newObject("/organization/HTAN_INT/project/one", "/organization/HTAN_INT/project/two")
		om, _ := newManager(map[string]models.BucketScope{
			"HTAN_INT|one": {Organization: "HTAN_INT", ProjectID: "one", Bucket: "bforepc-a", PathPrefix: "bforepc-prod"},
			"HTAN_INT|two": {Organization: "HTAN_INT", ProjectID: "two", Bucket: "bforepc-b", PathPrefix: "bforepc-prod"},
		}, nil)
		if _, err := om.SignObjectURL(context.Background(), obj, legacy, urlmanager.SignOptions{}); !errors.Is(err, faults.ErrConflict) {
			t.Fatalf("expected conflicting legacy mapping error, got %v", err)
		}
	})

	t.Run("leaves PUT behavior on canonical target path", func(t *testing.T) {
		om, um := newManager(map[string]models.BucketScope{"HTAN_INT|BForePC": scope}, nil)
		signed, err := om.SignObjectURL(context.Background(), newObject(resource), legacy, urlmanager.SignOptions{Method: "PUT"})
		if err != nil {
			t.Fatalf("SignObjectURL PUT failed: %v", err)
		}
		if signed != "signed:"+physical || um.signURLAccessURL != physical || um.signURLBucket != "bforepc" {
			t.Fatalf("unexpected PUT target: signed=%q bucket=%q url=%q", signed, um.signURLBucket, um.signURLAccessURL)
		}
	})
}

type credentialListErrorDB struct {
	*testutils.MockDatabase
	err error
}

func (db *credentialListErrorDB) ListS3Credentials(context.Context) ([]models.S3Credential, error) {
	return nil, db.err
}

func TestObjectManagerLegacyS3DownloadCredentialErrorsPropagate(t *testing.T) {
	wantErr := errors.New("credential list unavailable")
	db := &credentialListErrorDB{MockDatabase: &testutils.MockDatabase{BucketScopes: map[string]models.BucketScope{
		"HTAN_INT|BForePC": {
			Organization: "HTAN_INT",
			ProjectID:    "BForePC",
			Bucket:       "physical",
			PathPrefix:   "legacy",
		},
	}}, err: wantErr}
	om := NewObjectManager(db, &capturingURLManager{})
	resources := []string{"/organization/HTAN_INT/project/BForePC"}
	obj := &models.InternalObject{DrsObject: drs.DrsObject{ControlledAccess: &resources}}

	if _, err := om.SignObjectURL(context.Background(), obj, "s3://legacy/key", urlmanager.SignOptions{}); !errors.Is(err, wantErr) {
		t.Fatalf("expected credential lookup error to propagate, got %v", err)
	}
}

func TestObjectManagerScopedLogicalDownloadSigning(t *testing.T) {
	const (
		sha      = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		logical  = "s3://syfon-ci/" + sha
		physical = "s3://syfon-ci/project-a/" + sha
	)

	db := &coreTestDB{MockDatabase: &testutils.MockDatabase{BucketScopes: map[string]models.BucketScope{
		"ci|a": {
			Organization: "ci",
			ProjectID:    "a",
			Bucket:       "syfon-ci",
			PathPrefix:   "project-a",
		},
	}}}
	obj := &models.InternalObject{DrsObject: drs.DrsObject{
		Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
		ControlledAccess: &[]string{"/organization/ci/project/a"},
	}}

	t.Run("full download", func(t *testing.T) {
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		if _, err := om.SignObjectURL(context.Background(), obj, logical, urlmanager.SignOptions{}); err != nil {
			t.Fatalf("SignObjectURL failed: %v", err)
		}
		if um.signURLAccessURL != physical || um.signURLBucket != "syfon-ci" {
			t.Fatalf("expected logical URL to resolve to %q in syfon-ci, got %q in %q", physical, um.signURLAccessURL, um.signURLBucket)
		}
	})

	t.Run("ranged download", func(t *testing.T) {
		um := &capturingURLManager{}
		om := NewObjectManager(db, um)
		if _, err := om.SignObjectDownloadPart(context.Background(), obj, "syfon-ci", logical, 0, 1023, urlmanager.SignOptions{}); err != nil {
			t.Fatalf("SignObjectDownloadPart failed: %v", err)
		}
		if um.signDownloadURL != physical || um.signDownloadBucket != "syfon-ci" {
			t.Fatalf("expected logical URL to resolve to %q in syfon-ci, got %q in %q", physical, um.signDownloadURL, um.signDownloadBucket)
		}
	})

	for _, tc := range []struct {
		name string
		url  string
	}{
		{name: "imported path", url: "s3://syfon-ci/imported/legacy-object"},
		{name: "already scoped", url: physical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			um := &capturingURLManager{}
			om := NewObjectManager(db, um)
			if _, err := om.SignObjectURL(context.Background(), obj, tc.url, urlmanager.SignOptions{}); err != nil {
				t.Fatalf("SignObjectURL failed: %v", err)
			}
			if um.signURLAccessURL != tc.url {
				t.Fatalf("expected imported/scoped URL to remain %q, got %q", tc.url, um.signURLAccessURL)
			}
			if _, err := om.SignObjectDownloadPart(context.Background(), obj, "syfon-ci", tc.url, 0, 1023, urlmanager.SignOptions{}); err != nil {
				t.Fatalf("SignObjectDownloadPart failed: %v", err)
			}
			if um.signDownloadURL != tc.url || um.signDownloadBucket != "syfon-ci" {
				t.Fatalf("expected imported/scoped ranged URL to remain %q in syfon-ci, got %q in %q", tc.url, um.signDownloadURL, um.signDownloadBucket)
			}
		})
	}
}
