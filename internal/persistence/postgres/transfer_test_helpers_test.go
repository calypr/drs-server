package postgres

import (
	"fmt"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/usage"
	"github.com/lib/pq"
)

func transferAttributionWhere(filter usage.Filter) (string, []any) {
	parts := make([]string, 0)
	args := make([]any, 0)
	add := func(column string, value any) {
		args = append(args, value)
		parts = append(parts, fmt.Sprintf("%s = $%d", column, len(args)))
	}
	if strings.TrimSpace(filter.Organization) != "" {
		add("organization", strings.TrimSpace(filter.Organization))
	}
	if strings.TrimSpace(filter.Project) != "" {
		add("project", strings.TrimSpace(filter.Project))
	}
	if strings.TrimSpace(filter.EventType) != "" && strings.TrimSpace(filter.EventType) != "all" {
		add("event_type", strings.TrimSpace(filter.EventType))
	}
	direction := strings.TrimSpace(filter.Direction)
	if direction == "" {
		switch strings.TrimSpace(filter.EventType) {
		case usage.ProviderTransferDirectionDownload, usage.ProviderTransferDirectionUpload:
			direction = strings.TrimSpace(filter.EventType)
		}
	}
	if direction != "" && direction != "all" {
		add("direction", direction)
	}
	if filter.From != nil {
		args = append(args, filter.From.UTC())
		parts = append(parts, fmt.Sprintf("event_time >= $%d", len(args)))
	}
	if filter.To != nil {
		args = append(args, filter.To.UTC())
		parts = append(parts, fmt.Sprintf("event_time <= $%d", len(args)))
	}
	if strings.TrimSpace(filter.Provider) != "" {
		add("provider", strings.TrimSpace(filter.Provider))
	}
	if strings.TrimSpace(filter.Bucket) != "" {
		add("bucket", strings.TrimSpace(filter.Bucket))
	}
	if strings.TrimSpace(filter.SHA256) != "" {
		add("sha256", strings.TrimSpace(filter.SHA256))
	}
	if strings.TrimSpace(filter.User) != "" {
		user := strings.TrimSpace(filter.User)
		args = append(args, user, user)
		parts = append(parts, fmt.Sprintf("(actor_email = $%d OR actor_subject = $%d)", len(args)-1, len(args)))
	}
	if len(parts) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

func transferAttributionWhereByResources(filter usage.Filter, resources []string) (string, []any) {
	where, args := transferAttributionWhere(filter)
	clause, clauseArgs := postgresTransferResourceClause(resources, len(args)+1)
	if clause == "" {
		if where == "" {
			return " WHERE 1 = 0", args
		}
		return where + " AND 1 = 0", args
	}
	if where == "" {
		return " WHERE " + clause, append(args, clauseArgs...)
	}
	return where + " AND (" + clause + ")", append(args, clauseArgs...)
}

func postgresTransferResourceClause(resources []string, startIndex int) (string, []any) {
	resources = clientaccess.NormalizeAccessResources(resources)
	if len(resources) == 0 {
		return "", nil
	}
	orgOnly := make([]string, 0)
	orgSeen := make(map[string]struct{})
	projectClauses := make([]string, 0)
	args := make([]any, 0, len(resources)*2)
	for _, resource := range resources {
		org, project, ok := clientaccess.ResourceScope(resource)
		if !ok {
			continue
		}
		if project == "" {
			if _, exists := orgSeen[org]; exists {
				continue
			}
			orgSeen[org] = struct{}{}
			orgOnly = append(orgOnly, org)
			continue
		}
		args = append(args, org, project)
		projectClauses = append(projectClauses, fmt.Sprintf("(organization = $%d AND project = $%d)", startIndex+len(args)-2, startIndex+len(args)-1))
	}
	clauses := make([]string, 0, 2)
	if len(orgOnly) > 0 {
		args = append(args, pq.Array(orgOnly))
		clauses = append(clauses, fmt.Sprintf("organization = ANY($%d)", startIndex+len(args)-1))
	}
	if len(projectClauses) > 0 {
		clauses = append(clauses, strings.Join(projectClauses, " OR "))
	}
	return strings.Join(clauses, " OR "), args
}

func transferAttributionGroupExpr(groupBy string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "user":
		return "COALESCE(NULLIF(actor_email, ''), actor_subject), actor_email, actor_subject", "COALESCE(NULLIF(actor_email, ''), actor_subject) AS key, '' AS organization, '' AS project, '' AS provider, '' AS bucket, '' AS sha256, actor_email, actor_subject"
	case "provider":
		return "provider, bucket", "provider || ':' || bucket AS key, '' AS organization, '' AS project, provider, bucket, '' AS sha256, '' AS actor_email, '' AS actor_subject"
	case "object":
		return "sha256", "sha256 AS key, '' AS organization, '' AS project, '' AS provider, '' AS bucket, sha256, '' AS actor_email, '' AS actor_subject"
	default:
		return "organization, project", "organization || '/' || project AS key, organization, project, '' AS provider, '' AS bucket, '' AS sha256, '' AS actor_email, '' AS actor_subject"
	}
}

func normalizeTransferDirection(direction string) string {
	if strings.EqualFold(strings.TrimSpace(direction), usage.ProviderTransferDirectionUpload) {
		return usage.ProviderTransferDirectionUpload
	}
	return usage.ProviderTransferDirectionDownload
}
