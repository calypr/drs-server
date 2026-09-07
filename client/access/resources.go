package access

import (
	"fmt"
	"net/url"
	"strings"
)

// ResourcePath returns the canonical resource path for an org/project scope,
// e.g. "/organization/myorg/project/myproject".
func ResourcePath(org, project string) (string, error) {
	org = strings.TrimSpace(org)
	project = strings.TrimSpace(project)
	if org == "" && project == "" {
		return "", nil
	}
	if org == "" {
		return "", fmt.Errorf("organization required when project is specified")
	}
	if project == "" {
		return "/organization/" + org, nil
	}
	return "/organization/" + org + "/project/" + project, nil
}

// AuthzMapFromScope builds the wire-format authorizations map from an org and project.
// Empty project means org-wide access (value is empty slice).
func AuthzMapFromScope(org, project string) map[string][]string {
	org = strings.TrimSpace(org)
	if org == "" {
		return nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return map[string][]string{org: {}}
	}
	return map[string][]string{org: {project}}
}

// AuthzListToMap converts a list of resource-path strings (for example
// "/organization/org/project/proj" or legacy "/programs/org/projects/proj")
// to the wire-format org→projects map.
func AuthzListToMap(paths []string) map[string][]string {
	if len(paths) == 0 {
		return nil
	}
	result := make(map[string][]string)
	for _, path := range paths {
		path = strings.TrimSpace(path)
		org, project := parseResourcePath(path)
		if org == "" {
			continue
		}
		if _, ok := result[org]; !ok {
			result[org] = []string{}
		}
		if project != "" {
			result[org] = append(result[org], project)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// AuthzMapToList converts the wire-format org→projects map back to a list of
// canonical resource-path strings for internal storage.
func AuthzMapToList(authzMap map[string][]string) []string {
	if len(authzMap) == 0 {
		return nil
	}
	out := make([]string, 0, len(authzMap))
	for org, projects := range authzMap {
		if len(projects) == 0 {
			out = append(out, "/organization/"+org)
		} else {
			for _, project := range projects {
				project = strings.TrimSpace(project)
				if project == "" {
					out = append(out, "/organization/"+org)
					continue
				}
				out = append(out, "/organization/"+org+"/project/"+project)
			}
		}
	}
	return out
}

// NormalizeAccessResource converts GA4GH controlled_access URL claims and
// accepted resource path aliases into the canonical resource form used for
// privilege checks: /organization/<organization>[/project/<project>].
func NormalizeAccessResource(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	path := raw
	if parsed, err := url.Parse(raw); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	path = "/" + strings.Trim(path, "/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) >= 2 && (parts[0] == "program" || parts[0] == "programs" || parts[0] == "organization" || parts[0] == "organizations") {
		org := strings.TrimSpace(parts[1])
		if org == "" {
			return ""
		}
		if len(parts) >= 4 && (parts[2] == "project" || parts[2] == "projects") {
			project := strings.TrimSpace(parts[3])
			if project != "" {
				return "/organization/" + org + "/project/" + project
			}
		}
		return "/organization/" + org
	}
	return raw
}

func NormalizeAccessResources(resources []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(resources))
	for _, raw := range resources {
		resource := NormalizeAccessResource(raw)
		if resource == "" {
			continue
		}
		if _, ok := seen[resource]; ok {
			continue
		}
		seen[resource] = struct{}{}
		out = append(out, resource)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func ControlledAccessToAuthzMap(claims []string) map[string][]string {
	return AuthzListToMap(NormalizeAccessResources(claims))
}

func AuthzMapToControlledAccess(authzMap map[string][]string) []string {
	return NormalizeAccessResources(AuthzMapToList(authzMap))
}

// ResourceScope parses a canonical or raw access resource into
// (organization, project). It accepts "/organization/org",
// "/organization/org/project/proj", and legacy
// "/programs/org/projects/proj" alias forms.
func ResourceScope(resource string) (string, string, bool) {
	org, project := parseResourcePath(resource)
	if org == "" {
		return "", "", false
	}
	return org, project, true
}

// parseResourcePath splits supported resource-path forms into (org, project).
func parseResourcePath(path string) (string, string) {
	path = NormalizeAccessResource(path)
	if !strings.HasPrefix(path, "/organization/") {
		return "", ""
	}
	path = strings.TrimPrefix(path, "/organization/")
	parts := strings.SplitN(path, "/project/", 2)
	if len(parts) == 1 {
		return strings.TrimSpace(parts[0]), ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}
