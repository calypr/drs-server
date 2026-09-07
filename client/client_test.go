package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/client/internalapi"
	syfonclient "github.com/calypr/syfon/client/services"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func newTestClient(t *testing.T, fn roundTripFunc) *Client {
	t.Helper()
	httpClient := &http.Client{Transport: fn}
	c, err := NewClient(&Config{Address: "http://example.test", HTTPClient: httpClient})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	return c
}

func TestClientBasicAuthAndUserAgent(t *testing.T) {
	t.Parallel()
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "u" || pass != "p" {
			t.Fatalf("missing/invalid basic auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		if got := r.Header.Get("User-Agent"); got != "syfon-test-client" {
			t.Fatalf("unexpected user agent: %q", got)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}

	c, err := NewClient(&Config{
		Address:    "http://example.test",
		BasicAuth:  &BasicAuth{Username: "u", Password: "p"},
		UserAgent:  "syfon-test-client",
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if err := c.Health().Ping(context.Background()); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

func TestGeneratedClientUsesBasicAuthTransport(t *testing.T) {
	t.Parallel()
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/index" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "u" || pass != "p" {
			t.Fatalf("missing/invalid basic auth user=%q pass=%q ok=%v", user, pass, ok)
		}
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Body:       io.NopCloser(strings.NewReader(`{"records":[]}`)),
			Header:     header,
			Request:    r,
		}, nil
	})}

	c, err := NewClient(&Config{
		Address:    "http://example.test",
		BasicAuth:  &BasicAuth{Username: "u", Password: "p"},
		HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	if _, err := c.InternalAPI().InternalListWithResponse(context.Background(), &internalapi.InternalListParams{}); err != nil {
		t.Fatalf("generated client list failed: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestDataUploadBlank(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/data/upload" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var req internalapi.InternalUploadBlankRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Guid == nil || *req.Guid != "abc" {
			t.Fatalf("unexpected guid: %v", req.Guid)
		}
		out := internalapi.InternalUploadBlankOutput{
			Guid:   ptr("abc"),
			Url:    ptr("https://signed"),
			Bucket: ptr("b1"),
		}
		data, _ := json.Marshal(out)
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(string(data))),
			Header:     header,
		}, nil
	})
	req := internalapi.InternalUploadBlankRequest{Guid: ptr("abc")}
	out, err := c.data.UploadBlank(context.Background(), req)
	if err != nil {
		t.Fatalf("UploadBlank failed: %v", err)
	}
	if out.Url == nil || *out.Url != "https://signed" || out.Bucket == nil || *out.Bucket != "b1" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestIndexListByHash(t *testing.T) {
	t.Parallel()
	rec := internalapi.InternalRecord{
		Did: "id-1",
	}
	c := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/index" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.URL.Query().Get("hash"); got != "sha256:deadbeef" {
			t.Fatalf("unexpected hash query: %q", got)
		}
		data, _ := json.Marshal(internalapi.ListRecordsResponse{Records: &[]internalapi.InternalRecord{rec}})
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(string(data))),
			Header:     header,
		}, nil
	})
	out, err := c.index.List(context.Background(), syfonclient.ListRecordsOptions{Hash: "sha256:deadbeef"})
	if err != nil {
		t.Fatalf("Index.List failed: %v", err)
	}
	if out.Records == nil || len(*out.Records) != 1 || (*out.Records)[0].Did != "id-1" {
		t.Fatalf("unexpected response: %+v", out)
	}
}

func TestDataMultipartInitUsesCanonicalUploadId(t *testing.T) {
	t.Parallel()
	c := newTestClient(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/data/multipart/init" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		header := make(http.Header)
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"guid":"g1","uploadId":"u1"}`)),
			Header:     header,
		}, nil
	})
	// InitMultipartUpload returns (uploadID string, respGuid string, err error)
	// Wait, I updated the service methods.
	// c.data.InitMultipartUpload(ctx, guid, filename, bucket) -> (string, string, error)
	uploadID, respGuid, err := c.data.InitMultipartUpload(context.Background(), "g1", "", "")
	if err != nil {
		t.Fatalf("MultipartInit failed: %v", err)
	}
	if respGuid != "g1" || uploadID != "u1" {
		t.Fatalf("unexpected response: guid=%s uploadID=%s", respGuid, uploadID)
	}
}

func TestParseBaseURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty uses default", input: "", want: defaultAddress},
		{name: "missing scheme", input: "example.test:8080", want: "http://example.test:8080"},
		{name: "trim trailing slash", input: "https://example.test/root/", want: "https://example.test/root"},
		{name: "invalid address", input: "http://", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBaseURL(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBaseURL(%q) returned error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseBaseURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNewClientDefaultWiring(t *testing.T) {
	t.Parallel()

	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})}

	raw, err := NewClient(&Config{
		Address:    "example.test:8080/",
		HTTPClient: httpClient,
		UserAgent:  "   ",
		BasicAuth:  &BasicAuth{Username: "   ", Password: "ignored"},
	})
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	c := raw
	if c.Address() != "http://example.test:8080" {
		t.Fatalf("unexpected address: %q", c.Address())
	}
	if c.Health() == nil || c.Data() == nil || c.Index() == nil || c.DRS() == nil || c.Buckets() == nil || c.Metrics() == nil || c.LFS() == nil {
		t.Fatal("expected all service getters to be initialized")
	}
	if c.DRSAPI() == nil || c.BucketAPI() == nil || c.InternalAPI() == nil || c.MetricsAPI() == nil || c.LFSAPI() == nil {
		t.Fatal("expected generated clients to be initialized")
	}
	if c.HTTPClient() == http.DefaultClient {
		t.Fatal("expected request-backed HTTP client, got default client")
	}
	if c.Logger() == nil {
		t.Fatal("expected logger to be initialized")
	}
}

func TestNewClientNilConfigAndFallbackHelpers(t *testing.T) {
	t.Parallel()

	c, err := NewClient(nil)
	if err != nil {
		t.Fatalf("NewClient(nil) returned error: %v", err)
	}
	if c.Address() != defaultAddress {
		t.Fatalf("unexpected default address: %q", c.Address())
	}

	bare := &Client{}
	if bare.HTTPClient() != http.DefaultClient {
		t.Fatal("expected default HTTP client fallback")
	}
	if bare.Logger() == nil {
		t.Fatal("expected default logger fallback")
	}
}
