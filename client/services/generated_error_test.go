package services

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/bucketapi"
	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/client/apierror"
)

type generatedErrorRoundTripper struct {
	body string
	err  error
}

func (t generatedErrorRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.err != nil {
		return nil, t.err
	}
	header := make(http.Header)
	header.Set("Content-Type", "application/json")
	header.Set("X-Request-ID", "request-123")
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Status:     "404 Not Found",
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Request:    req,
	}, nil
}

func TestGeneratedServicesPreserveTransportErrors(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("transport failed")} {
		httpClient := &http.Client{Transport: generatedErrorRoundTripper{err: cause}}
		generated, err := internalapi.NewClientWithResponses("http://example.test", internalapi.WithHTTPClient(httpClient))
		if err != nil {
			t.Fatal(err)
		}
		_, err = NewDataService(generated, nil, nil, nil).DownloadURL(context.Background(), "missing", 0, false)
		var apiErr *apierror.APIError
		if !errors.Is(err, cause) || errors.As(err, &apiErr) {
			t.Fatalf("transport cause changed: %T %v", err, err)
		}
	}
}

func TestGeneratedServicesPreserveMalformedErrorResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		code errorapi.ErrorCode
	}{
		{name: "empty", body: "", code: errorapi.ErrorCodeNotFound},
		{name: "truncated", body: `{"code":"not_found"`, code: errorapi.ErrorCodeNotFound},
		{name: "numeric legacy", body: `{"code":404,"message":"gone"}`, code: errorapi.ErrorCodeNotFound},
		{name: "exact beats numeric", body: `{"code":"object_not_found","status":404}`, code: errorapi.ErrorCodeObjectNotFound},
		{name: "future code", body: `{"code":"future_failure","message":"later"}`, code: "future_failure"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			httpClient := &http.Client{Transport: generatedErrorRoundTripper{body: test.body}}
			internal, err := internalapi.NewClientWithResponses("http://example.test", internalapi.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			bucket, err := bucketapi.NewClientWithResponses("http://example.test", bucketapi.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			drsClient, err := drs.NewClientWithResponses("http://example.test", drs.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			lfs, err := lfsapi.NewClientWithResponses("http://example.test", lfsapi.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			metrics, err := metricsapi.NewClientWithResponses("http://example.test", metricsapi.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}

			ctx := context.Background()
			checks := []struct {
				name string
				call func() error
			}{
				{name: "data", call: func() error {
					_, err := NewDataService(internal, nil, nil, nil).DownloadURL(ctx, "missing", 0, false)
					return err
				}},
				{name: "index", call: func() error {
					_, err := NewIndexService(internal, nil).Get(ctx, "missing")
					return err
				}},
				{name: "buckets", call: func() error {
					_, err := NewBucketsService(bucket).ListScopes(ctx, "missing")
					return err
				}},
				{name: "drs", call: func() error {
					_, err := NewDRSService(drsClient, nil).GetObject(ctx, "missing")
					return err
				}},
				{name: "metrics", call: func() error {
					_, err := NewMetricsService(metrics).File(ctx, "missing")
					return err
				}},
				{name: "lfs", call: func() error {
					_, err := NewLFSService(lfs).Batch(ctx, lfsapi.Download, []lfsapi.BatchRequestObject{{Oid: "missing", Size: 1}})
					return err
				}},
			}
			for _, check := range checks {
				t.Run(check.name, func(t *testing.T) {
					err := check.call()
					if err == nil {
						t.Fatal("expected error")
					}
					var apiErr *apierror.APIError
					if !errors.As(err, &apiErr) {
						t.Fatalf("expected *apierror.APIError, got %T: %v", err, err)
					}
					if apiErr.Code != test.code || apiErr.Status != http.StatusNotFound || apiErr.Body != test.body || apiErr.RequestID != "request-123" {
						t.Fatalf("unexpected API error: %+v", apiErr)
					}
					if !errors.Is(err, errorapi.ErrNotFound) {
						t.Fatalf("expected not-found classification, got %v", err)
					}
					if test.code == errorapi.ErrorCodeObjectNotFound && !errors.Is(err, errorapi.ErrObjectNotFound) {
						t.Fatalf("expected object-not-found classification, got %v", err)
					}
				})
			}
		})
	}
}
