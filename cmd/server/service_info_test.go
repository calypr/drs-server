package server

import "testing"

func TestServiceInfoForBackend(t *testing.T) {
	tests := []struct {
		name        string
		sqlite      bool
		description string
	}{
		{name: "sqlite", sqlite: true, description: "Calypr-backed DRS server (SQLite)"},
		{name: "postgres", description: "Calypr-backed DRS server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := serviceInfoForBackend(tt.sqlite)
			if info.Id != "drs-service-calypr" || info.Name != "Calypr DRS Server" || info.Version != "1.0.0" {
				t.Fatalf("unexpected service identity: %+v", info)
			}
			if info.Type.Group != "org.ga4gh" || info.Type.Artifact != "drs" || info.Type.Version != "1.2.0" {
				t.Fatalf("unexpected service type: %+v", info.Type)
			}
			if info.Description == nil || *info.Description != tt.description {
				t.Fatalf("description = %v, want %q", info.Description, tt.description)
			}
			if info.Environment == nil || *info.Environment != "prod" {
				t.Fatalf("environment = %v, want prod", info.Environment)
			}
			if info.CreatedAt == nil || info.UpdatedAt == nil {
				t.Fatalf("timestamps must be populated: %+v", info)
			}
		})
	}
}
