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
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"google.golang.org/api/iterator"
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

func (b *backend) AbortMultipart(ctx context.Context, binding storageports.ProviderBinding, request storageports.AbortMultipartRequest) error {
	client, release, err := b.acquireClient(ctx, binding)
	if err != nil {
		return err
	}
	defer release()
	prefix, err := storageports.MultipartUploadPrefix(request.Target.Key, request.UploadID)
	if err != nil {
		return err
	}
	objects := client.Bucket(binding.PhysicalBucket).Objects(ctx, &storage.Query{Prefix: prefix})
	var cleanupErrs []error
	for {
		object, iterErr := objects.Next()
		if iterErr == iterator.Done {
			break
		}
		if iterErr != nil {
			return fmt.Errorf("list gcs multipart components: %w", iterErr)
		}
		if object == nil || object.Name == "" {
			continue
		}
		if deleteErr := client.Bucket(binding.PhysicalBucket).Object(object.Name).Delete(ctx); deleteErr != nil && !isNotFound(deleteErr) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete gcs multipart component %s: %w", object.Name, deleteErr))
		}
	}
	return errors.Join(cleanupErrs...)
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
	client, release, err := b.acquireClient(ctx, binding)
	if err != nil {
		return err
	}
	defer release()
	if matched, err := b.multipartCompletionMatches(ctx, client, request.Target, request.CompletionID); err != nil {
		return err
	} else if matched {
		partKeys := multipartPartKeys(request)
		return b.cleanupKeys(ctx, client, binding.PhysicalBucket, append(partKeys, multipartIntermediateKeys(request.Target.Key, request.UploadID, len(partKeys))...))
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
			return errors.Join(completionErr, reconcileErr, b.cleanupKeys(ctx, client, binding.PhysicalBucket, tempKeys))
		}
		if !matched {
			return errors.Join(completionErr, b.cleanupKeys(ctx, client, binding.PhysicalBucket, tempKeys))
		}
		if cleanupErr := b.cleanupKeys(ctx, client, binding.PhysicalBucket, append(partKeys, tempKeys...)); cleanupErr != nil {
			return cleanupErr
		}
	}

	if err == nil {
		if cleanupErr := b.cleanupKeys(ctx, client, binding.PhysicalBucket, append(partKeys, tempKeys...)); cleanupErr != nil {
			return cleanupErr
		}
	}
	return nil
}

func (b *backend) getClient(ctx context.Context, binding storageports.ProviderBinding) (*storage.Client, error) {
	client, _, err := b.clientFor(ctx, binding)
	return client, err
}

func (b *backend) acquireClient(ctx context.Context, binding storageports.ProviderBinding) (*storage.Client, func(), error) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	client, entry, err := b.clientForLocked(ctx, binding)
	if err != nil {
		return nil, nil, err
	}
	entry.users++
	var once sync.Once
	release := func() {
		once.Do(func() {
			b.cacheMu.Lock()
			if entry.users > 0 {
				entry.users--
			}
			if entry.users == 0 && entry.retired {
				b.closeEntryLocked(entry)
			}
			if b.activeCond != nil {
				b.activeCond.Broadcast()
			}
			b.cacheMu.Unlock()
		})
	}
	return client, release, nil
}

func (b *backend) clientFor(ctx context.Context, binding storageports.ProviderBinding) (*storage.Client, *clientEntry, error) {
	b.cacheMu.Lock()
	defer b.cacheMu.Unlock()
	return b.clientForLocked(ctx, binding)
}

func (b *backend) clientForLocked(ctx context.Context, binding storageports.ProviderBinding) (*storage.Client, *clientEntry, error) {
	if b.closed {
		return nil, nil, errors.New("gcs storage backend is closed")
	}
	if b.clients == nil {
		b.clients = make(map[string]*clientEntry)
	}
	if b.allClients == nil {
		b.allClients = make(map[*clientEntry]struct{})
	}
	key := canonicalCacheKey(binding.LookupKey)
	if key == "" {
		key = canonicalCacheKey(binding.PhysicalBucket)
	}
	credential := credentialIdentityOf(binding.Credential)
	if current := b.clients[key]; current != nil {
		if current.credential == credential {
			return current.client, current, nil
		}
		current.retired = true
		delete(b.clients, key)
		if current.users == 0 {
			b.closeEntryLocked(current)
		}
	}
	if value, ok := b.cache.Load(key); ok {
		if client, ok := value.(*storage.Client); ok {
			entry := &clientEntry{client: client, credential: credential}
			b.clients[key] = entry
			b.allClients[entry] = struct{}{}
			return client, entry, nil
		}
	}
	cred, err := b.credential(binding)
	if err != nil {
		return nil, nil, err
	}
	client, err := newClient(ctx, cred)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCS client: %w", err)
	}
	if b.closed {
		_ = client.Close()
		return nil, nil, errors.New("gcs storage backend is closed")
	}
	entry := &clientEntry{client: client, credential: credential}
	b.clients[key] = entry
	b.allClients[entry] = struct{}{}
	b.cache.Store(key, client)
	return client, entry, nil
}

func (b *backend) closeEntryLocked(entry *clientEntry) {
	if entry == nil || entry.closed {
		return
	}
	entry.closed = true
	if err := entry.client.Close(); err != nil {
		b.closeErrors = append(b.closeErrors, err)
	}
	entry.credential = credentialIdentity{}
}

func (b *backend) Close() error {
	b.closeOnce.Do(func() {
		b.cacheMu.Lock()
		if b.activeCond == nil {
			b.activeCond = sync.NewCond(&b.cacheMu)
		}
		b.closed = true
		if b.allClients == nil {
			b.allClients = make(map[*clientEntry]struct{})
		}
		b.cache.Range(func(_, value any) bool {
			client, ok := value.(*storage.Client)
			if !ok {
				return true
			}
			for entry := range b.allClients {
				if entry.client == client {
					return true
				}
			}
			b.allClients[&clientEntry{client: client}] = struct{}{}
			return true
		})
		for {
			active := false
			for entry := range b.allClients {
				if entry.users > 0 {
					active = true
					break
				}
			}
			if !active {
				break
			}
			b.activeCond.Wait()
		}
		closeErrs := append([]error(nil), b.closeErrors...)
		for entry := range b.allClients {
			if entry.closed {
				continue
			}
			entry.closed = true
			if err := entry.client.Close(); err != nil {
				closeErrs = append(closeErrs, err)
			}
			entry.credential = credentialIdentity{}
		}
		b.clients = make(map[string]*clientEntry)
		b.cache.Range(func(key, _ any) bool { b.cache.Delete(key); return true })
		b.cacheMu.Unlock()
		b.closeErr = errors.Join(closeErrs...)
	})
	return b.closeErr
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
			tempKeys = append(tempKeys, temporary)
			if err := b.composeBatch(ctx, client, bucket, temporary, current[i:end], ""); err != nil {
				return tempKeys, err
			}
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

func multipartPartKeys(request storageports.CompleteMultipartRequest) []string {
	parts := append([]storageports.CompletedPart(nil), request.Parts...)
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		keys = append(keys, storageports.MultipartPartObjectKey(request.Target.Key, request.UploadID, part.PartNumber))
	}
	return keys
}

func multipartIntermediateKeys(destinationKey string, uploadID storageports.UploadID, partCount int) []string {
	if partCount <= 32 {
		return nil
	}
	current := partCount
	round := 0
	keys := make([]string, 0)
	for current > 32 {
		groups := (current + 31) / 32
		for index := 0; index < groups; index++ {
			keys = append(keys, path.Join(".syfon-multipart", strings.TrimSpace(string(uploadID)), strings.Trim(strings.TrimSpace(destinationKey), "/"), "compose", fmt.Sprintf("%d-%d", round, index)))
		}
		current = groups
		round++
	}
	return keys
}

func (b *backend) cleanupKeys(ctx context.Context, client *storage.Client, bucket string, keys []string) error {
	var cleanupErrs []error
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := client.Bucket(bucket).Object(key).Delete(ctx); err != nil && !isNotFound(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete multipart component %s: %w", key, err))
		}
	}
	return errors.Join(cleanupErrs...)
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
