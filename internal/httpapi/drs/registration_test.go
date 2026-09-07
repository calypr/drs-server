package drs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func TestRegisterObjects(t *testing.T) {
	db := newDRSObjectStore(map[string]*objects.Record{})
	om := testDRSServices(db, nil)
	app := fiber.New()
	RegisterDRSRoutes(app, om.objectService, om.transferService, generated.Service{})

	candidate := generated.DrsObjectCandidate{
		Size: 50,
		Checksums: []generated.Checksum{{
			Type: "sha256", Checksum: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}},
		ControlledAccess: drsPtr([]string{"/organization/org1/project/proj1"}),
		AccessMethods: &[]generated.AccessMethod{{
			Type: generated.AccessMethodTypeS3,
			AccessUrl: &struct {
				Headers *[]string `json:"headers,omitempty"`
				Url     string    `json:"url"`
			}{Url: "s3://bucket/org1/proj1/object"},
		}},
	}
	body, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/register", bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created struct {
		Objects []map[string]json.RawMessage `json:"objects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	if len(created.Objects) != 1 || string(created.Objects[0]["id"]) == "" {
		t.Fatalf("unexpected register response: %+v", created)
	}
	if string(created.Objects[0]["did"]) != string(created.Objects[0]["id"]) {
		t.Fatalf("expected id and did in response: %+v", created.Objects[0])
	}
}

func TestRegisterObjectsRejectsMissingAccessMethods(t *testing.T) {
	db := newDRSObjectStore(map[string]*objects.Record{})
	om := testDRSServices(db, nil)
	app := fiber.New()
	RegisterDRSRoutes(app, om.objectService, om.transferService, generated.Service{})

	body, err := json.Marshal(generated.DrsObjectCandidate{
		Size:             100,
		Checksums:        []generated.Checksum{{Type: "sha256", Checksum: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
		ControlledAccess: drsPtr([]string{"/organization/org1/project/proj1"}),
	})
	if err != nil {
		t.Fatalf("marshal candidate: %v", err)
	}
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/objects/register", bytes.NewReader(body)))
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("register status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}
