package server

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
	syclient "github.com/calypr/syfon/client"
	"github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/internal/config"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func TestServerErrorContractAcrossRoutes(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{Internal: true, Metrics: true, Ga4gh: true, LFS: true})
	app.Get("/test-panic", recover.New(), func(fiber.Ctx) error { panic("private panic detail") })
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
		code                     errorapi.ErrorCode
	}{
		{"record absent", "GET", "/index/missing", "", 404, errorapi.ErrorCodeObjectNotFound},
		{"DRS absent", "GET", "/ga4gh/drs/v1/objects/missing", "", 404, errorapi.ErrorCodeObjectNotFound},
		{"generated query binding", "GET", "/index/v1/metrics/files?limit=bad", "", 400, errorapi.ErrorCodeInvalidInput},
		{"generated body binding", "POST", "/info/lfs/verify", "{", 400, errorapi.ErrorCodeInvalidInput},
		{"unmatched route", "GET", "/no-such-route", "", 404, errorapi.ErrorCodeNotFound},
		{"panic recovery", "GET", "/test-panic", "", 500, errorapi.ErrorCodeInternalError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Request-Id", "contract-request")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			var payload errorapi.APIError
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("HTTP %d is not an error envelope: %s: %v", resp.StatusCode, body, err)
			}
			if resp.StatusCode != tc.status || payload.Status != tc.status || payload.Code != tc.code {
				t.Fatalf("HTTP %d payload %+v, want %d %s", resp.StatusCode, payload, tc.status, tc.code)
			}
			if resp.Header.Get("X-Request-Id") != "contract-request" || payload.RequestId == nil || *payload.RequestId != "contract-request" {
				t.Fatalf("request ID missing from header/body: %v %+v", resp.Header, payload)
			}
			if strings.Contains(string(body), "private panic detail") {
				t.Fatalf("panic cause escaped redaction: %s", body)
			}
		})
	}
}

type routerErrorTransport struct{ app *fiber.App }

func (r routerErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-Request-Id", "sdk-router-request")
	resp, err := r.app.Test(req)
	if resp != nil {
		resp.Request = req
	}
	return resp, err
}

func TestServerErrorContractThroughSDK(t *testing.T) {
	app := buildMockServerRouterWithRoutes(config.RoutesConfig{Ga4gh: true})
	sdk, err := syclient.NewClient(&syclient.Config{
		Address:    "http://server.test",
		HTTPClient: &http.Client{Transport: routerErrorTransport{app: app}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sdk.DRS().GetObject(context.Background(), "missing")
	var apiErr *apierror.APIError
	if !errors.As(err, &apiErr) || !errors.Is(err, errorapi.ErrObjectNotFound) || !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("SDK lost source error identity: %T %v", err, err)
	}
	if apiErr.Status != 404 || apiErr.RequestID != "sdk-router-request" || apiErr.Method != "GET" || apiErr.URL != "http://server.test/ga4gh/drs/v1/objects/missing" {
		t.Fatalf("SDK lost HTTP metadata: %+v", apiErr)
	}
}
