package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

func TestStorageCompositionSharesOneManagerAcrossConsumerPorts(t *testing.T) {
	root := t.TempDir()
	credentials := &serverBucketStore{credentials: map[string]buckets.Credential{
		"bucket": {Bucket: "bucket", Provider: address.FileProvider, Endpoint: root},
	}}
	manager, err := newStorageManager(credentials, root, nil)
	if err != nil {
		t.Fatalf("newStorageManager: %v", err)
	}
	access, err := manager.Sign(context.Background(), storage.SignRequest{
		Target: storage.Target{OriginalURL: "s3://bucket/object"},
	})
	if err != nil {
		t.Fatalf("file-backed access through composed manager: %v", err)
	}
	if want := filepath.Join(root, "object"); access.Location != filepath.ToSlash(want) {
		t.Fatalf("access location=%q, want %q", access.Location, filepath.ToSlash(want))
	}

	invalidator := &storageInvalidator{}
	invalidator.InvalidateBucket("bucket")
	invalidator.manager = manager
	invalidator.InvalidateBucket("bucket")
}
