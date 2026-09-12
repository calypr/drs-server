package azure

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/calypr/syfon/internal/storage"
)

func (b *backend) SignMultipartPart(_ context.Context, binding storage.ProviderBinding, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	creds, err := b.getCreds(binding)
	if err != nil {
		return storage.SignedAccess{}, err
	}

	expires := request.ExpiresIn
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	signed, err := b.azureSignedURL(creds.ServiceURL, binding.PhysicalBucket, request.Target.Key, "PUT", expires, "", "", creds.SharedKey)
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
	if matched, err := b.multipartCompletionMatches(ctx, client, request.Target, request.CompletionID); err != nil {
		return err
	} else if matched {
		return nil
	}

	partList := append([]storage.CompletedPart(nil), request.Parts...)
	sort.Slice(partList, func(i, j int) bool { return partList[i].PartNumber < partList[j].PartNumber })
	blockIDs := make([]string, 0, len(partList))
	for _, part := range partList {
		// Azure identifies a block by its deterministic block ID. It does not
		// consume the ETag carried by the shared multipart value.
		blockIDs = append(blockIDs, b.azureBlockID(request.UploadID, part.PartNumber))
	}

	var options *blockblob.CommitBlockListOptions
	if strings.TrimSpace(request.CompletionID) != "" {
		options = &blockblob.CommitBlockListOptions{Metadata: map[string]*string{
			storage.MultipartCompletionMarkerMetadataKey: &request.CompletionID,
		}}
	}
	if _, err := client.CommitBlockList(ctx, blockIDs, options); err != nil {
		completionErr := fmt.Errorf("failed to complete azure multipart upload: %w", err)
		matched, reconcileErr := b.multipartCompletionMatches(ctx, client, request.Target, request.CompletionID)
		if reconcileErr != nil {
			return errors.Join(completionErr, reconcileErr)
		}
		if matched {
			return nil
		}
		return completionErr
	}
	return nil
}

func (b *backend) multipartCompletionMatches(ctx context.Context, client *blockblob.Client, target storage.Target, completionID string) (bool, error) {
	if strings.TrimSpace(completionID) == "" {
		return false, nil
	}
	properties, err := client.GetProperties(ctx, (*blob.GetPropertiesOptions)(nil))
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
			return false, nil
		}
		return false, errors.Join(storage.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect azure multipart completion marker for %s/%s: %w", target.PhysicalBucket, target.Key, err))
	}
	for key, value := range properties.Metadata {
		if strings.EqualFold(key, storage.MultipartCompletionMarkerMetadataKey) && value != nil {
			return *value == completionID, nil
		}
	}
	return false, nil
}

func (b *backend) blockBlobClientOptions() *blockblob.ClientOptions {
	if b.transport == nil {
		return nil
	}
	options := &blockblob.ClientOptions{}
	options.Transport = b.transport
	return options
}
