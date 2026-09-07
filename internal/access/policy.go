package access

import (
	"context"
	"strings"

	sycommon "github.com/calypr/syfon/common"
)

// GetUserAuthz returns the list of resources the user is authorized to access.
// If not found, returns empty list (no access to protected resources).
func GetUserAuthz(ctx context.Context) []string {
	return FromContext(ctx).Resources
}

// CheckAccess verifies if a user has access to a record based on RBAC resources.
// A record is accessible if:
// 1. It has NO required resources (public).
// 2. OR the user has at least one of the resources listed on the record.
func CheckAccess(recordResources []string, userResources []string) bool {
	recordResources = sycommon.NormalizeAccessResources(recordResources)
	if len(recordResources) == 0 {
		return true
	}
	userMap := make(map[string]bool)
	for _, r := range userResources {
		if normalized := sycommon.NormalizeAccessResource(r); normalized != "" {
			userMap[normalized] = true
		}
	}

	for _, r := range recordResources {
		if userMap[r] {
			return true
		}
	}
	return false
}

func HasAuthHeader(ctx context.Context) bool {
	return FromContext(ctx).AuthHeaderPresent
}

func IsGen3Mode(ctx context.Context) bool {
	return FromContext(ctx).Mode == "gen3"
}

func IsAuthzEnforced(ctx context.Context) bool {
	session := FromContext(ctx)
	if session.Mode == "gen3" {
		return true
	}
	return session.AuthzEnforced
}

func GetUserPrivileges(ctx context.Context) map[string]map[string]bool {
	return FromContext(ctx).Privileges
}

func HasMethodAccess(ctx context.Context, method string, resources []string) bool {
	if !IsAuthzEnforced(ctx) {
		return true
	}
	if IsGen3Mode(ctx) && !HasAuthHeader(ctx) {
		return false
	}
	privs := normalizePolicyPrivileges(GetUserPrivileges(ctx))
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 {
		return false
	}
	for _, resource := range resources {
		methods, ok := privs[resource]
		if !ok {
			return false
		}
		if methods[method] || methods["*"] {
			continue
		}
		return false
	}
	return true
}

func HasObjectMethodAccess(ctx context.Context, method string, resources []string) bool {
	if !IsAuthzEnforced(ctx) {
		return true
	}
	if IsGen3Mode(ctx) && !HasAuthHeader(ctx) {
		return false
	}
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 {
		return strings.EqualFold(strings.TrimSpace(method), "read")
	}
	privs := normalizePolicyPrivileges(GetUserPrivileges(ctx))
	if len(privs) == 0 {
		return CheckAccess(resources, GetUserAuthz(ctx))
	}
	for _, resource := range resources {
		methods, ok := privs[resource]
		if !ok {
			continue
		}
		if methods[method] || methods["*"] {
			return true
		}
	}
	return false
}

func HasAnyMethodAccess(ctx context.Context, resources []string, methods ...string) bool {
	if !IsAuthzEnforced(ctx) {
		return true
	}
	if len(resources) == 0 {
		return true
	}
	for _, m := range methods {
		if HasMethodAccess(ctx, m, resources) {
			return true
		}
	}
	return false
}

func HasServiceMethodAccess(ctx context.Context, service, method string, resources []string) bool {
	if !IsAuthzEnforced(ctx) {
		return true
	}
	if IsGen3Mode(ctx) && !HasAuthHeader(ctx) {
		return false
	}
	service = strings.ToLower(strings.TrimSpace(service))
	method = strings.ToLower(strings.TrimSpace(method))
	if service == "" || method == "" {
		return false
	}
	resources = sycommon.NormalizeAccessResources(resources)
	if len(resources) == 0 {
		return false
	}
	privs := normalizePolicyPrivileges(GetUserPrivileges(ctx))
	qualifiedMethod := service + ":" + method
	serviceWildcard := service + ":*"
	for _, resource := range resources {
		methods, ok := privs[resource]
		if !ok {
			return false
		}
		if methods[qualifiedMethod] || methods[serviceWildcard] || methods["*:*"] {
			continue
		}
		return false
	}
	return true
}

func HasAnyServiceMethodAccess(ctx context.Context, resources []string, service string, methods ...string) bool {
	if !IsAuthzEnforced(ctx) {
		return true
	}
	if len(resources) == 0 {
		return true
	}
	for _, method := range methods {
		if HasServiceMethodAccess(ctx, service, method, resources) {
			return true
		}
	}
	return false
}

func normalizePolicyPrivileges(in map[string]map[string]bool) map[string]map[string]bool {
	out := make(map[string]map[string]bool, len(in))
	for rawResource, methods := range in {
		resource := sycommon.NormalizeAccessResource(rawResource)
		if resource == "" {
			continue
		}
		if out[resource] == nil {
			out[resource] = map[string]bool{}
		}
		for method, allowed := range methods {
			if allowed {
				out[resource][method] = true
			}
		}
	}
	return out
}

func AuthStatusCode(ctx context.Context) int {
	if IsGen3Mode(ctx) && !HasAuthHeader(ctx) {
		return 401
	}
	return 403
}
