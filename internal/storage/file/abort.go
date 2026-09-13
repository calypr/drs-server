package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/calypr/syfon/internal/storage"
)

func (b *backend) AbortMultipart(_ context.Context, binding storage.ProviderBinding, request storage.AbortMultipartRequest) error {
	if err := b.beginOperation(); err != nil {
		return err
	}
	defer b.endOperation()
	prefix, err := storage.MultipartUploadPrefix(request.Target.Key, request.UploadID)
	if err != nil {
		return err
	}
	root := b.effectiveRoot(binding)
	multipartRoot := filepath.Join(root, ".syfon-multipart")
	directory := filepath.Join(root, filepath.FromSlash(prefix))
	relative, err := filepath.Rel(multipartRoot, directory)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		return fmt.Errorf("invalid multipart cleanup path")
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove file multipart components: %w", err)
	}
	return nil
}
