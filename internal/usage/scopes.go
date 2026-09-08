package usage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
)

// ScopeSelection is the HTTP-neutral organization/project selection used by
// the metrics use case. Empty values request an aggregate or global report.
type ScopeSelection struct {
	Organization string
	Project      string
}

// FileUsageBatchQuery contains the domain inputs for a bulk usage report.
type FileUsageBatchQuery struct {
	Scope         ScopeQuery
	ObjectIDs     []string
	InactiveSince *time.Time
}

// ResolveMetricsScope applies the metrics read policy and returns the
// already-authorized report scope consumed by the usage service.
func ResolveMetricsScope(ctx context.Context, selection ScopeSelection) (ScopeQuery, error) {
	organization := strings.TrimSpace(selection.Organization)
	project := strings.TrimSpace(selection.Project)
	if project != "" && organization == "" {
		return ScopeQuery{}, fmt.Errorf("organization is required when project is set")
	}

	scope := ScopeQuery{Organization: organization, Project: project}
	if !access.IsAuthzEnforced(ctx) {
		return scope, nil
	}
	if access.HasMethodAccess(ctx, "read", []string{"/data_file"}) ||
		access.HasMethodAccess(ctx, "read", []string{"/programs"}) {
		return scope, nil
	}
	if organization != "" {
		resource, err := clientaccess.ResourcePath(organization, project)
		if err != nil {
			return ScopeQuery{}, err
		}
		if !access.HasMethodAccess(ctx, "read", []string{resource}) {
			return ScopeQuery{}, errorapi.ErrAccessDenied
		}
		return scope, nil
	}
	readable := readableMetricsScopes(ctx)
	if len(readable) == 0 {
		return ScopeQuery{}, errorapi.ErrAccessDenied
	}
	scope.Scopes = readable
	scope.Resources = metricsResources(readable)
	return scope, nil
}

func readableMetricsScopes(ctx context.Context) []Scope {
	privileges := access.GetUserPrivileges(ctx)
	scopes := make([]Scope, 0, len(privileges))
	seen := make(map[string]struct{}, len(privileges))
	for resource, methods := range privileges {
		if !methods["read"] && !methods["*"] {
			continue
		}
		organization, project, ok := clientaccess.ResourceScope(resource)
		if !ok {
			continue
		}
		key := organization + "\x00" + project
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		scopes = append(scopes, Scope{Organization: organization, Project: project})
	}

	orgWide := make(map[string]struct{})
	for _, scope := range scopes {
		if scope.Project == "" {
			orgWide[scope.Organization] = struct{}{}
		}
	}
	filtered := scopes[:0]
	for _, scope := range scopes {
		if scope.Project != "" {
			if _, exists := orgWide[scope.Organization]; exists {
				continue
			}
		}
		filtered = append(filtered, scope)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Organization == filtered[j].Organization {
			return filtered[i].Project < filtered[j].Project
		}
		return filtered[i].Organization < filtered[j].Organization
	})
	return filtered
}

func metricsResources(scopes []Scope) []string {
	resources := make([]string, 0, len(scopes))
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		resource, err := clientaccess.ResourcePath(scope.Organization, scope.Project)
		if err != nil || resource == "" {
			continue
		}
		if _, exists := seen[resource]; exists {
			continue
		}
		seen[resource] = struct{}{}
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	return resources
}

// ParseInactiveSince computes the report cutoff from an explicit clock so the
// use case remains deterministic under tests and callers control UTC policy.
func ParseInactiveSince(now time.Time, inactiveDays *int) (*time.Time, error) {
	if inactiveDays == nil {
		return nil, nil
	}
	if *inactiveDays < 0 {
		return nil, fmt.Errorf("inactive_days must be a non-negative integer")
	}
	cutoff := now.UTC().AddDate(0, 0, -*inactiveDays)
	return &cutoff, nil
}
