package access

import (
	"context"

	clientaccess "github.com/calypr/syfon/client/access"
)

// ResourcePath returns the canonical resource path for an organization and
// optional project scope.
func ResourcePath(organization, project string) (string, error) {
	return clientaccess.ResourcePath(organization, project)
}

// AuthorizedResources returns resources for which the current session has the
// requested method. It preserves the canonicalization and de-duplication rules
// used by the rest of the access package.
func AuthorizedResources(ctx context.Context, method string) []string {
	privileges := GetUserPrivileges(ctx)
	if len(privileges) == 0 {
		return clientaccess.NormalizeAccessResources(GetUserAuthz(ctx))
	}
	resources := make([]string, 0, len(privileges))
	for resource, methods := range privileges {
		if methods[method] || methods["*"] {
			resources = append(resources, resource)
		}
	}
	return clientaccess.NormalizeAccessResources(resources)
}
