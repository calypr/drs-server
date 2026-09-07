package access

import (
	"fmt"
	"strings"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/faults"
)

type AuthorizationError struct {
	Method             string
	RecordID           string
	Resources          []string
	DeniedRecords      int
	TotalRecords       int
	TruncatedResources int
}

func (e *AuthorizationError) Error() string {
	return e.PublicMessage()
}

func (e *AuthorizationError) Unwrap() error {
	return faults.ErrUnauthorized
}

func (e *AuthorizationError) PublicMessage() string {
	if e == nil {
		return "Unauthorized"
	}
	method := strings.TrimSpace(e.Method)
	if method == "" {
		method = "requested"
	}

	denied := e.DeniedRecords
	if denied <= 0 {
		denied = 1
	}
	total := e.TotalRecords
	if total < denied {
		total = denied
	}

	var b strings.Builder
	if total > 1 {
		fmt.Fprintf(&b, "Unauthorized: missing %s access for %d/%d records", method, denied, total)
	} else {
		fmt.Fprintf(&b, "Unauthorized: missing %s access", method)
	}
	if strings.TrimSpace(e.RecordID) != "" {
		fmt.Fprintf(&b, "; first denied record=%q", e.RecordID)
	}
	if len(e.Resources) > 0 {
		fmt.Fprintf(&b, "; denied organization/project scopes: %s", strings.Join(formatResourceScopes(e.Resources), ", "))
		if e.TruncatedResources > 0 {
			fmt.Fprintf(&b, " (and %d more)", e.TruncatedResources)
		}
	}
	return b.String()
}

func formatResourceScopes(resources []string) []string {
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		org, project, ok := clientaccess.ResourceScope(resource)
		switch {
		case ok && org != "" && project != "":
			out = append(out, org+"/"+project)
		case ok && org != "":
			out = append(out, org+"/*")
		default:
			out = append(out, resource)
		}
	}
	return out
}
