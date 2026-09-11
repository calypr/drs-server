package common

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestResponseBodyError(t *testing.T) {
	t.Parallel()

	if err := ResponseBodyError(nil, "fetch failed"); err == nil || err.Error() != "fetch failed: nil response" {
		t.Fatalf("unexpected nil-response error: %v", err)
	}

	err := ResponseBodyError(&http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader("   denied   ")),
	}, "fetch failed")
	if err == nil || err.Error() != "fetch failed: status 403 body=denied" {
		t.Fatalf("unexpected body error: %v", err)
	}

	err = ResponseBodyError(&http.Response{
		StatusCode: http.StatusBadGateway,
		Body:       io.NopCloser(strings.NewReader("   ")),
	}, "fetch failed")
	if err == nil || err.Error() != "fetch failed: status 502" {
		t.Fatalf("unexpected empty-body error: %v", err)
	}
}

func TestIsCloudPresignedURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "aws v4", url: "https://example.test?X-Amz-Signature=abc", want: true},
		{name: "gcs", url: "https://example.test?X-Goog-Signature=abc", want: true},
		{name: "legacy", url: "https://example.test?AWSAccessKeyId=abc&Expires=1", want: true},
		{name: "plain", url: "https://example.test/file.txt", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsCloudPresignedURL(tc.url); got != tc.want {
				t.Fatalf("IsCloudPresignedURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

func TestProgressContextHelpers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	if GetProgress(ctx) != nil {
		t.Fatal("expected nil progress callback from bare context")
	}
	if got := GetOid(ctx); got != "" {
		t.Fatalf("expected empty oid from bare context, got %q", got)
	}

	var seen ProgressEvent
	cb := func(evt ProgressEvent) error {
		seen = evt
		return nil
	}
	ctx = WithProgress(ctx, cb)
	ctx = WithOid(ctx, "oid-123")

	gotCB := GetProgress(ctx)
	if gotCB == nil {
		t.Fatal("expected progress callback in context")
	}
	if err := gotCB(ProgressEvent{Event: "tick", Oid: "oid-123"}); err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	if seen.Event != "tick" || seen.Oid != "oid-123" {
		t.Fatalf("unexpected progress event: %+v", seen)
	}
	if got := GetOid(ctx); got != "oid-123" {
		t.Fatalf("unexpected oid: %q", got)
	}
}
