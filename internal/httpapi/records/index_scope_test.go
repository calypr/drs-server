package records

import (
	"testing"
)

func TestScopeFromQuery(t *testing.T) {
	tests := []struct {
		name         string
		organization string
		program      string
		project      string
		wantOrg      string
		wantProject  string
		wantOK       bool
		wantErr      bool
	}{
		{name: "organization and project build a resource path", organization: "org", project: "proj", wantOrg: "org", wantProject: "proj", wantOK: true},
		{name: "program falls back when organization is empty", program: "org", wantOrg: "org", wantOK: true},
		{name: "project without organization is invalid", project: "proj", wantErr: true},
		{name: "empty scope is allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scopeFromQuery(tt.organization, tt.program, tt.project)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotOK := got.Organization != ""; gotOK != tt.wantOK {
				t.Fatalf("unexpected ok: got %v want %v", gotOK, tt.wantOK)
			}
			if got.Organization != tt.wantOrg || got.Project != tt.wantProject {
				t.Fatalf("unexpected scope: got org=%q project=%q want org=%q project=%q", got.Organization, got.Project, tt.wantOrg, tt.wantProject)
			}
		})
	}
}
