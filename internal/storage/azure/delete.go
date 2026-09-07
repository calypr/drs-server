package azure

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/aws/smithy-go"
	"github.com/calypr/syfon/internal/storage"
)

func (b *backend) Delete(ctx context.Context, targets []storage.PhysicalTarget) error {
	for _, target := range targets {
		creds, err := b.getCreds(ctx, target.Bucket)
		if err != nil {
			return fmt.Errorf("lookup azure credential for bucket %s: %w", target.Bucket, err)
		}

		client, err := azblob.NewClientWithSharedKeyCredential(creds.DeleteServiceURL, creds.SharedKey, b.blobClientOptions())
		if err != nil {
			return fmt.Errorf("create azure client: %w", err)
		}

		if _, err := client.DeleteBlob(ctx, target.Bucket, target.Key, nil); err != nil {
			if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
				continue
			}
			var apiErr smithy.APIError
			if errors.As(err, &apiErr) && strings.EqualFold(apiErr.ErrorCode(), "BlobNotFound") {
				continue
			}
			return err
		}
	}
	return nil
}

func (b *backend) blobClientOptions() *azblob.ClientOptions {
	if b.transport == nil {
		return nil
	}
	options := &azblob.ClientOptions{}
	options.Transport = b.transport
	return options
}
