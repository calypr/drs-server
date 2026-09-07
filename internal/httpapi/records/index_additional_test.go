package records

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestParseInternalListPaginationFiber_InvalidInputs(t *testing.T) {
	om := newInternalDRSObjectManager(&internalRecordStore{})

	cases := []struct {
		name string
		url  string
	}{
		{name: "invalid limit", url: "/index?limit=abc"},
		{name: "negative limit", url: "/index?limit=-1"},
		{name: "invalid page", url: "/index?page=abc"},
		{name: "negative page", url: "/index?page=-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rr := doInternalDRSTestRequest(req, om)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for %s, got %d body=%s", tc.url, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestParseInternalListPaginationFiber_StartSuppressesPage(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		limit, start, offset, err := parseInternalListPaginationFiber(c)
		if err != nil {
			t.Fatalf("parseInternalListPaginationFiber returned error: %v", err)
		}
		if limit != 10 {
			t.Fatalf("expected limit 10, got %d", limit)
		}
		if start != "did-123" {
			t.Fatalf("expected start did-123, got %q", start)
		}
		if offset != 0 {
			t.Fatalf("expected offset 0 when start is present, got %d", offset)
		}
		return c.SendStatus(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/?limit=10&start=did-123&page=99", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestHandleInternalBulkDocuments_InvalidBodyAndMissingIDs(t *testing.T) {
	om := newInternalDRSObjectManager(&internalRecordStore{})

	req := httptest.NewRequest(http.MethodPost, "/bulk/documents", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	rr := doInternalDRSTestRequestWithAlias(req, om, http.MethodPost, "/bulk/documents", handleInternalBulkDocumentsFiber(om.ObjectService))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid json, got %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/bulk/documents", strings.NewReader(`{"ids":[]}`))
	req.Header.Set("Content-Type", "application/json")
	rr = doInternalDRSTestRequestWithAlias(req, om, http.MethodPost, "/bulk/documents", handleInternalBulkDocumentsFiber(om.ObjectService))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty ids, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleInternalList_IgnoresLegacyPathValidation(t *testing.T) {
	om := newInternalDRSObjectManager(&internalRecordStore{})

	req := httptest.NewRequest(http.MethodGet, "/index?path=nested", nil)
	rr := doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when legacy path query is ignored, got %d body=%s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/index?organization=org&project=proj&path=../nested", nil)
	rr = doInternalDRSTestRequest(req, om)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 when invalid legacy path query is ignored, got %d body=%s", rr.Code, rr.Body.String())
	}
}
