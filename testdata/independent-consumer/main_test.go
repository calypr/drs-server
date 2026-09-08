package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	syfonclient "github.com/calypr/syfon/client"
	"github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/client/services"
)

func TestPublishedModulesWorkWithoutWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   errorapi.ErrorCode
		exact  error
		broad  error
	}{
		{"canonical", 409, `{"code":"object_checksum_immutable","message":"immutable"}`, errorapi.ErrorCodeObjectChecksumImmutable, errorapi.ErrObjectChecksumImmutable, errorapi.ErrConflict},
		{"malformed", 404, `{"code":`, errorapi.ErrorCodeNotFound, nil, errorapi.ErrNotFound},
		{"numeric legacy", 404, `{"code":404,"message":"missing"}`, errorapi.ErrorCodeNotFound, nil, errorapi.ErrNotFound},
		{"fractional numeric", 500, `{"code":404.5}`, errorapi.ErrorCodeInternalError, nil, errorapi.Define(errorapi.ErrorCodeInternalError, errorapi.ErrorCategoryInternalError, "internal error")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-Id", "external-request")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			generated, err := drs.NewClientWithResponses(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			response, err := generated.GetObjectWithResponse(context.Background(), "object-id", nil)
			if err != nil || response == nil || response.StatusCode() != tc.status || string(response.Body) != tc.body {
				t.Fatalf("generated response=%+v error=%v", response, err)
			}
			sdk, err := syfonclient.New(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = sdk.DRS().GetObject(context.Background(), "object-id")
			var apiErr *apierror.APIError
			if !errors.As(err, &apiErr) || !errors.Is(err, tc.broad) {
				t.Fatalf("missing shared API error or broad match: %T %v", err, err)
			}
			if tc.exact != nil && !errors.Is(err, tc.exact) {
				t.Fatalf("missing exact match: %v", err)
			}
			if apiErr.Code != tc.code || apiErr.Status != tc.status || apiErr.Body != tc.body || apiErr.RequestID != "external-request" || apiErr.Method != "GET" || !strings.HasPrefix(apiErr.URL, server.URL) {
				t.Fatalf("HTTP metadata or code lost: %+v", apiErr)
			}
			if errors.Is(err, errorapi.ErrObjectSizeImmutable) {
				t.Fatalf("matched sibling exact code: %v", err)
			}
			if tc.status == 404 && !errors.Is(err, services.ErrObjectNotFound) {
				t.Fatalf("legacy broad sentinel compatibility lost: %v", err)
			}
		})
	}
}
