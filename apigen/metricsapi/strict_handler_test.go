package metricsapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type strictTestServer struct {
	StrictServerInterface
	listResponse ListMetricsFilesResponseObject
	listErr      error
	bulkCalled   bool
}

func (s *strictTestServer) ListMetricsFiles(context.Context, ListMetricsFilesRequestObject) (ListMetricsFilesResponseObject, error) {
	return s.listResponse, s.listErr
}

func (s *strictTestServer) BulkMetricsFiles(context.Context, BulkMetricsFilesRequestObject) (BulkMetricsFilesResponseObject, error) {
	s.bulkCalled = true
	return nil, nil
}

type strictTestResponse struct {
	err error
}

func (r strictTestResponse) VisitListMetricsFilesResponse(fiber.Ctx) error {
	return r.err
}

func TestStrictHandlerPreservesServiceError(t *testing.T) {
	sourceErr := errors.New("service sentinel")
	serviceErr := fmt.Errorf("list metrics: %w", sourceErr)
	server := &strictTestServer{listErr: serviceErr}
	var observed error
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		observed = err
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}})
	strict := NewStrictHandler(server, nil)
	app.Get("/metrics", func(c fiber.Ctx) error {
		return strict.ListMetricsFiles(c, ListMetricsFilesParams{})
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if observed != serviceErr {
		t.Fatalf("error identity changed: got %v, want %v", observed, serviceErr)
	}
	if !errors.Is(observed, sourceErr) {
		t.Fatalf("error lost source sentinel: %v", observed)
	}
}

func TestStrictHandlerPreservesResponseSerializationError(t *testing.T) {
	sourceErr := errors.New("serializer sentinel")
	serializerErr := fmt.Errorf("encode metrics: %w", sourceErr)
	server := &strictTestServer{listResponse: strictTestResponse{err: serializerErr}}
	var observed error
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		observed = err
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}})
	strict := NewStrictHandler(server, nil)
	app.Get("/metrics", func(c fiber.Ctx) error {
		return strict.ListMetricsFiles(c, ListMetricsFilesParams{})
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if observed != serializerErr {
		t.Fatalf("error identity changed: got %v, want %v", observed, serializerErr)
	}
	if !errors.Is(observed, sourceErr) {
		t.Fatalf("error lost source sentinel: %v", observed)
	}
}

func TestStrictHandlerKeepsInvalidJSONAsBadRequest(t *testing.T) {
	server := &strictTestServer{}
	var observed error
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		observed = err
		code := fiber.StatusInternalServerError
		var fiberErr *fiber.Error
		if errors.As(err, &fiberErr) {
			code = fiberErr.Code
		}
		return c.Status(code).SendString(err.Error())
	}})
	strict := NewStrictHandler(server, nil)
	app.Post("/metrics", func(c fiber.Ctx) error {
		return strict.BulkMetricsFiles(c, BulkMetricsFilesParams{})
	})

	request := httptest.NewRequest(http.MethodPost, "/metrics", strings.NewReader("{"))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	var fiberErr *fiber.Error
	if !errors.As(observed, &fiberErr) || fiberErr.Code != fiber.StatusBadRequest {
		t.Fatalf("observed error = %v, want Fiber 400", observed)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(body) != observed.Error() {
		t.Fatalf("error string changed: body %q, error %q", body, observed.Error())
	}
	if server.bulkCalled {
		t.Fatal("service called after invalid JSON")
	}
}
