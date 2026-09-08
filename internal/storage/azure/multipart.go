package azure

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/calypr/syfon/internal/storage"
)

func (b *backend) SignMultipartPart(_ context.Context, binding storage.ProviderBinding, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	creds, err := b.getCreds(binding)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	signed, err := b.azureSignedURL(creds.ServiceURL, binding.PhysicalBucket, request.Target.Key, "PUT", 15*time.Minute, "", "", creds.SharedKey)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	u, err := url.Parse(signed)
	if err != nil {
		return storage.SignedAccess{}, err
	}
	query := u.Query()
	query.Set("comp", "block")
	query.Set("blockid", b.azureBlockID(request.UploadID, request.PartNumber))
	u.RawQuery = query.Encode()
	return storage.SignedAccess{Location: u.String()}, nil
}

func (b *backend) CompleteMultipart(ctx context.Context, binding storage.ProviderBinding, request storage.CompleteMultipartRequest) error {
	creds, err := b.getCreds(binding)
	if err != nil {
		return err
	}

	blobURL := b.azureBlobURL(creds.ServiceURL, binding.PhysicalBucket, request.Target.Key)
	client, err := blockblob.NewClientWithSharedKeyCredential(blobURL, creds.SharedKey, b.blockBlobClientOptions())
	if err != nil {
		return fmt.Errorf("failed to create azure block blob client: %w", err)
	}

	partList := append([]storage.CompletedPart(nil), request.Parts...)
	sort.Slice(partList, func(i, j int) bool { return partList[i].PartNumber < partList[j].PartNumber })
	blockIDs := make([]string, 0, len(partList))
	for _, part := range partList {
		// Azure identifies a block by its deterministic block ID. It does not
		// consume the ETag carried by the shared multipart value.
		blockIDs = append(blockIDs, b.azureBlockID(request.UploadID, part.PartNumber))
	}

	if _, err := client.CommitBlockList(ctx, blockIDs, nil); err != nil {
		return fmt.Errorf("failed to complete azure multipart upload: %w", err)
	}
	return nil
}

func (b *backend) blockBlobClientOptions() *blockblob.ClientOptions {
	if b.transport == nil {
		return nil
	}
	options := &blockblob.ClientOptions{}
	options.Transport = b.transport
	return options
}
