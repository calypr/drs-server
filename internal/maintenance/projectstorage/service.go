package projectstorage

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
)

const (
	readMethod              = "read"
	listCoalesceThreshold   = 25
	listFallbackObjectLimit = 5000
)

type Inspector struct {
	scopes      ScopeReader
	credentials CredentialReader
	visibility  VisibilityReader
	inventory   InventoryPort
	probe       ProbePort
	physical    PhysicalScopeReader
}

type ProjectCleanup struct {
	inspector      *Inspector
	delete         DeletePort
	cleanupObjects ObjectScopeDeleter
	cleanupScopes  ScopeCatalog
}

type Service struct {
	*Inspector
	*ProjectCleanup
}

func NewService(deps Dependencies) *Service {
	inspector := &Inspector{
		scopes:      deps.Scopes,
		credentials: deps.Credentials,
		visibility:  deps.Visibility,
		inventory:   deps.Inventory,
		probe:       deps.Probe,
		physical:    deps.Physical,
	}
	return &Service{
		Inspector: inspector,
		ProjectCleanup: &ProjectCleanup{
			inspector:      inspector,
			delete:         deps.Delete,
			cleanupObjects: deps.CleanupObjects,
			cleanupScopes:  deps.CleanupScopes,
		},
	}
}

// InspectProjectStorage inventories the S3 target selected by the project's
// configured scope.
func (s *Inspector) InspectProjectStorage(ctx context.Context, organization, project string, options InspectionOptions) (*InspectionResult, error) {
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
		warning = strings.TrimSpace(listErr.Error())
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

func (s *Inspector) ListObjects(ctx context.Context, organization, project string, includeHead bool) ([]StorageObject, error) {
	result, err := s.InspectProjectStorage(ctx, organization, project, InspectionOptions{Mode: ModeItems, IncludeHead: includeHead})
	if err != nil {
		return nil, err
	}
	return result.Items, nil
}

func (s *Inspector) ResolvePathPrefix(ctx context.Context, organization, project, requestPrefix string) (string, error) {
	target, err := s.resolveScope(withRequestCache(ctx), organization, project, readMethod)
	if err != nil {
		return "", err
	}
	return strings.Trim(strings.TrimSpace(target.withPathPrefix(requestPrefix).Prefix), "/"), nil
}

func (s *Inspector) inventoryObjects(ctx context.Context, bucket, prefix string, options InventoryOptions) ([]StorageObject, error) {
	if s.inventory == nil {
		return nil, &Error{Kind: ErrorUnsupported, Message: "storage inventory is not configured"}
	}
	result, err := s.inventory.Inventory(ctx, storage.InventoryRequest{
		Target:      storage.PrefixTarget{Bucket: bucket, Prefix: prefix},
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

func (s *Inspector) resolveScope(ctx context.Context, organization, project, method string) (scopeTarget, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return scopeTarget{}, &Error{Kind: ErrorInvalidInput, Message: "organization is required"}
	}
	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return scopeTarget{}, &Error{Kind: ErrorInvalidInput, Message: err.Error()}
	}
	if access.IsAuthzEnforced(ctx) && !access.HasMethodAccess(ctx, method, []string{resource}) {
		return scopeTarget{}, &access.AuthorizationError{Method: method, Resources: []string{resource}}
	}
	if s.scopes == nil {
		return scopeTarget{}, &Error{Kind: ErrorUnsupported, Message: "bucket scope reader is not configured"}
	}
	scopes := make([]buckets.Scope, 0, 2)
	if scope, found, lookupErr := s.scopes.LookupBucketScope(ctx, organization, ""); lookupErr != nil {
		return scopeTarget{}, lookupErr
	} else if found {
		scopes = append(scopes, scope)
	}
	if project != "" {
		if scope, found, lookupErr := s.scopes.LookupBucketScope(ctx, organization, project); lookupErr != nil {
			return scopeTarget{}, lookupErr
		} else if found {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		if project != "" {
			return scopeTarget{}, &Error{Kind: ErrorScopeNotFound, Message: fmt.Sprintf("no bucket scope configured for organization %q project %q", organization, project)}
		}
		return scopeTarget{}, &Error{Kind: ErrorScopeNotFound, Message: fmt.Sprintf("no bucket scope configured for organization %q", organization)}
	}
	bucket := ""
	for _, scope := range scopes {
		if candidate := strings.TrimSpace(scope.Bucket); candidate != "" {
			bucket = candidate
		}
	}
	if bucket == "" {
		return scopeTarget{}, &Error{Kind: ErrorInvalidInput, Message: fmt.Sprintf("unable to resolve scoped storage bucket for organization %q project %q", organization, project)}
	}
	credential, err := s.credentialForBucket(ctx, bucket)
	if err != nil {
		return scopeTarget{}, err
	}
	if address.NormalizeProvider(credential.Provider, address.S3Provider) != address.S3Provider {
		return scopeTarget{}, &Error{Kind: ErrorUnsupported, Message: fmt.Sprintf("provider %q is not supported for scoped bucket listing", credential.Provider)}
	}
	prefixes := normalizedPrefixes(scopes)
	return scopeTarget{
		Provider:   address.S3Provider,
		Bucket:     bucket,
		Prefix:     strings.Join(prefixes, "/"),
		prefixes:   prefixes,
		Credential: *credential,
	}, nil
}

func normalizedPrefixes(scopes []buckets.Scope) []string {
	prefixes := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		prefix := strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
		if prefix == "" {
			continue
		}
		if len(prefixes) == 0 {
			prefixes = append(prefixes, prefix)
			continue
		}
		last := prefixes[len(prefixes)-1]
		switch {
		case prefix == last:
		case strings.HasPrefix(prefix, last+"/"):
			prefixes[len(prefixes)-1] = prefix
		case strings.HasPrefix(last, prefix+"/"):
		default:
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
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
		kind = ErrorBucketUnavailable
	case storage.ErrorIncomplete:
		kind = ErrorListingIncomplete
	case storage.ErrorUnsupported:
		kind = ErrorUnsupported
	case storage.ErrorProvider:
		return err
	}
	message := operation.Error()
	if strings.TrimSpace(message) == "" {
		message = fmt.Sprintf("storage %s failed for %s/%s", capability, bucket, key)
	}
	return &Error{Kind: kind, Message: message}
}

func (s *Inspector) credentialForBucket(ctx context.Context, bucket string) (*buckets.Credential, error) {
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

func (s *Inspector) visibleBuckets(ctx context.Context) (map[string]buckets.VisibleBucket, error) {
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
