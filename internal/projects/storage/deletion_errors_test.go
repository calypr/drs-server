package storage

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/requestid"
	providerstorage "github.com/calypr/syfon/internal/storage"
)

type markerDelete struct {
	locations []string
}

type failingDeleteScopes struct{ err error }

func (s failingDeleteScopes) ResolveStorageScope(context.Context, string, string) (buckets.StorageScope, error) {
	return buckets.StorageScope{}, s.err
}

func TestDeleteProjectObjectsRedactsScopeDatabaseFailure(t *testing.T) {
	service, _ := projectService(&fakeInventory{}, &markerDelete{})
	service.inspector.resolver = failingDeleteScopes{err: errors.New("PRIVATE_SCOPE_DATABASE_MARKER")}
	ctx := requestid.WithRequestID(context.Background(), "scope-failure-request")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	results := service.DeleteProjectObjects(ctx, "org", "project", []string{
		"s3://bucket/prefix/project/one", "s3://bucket/prefix/project/two",
	})
	if len(results) != 2 {
		t.Fatalf("expected two failed results, got %+v", results)
	}
	for _, result := range results {
		if result.Status != "error" || result.Error == "" || strings.Contains(result.Error, "PRIVATE_SCOPE_DATABASE_MARKER") {
			t.Fatalf("unsafe database failure result: %+v", result)
		}
	}
	if strings.Count(logs.String(), "PRIVATE_SCOPE_DATABASE_MARKER") != 1 || !strings.Contains(logs.String(), "scope-failure-request") {
		t.Fatalf("database cause or request ID missing from single diagnostic: %s", logs.String())
	}
}

func (d *markerDelete) DeleteExact(_ context.Context, targets []providerstorage.DeleteTarget) error {
	for _, target := range targets {
		d.locations = append(d.locations, target.Location)
		if strings.HasSuffix(target.Location, "/bad") {
			return &providerstorage.OperationError{
				Kind:       providerstorage.ErrorUnavailable,
				Provider:   "s3",
				Capability: "delete",
				Cause:      errors.New("PRIVATE_PROVIDER_DATABASE_MARKER"),
			}
		}
	}
	return nil
}

func TestDeleteProjectObjectsRedactsProviderCauseAndPreservesSiblingOrder(t *testing.T) {
	deletePort := &markerDelete{}
	service, _ := projectService(&fakeInventory{}, deletePort)
	ctx := requestid.WithRequestID(context.Background(), "request-delete-123")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	results := service.DeleteProjectObjects(ctx, "org", "project", []string{
		"s3://bucket/prefix/project/good",
		"s3://bucket/prefix/project/bad",
		"s3://bucket/prefix/project/good",
	})
	if len(results) != 2 {
		t.Fatalf("results = %+v, want two unique siblings", results)
	}
	if results[0].ObjectURL != "s3://bucket/prefix/project/good" || results[0].Status != "deleted" {
		t.Fatalf("successful sibling = %+v", results[0])
	}
	if results[1].ObjectURL != "s3://bucket/prefix/project/bad" || results[1].Status != "error" {
		t.Fatalf("failed sibling = %+v", results[1])
	}
	if strings.Contains(results[1].Error, "PRIVATE_PROVIDER_DATABASE_MARKER") {
		t.Fatalf("provider cause escaped deletion result: %+v", results[1])
	}
	if len(deletePort.locations) != 2 || deletePort.locations[0] != results[0].ObjectURL || deletePort.locations[1] != results[1].ObjectURL {
		t.Fatalf("delete order = %+v", deletePort.locations)
	}
	if count := strings.Count(logs.String(), "PRIVATE_PROVIDER_DATABASE_MARKER"); count != 1 {
		t.Fatalf("provider diagnostic count = %d, logs = %q", count, logs.String())
	}
	if !strings.Contains(logs.String(), "request_id=request-delete-123") {
		t.Fatalf("request ID missing from logs: %q", logs.String())
	}
}
