package buckets

import (
	"context"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
)

// ScopeAllowed reports whether the caller may use the requested methods for a
// bucket scope. Transport authentication is checked by the HTTP handler; this
// function owns the authorization policy shared by bucket endpoints.
func ScopeAllowed(ctx context.Context, scope Scope, methods ...string) bool {
	resource, err := clientaccess.ResourcePath(scope.Organization, scope.ProjectID)
	if err != nil || resource == "" {
		return false
	}
	return access.HasAnyMethodAccess(ctx, []string{resource}, methods...)
}

// BucketsAllowedByNames filters scopes by their physical bucket name and
// applies ScopeAllowed to matching scopes.
func BucketsAllowedByNames(ctx context.Context, scopes []Scope, bucket string, methods ...string) bool {
	for _, scope := range scopes {
		if scope.Bucket != bucket {
			continue
		}
		if ScopeAllowed(ctx, scope, methods...) {
			return true
		}
	}
	return false
}

// AuthorizeScopeWrite checks the shared authorization policy for creating,
// updating, or deleting a bucket scope. Missing Gen3 headers remain a
// transport concern and must be rejected by the caller before invoking this
// function.
func AuthorizeScopeWrite(ctx context.Context, organization, project string, methods ...string) error {
	if strings.TrimSpace(organization) == "" {
		if !access.IsAuthzEnforced(ctx) {
			return nil
		}
		return faults.ErrUnauthorized
	}
	res, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if res != "" && access.HasAnyMethodAccess(ctx, []string{res}, methods...) {
		return nil
	}

	orgResource, err := clientaccess.ResourcePath(organization, "")
	if err != nil {
		return err
	}
	if orgResource != "" && access.HasAnyServiceMethodAccess(ctx, []string{orgResource}, "arborist", "create-descendant", "manage-owners") {
		return nil
	}
	return faults.ErrUnauthorized
}
