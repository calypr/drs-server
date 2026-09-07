package gcs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"google.golang.org/api/option"

	"github.com/calypr/syfon/internal/buckets"
	storageports "github.com/calypr/syfon/internal/storage"
)

// newClient is kept as a narrow package seam for provider tests. Its default
// intentionally ignores Credential.Endpoint, matching the existing native
// client path used by completion and deletion.
var newClient = func(ctx context.Context, cred *buckets.Credential) (*storage.Client, error) {
	secret := strings.TrimSpace(cred.SecretKey)
	if secret != "" && json.Valid([]byte(secret)) {
		client, err := storage.NewClient(ctx, option.WithCredentialsJSON([]byte(secret)))
		if err != nil {
			return nil, err
		}
		return client, nil
	}
	client, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (b *backend) InitMultipartUpload(context.Context, storageports.ObjectTarget) (storageports.UploadID, error) {
	return storageports.UploadID(uuid.NewString()), nil
}

func (b *backend) SignMultipartPart(ctx context.Context, request storageports.MultipartPartRequest) (storageports.Access, error) {
	cred, err := b.credential(ctx, request.Target.Bucket)
	if err != nil {
		return storageports.Access{}, err
	}

	partKey := storageports.MultipartPartObjectKey(request.Target.Key, request.UploadID, request.PartNumber)
	location, err := b.signedURL(request.Target.Bucket, partKey, http.MethodPut, 15*time.Minute, "", "", cred)
	if err != nil {
		return storageports.Access{}, err
	}
	return storageports.Access{Location: location}, nil
}

func (b *backend) CompleteMultipartUpload(ctx context.Context, request storageports.CompleteMultipartRequest) error {
	client, err := b.getClient(ctx, request.Target.Bucket)
	if err != nil {
		return err
	}

	partList := storageports.NormalizedMultipartParts(request.Parts)
	partKeys := make([]string, 0, len(partList))
	for _, part := range partList {
		partKeys = append(partKeys, storageports.MultipartPartObjectKey(request.Target.Key, request.UploadID, part.PartNumber))
	}

	tempKeys, err := b.composeObjects(ctx, client, request.Target.Bucket, strings.Trim(strings.TrimSpace(request.Target.Key), "/"), request.UploadID, partKeys)
	if err != nil {
		return err
	}

	for _, key := range append(partKeys, tempKeys...) {
		if err := client.Bucket(request.Target.Bucket).Object(key).Delete(ctx); err != nil {
			return fmt.Errorf("delete multipart component %s: %w", key, err)
		}
	}
	return nil
}

func (b *backend) getClient(ctx context.Context, bucket string) (*storage.Client, error) {
	if value, ok := b.cache.Load(bucket); ok {
		return value.(*storage.Client), nil
	}

	cred, err := b.credential(ctx, bucket)
	if err != nil {
		return nil, err
	}
	client, err := newClient(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	b.cache.Store(bucket, client)
	return client, nil
}

func (b *backend) composeObjects(ctx context.Context, client *storage.Client, bucket, destinationKey string, uploadID storageports.UploadID, partKeys []string) ([]string, error) {
	if len(partKeys) == 0 {
		return nil, fmt.Errorf("multipart complete requires at least one part")
	}

	current := append([]string(nil), partKeys...)
	tempKeys := []string{}
	round := 0
	for len(current) > 32 {
		next := []string{}
		for i := 0; i < len(current); i += 32 {
			end := i + 32
			if end > len(current) {
				end = len(current)
			}
			temporary := path.Join(".syfon-multipart", strings.TrimSpace(string(uploadID)), strings.Trim(strings.TrimSpace(destinationKey), "/"), "compose", fmt.Sprintf("%d-%d", round, i/32))
			if err := b.composeBatch(ctx, client, bucket, temporary, current[i:end]); err != nil {
				return tempKeys, err
			}
			tempKeys = append(tempKeys, temporary)
			next = append(next, temporary)
		}
		current = next
		round++
	}
	if err := b.composeBatch(ctx, client, bucket, destinationKey, current); err != nil {
		return tempKeys, err
	}
	return tempKeys, nil
}

func (b *backend) composeBatch(ctx context.Context, client *storage.Client, bucket, destination string, sources []string) error {
	destinationObject := client.Bucket(bucket).Object(destination)
	sourceObjects := make([]*storage.ObjectHandle, 0, len(sources))
	for _, source := range sources {
		sourceObjects = append(sourceObjects, client.Bucket(bucket).Object(source))
	}
	if _, err := destinationObject.ComposerFrom(sourceObjects...).Run(ctx); err != nil {
		return fmt.Errorf("failed gcs compose for %s: %w", destination, err)
	}
	return nil
}
