package authentication

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"strings"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/plugin"
)

const (
	localAuthzResourcesClaim  = "syfon_local_authz_resources"
	localAuthzPrivilegesClaim = "syfon_local_authz_privileges"
)

func AuthorizationFromClaims(claims map[string]interface{}) ([]string, map[string]map[string]bool, bool) {
	if claims == nil {
		return nil, nil, false
	}
	resources, ok := claims[localAuthzResourcesClaim].([]string)
	if !ok {
		return nil, nil, false
	}
	privileges, ok := claims[localAuthzPrivilegesClaim].(map[string]map[string]bool)
	if !ok {
		return nil, nil, false
	}
	return append([]string(nil), resources...), clonePrivMap(privileges), true
}

type LocalAuthzStore struct {
	users map[string]*localAuthzUser
}

type localAuthzUser struct {
	password   string
	resources  []string
	privileges map[string]map[string]bool
}

func LoadLocalAuthzCSV(path string) (*LocalAuthzStore, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open local authz csv: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.TrimLeadingSpace = true
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read local authz csv header: %w", err)
	}
	cols := localAuthzHeaderIndex(header)
	usernameCol := firstHeader(cols, "username", "user", "subject")
	passwordCol := firstHeader(cols, "password", "pass")
	methodsCol := firstHeader(cols, "methods", "permissions", "access")
	resourceCol := firstHeader(cols, "resource", "path", "authz", "authz_path")
	orgCol := firstHeader(cols, "organization", "org", "program")
	projectCol := firstHeader(cols, "project", "project_id")
	if usernameCol < 0 || passwordCol < 0 || methodsCol < 0 || (resourceCol < 0 && orgCol < 0) {
		return nil, fmt.Errorf("local authz csv requires username, password, methods, and either resource or organization columns")
	}

	store := &LocalAuthzStore{users: map[string]*localAuthzUser{}}
	line := 1
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return nil, fmt.Errorf("read local authz csv line %d: %w", line, err)
		}
		if localAuthzBlankOrComment(record) {
			continue
		}

		username := localAuthzCell(record, usernameCol)
		password := localAuthzCell(record, passwordCol)
		if username == "" || password == "" {
			return nil, fmt.Errorf("local authz csv line %d: username and password are required", line)
		}
		resource := localAuthzCell(record, resourceCol)
		if resource == "" {
			var pathErr error
			resource, pathErr = syfoncommon.ResourcePath(localAuthzCell(record, orgCol), localAuthzCell(record, projectCol))
			if pathErr != nil {
				return nil, fmt.Errorf("local authz csv line %d: %w", line, pathErr)
			}
		}
		if resource == "" {
			return nil, fmt.Errorf("local authz csv line %d: resource is required", line)
		}
		resource = syfoncommon.NormalizeAccessResource(resource)
		methods := expandLocalAuthzMethods(localAuthzCell(record, methodsCol))
		if len(methods) == 0 {
			return nil, fmt.Errorf("local authz csv line %d: methods are required", line)
		}

		user := store.users[username]
		if user == nil {
			user = &localAuthzUser{
				password:   password,
				privileges: map[string]map[string]bool{},
			}
			store.users[username] = user
		} else if subtle.ConstantTimeCompare([]byte(user.password), []byte(password)) != 1 {
			return nil, fmt.Errorf("local authz csv line %d: conflicting password for user %q", line, username)
		}
		if _, ok := user.privileges[resource]; !ok {
			user.resources = append(user.resources, resource)
			user.privileges[resource] = map[string]bool{}
		}
		for _, method := range methods {
			user.privileges[resource][method] = true
		}
	}
	if len(store.users) == 0 {
		return nil, fmt.Errorf("local authz csv contains no users")
	}
	return store, nil
}

func (s *LocalAuthzStore) authenticate(authHeader string) (*plugin.AuthenticationOutput, error) {
	username, password, err := parseBasicCredentials(authHeader)
	if err != nil {
		return &plugin.AuthenticationOutput{Authenticated: false, Reason: err.Error()}, nil
	}
	user := s.users[username]
	if user == nil || subtle.ConstantTimeCompare([]byte(user.password), []byte(password)) != 1 {
		return &plugin.AuthenticationOutput{Authenticated: false, Reason: "invalid basic auth credentials"}, nil
	}
	claims := map[string]interface{}{
		"username":                username,
		localAuthzResourcesClaim:  append([]string(nil), user.resources...),
		localAuthzPrivilegesClaim: clonePrivMap(user.privileges),
	}
	if strings.Contains(username, "@") {
		claims["email"] = username
	}
	return &plugin.AuthenticationOutput{
		Authenticated: true,
		Subject:       username,
		Claims:        claims,
	}, nil
}

func (s *LocalAuthzStore) AuthzForSubject(subject string) ([]string, map[string]map[string]bool, bool) {
	user := s.users[strings.TrimSpace(subject)]
	if user == nil {
		return nil, nil, false
	}
	return append([]string(nil), user.resources...), clonePrivMap(user.privileges), true
}

func clonePrivMap(in map[string]map[string]bool) map[string]map[string]bool {
	if len(in) == 0 {
		return map[string]map[string]bool{}
	}
	out := make(map[string]map[string]bool, len(in))
	for k, methods := range in {
		if methods == nil {
			out[k] = map[string]bool{}
			continue
		}
		mm := make(map[string]bool, len(methods))
		for mk, mv := range methods {
			mm[mk] = mv
		}
		out[k] = mm
	}
	return out
}

func parseBasicCredentials(authHeader string) (string, string, error) {
	if authHeader == "" || !strings.HasPrefix(strings.ToLower(authHeader), "basic ") {
		return "", "", fmt.Errorf("missing basic auth header")
	}
	payload := strings.TrimSpace(authHeader[len("basic "):])
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", "", fmt.Errorf("decode basic auth header: %w", err)
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("malformed basic auth credentials")
	}
	return parts[0], parts[1], nil
}

func localAuthzHeaderIndex(header []string) map[string]int {
	out := make(map[string]int, len(header))
	for i, h := range header {
		out[strings.ToLower(strings.TrimSpace(h))] = i
	}
	return out
}

func firstHeader(cols map[string]int, names ...string) int {
	for _, name := range names {
		if idx, ok := cols[name]; ok {
			return idx
		}
	}
	return -1
}

func localAuthzCell(record []string, idx int) string {
	if idx < 0 || idx >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[idx])
}

func localAuthzBlankOrComment(record []string) bool {
	for i, cell := range record {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		return i == 0 && strings.HasPrefix(trimmed, "#")
	}
	return true
}

func expandLocalAuthzMethods(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '|' || r == ';' || r == ' ' || r == '\t'
	})
	seen := map[string]struct{}{}
	for _, field := range fields {
		method := strings.ToLower(strings.TrimSpace(field))
		if method == "" {
			continue
		}
		switch method {
		case "write":
			for _, expanded := range []string{"file_upload", "create", "update", "delete"} {
				seen[expanded] = struct{}{}
			}
		default:
			seen[method] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for method := range seen {
		out = append(out, method)
	}
	return out
}
