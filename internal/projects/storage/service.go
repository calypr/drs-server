package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const (
	readMethod              = "read"
	listCoalesceThreshold   = 25
	listFallbackObjectLimit = 5000
)

type Service struct {
	resolver       ScopeResolver
	credentials    CredentialReader
	visibility     VisibilityReader
	inventory      InventoryPort
	probe          ProbePort
	delete         DeletePort
	cleanupObjects ObjectScopeDeleter
	cleanupScopes  ScopeCatalog
}

func NewService(deps Dependencies) *Service {
	return &Service{
		resolver:       deps.ScopeResolver,
		credentials:    deps.Credentials,
		visibility:     deps.Visibility,
		inventory:      deps.Providers.Inventory,
		probe:          deps.Providers.Probe,
		delete:         deps.Providers.Delete,
		cleanupObjects: deps.ObjectCleanup,
		cleanupScopes:  deps.ScopeCatalog,
	}
}

// InspectProjectStorage inventories the S3 target selected by the project's
// configured scope.
func (s *Service) InspectProjectStorage(ctx context.Context, organization, project string, options InspectionOptions) (*InspectionResult, error) {
	ctx = withRequestCache(ctx)
	target, err := s.resolveScope(ctx, organization, project, readMethod)
	if err != nil {
		return nil, err
	}
	target = target.withPathPrefix(options.PathPrefix)
	mode := normalizeMode(options.Mode)
	listOptions := InventoryOptions{IncludeHead: options.IncludeHead}
	if mode == ModeExists {
		listOptions.MaxKeys = 1
	}
	items, listErr := s.inventoryObjects(ctx, target.Bucket, target.Prefix, listOptions)
	complete := listErr == nil
	warning := ""
	if listErr != nil {
		var inspectErr *Error
		if len(items) == 0 || !errors.As(listErr, &inspectErr) || inspectErr.Kind != ErrorListingIncomplete {
			return nil, listErr
		}
		logStorageDiagnostic(ctx, listErr, "inventory")
		warning = safeStorageErrorMessage(listErr, "inventory")
	}
	normalized := normalizeObjects(items, target)
	summary := summarize(normalized, target, mode)
	summary.InventoryComplete = complete
	summary.InventoryWarning = warning
	if mode != ModeItems {
		normalized = []StorageObject{}
	}
	return &InspectionResult{Summary: summary, Items: normalized}, nil
}

func (s *Service) inventoryObjects(ctx context.Context, bucket, prefix string, options InventoryOptions) ([]StorageObject, error) {
	if s.inventory == nil {
		return nil, &Error{Kind: ErrorUnsupported, Message: "storage inventory is not configured"}
	}
	result, err := s.inventory.Inventory(ctx, storage.InventoryRequest{
		Target:      storage.Target{Provider: address.S3Provider, PhysicalBucket: bucket, LookupKey: bucket, LookupCandidates: []string{bucket}},
		Prefix:      prefix,
		IncludeHead: options.IncludeHead,
		ExactPrefix: options.ExactPrefix,
		MaxKeys:     options.MaxKeys,
	})
	items := make([]StorageObject, 0, len(result.Items))
	for _, metadata := range result.Items {
		key := strings.Trim(strings.TrimSpace(metadata.Key), "/")
		if key == "" {
			continue
		}
		item := StorageObject{
			ObjectURL:   address.BucketToURL(bucket, key),
			Provider:    strings.TrimSpace(metadata.Provider),
			Bucket:      strings.TrimSpace(metadata.Bucket),
			Key:         key,
			Path:        strings.TrimSpace(metadata.Path),
			SizeBytes:   metadata.SizeBytes,
			MetaSHA256:  strings.TrimSpace(metadata.MetaSHA256),
			ETag:        strings.TrimSpace(metadata.ETag),
			LastModTime: metadata.LastModified,
		}
		if item.Provider == "" {
			item.Provider = address.S3Provider
		}
		if item.Bucket == "" {
			item.Bucket = bucket
		}
		if item.Path == "" {
			item.Path = path.Base(key)
		}
		items = append(items, item)
	}
	if err != nil {
		return items, mapStorageError(err, "inventory", bucket, prefix)
	}
	if !result.Complete {
		return items, &Error{Kind: ErrorListingIncomplete, Message: fmt.Sprintf("provider returned an incomplete listing for s3://%s/%s", bucket, strings.Trim(strings.TrimSpace(prefix), "/"))}
	}
	return items, nil
}

func normalizeMode(mode InspectionMode) InspectionMode {
	switch mode {
	case ModeExists, ModeSummary:
		return mode
	default:
		return ModeItems
	}
}

type scopeTarget struct {
	Provider   string
	Bucket     string
	Prefix     string
	prefixes   []string
	Credential buckets.Credential
}

func (target scopeTarget) withPathPrefix(requestPrefix string) scopeTarget {
	trimmed := strings.Trim(strings.TrimSpace(requestPrefix), "/")
	if trimmed == "" {
		return target
	}
	if target.Prefix == "" {
		target.Prefix = trimmed
	} else {
		target.Prefix = strings.Trim(strings.TrimSpace(target.Prefix), "/") + "/" + trimmed
	}
	return target
}

func (s *Service) resolveScope(ctx context.Context, organization, project, method string) (scopeTarget, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return scopeTarget{}, &Error{Kind: ErrorInvalidInput, Message: "organization is required"}
	}
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return scopeTarget{}, &Error{Kind: ErrorInvalidInput, Message: err.Error()}
	}
	if access.IsAuthzEnforced(ctx) && !access.HasMethodAccess(ctx, method, []string{resource}) {
		return scopeTarget{}, &access.AuthorizationError{Method: method, Resources: []string{resource}}
	}
	if s.resolver == nil {
		return scopeTarget{}, &Error{Kind: ErrorUnsupported, Message: "bucket scope resolver is not configured"}
	}
	resolved, err := s.resolver.ResolveStorageScope(ctx, organization, project)
	if err != nil {
		return scopeTarget{}, mapScopeResolutionError(err)
	}
	return scopeTarget{
		Provider:   resolved.Provider,
		Bucket:     resolved.Bucket,
		Prefix:     resolved.Prefix,
		prefixes:   append([]string(nil), resolved.Prefixes...),
		Credential: resolved.Credential,
	}, nil
}

func mapScopeResolutionError(err error) error {
	if err == nil {
		return nil
	}
	var resolutionErr *buckets.StorageScopeError
	if !errors.As(err, &resolutionErr) {
		if errors.Is(err, errorapi.ErrProjectScopeNotFound) {
			return &Error{Kind: ErrorScopeNotFound, Message: err.Error(), Cause: err}
		}
		if errors.Is(err, errorapi.ErrStorageCredentialMissing) {
			return &Error{Kind: ErrorCredentialMissing, Message: err.Error(), Cause: err}
		}
		return err
	}
	switch resolutionErr.Kind {
	case buckets.StorageScopeInvalidInput:
		return &Error{Kind: ErrorInvalidInput, Message: resolutionErr.Message, Cause: resolutionErr.Cause}
	case buckets.StorageScopeNotFound:
		return &Error{Kind: ErrorScopeNotFound, Message: resolutionErr.Message, Cause: resolutionErr.Cause}
	case buckets.StorageScopeCredentialMissing:
		return &Error{Kind: ErrorCredentialMissing, Message: resolutionErr.Message, Cause: resolutionErr.Cause}
	case buckets.StorageScopeUnsupported:
		return &Error{Kind: ErrorUnsupported, Message: resolutionErr.Message, Cause: resolutionErr.Cause}
	default:
		return err
	}
}

func normalizeObjects(items []StorageObject, target scopeTarget) []StorageObject {
	out := make([]StorageObject, 0, len(items))
	for _, item := range items {
		item.Provider = address.S3Provider
		item.Bucket = target.Bucket
		item.Key = strings.Trim(strings.TrimSpace(item.Key), "/")
		item.ObjectURL = address.BucketToURL(target.Bucket, item.Key)
		if strings.TrimSpace(item.Path) == "" {
			item.Path = path.Base(item.Key)
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func summarize(items []StorageObject, target scopeTarget, mode InspectionMode) Summary {
	result := Summary{
		Provider:          target.Provider,
		Bucket:            target.Bucket,
		Prefix:            strings.Trim(strings.TrimSpace(target.Prefix), "/"),
		ObjectURL:         address.BucketToURL(target.Bucket, strings.Trim(strings.TrimSpace(target.Prefix), "/")),
		Exists:            len(items) > 0,
		ObjectCount:       len(items),
		ComputedAt:        time.Now().UTC(),
		Mode:              mode,
		InventoryComplete: true,
	}
	for _, item := range items {
		result.TotalBytes += item.SizeBytes
	}
	return result
}

func mapStorageError(err error, capability, bucket, key string) error {
	if err == nil {
		return nil
	}
	var operation *storage.OperationError
	if !errors.As(err, &operation) {
		return err
	}
	kind := ErrorBucketUnavailable
	switch operation.Kind {
	case storage.ErrorInvalid:
		kind = ErrorInvalidInput
	case storage.ErrorNotFound:
		if strings.TrimSpace(operation.Provider) == "" {
			kind = ErrorCredentialMissing
		} else {
			kind = ErrorObjectNotFound
		}
	case storage.ErrorForbidden:
		kind = ErrorPermissionDenied
	case storage.ErrorUnavailable:
		return err
	case storage.ErrorIncomplete:
		kind = ErrorListingIncomplete
	case storage.ErrorUnsupported:
		kind = ErrorUnsupported
	case storage.ErrorProvider:
		return err
	default:
		return err
	}
	return &Error{Kind: kind, Message: storageErrorMessage(kind, capability, bucket, key), Cause: err}
}

func safeStorageErrorMessage(err error, capability string) string {
	if err == nil {
		return ""
	}
	var inspectErr *Error
	if errors.As(err, &inspectErr) {
		return inspectErr.PublicMessage()
	}
	var operation *storage.OperationError
	if errors.As(err, &operation) {
		switch operation.Kind {
		case storage.ErrorUnavailable:
			return fmt.Sprintf("storage %s is temporarily unavailable", capability)
		case storage.ErrorIncomplete:
			return fmt.Sprintf("storage %s returned incomplete results", capability)
		case storage.ErrorForbidden:
			return "storage access is forbidden"
		case storage.ErrorNotFound:
			return "storage object was not found"
		case storage.ErrorInvalid:
			return fmt.Sprintf("storage %s request is invalid", capability)
		case storage.ErrorUnsupported:
			return fmt.Sprintf("storage %s is not supported", capability)
		default:
			return fmt.Sprintf("storage %s failed", capability)
		}
	}
	return fmt.Sprintf("storage %s failed", capability)
}

func logStorageDiagnostic(ctx context.Context, err error, capability string) {
	if err == nil {
		return
	}
	provider := ""
	diagnostic := err
	var operation *storage.OperationError
	if errors.As(err, &operation) {
		provider = operation.Provider
		diagnostic = operation
	}
	slog.Warn("project storage operation failed", "request_id", requestid.GetRequestID(ctx), "provider", provider, "capability", capability, "err", diagnostic)
}

func storageErrorMessage(kind ErrorKind, capability, bucket, key string) string {
	switch kind {
	case ErrorInvalidInput:
		return fmt.Sprintf("storage %s request is invalid", capability)
	case ErrorCredentialMissing:
		return fmt.Sprintf("no stored storage credential found for bucket %q", bucket)
	case ErrorObjectNotFound:
		return fmt.Sprintf("storage object %q was not found", key)
	case ErrorPermissionDenied:
		return "storage access is forbidden"
	case ErrorListingIncomplete:
		return fmt.Sprintf("storage %s returned incomplete results", capability)
	case ErrorUnsupported:
		return fmt.Sprintf("storage %s is not supported", capability)
	default:
		return fmt.Sprintf("storage %s failed", capability)
	}
}

func (s *Service) credentialForBucket(ctx context.Context, bucket string) (*buckets.Credential, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return nil, &Error{Kind: ErrorInvalidInput, Message: "bucket is required"}
	}
	if cache := cacheFromContext(ctx); cache != nil {
		if credential, err, ok := cache.credential(bucket); ok {
			return credential, err
		}
	}
	if s.credentials == nil {
		err := &Error{Kind: ErrorUnsupported, Message: "bucket credential reader is not configured"}
		cacheCredential(ctx, bucket, nil, err)
		return nil, err
	}
	if credential, err := s.credentials.GetS3Credential(ctx, bucket); err == nil && credential != nil {
		copy := *credential
		cacheCredential(ctx, bucket, &copy, nil)
		return &copy, nil
	}
	credentials, err := s.credentials.ListS3Credentials(ctx)
	if err != nil {
		cacheCredential(ctx, bucket, nil, err)
		return nil, err
	}
	for _, credential := range credentials {
		if strings.EqualFold(strings.TrimSpace(credential.Bucket), bucket) || strings.EqualFold(strings.TrimSpace(credential.CredentialID), bucket) {
			copy := credential
			cacheCredential(ctx, bucket, &copy, nil)
			return &copy, nil
		}
	}
	err = &Error{Kind: ErrorCredentialMissing, Message: fmt.Sprintf("no stored bucket credential found for bucket %q", bucket)}
	cacheCredential(ctx, bucket, nil, err)
	return nil, err
}

func (s *Service) visibleBuckets(ctx context.Context) (map[string]buckets.VisibleBucket, error) {
	if cache := cacheFromContext(ctx); cache != nil {
		if visible, err, ok := cache.visible(); ok {
			return visible, err
		}
	}
	if s.visibility == nil {
		err := &Error{Kind: ErrorUnsupported, Message: "bucket visibility reader is not configured"}
		cacheVisible(ctx, nil, err)
		return nil, err
	}
	visible, err := s.visibility.ListVisibleBuckets(ctx)
	cacheVisible(ctx, visible, err)
	return cloneVisible(visible), err
}
