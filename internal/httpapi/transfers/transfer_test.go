package transfers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/server/internalapi"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func TestHandleInternalMultipartUpload_NotFound(t *testing.T) {
	mockDB := &transferHTTPFixture{}
	mockUM := &internalDRSStorageFake{}
	om := newInternalDRSObjectManager(mockDB, mockUM)
	lifecycle := domaintransfers.NewMultipartLifecycle(om.TransferService)
	app := fiber.New()
	app.Post("/multipart/upload", handleInternalMultipartUploadFiber(lifecycle))

	reqBody := internalapi.InternalMultipartUploadRequest{
		UploadId:   "non-existent",
		PartNumber: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/multipart/upload", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	if string(responseBody) != "Upload ID not found" {
		t.Errorf("expected exact not-found body, got %q", responseBody)
	}
}

func TestHandleInternalMultipartComplete_NotFound(t *testing.T) {
	mockDB := &transferHTTPFixture{}
	mockUM := &internalDRSStorageFake{}
	om := newInternalDRSObjectManager(mockDB, mockUM)
	lifecycle := domaintransfers.NewMultipartLifecycle(om.TransferService)
	app := fiber.New()
	app.Post("/multipart/complete", handleInternalMultipartCompleteFiber(lifecycle))

	reqBody := internalapi.InternalMultipartCompleteRequest{
		UploadId: "non-existent",
		Parts:    []internalapi.InternalMultipartPart{},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/multipart/complete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
	if string(responseBody) != "Upload ID not found" {
		t.Errorf("expected exact not-found body, got %q", responseBody)
	}
}

func TestHandleInternalMultipartCompletePreservesPartOrderAndOpaqueETags(t *testing.T) {
	fake := &internalDRSStorageFake{}
	om := newInternalDRSObjectManager(&transferHTTPFixture{}, fake)
	lifecycle := domaintransfers.NewMultipartLifecycle(om.TransferService)
	uploadID, err := lifecycle.Begin(t.Context(), "bucket-a", "path/object.bin")
	if err != nil || uploadID != "mock-upload-id" {
		t.Fatalf("begin multipart upload = (%q, %v)", uploadID, err)
	}

	body, _ := json.Marshal(internalapi.InternalMultipartCompleteRequest{
		UploadId: uploadID,
		Parts: []internalapi.InternalMultipartPart{
			{PartNumber: 7, ETag: `"opaque-seven"`},
			{PartNumber: 2, ETag: `"opaque-two"`},
		},
	})
	app := fiber.New()
	app.Post("/multipart/complete", handleInternalMultipartCompleteFiber(lifecycle))
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/multipart/complete", bytes.NewBuffer(body)))
	if err != nil {
		t.Fatalf("complete request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if len(fake.completeParts) != 2 || fake.completeParts[0].PartNumber != 7 || fake.completeParts[0].ETag != `"opaque-seven"` || fake.completeParts[1].PartNumber != 2 || fake.completeParts[1].ETag != `"opaque-two"` {
		t.Fatalf("completed parts were not preserved in caller order: %+v", fake.completeParts)
	}
}

func TestHandleInternalMultipartCompleteDeletesSessionBeforeProviderError(t *testing.T) {
	fake := &internalDRSStorageFake{completeErr: errors.New("provider completion failed")}
	om := newInternalDRSObjectManager(&transferHTTPFixture{}, fake)
	lifecycle := domaintransfers.NewMultipartLifecycle(om.TransferService)
	uploadID, err := lifecycle.Begin(t.Context(), "bucket-a", "path/object.bin")
	if err != nil || uploadID != "mock-upload-id" {
		t.Fatalf("begin multipart upload = (%q, %v)", uploadID, err)
	}

	body, _ := json.Marshal(internalapi.InternalMultipartCompleteRequest{UploadId: uploadID})
	app := fiber.New()
	app.Post("/multipart/complete", handleInternalMultipartCompleteFiber(lifecycle))
	resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/multipart/complete", bytes.NewBuffer(body)))
	if err != nil {
		t.Fatalf("complete request failed: %v", err)
	}
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected provider failure to map to 500, got %d", resp.StatusCode)
	}
	if err := lifecycle.Complete(t.Context(), uploadID, nil); !errors.Is(err, domaintransfers.ErrMultipartUploadNotFound) {
		t.Fatalf("expected consumed upload ID after provider failure, got %v", err)
	}
}
