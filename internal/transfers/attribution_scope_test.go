package transfers

import (
	"context"
	"testing"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/calypr/syfon/internal/usage"
)

func TestScopeForAccessUsesOnlyOneCanonicalResource(t *testing.T) {
	tests := []struct {
		name           string
		controlled     []string
		authorizations map[string][]string
		wantOrg        string
		wantProject    string
	}{
		{
			name:        "single project",
			controlled:  []string{"/programs/org/projects/project"},
			wantOrg:     "org",
			wantProject: "project",
		},
		{
			name:       "two projects in one organization",
			controlled: []string{"/organization/org/project/a", "/organization/org/project/b"},
		},
		{
			name:       "multiple organizations",
			controlled: []string{"/organization/org-a/project/p", "/organization/org-b/project/p"},
		},
		{
			name:           "legacy authorization map with multiple organizations",
			authorizations: map[string][]string{"org-a": {"p"}, "org-b": {"p"}},
		},
		{
			name:           "legacy authorization map with one project",
			authorizations: map[string][]string{"org": {"p"}},
			wantOrg:        "org",
			wantProject:    "p",
		},
		{
			name:        "duplicate resources",
			controlled:  []string{"/organization/org/project/p", "/programs/org/projects/p"},
			wantOrg:     "org",
			wantProject: "p",
		},
		{
			name:           "controlled access takes precedence",
			controlled:     []string{"/organization/controlled/project/p"},
			authorizations: map[string][]string{"authorization": {"p"}},
			wantOrg:        "controlled",
			wantProject:    "p",
		},
		{
			name:       "no resources",
			controlled: []string{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			obj := &objects.Record{Authorizations: test.authorizations}
			if test.controlled != nil {
				obj.ControlledAccess = &test.controlled
			}
			gotOrg, gotProject := scopeForAccess(context.Background(), obj, nil, usage.ProviderTransferDirectionDownload)
			if gotOrg != test.wantOrg || gotProject != test.wantProject {
				t.Fatalf("scopeForAccess() = %q/%q, want %q/%q", gotOrg, gotProject, test.wantOrg, test.wantProject)
			}
		})
	}
}

func TestScopeForAccessHonorsValidExplicitScopeOnly(t *testing.T) {
	obj := &objects.Record{ControlledAccess: &[]string{
		"/organization/org/project/project",
		"/organization/org/project/other",
	}}

	tests := []struct {
		name        string
		selected    *AccessScope
		wantOrg     string
		wantProject string
	}{
		{
			name:        "valid selected project",
			selected:    &AccessScope{Organization: " org ", Project: " project "},
			wantOrg:     "org",
			wantProject: "project",
		},
		{
			name:     "unsupported selected project is unattributed",
			selected: &AccessScope{Organization: "org", Project: "missing"},
		},
		{
			name:     "invalid selected project is unattributed",
			selected: &AccessScope{Project: "project"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotOrg, gotProject := scopeForAccess(context.Background(), obj, test.selected, usage.ProviderTransferDirectionDownload)
			if gotOrg != test.wantOrg || gotProject != test.wantProject {
				t.Fatalf("scopeForAccess() = %q/%q, want %q/%q", gotOrg, gotProject, test.wantOrg, test.wantProject)
			}
		})
	}
}

func TestEventFromObjectFixedRequestIdentityHasStableScopedIDs(t *testing.T) {
	obj := &objects.Record{
		Id:               "object-1",
		Size:             42,
		ControlledAccess: &[]string{"/organization/org/project/project"},
	}
	ctx := requestid.WithRequestID(context.Background(), "request-1")
	request := AccessRequest{
		Object:     obj,
		AccessID:   "s3",
		StorageURL: "s3://bucket/object-1",
		Scope:      &AccessScope{Organization: "org", Project: "project"},
	}

	first := eventFromObject(ctx, request)
	second := eventFromObject(ctx, request)
	if first.Organization != "org" || first.Project != "project" {
		t.Fatalf("explicit scope = %q/%q", first.Organization, first.Project)
	}
	if first.EventID != second.EventID || first.AccessGrantID != second.AccessGrantID {
		t.Fatalf("fixed request identity changed IDs: first=%+v second=%+v", first, second)
	}
	if first.EventID != usage.EventID(first) || first.AccessGrantID != usage.GrantID(first) {
		t.Fatalf("event IDs were not computed from the attributed event: %+v", first)
	}
}

func TestEventFromObjectRejectsExplicitScopeWithoutOperationAuthorization(t *testing.T) {
	obj := &objects.Record{ControlledAccess: &[]string{
		"/organization/org/project/authorized",
		"/organization/org/project/other",
	}}
	session := access.NewSession("jwt")
	session.SetAuthorizations(
		[]string{"/organization/org/project/authorized"},
		map[string]map[string]bool{
			"/organization/org/project/authorized": {"update": true},
		},
		true,
	)
	ctx := access.WithSession(context.Background(), session)
	event := eventFromObject(ctx, AccessRequest{
		Object:    obj,
		Direction: usage.ProviderTransferDirectionUpload,
		Scope:     &AccessScope{Organization: "org", Project: "other"},
	})
	if event.Organization != "" || event.Project != "" {
		t.Fatalf("unauthorized explicit scope = %q/%q, want empty scope", event.Organization, event.Project)
	}
}
