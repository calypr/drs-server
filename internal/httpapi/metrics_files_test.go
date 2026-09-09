package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func TestMetricsRoutes_ListAndSummary(t *testing.T) {
	now := time.Now().UTC()
	reports := &metricsReporterFake{
		files: []usage.FileUsage{
			{ObjectID: "sha-1", Name: "f1", Size: 1, UploadCount: 1, DownloadCount: 3, LastDownloadTime: metricsTimePtr(now.AddDate(0, 0, -10))},
			{ObjectID: "sha-2", Name: "f2", Size: 2, UploadCount: 1},
		},
		summary: usage.FileUsageSummary{TotalFiles: 2},
	}
	app := fiber.New()
	registerMetricsRoutes(app, reports, &metricsIngestFake{})

	t.Run("list", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?limit=10&offset=0&inactive_days=365", nil))
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", resp.StatusCode, body)
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := payload["data"]; !ok {
			t.Fatalf("expected data field in response: %v", payload)
		}
	})

	t.Run("summary", func(t *testing.T) {
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?inactive_days=365", nil))
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
		if payload.TotalFiles == nil || *payload.TotalFiles != 2 {
			t.Fatalf("expected total files 2, got %+v", payload.TotalFiles)
		}
	})
}

func TestMetricsRoutes_GetNotFoundAndValidation(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	registerMetricsRoutes(app, &metricsReporterFake{}, &metricsIngestFake{})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/missing", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}

	resp, err = app.Test(httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?limit=0", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

func TestMetricsFileHandlersCoverBoundaryErrors(t *testing.T) {
	server := newMetricsServer(&metricsReporterFake{}, nil)
	limit := 0
	response, err := server.ListMetricsFiles(context.Background(), metricsapi.ListMetricsFilesRequestObject{Params: metricsapi.ListMetricsFilesParams{Limit: &limit}})
	if err != nil {
		t.Fatalf("invalid list request error: %v", err)
	}
	if _, ok := response.(metricsapi.ListMetricsFiles400JSONResponse); !ok {
		t.Fatalf("invalid list response type = %T", response)
	}

	unauthorized := metricsTestContext(context.Background(), "gen3", false, false, nil)
	summaryResponse, err := server.GetMetricsSummary(unauthorized, metricsapi.GetMetricsSummaryRequestObject{})
	if err != nil {
		t.Fatalf("unauthorized summary error: %v", err)
	}
	if _, ok := summaryResponse.(metricsapi.GetMetricsSummary401JSONResponse); !ok {
		t.Fatalf("unauthorized summary response type = %T", summaryResponse)
	}

	_, err = server.GetMetricsFile(context.Background(), metricsapi.GetMetricsFileRequestObject{ObjectId: "missing"})
	if !errors.Is(err, errorapi.ErrNotFound) {
		t.Fatalf("missing file error = %v", err)
	}
}

func TestMetricsRoutes_BulkFiles(t *testing.T) {
	reports := &metricsReporterFake{batch: []usage.FileUsage{{ObjectID: "obj-a", Name: "a.txt", Size: 10}}}
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
	reports := &metricsReporterFake{summary: usage.FileUsageSummary{TotalFiles: 1, TotalUploads: 2, TotalDownloads: 3, RecordCount: 1}}
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
	if payload.TotalFiles == nil || *payload.TotalFiles != 1 || payload.RecordCount == nil || *payload.RecordCount != 1 {
		t.Fatalf("unexpected scoped summary: %+v", payload)
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
		files:           []usage.FileUsage{{ObjectID: "scoped-1", Name: "f1", Size: 1, UploadCount: 2, DownloadCount: 3}},
		fileUsage:       map[string]usage.FileUsage{"other-1": {ObjectID: "other-1", Name: "f2", Size: 2}},
		scopedFileUsage: map[string]usage.FileUsage{"scoped-1": {ObjectID: "scoped-1", Name: "f1", Size: 1}},
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

func metricsTimePtr(value time.Time) *time.Time { return &value }

type metricsErrorPropagationReporter struct {
	usage.Reporter
	err error
}

func (r metricsErrorPropagationReporter) GetFileUsage(context.Context, string) (*usage.FileUsage, error) {
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
			app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
			app.Use(RequestIDHandler(nil))
			registerMetricsRoutes(app, metricsErrorPropagationReporter{err: test.source}, nil)

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
	registerMetricsRoutes(app, metricsErrorPropagationReporter{err: privateCause}, nil)

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
