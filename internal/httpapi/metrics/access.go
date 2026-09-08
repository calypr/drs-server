package metrics

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/syfon/internal/access"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/usage"
)

func (s *MetricsServer) checkAuth(ctx context.Context) (metricsAccess, int, bool) {
	organization, project, _, err := parseScopeQuery(ctx)
	if err != nil {
		return metricsAccess{}, http.StatusBadRequest, false
	}
	if access.IsAuthzEnforced(ctx) && apimiddleware.MissingGen3AuthHeader(ctx) {
		return metricsAccess{}, http.StatusUnauthorized, false
	}
	scope, err := usage.ResolveMetricsScope(ctx, usage.ScopeSelection{
		Organization: organization,
		Project:      project,
	})
	if err != nil {
		return metricsAccess{}, http.StatusForbidden, false
	}
	return metricsAccess{
		organization: scope.Organization,
		project:      scope.Project,
		scope:        scope,
	}, 0, true
}

// metricsAccess is the HTTP response/status projection of an authorized
// usage scope. Authorization and scope construction live in usage.
type metricsAccess struct {
	organization string
	project      string
	scope        usage.ScopeQuery
}

func (a metricsAccess) scopeQuery() usage.ScopeQuery {
	if a.scope.Organization != "" || a.scope.Project != "" || len(a.scope.Scopes) > 0 || len(a.scope.Resources) > 0 {
		return a.scope
	}
	return usage.ScopeQuery{Organization: a.organization, Project: a.project}
}

func (a metricsAccess) isScoped() bool {
	return strings.TrimSpace(a.scopeQuery().Organization) != ""
}

func (a metricsAccess) hasScopeAggregate() bool {
	query := a.scopeQuery()
	return !a.isScoped() && len(query.Scopes) > 0
}

func parseScopeQuery(ctx context.Context) (string, string, bool, error) {
	params, _ := ctx.Value(metricsQueryContextKey{}).(metricsQueryParams)
	organization := strings.TrimSpace(params.organization)
	if organization == "" {
		organization = strings.TrimSpace(params.program)
	}
	project := strings.TrimSpace(params.project)
	if project != "" && organization == "" {
		return "", "", false, fmt.Errorf("organization is required when project is set")
	}
	if organization != "" {
		return organization, project, true, nil
	}
	return "", "", false, nil
}
