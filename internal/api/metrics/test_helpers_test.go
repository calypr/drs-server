package metrics

import (
	"context"

	"github.com/calypr/syfon/internal/access"
)

func metricsTestContext(base context.Context, mode string, headerSet bool, headerValue bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	if headerSet {
		session.AuthHeaderPresent = headerValue
	}
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}
