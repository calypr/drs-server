package drsapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/gofiber/fiber/v3"
)

func TestRegisterObjects(t *testing.T) {
	db := &testutils.MockDatabase{Objects: map[string]*drs.DrsObject{}}
	um := &testutils.MockUrlManager{}
	om := core.NewObjectManager(db, um)
	app := fiber.New()
	RegisterDRSRoutes(app, om)

	t.Run("Register_Single", func(t *testing.T) {
		size := int64(50)
		authz := []string{"/programs/org1/projects/proj1"}
		cand := drs.DrsObjectCandidate{
			Size: size,
			Checksums: []drs.Checksum{
				{Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			},
			ControlledAccess: &authz,
			AccessMethods: &[]drs.AccessMethod{{
				Type: "s3",
				AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/org1/proj1/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			}},
		}
		body, _ := json.Marshal(cand)
		req := httptest.NewRequest("POST", "/objects/register", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)

		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d. check internal/api/apiutil/error.go for mapping", resp.StatusCode)
		}

		var created drs.N201ObjectsCreated
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(created.Objects) != 1 || created.Objects[0].Id == "" {
			t.Errorf("unexpected response: %+v", created)
		}
		if created.Objects[0].AccessMethods == nil || len(*created.Objects[0].AccessMethods) == 0 {
			t.Fatalf("expected access methods in response: %+v", created.Objects[0])
		}
		if created.Objects[0].ControlledAccess == nil || len(*created.Objects[0].ControlledAccess) == 0 {
			t.Fatalf("expected controlled access in response: %+v", created.Objects[0])
		}
	})

	t.Run("Register_Single_ControlledAccess", func(t *testing.T) {
		body := []byte(`{
			"size": 64,
			"checksums": [{"type": "sha256", "checksum": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}],
			"controlled_access": ["https://calypr.org/program/org2/project/proj2"],
			"access_methods": [{"type": "s3", "access_url": {"url": "s3://bucket/path/to/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}}]
		}`)
		req := httptest.NewRequest("POST", "/objects/register", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}

		var created struct {
			Objects []map[string]any `json:"objects"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if len(created.Objects) != 1 {
			t.Fatalf("expected one object, got %+v", created)
		}
		controlled, ok := created.Objects[0]["controlled_access"].([]any)
		if !ok || len(controlled) != 1 || controlled[0] != "/organization/org2/project/proj2" {
			t.Fatalf("expected controlled_access in response: %+v", created.Objects[0])
		}
		methods, ok := created.Objects[0]["access_methods"].([]any)
		if !ok || len(methods) != 1 {
			t.Fatalf("expected access_methods in response: %+v", created.Objects[0])
		}
	})

	t.Run("Register_Bulk", func(t *testing.T) {
		size := int64(100)
		authz1 := []string{"/organization/org1/project/proj1"}
		authz2 := []string{"/organization/org1/project/proj2"}
		bodyObj := struct {
			Candidates []drs.DrsObjectCandidate `json:"candidates"`
		}{
			Candidates: []drs.DrsObjectCandidate{
				{Size: size, Checksums: []drs.Checksum{{Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}, ControlledAccess: &authz1, AccessMethods: &[]drs.AccessMethod{{Type: "s3", AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/org1/proj1/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}}},
				{Size: size, Checksums: []drs.Checksum{{Type: "sha256", Checksum: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}}, ControlledAccess: &authz2, AccessMethods: &[]drs.AccessMethod{{Type: "s3", AccessUrl: &struct {
					Headers *[]string `json:"headers,omitempty"`
					Url     string    `json:"url"`
				}{Url: "s3://bucket/org1/proj2/cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}}}},
			},
		}
		body, _ := json.Marshal(bodyObj)
		req := httptest.NewRequest("POST", "/objects/register", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d", resp.StatusCode)
		}
	})

	t.Run("Register_WithoutIDOrProjectScope_Fails", func(t *testing.T) {
		size := int64(100)
		cand := drs.DrsObjectCandidate{
			Size: size,
			Checksums: []drs.Checksum{
				{Type: "sha256", Checksum: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},
			},
			ControlledAccess: &[]string{"/organization/org1"},
		}
		body, _ := json.Marshal(cand)
		req := httptest.NewRequest("POST", "/objects/register", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})

	t.Run("Register_WithoutAccessMethods_Fails", func(t *testing.T) {
		size := int64(100)
		cand := drs.DrsObjectCandidate{
			Size: size,
			Checksums: []drs.Checksum{
				{Type: "sha256", Checksum: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
			},
			ControlledAccess: &[]string{"/organization/org1/project/proj1"},
		}
		body, _ := json.Marshal(cand)
		req := httptest.NewRequest("POST", "/objects/register", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := app.Test(req)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", resp.StatusCode)
		}
	})
}
