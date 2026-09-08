package file

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/calypr/syfon/internal/storage"
	"github.com/google/uuid"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"
)

type backend struct {
	rootPath   string
	rootBucket *blob.Bucket
}

// New creates a file-backed storage registration rooted at root.
func New(root string) (storage.Registration, error) {
	b, err := newBackend(root)
	if err != nil {
		return storage.Registration{}, err
	}
	return storage.NewRegistration("file", b), nil
}

func newBackend(root string) (*backend, error) {
	absPath, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %s: %w", root, err)
	}

	bucket, err := blob.OpenBucket(context.Background(), "file:"+"//"+filepath.ToSlash(absPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open file bucket at %s: %w", absPath, err)
	}
	return &backend{rootPath: absPath, rootBucket: bucket}, nil
}

func (b *backend) Sign(_ context.Context, _ storage.ProviderBinding, request storage.SignRequest) (storage.SignedAccess, error) {
	return storage.SignedAccess{Location: filepath.ToSlash(filepath.Join(b.rootPath, request.Target.Key))}, nil
}

func (b *backend) BeginMultipart(context.Context, storage.ProviderBinding, storage.Target) (storage.UploadID, error) {
	return storage.UploadID(uuid.NewString()), nil
}
