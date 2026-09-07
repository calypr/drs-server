package gcs

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"

	storageports "github.com/calypr/syfon/internal/storage"
)

func (b *backend) Delete(ctx context.Context, targets []storageports.PhysicalTarget) error {
	for _, target := range targets {
		cred, err := b.credentials.GetS3Credential(ctx, target.Bucket)
		if err != nil {
			return fmt.Errorf("lookup gcs credential for bucket %s: %w", target.Bucket, err)
		}
		if cred == nil {
			return fmt.Errorf("credentials not found for bucket %s", target.Bucket)
		}

		client, err := newClient(ctx, cred)
		if err != nil {
			return fmt.Errorf("create gcs client: %w", err)
		}
		err = client.Bucket(target.Bucket).Object(target.Key).Delete(ctx)
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
