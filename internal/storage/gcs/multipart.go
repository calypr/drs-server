package gcs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
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
		client, err := storage.NewClient(ctx, option.WithAuthCredentialsJSON(option.ServiceAccount, []byte(secret)))
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

func (b *backend) BeginMultipart(context.Context, storageports.ProviderBinding, storageports.BeginMultipartRequest) (storageports.UploadID, error) {
	return storageports.UploadID(uuid.NewString()), nil
}

func (b *backend) SignMultipartPart(_ context.Context, binding storageports.ProviderBinding, request storageports.MultipartPartRequest) (storageports.SignedAccess, error) {
	cred, err := b.credential(binding)
	if err != nil {
		return storageports.SignedAccess{}, err
	}

	partKey := storageports.MultipartPartObjectKey(request.Target.Key, request.UploadID, request.PartNumber)
	expires := request.ExpiresIn
	if expires <= 0 {
		expires = 15 * time.Minute
	}
	location, err := b.signedURL(binding.PhysicalBucket, partKey, http.MethodPut, expires, "", "", cred)
	if err != nil {
		return storageports.SignedAccess{}, err
	}
	return storageports.SignedAccess{Location: location}, nil
}

func (b *backend) CompleteMultipart(ctx context.Context, binding storageports.ProviderBinding, request storageports.CompleteMultipartRequest) error {
	client, err := b.getClient(ctx, binding)
	if err != nil {
		return err
	}
	if matched, err := b.multipartCompletionMatches(ctx, client, request.Target, request.CompletionID); err != nil {
		return err
	} else if matched {
		return nil
	}

	partList := append([]storageports.CompletedPart(nil), request.Parts...)
	sort.Slice(partList, func(i, j int) bool { return partList[i].PartNumber < partList[j].PartNumber })
	partKeys := make([]string, 0, len(partList))
	for _, part := range partList {
		partKeys = append(partKeys, storageports.MultipartPartObjectKey(request.Target.Key, request.UploadID, part.PartNumber))
	}

	tempKeys, err := b.composeObjects(ctx, client, binding.PhysicalBucket, strings.Trim(strings.TrimSpace(request.Target.Key), "/"), request.UploadID, partKeys, request.CompletionID)
	if err != nil {
		completionErr := err
		matched, reconcileErr := b.multipartCompletionMatches(ctx, client, request.Target, request.CompletionID)
		if reconcileErr != nil {
			return errors.Join(completionErr, reconcileErr)
		}
		if !matched {
			return completionErr
		}
	}

	cleanupErr := error(nil)
	for _, key := range append(partKeys, tempKeys...) {
		if err := client.Bucket(binding.PhysicalBucket).Object(key).Delete(ctx); err != nil {
			cleanupErr = fmt.Errorf("delete multipart component %s: %w", key, err)
			break
		}
	}
	if cleanupErr != nil && strings.TrimSpace(request.CompletionID) == "" {
		return cleanupErr
	}
	return nil
}

func (b *backend) getClient(ctx context.Context, binding storageports.ProviderBinding) (*storage.Client, error) {
	if value, ok := b.cache.Load(binding.LookupKey); ok {
		return value.(*storage.Client), nil
	}

	cred, err := b.credential(binding)
	if err != nil {
		return nil, err
	}
	client, err := newClient(ctx, cred)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	b.cache.Store(binding.LookupKey, client)
	return client, nil
}

func (b *backend) composeObjects(ctx context.Context, client *storage.Client, bucket, destinationKey string, uploadID storageports.UploadID, partKeys []string, completionID string) ([]string, error) {
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
			if err := b.composeBatch(ctx, client, bucket, temporary, current[i:end], ""); err != nil {
				return tempKeys, err
			}
			tempKeys = append(tempKeys, temporary)
			next = append(next, temporary)
		}
		current = next
		round++
	}
	if err := b.composeBatch(ctx, client, bucket, destinationKey, current, completionID); err != nil {
		return tempKeys, err
	}
	return tempKeys, nil
}

func (b *backend) composeBatch(ctx context.Context, client *storage.Client, bucket, destination string, sources []string, completionID string) error {
	destinationObject := client.Bucket(bucket).Object(destination)
	sourceObjects := make([]*storage.ObjectHandle, 0, len(sources))
	for _, source := range sources {
		sourceObjects = append(sourceObjects, client.Bucket(bucket).Object(source))
	}
	composer := destinationObject.ComposerFrom(sourceObjects...)
	if strings.TrimSpace(completionID) != "" {
		composer.ObjectAttrs.Metadata = map[string]string{storageports.MultipartCompletionMarkerMetadataKey: completionID}
	}
	if _, err := composer.Run(ctx); err != nil {
		return fmt.Errorf("failed gcs compose for %s: %w", destination, err)
	}
	return nil
}

func (b *backend) multipartCompletionMatches(ctx context.Context, client *storage.Client, target storageports.Target, completionID string) (bool, error) {
	if strings.TrimSpace(completionID) == "" {
		return false, nil
	}
	attrs, err := client.Bucket(target.PhysicalBucket).Object(strings.Trim(strings.TrimSpace(target.Key), "/")).Attrs(ctx)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, errors.Join(storageports.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect gcs multipart completion marker for %s/%s: %w", target.PhysicalBucket, target.Key, err))
	}
	if attrs == nil {
		return false, errors.Join(storageports.ErrMultipartCompletionIndeterminate, fmt.Errorf("inspect gcs multipart completion marker for %s/%s: provider returned an empty response", target.PhysicalBucket, target.Key))
	}
	return attrs.Metadata[storageports.MultipartCompletionMarkerMetadataKey] == completionID, nil
}
