package buckets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/storage/address"
)

// StorageScope is the resolved physical target for an organization/project
// scope. It contains no caller authorization state. Callers authorize the
// resource before asking the bucket service to resolve it.
type StorageScope struct {
	Provider   string
	Bucket     string
	Prefix     string
	Prefixes   []string
	Credential Credential
}

type StorageScopeErrorKind string

const (
	StorageScopeInvalidInput      StorageScopeErrorKind = "invalid_input"
	StorageScopeNotFound          StorageScopeErrorKind = "scope_not_found"
	StorageScopeCredentialMissing StorageScopeErrorKind = "credential_missing"
	StorageScopeUnsupported       StorageScopeErrorKind = "unsupported"
)

// StorageScopeError preserves the public message while allowing an adapter at
// another boundary to classify the resolution failure without importing its
// error type.
type StorageScopeError struct {
	Kind    StorageScopeErrorKind
	Message string
	Cause   error
}

func (e *StorageScopeError) Error() string {
	if e == nil {
		return "storage scope resolution failed"
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}

func (e *StorageScopeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// ResolveStorageScope resolves the organization/project hierarchy to one
// physical S3 bucket and its effective prefix. Organization-level and
// project-level scopes are composed in lookup order, preserving the existing
// project-prefix behavior.
func (s *Service) ResolveStorageScope(ctx context.Context, organization, project string) (StorageScope, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" {
		return StorageScope{}, &StorageScopeError{
			Kind:    StorageScopeInvalidInput,
			Message: "organization is required",
			Cause:   errorapi.ErrInvalidInput,
		}
	}

	scopes := make([]Scope, 0, 2)
	if scope, found, err := s.LookupBucketScope(ctx, organization, ""); err != nil {
		return StorageScope{}, err
	} else if found {
		scopes = append(scopes, scope)
	}
	if project != "" {
		if scope, found, err := s.LookupBucketScope(ctx, organization, project); err != nil {
			return StorageScope{}, err
		} else if found {
			scopes = append(scopes, scope)
		}
	}
	if len(scopes) == 0 {
		message := fmt.Sprintf("no bucket scope configured for organization %q", organization)
		if project != "" {
			message = fmt.Sprintf("no bucket scope configured for organization %q project %q", organization, project)
		}
		return StorageScope{}, &StorageScopeError{
			Kind:    StorageScopeNotFound,
			Message: message,
			Cause:   errorapi.ErrProjectScopeNotFound,
		}
	}

	bucket := ""
	for _, scope := range scopes {
		if candidate := strings.TrimSpace(scope.Bucket); candidate != "" {
			bucket = candidate
		}
	}
	if bucket == "" {
		return StorageScope{}, &StorageScopeError{
			Kind:    StorageScopeInvalidInput,
			Message: fmt.Sprintf("unable to resolve scoped storage bucket for organization %q project %q", organization, project),
			Cause:   errorapi.ErrInvalidInput,
		}
	}

	credential, err := s.GetS3Credential(ctx, bucket)
	if err != nil {
		if errors.Is(err, errorapi.ErrStorageCredentialMissing) {
			return StorageScope{}, &StorageScopeError{
				Kind:    StorageScopeCredentialMissing,
				Message: fmt.Sprintf("no stored bucket credential found for bucket %q", bucket),
				Cause:   err,
			}
		}
		return StorageScope{}, err
	}
	if credential == nil {
		err := errorapi.ErrStorageCredentialMissing
		return StorageScope{}, &StorageScopeError{
			Kind:    StorageScopeCredentialMissing,
			Message: fmt.Sprintf("no stored bucket credential found for bucket %q", bucket),
			Cause:   err,
		}
	}
	if address.NormalizeProvider(credential.Provider, address.S3Provider) != address.S3Provider {
		return StorageScope{}, &StorageScopeError{
			Kind:    StorageScopeUnsupported,
			Message: fmt.Sprintf("provider %q is not supported for scoped bucket listing", credential.Provider),
			Cause:   errorapi.ErrStorageUnsupported,
		}
	}

	prefixes := normalizedStoragePrefixes(scopes)
	return StorageScope{
		Provider:   address.S3Provider,
		Bucket:     bucket,
		Prefix:     strings.Join(prefixes, "/"),
		Prefixes:   prefixes,
		Credential: *credential,
	}, nil
}

func normalizedStoragePrefixes(scopes []Scope) []string {
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
