package access

import (
	"context"
	"strings"
)

type contextKey struct{}

const (
	SourceAnonymous  = "anonymous"
	SourceLocalBasic = "local-basic"
	SourceLocalCSV   = "local-csv"
	SourceGen3Fence  = "gen3-fence"
	SourceGen3Mock   = "gen3-mock"
	SourcePlugin     = "plugin"
)

// Session is the canonical auth state propagated through request handling.
type Session struct {
	Mode              string
	Source            string
	Subject           string
	Claims            map[string]interface{}
	Resources         []string
	Privileges        map[string]map[string]bool
	AuthHeaderPresent bool
	AuthzEnforced     bool
}

func NewSession(mode string) *Session {
	return &Session{
		Mode:       strings.ToLower(strings.TrimSpace(mode)),
		Source:     SourceAnonymous,
		Claims:     map[string]interface{}{},
		Resources:  []string{},
		Privileges: map[string]map[string]bool{},
	}
}

func (s *Session) Clone() *Session {
	if s == nil {
		return nil
	}
	out := *s
	out.Claims = cloneClaims(s.Claims)
	out.Resources = cloneStrings(s.Resources)
	out.Privileges = clonePrivileges(s.Privileges)
	return &out
}

func (s *Session) SetSource(source string) {
	if s == nil {
		return
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return
	}
	s.Source = source
}

func (s *Session) SetSubject(subject string) {
	if s == nil {
		return
	}
	s.Subject = strings.TrimSpace(subject)
}

func (s *Session) SetClaims(claims map[string]interface{}) {
	if s == nil {
		return
	}
	s.Claims = cloneClaims(claims)
}

func (s *Session) SetAuthorizations(resources []string, privileges map[string]map[string]bool, enforced bool) {
	if s == nil {
		return
	}
	s.Resources = cloneStrings(resources)
	s.Privileges = normalizePrivileges(privileges)
	s.AuthzEnforced = enforced
}

func WithSession(ctx context.Context, session *Session) context.Context {
	if session == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, session.Clone())
}

func FromContext(ctx context.Context) *Session {
	if ctx == nil {
		return NewSession("")
	}
	if session, ok := ctx.Value(contextKey{}).(*Session); ok && session != nil {
		return session.Clone()
	}
	return NewSession("")
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneClaims(in map[string]interface{}) map[string]interface{} {
	if len(in) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonePrivileges(in map[string]map[string]bool) map[string]map[string]bool {
	if len(in) == 0 {
		return map[string]map[string]bool{}
	}
	out := make(map[string]map[string]bool, len(in))
	for resource, methods := range in {
		if methods == nil {
			out[resource] = map[string]bool{}
			continue
		}
		clonedMethods := make(map[string]bool, len(methods))
		for method, allowed := range methods {
			clonedMethods[method] = allowed
		}
		out[resource] = clonedMethods
	}
	return out
}

func normalizePrivileges(in map[string]map[string]bool) map[string]map[string]bool {
	if len(in) == 0 {
		return map[string]map[string]bool{}
	}
	out := make(map[string]map[string]bool, len(in))
	for resource, methods := range in {
		normalized := map[string]bool{}
		for method, allowed := range methods {
			for _, canonical := range normalizeMethodName(method, allowed) {
				normalized[canonical] = true
			}
		}
		out[resource] = normalized
	}
	return out
}

func normalizeMethodName(method string, allowed bool) []string {
	method = strings.ToLower(strings.TrimSpace(method))
	if method == "" || !allowed {
		return nil
	}
	switch method {
	case "write":
		return []string{"file_upload", "create", "update", "delete"}
	default:
		return []string{method}
	}
}
