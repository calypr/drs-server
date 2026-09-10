package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access/authentication"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type endpointCase struct {
	Method   string
	Template string
}

var pathVarPattern = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

func endpointPtr[T any](value T) *T { return &value }

func TestAdminRoutesNotRegistered(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{
		Docs:     true,
		Ga4gh:    true,
		Metrics:  true,
		Internal: true,
		LFS:      true,
	})

	reqSign := httptest.NewRequest(http.MethodPost, "/admin/sign_url", bytes.NewBufferString(`{"url":"s3://b/k","method":"GET"}`))
	reqSign.Header.Set("Content-Type", "application/json")
	respSign, err := app.Test(reqSign)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if respSign.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /admin/sign_url to be absent (404), got %d", respSign.StatusCode)
	}

	reqCreds := httptest.NewRequest(http.MethodGet, "/admin/credentials", nil)
	respCreds, err := app.Test(reqCreds)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if respCreds.StatusCode != http.StatusNotFound {
		t.Fatalf("expected /admin/credentials to be absent (404), got %d", respCreds.StatusCode)
	}
}

func buildMockServerRouter() *fiber.App {
	return buildMockServerRouterWithRoutes(config.RoutesConfig{
		Docs:     true,
		Ga4gh:    true,
		Metrics:  true,
		Internal: true,
		LFS:      true,
	})
}

func TestHealthOnlyServerExposesNoOptionalRoutes(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{})

	endpoints := collectEndpoints(t, app)
	foundHealth := false
	for _, ep := range endpoints {
		if ep.Template == "/healthz" {
			foundHealth = true
			continue
		}
		if ep.Template == "/" {
			continue
		}
		t.Fatalf("expected only healthz route when no modules are enabled, found %s %s", ep.Method, ep.Template)
	}
	if !foundHealth {
		t.Fatal("expected /healthz route when no modules are enabled")
	}
}

func buildMockServerRouterWithRoutes(routes config.RoutesConfig) *fiber.App {
	objectStore := newServerObjectStore(map[string]*drs.DrsObject{
		"sha-1": {
			Id:          "sha-1",
			Name:        endpointPtr("mock-object"),
			Size:        1,
			Version:     endpointPtr("1"),
			Description: endpointPtr("mock"),
			Checksums:   []drs.Checksum{{Type: "sha256", Checksum: "sha-1"}},
			AccessMethods: &[]drs.AccessMethod{
				{
					Type:      "s3",
					AccessId:  endpointPtr("s3"),
					AccessUrl: &drs.AccessURL{Url: "s3://test-bucket-1/sha-1"},
				},
			},
			ControlledAccess: &[]string{"/programs/data_file"},
		},
	})
	bucketStore := &serverBucketStore{credentials: map[string]buckets.Credential{
		"test-bucket-1": {
			Bucket:    "test-bucket-1",
			Region:    "us-east-1",
			AccessKey: "mock-key",
			SecretKey: "mock-secret",
		},
	}}
	app := fiber.New(fiber.Config{ErrorHandler: httpapi.FiberErrorHandler})

	logger := slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
	authRuntime := authentication.NewRuntime(logger, config.AuthConfig{Mode: config.AuthModeLocal})
	authzHandler := httpapi.AuthorizationHandler(httpapi.AuthzOptions{Mode: "local", Evaluator: authRuntime})
	requestIDHandler := httpapi.RequestIDHandler(logger)
	cfg := &config.Config{Routes: routes}
	dependencies := mockServerDependencies(objectStore, bucketStore)
	objectService := objects.NewService(dependencies.objects)
	usageService := usage.NewService(usage.Dependencies{Reports: dependencies.usageReports, Objects: objectService})
	transferService := transfers.NewService(transfers.Dependencies{
		Objects: objectService,
		Scopes:  dependencies.bucketService, Credentials: dependencies.bucketService,
		Events: dependencies.usageIngest,
	})
	lfsService := transferlfs.NewService(transferService, objectService, dependencies.bucketService, dependencies.pending, dependencies.usageIngest, nil)
	rt := &serverRuntime{
		app:              app,
		cfg:              cfg,
		serviceInfo:      serviceInfoForBackend(true),
		objectService:    objectService,
		transferService:  transferService,
		lfsService:       lfsService,
		usageService:     usageService,
		usageIngest:      dependencies.usageIngest,
		bucketService:    dependencies.bucketService,
		authzHandler:     authzHandler,
		requestIDHandler: requestIDHandler,
	}
	registerServerRoutes(rt)
	return app
}

func collectEndpoints(t *testing.T, app *fiber.App) []endpointCase {
	t.Helper()
	routes := app.GetRoutes(false)
	out := make([]endpointCase, 0, len(routes))
	for _, route := range routes {
		if route.Method == http.MethodHead {
			continue
		}
		out = append(out, endpointCase{Method: route.Method, Template: route.Path})
	}
	return out
}
