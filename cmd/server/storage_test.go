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
	credentials := storageTestCredentials{credential: buckets.Credential{
		Bucket: "bucket", Provider: address.FileProvider, Endpoint: root,
	}}
	manager, err := newStorageManager(credentials, root, nil)
	if err != nil {
		t.Fatalf("newStorageManager: %v", err)
	}
	access, err := manager.Access(context.Background(), storage.AccessRequest{
		Target: storage.AccessTarget{Location: "s3://bucket/object"},
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

type storageTestCredentials struct {
	credential buckets.Credential
}

func (c storageTestCredentials) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	if bucket != c.credential.Bucket {
		return nil, nil
	}
	credential := c.credential
	return &credential, nil
}
