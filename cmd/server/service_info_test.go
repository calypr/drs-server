package server

import (
	"errors"
	"testing"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/version"
)

func TestRetryProductionSchemaCheckOnlyRetriesTransientSchemaState(t *testing.T) {
	for _, message := range []string{
		"failed to ping database: connection refused",
		"schema migration ledger is missing; run the cluster DB-init Job",
		"required schema relation \"drs_object\" is missing",
		"database schema is behind supported version 2",
	} {
		if !retryProductionSchemaCheck(errors.New(message)) {
			t.Fatalf("retryProductionSchemaCheck(%q) = false, want true", message)
		}
	}
	for _, message := range []string{
		"database schema migration version 3 is newer than this binary",
		"schema migration 1 checksum or name mismatch",
		"schema migration ledger has a gap before version 2",
	} {
		if retryProductionSchemaCheck(errors.New(message)) {
			t.Fatalf("retryProductionSchemaCheck(%q) = true, want false", message)
		}
	}
}

func TestServiceInfoUsesLinkerProvidedVersion(t *testing.T) {
	original := version.Version
	t.Cleanup(func() { version.Version = original })
	version.Version = "v9.8.7-test"

	info := serviceInfoForConfig(testServiceInfoConfig())
	if info.Version != "v9.8.7-test" {
		t.Fatalf("service version = %q, want linker-provided version", info.Version)
	}
	if info.Type.Version != "1.5.0" {
		t.Fatalf("DRS type version = %q, want 1.5.0", info.Type.Version)
	}
}

func testServiceInfoConfig() *config.Config {
	return &config.Config{
		Profile: config.ProfileDevelopment,
		Service: config.ServiceConfig{
			ID:              "drs-service-calypr",
			Name:            "Calypr DRS Server",
			Description:     "Calypr-backed DRS server",
			Environment:     "dev",
			Organization:    "Calypr",
			OrganizationURL: "https://github.com/calypr/syfon",
		},
		DRS:    config.DRSConfig{MaxBulkRequestLength: 100},
		Routes: config.RoutesConfig{Ga4gh: true},
	}
}

func TestServiceInfoForConfigUsesCompositeIdentityAndAdvertisedLimit(t *testing.T) {
	cfg := &config.Config{
		Profile: config.ProfileProduction,
		Service: config.ServiceConfig{
			ID:               "org.example.drs",
			Name:             "Example DRS",
			Description:      "Example service",
			Environment:      "prod",
			Organization:     "Example Org",
			OrganizationURL:  "https://example.org",
			ContactURL:       "https://example.org/contact",
			DocumentationURL: "https://example.org/docs",
		},
		DRS:    config.DRSConfig{MaxBulkRequestLength: 7},
		Routes: config.RoutesConfig{Ga4gh: true},
	}
	info := serviceInfoForConfig(cfg)
	if info.Id != "org.example.drs" || info.Type.Version != "1.5.0" || info.MaxBulkRequestLength != 7 {
		t.Fatalf("unexpected service identity: %+v", info)
	}
	if info.Organization.Name != "Example Org" || info.Organization.Url != "https://example.org" {
		t.Fatalf("unexpected organization: %+v", info.Organization)
	}
	if info.Drs == nil || info.Drs.MaxBulkRequestLength != 7 || info.Drs.ObjectRegistrationSupported == nil || !*info.Drs.ObjectRegistrationSupported {
		t.Fatalf("unexpected DRS capabilities: %+v", info.Drs)
	}
	if info.Drs.ObjectCount != nil || info.Drs.TotalObjectSize != nil {
		t.Fatal("service info fabricated object counts")
	}
}
