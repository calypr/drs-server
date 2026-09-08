package scoperepair

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage/address"
)

func (s *Service) loadScopeTargets(ctx context.Context) (map[string][]scopeTarget, error) {
	if s.scopes == nil {
		return nil, fmt.Errorf("scope reader is not configured")
	}
	credentials, err := s.scopes.ListCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buckets: %w", err)
	}
	out := make(map[string][]scopeTarget)
	for _, credential := range credentials {
		if address.NormalizeProvider(credential.Provider, address.S3Provider) != address.S3Provider {
			continue
		}
		bucket := strings.TrimSpace(credential.Bucket)
		if bucket == "" {
			continue
		}
		scopes, err := s.scopes.ListScopes(ctx, bucket)
		if err != nil {
			return nil, fmt.Errorf("list bucket scopes for %s: %w", bucket, err)
		}
		for _, scope := range scopes {
			if !scopeBelongsToCredential(scope, credential) {
				continue
			}
			resource, err := clientaccess.ResourcePath(strings.TrimSpace(scope.Organization), strings.TrimSpace(scope.ProjectID))
			if err != nil || resource == "" {
				continue
			}
			target := scopeTarget{Resource: resource, Organization: strings.TrimSpace(scope.Organization), Project: strings.TrimSpace(scope.ProjectID), Bucket: bucket}
			if scopeBucket, prefix, ok := parseScopePath(scope.PathPrefix); ok {
				if scopeBucket != "" {
					target.Bucket = scopeBucket
				}
				target.Prefix = prefix
			} else {
				target.Prefix = strings.Trim(strings.TrimSpace(scope.PathPrefix), "/")
			}
			out[resource] = append(out[resource], target)
		}
	}
	return out, nil
}

func scopeBelongsToCredential(scope buckets.Scope, credential buckets.Credential) bool {
	for _, value := range []string{scope.CredentialID, scope.Bucket} {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.EqualFold(value, strings.TrimSpace(credential.CredentialID)) || strings.EqualFold(value, strings.TrimSpace(credential.Bucket)) {
			return true
		}
	}
	return strings.TrimSpace(scope.CredentialID) == "" && strings.TrimSpace(scope.Bucket) == ""
}

func parseScopePath(raw string) (string, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" {
		return "", "", false
	}
	if address.ProviderFromScheme(parsed.Scheme) != address.S3Provider {
		return strings.TrimSpace(parsed.Host), strings.Trim(strings.TrimSpace(parsed.Path), "/"), true
	}
	return strings.TrimSpace(parsed.Host), strings.Trim(strings.TrimSpace(parsed.Path), "/"), true
}

func inferRecordResource(record objects.Record, sha string, scopes map[string][]scopeTarget) (string, bool, bool) {
	resources := recordProjectResources(record, "")
	if len(resources) == 1 {
		resource := resources[0]
		if len(scopes[resource]) == 0 {
			return resource, false, false
		}
		return resource, true, false
	}
	if len(resources) > 1 {
		return "", false, true
	}
	if strings.TrimSpace(sha) == "" {
		return "", false, false
	}
	matches := make([]string, 0)
	for resource, targets := range scopes {
		if len(targets) == 0 || strings.TrimSpace(targets[0].Project) == "" {
			continue
		}
		minted, err := objects.MintRecordIDFromChecksum(sha, []string{resource})
		if err == nil && minted == record.Id {
			matches = append(matches, resource)
		}
	}
	if len(matches) == 1 {
		return matches[0], true, false
	}
	if len(matches) > 1 {
		return "", false, true
	}
	return "", false, false
}

func recordProjectResources(record objects.Record, inferred string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, resource := range objects.AccessResources(&record) {
		org, project, ok := clientaccess.ResourceScope(resource)
		if !ok || org == "" || project == "" {
			continue
		}
		if _, exists := seen[resource]; exists {
			continue
		}
		seen[resource] = struct{}{}
		result = append(result, resource)
	}
	if inferred != "" {
		if _, exists := seen[inferred]; !exists {
			result = append(result, inferred)
		}
	}
	sort.Strings(result)
	return result
}

func recordMatchesResource(record objects.Record, resource string) bool {
	for _, candidate := range objects.AccessResources(&record) {
		if candidate == resource {
			return true
		}
	}
	return false
}

func addControlledAccess(controlled *[]string, resource string) *[]string {
	values := make([]string, 0)
	if controlled != nil {
		values = append(values, (*controlled)...)
	}
	values = append(values, resource)
	normalized := clientaccess.NormalizeAccessResources(values)
	return &normalized
}
