package transfers

import (
	"context"
	"errors"
	"testing"
)

func TestMultipartLifecycleSessionsAreInstanceLocal(t *testing.T) {
	service := NewService(Dependencies{Multipart: &multipartFake{beginID: "opaque-upload"}})
	assertMultipartLifecycleIsolated(t, NewMultipartLifecycle(service), NewMultipartLifecycle(service))
}

func TestMultipartLifecycleSessionsAreIsolatedAcrossServices(t *testing.T) {
	firstService := NewService(Dependencies{Multipart: &multipartFake{beginID: "opaque-upload"}})
	secondService := NewService(Dependencies{Multipart: &multipartFake{beginID: "opaque-upload"}})
	assertMultipartLifecycleIsolated(t, NewMultipartLifecycle(firstService), NewMultipartLifecycle(secondService))
}

func assertMultipartLifecycleIsolated(t *testing.T, first, second *MultipartLifecycle) {
	t.Helper()
	uploadID, err := first.Begin(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	if _, err := second.SignPart(context.Background(), uploadID, 1); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("SignPart() error = %v, want ErrMultipartUploadNotFound", err)
	}
}

func TestMultipartLifecycleBeginDoesNotStoreFailedProviderUpload(t *testing.T) {
	providerErr := errors.New("provider begin failed")
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: &multipartFake{
		beginID:  "opaque-upload",
		beginErr: providerErr,
	}}))

	if _, err := lifecycle.Begin(context.Background(), "bucket", "key"); !errors.Is(err, providerErr) {
		t.Fatalf("Begin() error = %v, want provider error", err)
	}
	if _, err := lifecycle.SignPart(context.Background(), "opaque-upload", 1); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("SignPart() error = %v, want ErrMultipartUploadNotFound", err)
	}
}

func TestMultipartLifecycleCompleteConsumesBeforeProviderCompletion(t *testing.T) {
	providerErr := errors.New("provider completion failed")
	lifecycle := NewMultipartLifecycle(NewService(Dependencies{Multipart: &multipartFake{
		beginID:     "opaque-upload",
		completeErr: providerErr,
	}}))
	uploadID, err := lifecycle.Begin(context.Background(), "bucket", "key")
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}

	if err := lifecycle.Complete(context.Background(), uploadID, nil); !errors.Is(err, providerErr) {
		t.Fatalf("Complete() error = %v, want provider error", err)
	}
	if err := lifecycle.Complete(context.Background(), uploadID, nil); !errors.Is(err, ErrMultipartUploadNotFound) {
		t.Fatalf("retry Complete() error = %v, want ErrMultipartUploadNotFound", err)
	}
}
