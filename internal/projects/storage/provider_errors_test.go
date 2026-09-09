package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/requestid"
	providerstorage "github.com/calypr/syfon/internal/storage"
	"github.com/gofiber/fiber/v3"
)

func TestMapStorageErrorPreservesProviderIdentityAndCause(t *testing.T) {
	tests := []struct {
		name     string
		kind     providerstorage.ErrorKind
		provider string
		wantKind ErrorKind
	}{
		{name: "invalid", kind: providerstorage.ErrorInvalid, provider: "s3", wantKind: ErrorInvalidInput},
		{name: "not found", kind: providerstorage.ErrorNotFound, provider: "s3", wantKind: ErrorObjectNotFound},
		{name: "credential missing", kind: providerstorage.ErrorNotFound, wantKind: ErrorCredentialMissing},
		{name: "forbidden", kind: providerstorage.ErrorForbidden, provider: "s3", wantKind: ErrorPermissionDenied},
		{name: "incomplete", kind: providerstorage.ErrorIncomplete, provider: "s3", wantKind: ErrorListingIncomplete},
		{name: "unsupported", kind: providerstorage.ErrorUnsupported, provider: "s3", wantKind: ErrorUnsupported},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cause := errors.New("provider diagnostic")
			source := &providerstorage.OperationError{Kind: test.kind, Provider: test.provider, Capability: "inventory", Cause: cause}
			mapped := mapStorageError(source, "inventory", "bucket", "key")
			var projectErr *Error
			if !errors.As(mapped, &projectErr) || projectErr.Kind != test.wantKind {
				t.Fatalf("mapped error = %T %v, want project kind %q", mapped, mapped, test.wantKind)
			}
			var operationErr *providerstorage.OperationError
			if !errors.As(mapped, &operationErr) || operationErr != source {
				t.Fatalf("provider operation identity was not preserved: %v", mapped)
			}
			if !errors.Is(mapped, cause) {
				t.Fatal("provider cause was not preserved")
			}
			if !strings.Contains(mapped.Error(), "provider diagnostic") {
				t.Fatalf("diagnostic cause missing from Error(): %q", mapped.Error())
			}
			if strings.Contains(projectErr.PublicMessage(), "provider diagnostic") {
				t.Fatalf("diagnostic cause escaped PublicMessage(): %q", projectErr.PublicMessage())
			}
		})
	}
}

func TestTransientProviderErrorUsesUnavailableContractAndRedactsCause(t *testing.T) {
	cause := fmt.Errorf("private provider detail: %w", context.DeadlineExceeded)
	source := &providerstorage.OperationError{Kind: providerstorage.ErrorUnavailable, Provider: "s3", Capability: "probe", Cause: cause}
	mapped := mapStorageError(source, "probe", "bucket", "key")
	var operationErr *providerstorage.OperationError
	if !errors.As(mapped, &operationErr) || operationErr != source {
		t.Fatalf("provider operation identity was not preserved: %v", mapped)
	}
	if !errors.Is(mapped, context.DeadlineExceeded) {
		t.Fatal("deadline cause was not preserved")
	}

	app := fiber.New(fiber.Config{ErrorHandler: middleware.FiberErrorHandler})
	app.Get("/probe", func(c fiber.Ctx) error { return middleware.HandleError(c, mapped) })
	response, err := app.Test(httptest.NewRequest("GET", "/probe", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 503 {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
	if strings.Contains(string(body), "private provider detail") {
		t.Fatalf("provider cause escaped public response: %s", body)
	}
}

func TestMappedIncompleteDiagnosticLogsCauseOnceWithRequestID(t *testing.T) {
	cause := errors.New("private provider detail")
	source := &providerstorage.OperationError{Kind: providerstorage.ErrorIncomplete, Provider: "s3", Capability: "inventory", Cause: cause}
	mapped := mapStorageError(source, "inventory", "bucket", "prefix")
	ctx := requestid.WithRequestID(context.Background(), "request-123")
	public := safeStorageErrorMessage(mapped, "inventory")
	if strings.Contains(public, "private provider detail") {
		t.Fatalf("private cause escaped public warning: %q", public)
	}

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logStorageDiagnostic(ctx, mapped, "inventory")
	output := logs.String()
	if strings.Count(output, "private provider detail") != 1 {
		t.Fatalf("diagnostic cause count = %d, logs = %q", strings.Count(output, "private provider detail"), output)
	}
	if !strings.Contains(output, "request_id=request-123") {
		t.Fatalf("request ID missing from diagnostic logs: %q", output)
	}
}

func TestMappedProviderErrorsRedactCauseAtHTTPBoundary(t *testing.T) {
	tests := []struct {
		name string
		kind providerstorage.ErrorKind
	}{
		{name: "invalid", kind: providerstorage.ErrorInvalid},
		{name: "incomplete", kind: providerstorage.ErrorIncomplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped := mapStorageError(&providerstorage.OperationError{
				Kind:       test.kind,
				Provider:   "s3",
				Capability: "inventory",
				Cause:      errors.New("private provider detail"),
			}, "inventory", "bucket", "prefix")
			app := fiber.New(fiber.Config{ErrorHandler: middleware.FiberErrorHandler})
			app.Get("/storage", func(c fiber.Ctx) error { return middleware.HandleError(c, mapped) })
			response, err := app.Test(httptest.NewRequest("GET", "/storage", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "private provider detail") {
				t.Fatalf("provider cause escaped HTTP response: %s", body)
			}
		})
	}
}

type errorProbe struct {
	cause error
}

func (p errorProbe) Probe(_ context.Context, targets []providerstorage.ProbeTarget) []providerstorage.ProbeResult {
	target := targets[0].Target
	if target.Key == "bad" {
		return []providerstorage.ProbeResult{{ID: targets[0].ID, Target: target, Err: p.cause}}
	}
	return []providerstorage.ProbeResult{{ID: targets[0].ID, Target: target, Metadata: providerstorage.ObjectMetadata{Bucket: target.PhysicalBucket, Key: target.Key}}}
}

type errorInventory struct {
	cause error
}

func (i errorInventory) Inventory(_ context.Context, request providerstorage.InventoryRequest) (providerstorage.InventoryResult, error) {
	if request.Prefix == "prefix/project/good" {
		return providerstorage.InventoryResult{Items: []providerstorage.ObjectMetadata{{Bucket: "bucket", Key: request.Prefix}}, Complete: true}, nil
	}
	return providerstorage.InventoryResult{}, i.cause
}

func TestBatchProbeAndValidationRedactPartialProviderFailures(t *testing.T) {
	cause := errors.New("private provider detail")
	service, _ := projectService(&fakeInventory{}, nil)
	service.probe = errorProbe{cause: &providerstorage.OperationError{Kind: providerstorage.ErrorUnavailable, Provider: "s3", Capability: "probe", Cause: cause}}
	probeResults := service.ProbeObjects(context.Background(), []InspectRequest{
		{ID: "good", ObjectURL: "s3://bucket/good"},
		{ID: "bad", ObjectURL: "s3://bucket/bad"},
	})
	if probeResults[0].Status != ProbePresent || probeResults[1].Status != ProbeError {
		t.Fatalf("probe results = %+v", probeResults)
	}
	if probeResults[1].ErrorKind != "storage_unavailable" || strings.Contains(probeResults[1].Error, "private provider detail") {
		t.Fatalf("unsafe probe result = %+v", probeResults[1])
	}

	service.inventory = errorInventory{cause: &providerstorage.OperationError{Kind: providerstorage.ErrorUnavailable, Provider: "s3", Capability: "inventory", Cause: cause}}
	validationResults := service.ValidateInventoryObjects(context.Background(), []InspectRequest{
		{ID: "good", ObjectURL: "s3://bucket/prefix/project/good"},
		{ID: "bad", ObjectURL: "s3://bucket/prefix/project/bad"},
	})
	if validationResults[0].Status != ProbePresent || validationResults[1].Status != ProbeError {
		t.Fatalf("validation results = %+v", validationResults)
	}
	if validationResults[1].ErrorKind != "storage_unavailable" || strings.Contains(validationResults[1].Error, "private provider detail") {
		t.Fatalf("unsafe validation result = %+v", validationResults[1])
	}
}
