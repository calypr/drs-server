package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/httpapi"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	providerstorage "github.com/calypr/syfon/internal/storage"
	"github.com/gofiber/fiber/v3"
)

type failingProjectScopeResolver struct{}

func (failingProjectScopeResolver) ResolveStorageScope(context.Context, string, string) (buckets.StorageScope, error) {
	return buckets.StorageScope{Provider: "s3", Bucket: "bucket", Prefix: "prefix"}, nil
}

type failingProjectInventory struct {
	err error
}

func (f failingProjectInventory) Inventory(context.Context, providerstorage.InventoryRequest) (providerstorage.InventoryResult, error) {
	return providerstorage.InventoryResult{}, f.err
}

func TestProjectStorageProviderErrorContractAtHTTPBoundary(t *testing.T) {
	source := &providerstorage.OperationError{
		Kind:       providerstorage.ErrorUnavailable,
		Provider:   "s3",
		Capability: "inventory",
		Cause:      errors.New("private provider detail"),
	}
	service := projectstorage.NewService(projectstorage.Dependencies{
		ScopeResolver: failingProjectScopeResolver{},
		Providers:     projectstorage.Providers{Inventory: failingProjectInventory{err: source}},
	})

	app := fiber.New(fiber.Config{ErrorHandler: httpapi.FiberErrorHandler})
	app.Get("/storage", func(c fiber.Ctx) error {
		_, err := service.InspectProjectStorage(c.Context(), "org", "project", projectstorage.InspectionOptions{})
		return httpapi.HandleError(c, err)
	})
	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/storage", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload errorapi.APIError
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, body)
	}
	if response.StatusCode != http.StatusServiceUnavailable || payload.Status != http.StatusServiceUnavailable || payload.Code != errorapi.ErrorCodeStorageUnavailable || payload.Category != errorapi.ErrorCategoryUnavailable {
		t.Fatalf("unexpected provider error response: status=%d payload=%+v", response.StatusCode, payload)
	}
	if payload.Message != "Service Unavailable" || strings.Contains(string(body), "private provider detail") {
		t.Fatalf("provider cause escaped public response: %s", body)
	}
}
