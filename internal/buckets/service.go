package buckets

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/faults"
)

const defaultScopeCacheTTL = 30 * time.Second

var errMissingVisibilitySource = errors.New("bucket service requires a visibility query or fallback")

// Dependencies are the narrow repository and visibility ports used by Service.
// Visibility is an optional persistence optimization; Fallback is the
// composition-owned object scan used when that optimization is unavailable.
type Dependencies struct {
	Credentials     CredentialReader
	CredentialAdmin CredentialAdmin
	Scopes          ScopeStore
	Visibility      VisibilityQuery
	Fallback        VisibilityFallback
}

type cacheInvalidator interface {
	InvalidateBucket(string)
}

// Service owns bucket credential and scope policy, including cache
// invalidation. Repository adapters remain responsible for SQL, encryption,
// auditing, and transaction semantics.
type Service struct {
	credentialReader       CredentialReader
	credentialAdmin        CredentialAdmin
	scopeStore             ScopeStore
	visibility             VisibilityQuery
	fallback               VisibilityFallback
	scopeCache             *scopeCache
	signerCacheInvalidator cacheInvalidator
}

// NewService validates and constructs the bucket service with its production
// scope-cache lifetime. A nil invalidator is a supported no-op configuration.
func NewService(deps Dependencies, invalidator cacheInvalidator) (*Service, error) {
	if deps.Credentials == nil {
		return nil, errors.New("bucket service requires credential reader")
	}
	if deps.CredentialAdmin == nil {
		return nil, errors.New("bucket service requires credential admin")
	}
	if deps.Scopes == nil {
		return nil, errors.New("bucket service requires scope store")
	}
	if deps.Visibility == nil && deps.Fallback == nil {
		return nil, errMissingVisibilitySource
	}
	return newService(deps, invalidator, defaultScopeCacheTTL, time.Now), nil
}

func newService(deps Dependencies, invalidator cacheInvalidator, ttl time.Duration, now func() time.Time) *Service {
	return &Service{
		credentialReader:       deps.Credentials,
		credentialAdmin:        deps.CredentialAdmin,
		scopeStore:             deps.Scopes,
		visibility:             deps.Visibility,
		fallback:               deps.Fallback,
		scopeCache:             newScopeCache(ttl, now),
		signerCacheInvalidator: invalidator,
	}
}

// ListS3Credentials returns configured credentials in repository order. The
// first returned credential remains the default bucket selection.
func (s *Service) ListS3Credentials(ctx context.Context) ([]Credential, error) {
	return s.credentialReader.ListS3Credentials(ctx)
}

// GetS3Credential resolves a credential by its canonical ID or legacy physical
// bucket alias according to the repository compatibility contract.
func (s *Service) GetS3Credential(ctx context.Context, bucket string) (*Credential, error) {
	credential, exactErr := s.credentialReader.GetS3Credential(ctx, bucket)
	if exactErr == nil && credential != nil {
		return credential, nil
	}
	if exactErr != nil && !isCredentialNotFoundError(exactErr) {
		return nil, exactErr
	}

	credentials, listErr := s.credentialReader.ListS3Credentials(ctx)
	if listErr != nil {
		return nil, listErr
	}
	requested := strings.TrimSpace(bucket)
	for _, candidate := range credentials {
		if strings.EqualFold(strings.TrimSpace(candidate.Bucket), requested) ||
			strings.EqualFold(strings.TrimSpace(candidate.CredentialID), requested) {
			copy := candidate
			return &copy, nil
		}
	}
	return nil, exactErr
}

func isCredentialNotFoundError(err error) bool {
	return faults.IsNotFoundError(err) || strings.EqualFold(strings.TrimSpace(err.Error()), "credential not found")
}
