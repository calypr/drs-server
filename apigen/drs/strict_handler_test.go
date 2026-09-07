package drs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type strictTestServer struct {
	StrictServerInterface
	optionsResponse OptionsBulkObjectResponseObject
	optionsErr      error
}

func (s *strictTestServer) OptionsBulkObject(context.Context, OptionsBulkObjectRequestObject) (OptionsBulkObjectResponseObject, error) {
	return s.optionsResponse, s.optionsErr
}

type strictTestResponse struct {
	err error
}

func (r strictTestResponse) VisitOptionsBulkObjectResponse(fiber.Ctx) error {
	return r.err
}

func TestStrictHandlerPreservesServiceError(t *testing.T) {
	sourceErr := errors.New("service sentinel")
	serviceErr := fmt.Errorf("load objects: %w", sourceErr)
	server := &strictTestServer{optionsErr: serviceErr}
	var observed error
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		observed = err
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}})
	strict := NewStrictHandler(server, nil)
	app.Options("/objects", strict.OptionsBulkObject)

	response, err := app.Test(httptest.NewRequest(http.MethodOptions, "/objects", nil))
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

func TestStrictHandlerPreservesContextCancellation(t *testing.T) {
	server := &strictTestServer{optionsErr: context.Canceled}
	var observed error
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		observed = err
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}})
	strict := NewStrictHandler(server, nil)
	app.Options("/objects", strict.OptionsBulkObject)

	response, err := app.Test(httptest.NewRequest(http.MethodOptions, "/objects", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusInternalServerError)
	}
	if observed != context.Canceled {
		t.Fatalf("context error changed: got %v, want %v", observed, context.Canceled)
	}
}

func TestStrictHandlerPreservesResponseSerializationError(t *testing.T) {
	sourceErr := errors.New("serializer sentinel")
	serializerErr := fmt.Errorf("encode response: %w", sourceErr)
	server := &strictTestServer{optionsResponse: strictTestResponse{err: serializerErr}}
	var observed error
	app := fiber.New(fiber.Config{ErrorHandler: func(c fiber.Ctx, err error) error {
		observed = err
		return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}})
	strict := NewStrictHandler(server, nil)
	app.Options("/objects", strict.OptionsBulkObject)

	response, err := app.Test(httptest.NewRequest(http.MethodOptions, "/objects", nil))
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
