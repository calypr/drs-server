package metrics

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type errorPropagationReporter struct {
	usage.Reporter
	err error
}

func (r errorPropagationReporter) GetFileUsage(context.Context, string) (*usage.FileUsage, error) {
	return nil, r.err
}

func TestMetricsRoutesPropagateSourceErrorsThroughSDKBoundary(t *testing.T) {
	tests := []struct {
		name      string
		source    error
		status    int
		requestID string
	}{
		{name: "file usage not found", source: errorapi.ErrFileUsageNotFound, status: http.StatusNotFound, requestID: "metrics-not-found"},
		{name: "service unavailable", source: errorapi.ErrUnavailable, status: http.StatusServiceUnavailable, requestID: "metrics-unavailable"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := fiber.New(fiber.Config{ErrorHandler: middleware.FiberErrorHandler})
			app.Use(middleware.NewRequestIDMiddleware(nil).FiberMiddleware())
			RegisterMetricsRoutes(app, errorPropagationReporter{err: test.source}, nil)

			req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/missing", nil)
			req.Header.Set("X-Request-Id", test.requestID)
			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}

			decoded := apierror.FromResponse(resp, body)
			if resp.StatusCode != test.status {
				t.Fatalf("expected status %d, got %d body=%s", test.status, resp.StatusCode, body)
			}
			if !errors.Is(decoded, test.source) {
				t.Fatalf("source error identity was lost: source=%v decoded=%v body=%s", test.source, decoded, body)
			}
			if decoded.RequestID != test.requestID {
				t.Fatalf("expected SDK request ID %q, got %q body=%s", test.requestID, decoded.RequestID, body)
			}
			if got := resp.Header.Get("X-Request-Id"); got != test.requestID {
				t.Fatalf("expected response request ID %q, got %q", test.requestID, got)
			}
		})
	}
}

func TestMetricsRoutesRedactAndLogUntypedSourceErrors(t *testing.T) {
	privateCause := errors.New("private database detail")
	requestID := "metrics-internal"
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	app := fiber.New(fiber.Config{ErrorHandler: middleware.FiberErrorHandler})
	app.Use(middleware.NewRequestIDMiddleware(nil).FiberMiddleware())
	RegisterMetricsRoutes(app, errorPropagationReporter{err: privateCause}, nil)

	req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/private", nil)
	req.Header.Set("X-Request-Id", requestID)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	decoded := apierror.FromResponse(resp, body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%s", resp.StatusCode, body)
	}
	if decoded.Message == privateCause.Error() || strings.Contains(string(body), privateCause.Error()) {
		t.Fatalf("private source detail leaked in response: %s", body)
	}
	if decoded.Message != http.StatusText(http.StatusInternalServerError) || decoded.RequestID != requestID {
		t.Fatalf("unexpected SDK error: %+v body=%s", decoded, body)
	}
	if count := strings.Count(logs.String(), privateCause.Error()); count != 1 {
		t.Fatalf("expected source cause in one log record, got %d logs=%s", count, logs.String())
	}
	if !strings.Contains(logs.String(), "request_id="+requestID) {
		t.Fatalf("expected request ID in error log: %s", logs.String())
	}
}
