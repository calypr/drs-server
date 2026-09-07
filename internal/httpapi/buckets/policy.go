package buckets

import (
	"context"
	"strings"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
)

func bucketScopeAllowed(ctx context.Context, scope domainbuckets.Scope, methods ...string) bool {
	resource, err := sycommon.ResourcePath(scope.Organization, scope.ProjectID)
	if err != nil || resource == "" {
		return false
	}
	return access.HasAnyMethodAccess(ctx, []string{resource}, methods...)
}

func resourceAllowed(ctx context.Context, resource string, methods ...string) bool {
	return access.HasAnyMethodAccess(ctx, []string{resource}, methods...)
}

func serviceResourceAllowed(ctx context.Context, resource, service string, methods ...string) bool {
	return access.HasAnyServiceMethodAccess(ctx, []string{resource}, service, methods...)
}

func bucketsAllowedByNames(ctx context.Context, scopes []domainbuckets.Scope, bucket string, methods ...string) bool {
	for _, scope := range scopes {
		if scope.Bucket != bucket {
			continue
		}
		if bucketScopeAllowed(ctx, scope, methods...) {
			return true
		}
	}
	return false
}

func authorizeBucketScopeWrite(ctx context.Context, organization, project string, methods ...string) error {
	if strings.TrimSpace(organization) == "" {
		if access.IsGen3Mode(ctx) && apimiddleware.MissingGen3AuthHeader(ctx) {
			return faults.ErrUnauthorized
		}
		if !access.IsAuthzEnforced(ctx) {
			return nil
		}
		return faults.ErrUnauthorized
	}
	if apimiddleware.MissingGen3AuthHeader(ctx) {
		return faults.ErrUnauthorized
	}
	res, err := sycommon.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if res != "" && resourceAllowed(ctx, res, methods...) {
		return nil
	}

	orgResource, err := sycommon.ResourcePath(organization, "")
	if err != nil {
		return err
	}
	if orgResource != "" && serviceResourceAllowed(ctx, orgResource, "arborist", "create-descendant", "manage-owners") {
		return nil
	}
	return faults.ErrUnauthorized
}

func authorizeBucketDelete(ctx context.Context, bucketService *domainbuckets.Service, bucket string) error {
	if apimiddleware.MissingGen3AuthHeader(ctx) {
		return faults.ErrUnauthorized
	}
	scopes, err := bucketService.ListBucketScopes(ctx)
	if err != nil {
		return err
	}
	if !bucketsAllowedByNames(ctx, scopes, bucket, "delete", "update") {
		return faults.ErrUnauthorized
	}
	return nil
}
