package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func TestMultipartRoutesRejectInvalidParts(t *testing.T) {
	app := fiber.New()
	internalapi.RegisterHandlers(app, &internalServer{transfers: transfers.NewService(transfers.Dependencies{})})
	tests := []struct {
		name string
		path string
		body any
	}{
		{name: "sign zero", path: "/data/multipart/upload", body: internalapi.InternalMultipartUploadRequest{UploadId: "missing", PartNumber: 0}},
		{name: "complete empty", path: "/data/multipart/complete", body: internalapi.InternalMultipartCompleteRequest{UploadId: "missing", Parts: []internalapi.InternalMultipartPart{}}},
		{name: "complete duplicate", path: "/data/multipart/complete", body: internalapi.InternalMultipartCompleteRequest{UploadId: "missing", Parts: []internalapi.InternalMultipartPart{{PartNumber: 1}, {PartNumber: 1}}}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			body, err := json.Marshal(testCase.body)
			if err != nil {
				t.Fatal(err)
			}
			response, err := app.Test(httptest.NewRequest(http.MethodPost, testCase.path, bytes.NewReader(body)))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			payload, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusBadRequest || !bytes.Contains(payload, []byte(errorapi.ErrorCodeInvalidInput)) {
				t.Fatalf("status=%d body=%s", response.StatusCode, payload)
			}
		})
	}
}
