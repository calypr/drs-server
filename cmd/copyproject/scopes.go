package copyproject

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/bucketapi"
	"github.com/calypr/syfon/client/services"
	"github.com/calypr/syfon/cmd/projectcopy"
	"github.com/spf13/cobra"
)

type resolvedCopyScopes struct {
	source projectcopy.Scope
	target projectcopy.Scope

	sourceBucket string
	targetBucket string

	sourceProject *bucketapi.BucketScopeResponse
	sourceOrg     *bucketapi.BucketScopeResponse
	targetProject *bucketapi.BucketScopeResponse
	targetOrg     *bucketapi.BucketScopeResponse
}

func resolveCopyScopes(ctx context.Context, sourceBuckets, targetBuckets *services.BucketsService, sourceBucketMap, targetBucketMap map[string]bucketapi.BucketMetadata, srcScope, dstScope projectcopy.Scope) (*resolvedCopyScopes, error) {
	resolved := &resolvedCopyScopes{
		source: srcScope,
		target: dstScope,
	}

	for bucketName := range sourceBucketMap {
		scopes, err := sourceBuckets.ListScopes(ctx, bucketName)
		if err != nil {
			return nil, fmt.Errorf("failed to list source scopes for bucket %q: %w", bucketName, err)
		}
		for _, scope := range scopes {
			switch {
			case scope.Organization == srcScope.Organization && scope.ProjectId == srcScope.Project:
				scopeCopy := scope
				resolved.sourceProject = &scopeCopy
				resolved.sourceBucket = bucketName
			case scope.Organization == srcScope.Organization && scope.ProjectId == "":
				scopeCopy := scope
				resolved.sourceOrg = &scopeCopy
			}
		}
	}

	for bucketName := range targetBucketMap {
		scopes, err := targetBuckets.ListScopes(ctx, bucketName)
		if err != nil {
			return nil, fmt.Errorf("failed to list destination scopes for bucket %q: %w", bucketName, err)
		}
		for _, scope := range scopes {
			switch {
			case scope.Organization == dstScope.Organization && scope.ProjectId == dstScope.Project:
				scopeCopy := scope
				resolved.targetProject = &scopeCopy
				resolved.targetBucket = bucketName
			case scope.Organization == dstScope.Organization && scope.ProjectId == "":
				scopeCopy := scope
				resolved.targetOrg = &scopeCopy
				if resolved.targetBucket == "" {
					resolved.targetBucket = bucketName
				}
			}
		}
	}

	if resolved.sourceProject == nil {
		return resolved, nil
	}
	if resolved.sourceBucket == "" {
		return nil, fmt.Errorf("source project scope %s/%s exists but has no bucket mapping", srcScope.Organization, srcScope.Project)
	}
	if resolved.targetBucket == "" {
		if _, ok := targetBucketMap[resolved.sourceBucket]; ok {
			resolved.targetBucket = resolved.sourceBucket
		} else {
			return nil, fmt.Errorf("destination scope %s/%s has no bucket mapping on the destination instance, and source bucket %q is not configured there", dstScope.Organization, dstScope.Project, resolved.sourceBucket)
		}
	}
	return resolved, nil
}

func ensureDestinationScopes(ctx context.Context, cmd *cobra.Command, buckets *services.BucketsService, resolved *resolvedCopyScopes) error {
	if resolved.targetBucket == "" {
		return fmt.Errorf("failed to resolve a destination bucket for %s/%s", resolved.target.Organization, resolved.target.Project)
	}

	if resolved.targetOrg == nil {
		orgPath := defaultOrgScopePath(resolved.targetBucket, resolved.target.Organization)
		if remapped, ok := remapOrgScopePath(resolved.sourceOrg, resolved.source.Organization, resolved.targetBucket, resolved.target.Organization); ok {
			orgPath = remapped
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Creating organization scope mapping on bucket %s: %s -> %s\n", resolved.targetBucket, resolved.target.Organization, orgPath)
		if err := buckets.AddScope(ctx, resolved.targetBucket, bucketapi.AddBucketScopeRequest{
			Organization: resolved.target.Organization,
			Path:         &orgPath,
		}); err != nil {
			return fmt.Errorf("failed to map organization scope on target bucket: %w", err)
		}

		resolved.targetOrg = &bucketapi.BucketScopeResponse{
			Organization: resolved.target.Organization,
			Path:         &orgPath,
		}
	}

	if resolved.targetProject != nil {
		return nil
	}

	projectPath := defaultProjectScopePath(resolved.targetBucket, resolved.target.Organization, resolved.target.Project)
	if resolved.targetOrg != nil && resolved.targetOrg.Path != nil && strings.TrimSpace(*resolved.targetOrg.Path) != "" {
		projectPath = strings.TrimRight(strings.TrimSpace(*resolved.targetOrg.Path), "/") + "/" + resolved.target.Project
	}
	if remapped, ok := remapProjectScopePath(resolved.sourceProject, resolved.sourceOrg, resolved.targetOrg, resolved.source, resolved.target, resolved.targetBucket); ok {
		projectPath = remapped
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Creating project scope mapping on bucket %s: %s/%s -> %s\n", resolved.targetBucket, resolved.target.Organization, resolved.target.Project, projectPath)
	if err := buckets.AddScope(ctx, resolved.targetBucket, bucketapi.AddBucketScopeRequest{
		Organization: resolved.target.Organization,
		ProjectId:    resolved.target.Project,
		Path:         &projectPath,
	}); err != nil {
		return fmt.Errorf("failed to map project scope on target bucket: %w", err)
	}

	resolved.targetProject = &bucketapi.BucketScopeResponse{
		Organization: resolved.target.Organization,
		ProjectId:    resolved.target.Project,
		Path:         &projectPath,
	}
	return nil
}
