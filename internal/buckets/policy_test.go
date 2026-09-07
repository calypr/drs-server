package buckets

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
)

func bucketPolicyContext(mode string, header bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	session.AuthHeaderPresent = header
	session.SetAuthorizations(nil, privileges, mode == "gen3" || mode == "local-authz")
	return access.WithSession(context.Background(), session)
}

func TestAuthorizeScopeWritePolicy(t *testing.T) {
	const projectResource = "/organization/org/project/project"
	const organizationResource = "/organization/org"

	tests := []struct {
		name    string
		ctx     context.Context
		org     string
		project string
		wantErr bool
	}{
		{
			name:    "local enforcement off allows bucket control",
			ctx:     bucketPolicyContext("local", false, nil),
			org:     "org",
			project: "project",
		},
		{
			name:    "gen3 missing header is denied",
			ctx:     bucketPolicyContext("gen3", false, map[string]map[string]bool{projectResource: {"create": true}}),
			org:     "org",
			project: "project",
			wantErr: true,
		},
		{
			name:    "project method allows write",
			ctx:     bucketPolicyContext("gen3", true, map[string]map[string]bool{projectResource: {"create": true}}),
			org:     "org",
			project: "project",
		},
		{
			name:    "wildcard method allows write",
			ctx:     bucketPolicyContext("gen3", true, map[string]map[string]bool{projectResource: {"*": true}}),
			org:     "org",
			project: "project",
		},
		{
			name:    "arborist create descendant allows project",
			ctx:     bucketPolicyContext("gen3", true, map[string]map[string]bool{organizationResource: {"arborist:create-descendant": true}}),
			org:     "org",
			project: "project",
		},
		{
			name:    "arborist manage owners allows project",
			ctx:     bucketPolicyContext("gen3", true, map[string]map[string]bool{organizationResource: {"arborist:manage-owners": true}}),
			org:     "org",
			project: "project",
		},
		{
			name:    "unrelated resource is denied",
			ctx:     bucketPolicyContext("gen3", true, map[string]map[string]bool{"/organization/other/project/project": {"create": true}}),
			org:     "org",
			project: "project",
			wantErr: true,
		},
		{
			name:    "enforced empty organization is denied",
			ctx:     bucketPolicyContext("gen3", true, nil),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AuthorizeScopeWrite(tt.ctx, tt.org, tt.project, "create", "update")
			if (err != nil) != tt.wantErr {
				t.Fatalf("AuthorizeScopeWrite() error = %v, wantErr=%t", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, faults.ErrUnauthorized) {
				t.Fatalf("AuthorizeScopeWrite() error = %v, want unauthorized", err)
			}
		})
	}
}

func TestScopeAllowedAndBucketNameFiltering(t *testing.T) {
	ctx := bucketPolicyContext("gen3", true, map[string]map[string]bool{
		"/organization/org/project/project": {"read": true},
	})
	scope := Scope{Organization: "org", ProjectID: "project", Bucket: "bucket"}
	if !ScopeAllowed(ctx, scope, "read") {
		t.Fatal("expected project read access")
	}
	if !BucketsAllowedByNames(ctx, []Scope{scope}, "bucket", "read") {
		t.Fatal("expected matching bucket access")
	}
	if BucketsAllowedByNames(ctx, []Scope{scope}, "other", "read") {
		t.Fatal("expected non-matching bucket denial")
	}
}
