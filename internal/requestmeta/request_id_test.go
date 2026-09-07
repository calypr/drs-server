package requestmeta

import (
	"context"
	"testing"
)

func TestWithAndGetRequestID(t *testing.T) {
	ctx := context.Background()
	if got := GetRequestID(ctx); got != "" {
		t.Fatalf("expected empty request id, got %q", got)
	}

	ctx = WithRequestID(ctx, "rid-123")
	if got := GetRequestID(ctx); got != "rid-123" {
		t.Fatalf("expected request id rid-123, got %q", got)
	}
}
