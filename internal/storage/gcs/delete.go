package gcs

import (
	"context"
	"errors"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"

	storageports "github.com/calypr/syfon/internal/storage"
)

func (b *backend) Delete(ctx context.Context, binding storageports.ProviderBinding, targets []storageports.PhysicalTarget) error {
	for _, target := range targets {
		client, err := newClient(ctx, binding.Credential)
		if err != nil {
			return err
		}
		err = client.Bucket(target.PhysicalBucket).Object(target.Key).Delete(ctx)
		_ = client.Close()
		if err == nil || isNotFound(err) {
			continue
		}
		return err
	}
	return nil
}

func isNotFound(err error) bool {
	if errors.Is(err, storage.ErrObjectNotExist) {
		return true
	}
	googleErr, ok := err.(*googleapi.Error)
	return ok && googleErr.Code == 404
}
