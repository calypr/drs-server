package internaldrs

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/gofiber/fiber/v3"
)

func TestHandleInternalMultipartUpload_NotFound(t *testing.T) {
	mockDB := &testutils.MockDatabase{}
	mockUM := &testutils.MockUrlManager{}
	om := core.NewObjectManager(mockDB, mockUM)
	app := fiber.New()
	app.Post("/multipart/upload", handleInternalMultipartUploadFiber(om))

	reqBody := internalapi.InternalMultipartUploadRequest{
		UploadId:   "non-existent",
		PartNumber: 1,
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/multipart/upload", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestHandleInternalMultipartComplete_NotFound(t *testing.T) {
	mockDB := &testutils.MockDatabase{}
	mockUM := &testutils.MockUrlManager{}
	om := core.NewObjectManager(mockDB, mockUM)
	app := fiber.New()
	app.Post("/multipart/complete", handleInternalMultipartCompleteFiber(om))

	reqBody := internalapi.InternalMultipartCompleteRequest{
		UploadId: "non-existent",
		Parts:    []internalapi.InternalMultipartPart{},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest("POST", "/multipart/complete", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	resp, _ := app.Test(req)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}
