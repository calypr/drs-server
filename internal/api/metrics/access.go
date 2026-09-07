package metrics

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
)

func (s *MetricsServer) checkAuth(ctx context.Context) (metricsAccess, int, bool) {
	resolved, err := resolveMetricsAccess(ctx)
	if err != nil {
		return metricsAccess{}, http.StatusBadRequest, false
	}

	if !access.IsAuthzEnforced(ctx) {
		return resolved, 0, true
	}
	if apimiddleware.MissingGen3AuthHeader(ctx) {
		return resolved, http.StatusUnauthorized, false
	}

	// Baseline read access for metrics: global access or scoped access
	if access.HasMethodAccess(ctx, "read", []string{"/data_file"}) ||
		access.HasMethodAccess(ctx, "read", []string{"/programs"}) {
		return resolved, 0, true
	}

	if resolved.isScoped() {
		scope, err := sycommon.ResourcePath(resolved.organization, resolved.project)
		if err != nil {
			return resolved, http.StatusBadRequest, false
		}
		if access.HasMethodAccess(ctx, "read", []string{scope}) {
			return resolved, 0, true
		}
		return resolved, http.StatusForbidden, false
	}

	scopes := readableMetricsScopes(ctx)
	if len(scopes) > 0 {
		resolved.scopes = scopes
		return resolved, 0, true
	}

	return resolved, http.StatusForbidden, false
}

type metricsAccess struct {
	organization string
	project      string
	scopes       []metricsScope
}

func (a metricsAccess) isScoped() bool {
	return strings.TrimSpace(a.organization) != ""
}

func (a metricsAccess) hasScopeAggregate() bool {
	return !a.isScoped() && len(a.scopes) > 0
}

type metricsScope struct {
	organization string
	project      string
}

func resolveMetricsAccess(ctx context.Context) (metricsAccess, error) {
	org, project, _, err := parseScopeQuery(ctx)
	if err != nil {
		return metricsAccess{}, err
	}
	return metricsAccess{organization: org, project: project}, nil
}

func readableMetricsScopes(ctx context.Context) []metricsScope {
	privs := access.GetUserPrivileges(ctx)
	scopes := make([]metricsScope, 0, len(privs))
	seen := map[string]bool{}
	for resource, methods := range privs {
		if !(methods["read"] || methods["*"]) {
			continue
		}
		scope, ok := metricsScopeFromResource(resource)
		if !ok {
			continue
		}
		key := scope.organization + "\x00" + scope.project
		if seen[key] {
			continue
		}
		seen[key] = true
		scopes = append(scopes, scope)
	}
	orgWide := map[string]bool{}
	for _, scope := range scopes {
		if scope.project == "" {
			orgWide[scope.organization] = true
		}
	}
	if len(orgWide) > 0 {
		filtered := scopes[:0]
		for _, scope := range scopes {
			if scope.project != "" && orgWide[scope.organization] {
				continue
			}
			filtered = append(filtered, scope)
		}
		scopes = filtered
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].organization == scopes[j].organization {
			return scopes[i].project < scopes[j].project
		}
		return scopes[i].organization < scopes[j].organization
	})
	return scopes
}

func metricsResources(scopes []metricsScope) []string {
	resources := make([]string, 0, len(scopes))
	seen := map[string]bool{}
	for _, scope := range scopes {
		resource, err := sycommon.ResourcePath(scope.organization, scope.project)
		if err != nil || resource == "" || seen[resource] {
			continue
		}
		seen[resource] = true
		resources = append(resources, resource)
	}
	sort.Strings(resources)
	return resources
}

func metricsScopeFromResource(resource string) (metricsScope, bool) {
	org, project, ok := sycommon.ResourceScope(resource)
	if !ok {
		return metricsScope{}, false
	}
	return metricsScope{organization: org, project: project}, true
}

func parseScopeQuery(ctx context.Context) (string, string, bool, error) {
	params, _ := ctx.Value(metricsQueryContextKey{}).(metricsQueryParams)
	org := strings.TrimSpace(params.organization)
	if org == "" {
		org = strings.TrimSpace(params.program)
	}
	project := strings.TrimSpace(params.project)
	if project != "" && org == "" {
		return "", "", false, errors.New("organization is required when project is set")
	}
	if org != "" {
		return org, project, true, nil
	}
	return "", "", false, nil
}
