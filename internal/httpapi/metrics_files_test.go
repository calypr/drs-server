package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/client/apierror"
	"github.com/gofiber/fiber/v3"
)

func TestMetricsRoutes_ValidateListParams(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	registerMetricsRoutes(app, &metricsReporterFake{}, &metricsIngestFake{})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?limit=0", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestMetricsRoutes_BulkFiles(t *testing.T) {
	reports := &metricsReporterFake{batch: []metricsapi.FileUsage{{ObjectId: metricsString("obj-a"), Name: metricsString("a.txt"), Size: metricsInt64(10)}}}
	app := newMetricsTestApp(reports, &metricsIngestFake{})
	request := httptest.NewRequest(http.MethodPost, "/index/v1/metrics/files/bulk?organization=cbds&project=end_to_end_test", strings.NewReader(`{"object_ids":["obj-a","obj-b","missing","obj-a"],"inactive_days":30}`))
	request.Header.Set("Content-Type", "application/json")
	setMetricsAuthHeaders(request, "gen3", true, map[string]map[string]bool{"/programs/cbds/projects/end_to_end_test": {"read": true}})

	resp, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}
	var payload metricsapi.MetricsListResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Data == nil || len(*payload.Data) != 1 || *(*payload.Data)[0].ObjectId != "obj-a" {
		t.Fatalf("expected configured batch response, got %s", body)
	}
}

func TestMetricsSummaryAuthzAndScope(t *testing.T) {
	reports := &metricsReporterFake{summary: metricsapi.FileUsageSummary{TotalFiles: metricsInt64(1), TotalUploads: metricsInt64(0), TotalDownloads: metricsInt64(0), InactiveFileCount: metricsInt64(0), RecordCount: metricsInt64(1)}}
	app := newMetricsTestApp(reports, &metricsIngestFake{})

	request := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?organization=cbds&project=end_to_end_test", nil)
	setMetricsAuthHeaders(request, "gen3", true, map[string]map[string]bool{"/programs/cbds/projects/end_to_end_test": {"read": true}})
	resp, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}
	var payload metricsapi.FileUsageSummary
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.TotalFiles == nil || *payload.TotalFiles != 1 || payload.RecordCount == nil || *payload.RecordCount != 1 || payload.TotalUploads == nil || *payload.TotalUploads != 0 || payload.TotalDownloads == nil || *payload.TotalDownloads != 0 || payload.InactiveFileCount == nil || *payload.InactiveFileCount != 0 {
		t.Fatalf("unexpected scoped summary: %+v", payload)
	}

	request = httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?program=cbds&project=end_to_end_test", nil)
	setMetricsAuthHeaders(request, "gen3", true, map[string]map[string]bool{"/programs/cbds/projects/end_to_end_test": {"read": true}})
	resp, err = app.Test(request)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("legacy program scope response = %v, %v", resp, err)
	}

	request = httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?organization=cbds&project=end_to_end_test", nil)
	setMetricsAuthHeaders(request, "gen3", false, nil)
	resp, err = app.Test(request)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

func TestMetricsFilesAuthzAndScope(t *testing.T) {
	reports := &metricsReporterFake{
		files:           []metricsapi.FileUsage{{ObjectId: metricsString("scoped-1"), Name: metricsString("f1"), Size: metricsInt64(1), UploadCount: metricsInt64(2), DownloadCount: metricsInt64(3)}},
		fileUsage:       map[string]metricsapi.FileUsage{"other-1": {ObjectId: metricsString("other-1"), Name: metricsString("f2"), Size: metricsInt64(2)}},
		scopedFileUsage: map[string]metricsapi.FileUsage{"scoped-1": {ObjectId: metricsString("scoped-1"), Name: metricsString("f1"), Size: metricsInt64(1)}},
	}
	app := newMetricsTestApp(reports, &metricsIngestFake{})
	privileges := map[string]map[string]bool{"/programs/cbds/projects/end_to_end_test": {"read": true}}

	request := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?organization=cbds&project=end_to_end_test&limit=10&offset=0", nil)
	setMetricsAuthHeaders(request, "gen3", true, privileges)
	resp, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
	}
	var list map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, ok := list["data"].([]any)
	if !ok || len(data) != 1 || data[0].(map[string]any)["object_id"] != "scoped-1" {
		t.Fatalf("unexpected scoped list: %v", list)
	}

	request = httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/other-1?organization=cbds&project=end_to_end_test", nil)
	setMetricsAuthHeaders(request, "gen3", true, privileges)
	resp, err = app.Test(request)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for scoped lookup outside scope, got %d", resp.StatusCode)
	}

	request = httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/other-1", nil)
	setMetricsAuthHeaders(request, "gen3", true, map[string]map[string]bool{"/programs": {"read": true}})
	resp, err = app.Test(request)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for global lookup, got %d", resp.StatusCode)
	}
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
			app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
			app.Use(RequestIDHandler(nil))
			registerMetricsRoutes(app, &metricsReporterFake{getFileUsageErr: test.source}, nil)

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

	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	app.Use(RequestIDHandler(nil))
	registerMetricsRoutes(app, &metricsReporterFake{getFileUsageErr: privateCause}, nil)

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
