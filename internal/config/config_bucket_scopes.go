package config

import (
	"fmt"
	"path"
	"strings"
)

func cleanBucketScopeSubPath(raw string) string {
	return strings.Trim(path.Clean("/"+strings.TrimSpace(raw)), "/")
}

func deriveBucketScopesFromBuckets(buckets []BucketConfig) ([]BucketScopeConfig, error) {
	scopes := make([]BucketScopeConfig, 0)
	for i, bucket := range buckets {
		for j, resource := range bucket.Resources {
			resourceScopes, err := bucketResourceScopes(bucket.CredentialID, bucket.Bucket, resource)
			if err != nil {
				return nil, fmt.Errorf("buckets[%d].resources[%d]: %w", i, j, err)
			}
			scopes = append(scopes, resourceScopes...)
		}
	}
	return scopes, nil
}

func credentialIDsByPhysicalBucket(buckets []BucketConfig) map[string][]string {
	idsByBucket := make(map[string][]string)
	seen := make(map[string]map[string]bool)
	for _, bucket := range buckets {
		bucketName := strings.ToLower(strings.TrimSpace(bucket.Bucket))
		credentialID := strings.TrimSpace(bucket.CredentialID)
		if bucketName == "" || credentialID == "" {
			continue
		}
		if seen[bucketName] == nil {
			seen[bucketName] = make(map[string]bool)
		}
		if seen[bucketName][credentialID] {
			continue
		}
		seen[bucketName][credentialID] = true
		idsByBucket[bucketName] = append(idsByBucket[bucketName], credentialID)
	}
	return idsByBucket
}

func resolveScopeCredentialID(bucket string, idsByBucket map[string][]string) (string, error) {
	bucket = strings.TrimSpace(bucket)
	if bucket == "" {
		return "", nil
	}
	ids := idsByBucket[strings.ToLower(bucket)]
	switch len(ids) {
	case 0:
		return bucket, nil
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("bucket %q maps to multiple credentials; define the scope under the intended buckets[].resources entry", bucket)
	}
}

func bucketResourceScopes(credentialID, bucketName string, resource BucketResourceConfig) ([]BucketScopeConfig, error) {
	org := strings.TrimSpace(resource.Organization)
	if org == "" {
		return nil, fmt.Errorf("organization is required")
	}
	orgPath := cleanBucketScopeSubPath(resource.OrgPath)

	if len(resource.Projects) == 0 {
		return []BucketScopeConfig{{
			Organization:        org,
			CredentialID:        credentialID,
			Bucket:              bucketName,
			OrganizationSubPath: orgPath,
		}}, nil
	}

	scopes := make([]BucketScopeConfig, 0, len(resource.Projects))
	for idx, project := range resource.Projects {
		projectID := strings.TrimSpace(project.ProjectID)
		if projectID == "" {
			projectID = strings.TrimSpace(project.Project)
		}
		if projectID == "" {
			return nil, fmt.Errorf("projects[%d]: project_id is required", idx)
		}
		scopes = append(scopes, BucketScopeConfig{
			Organization:        org,
			ProjectID:           projectID,
			CredentialID:        credentialID,
			Bucket:              bucketName,
			Path:                strings.TrimSpace(project.Path),
			PathPrefix:          strings.Trim(strings.TrimSpace(project.PathPrefix), "/"),
			OrganizationSubPath: orgPath,
			ProjectSubPath:      cleanBucketScopeSubPath(project.ProjectPath),
		})
	}
	return scopes, nil
}

func joinBucketScopeSubPaths(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if p := cleanBucketScopeSubPath(part); p != "" {
			cleaned = append(cleaned, p)
		}
	}
	if len(cleaned) == 0 {
		return ""
	}
	return path.Join(cleaned...)
}
