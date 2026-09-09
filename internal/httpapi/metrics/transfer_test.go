package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type providerErrorIngestor struct {
	err error
}

type transferErrorReporter struct {
	usage.Reporter
	freshnessErr error
	summaryErr   error
	breakdownErr error
}

func (r transferErrorReporter) GetTransferFreshness(context.Context, usage.Filter) (usage.Freshness, error) {
	return usage.Freshness{}, r.freshnessErr
}

func (r transferErrorReporter) GetTransferAttributionSummary(context.Context, usage.TransferSummaryQuery) (usage.Summary, error) {
	return usage.Summary{}, r.summaryErr
}

func (r transferErrorReporter) GetTransferAttributionBreakdown(context.Context, usage.TransferBreakdownQuery) ([]usage.Breakdown, error) {
	return nil, r.breakdownErr
}

func (i providerErrorIngestor) RecordProviderTransferEvents(context.Context, []usage.ProviderEvent) error {
	return i.err
}

func TestMetricsRoutes_TransferAttribution(t *testing.T) {
	ingest := &metricsIngestFake{}
	reports := &metricsReporterFake{
		transferSummary:   usage.Summary{EventCount: 1, DownloadEventCount: 1, BytesDownloaded: 42},
		transferBreakdown: []usage.Breakdown{{Key: "user@example.com", BytesDownloaded: 42}},
	}
	app := fiber.New()
	registerMetricsRoutesForTest(app, reports, ingest)

	body := `{"events":[{
		"provider_event_id":"event-download-1",
		"access_grant_id":"grant-1",
		"direction":"download",
		"event_time":"2026-04-26T20:00:00Z",
		"request_id":"request-1",
		"provider_request_id":"provider-request-1",
		"object_id":"did-1",
		"sha256":"sha-1",
		"object_size":42,
		"organization":"calypr",
		"project":"proj-a",
		"access_id":"s3",
		"provider":"s3",
		"bucket":"bucket-a",
		"storage_url":"s3://bucket-a/root/sha-1",
		"range_start":0,
		"range_end":41,
		"bytes_transferred":42,
		"http_method":"GET",
		"http_status":200
	}]}`
	req := httptest.NewRequest(http.MethodPost, "/index/v1/metrics/provider-transfer-events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	httpResp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	respBody, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", httpResp.StatusCode, string(respBody))
	}
	if len(ingest.events) != 1 {
		t.Fatalf("expected one provider transfer event, got %+v", ingest.events)
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/summary?organization=calypr&project=proj-a&direction=download&allow_stale=true", nil)
	summaryResp, err := app.Test(summaryReq)
	if err != nil {
		t.Fatalf("summary request failed: %v", err)
	}
	summaryBody, _ := io.ReadAll(summaryResp.Body)
	if summaryResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", summaryResp.StatusCode, string(summaryBody))
	}
	var summary usage.Summary
	if err := json.Unmarshal(summaryBody, &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.EventCount != 1 || summary.DownloadEventCount != 1 || summary.BytesDownloaded != 42 {
		t.Fatalf("unexpected summary: %+v", summary)
	}

	breakdownReq := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/breakdown?group_by=user&user=user@example.com&allow_stale=true", nil)
	breakdownResp, err := app.Test(breakdownReq)
	if err != nil {
		t.Fatalf("breakdown request failed: %v", err)
	}
	breakdownBody, _ := io.ReadAll(breakdownResp.Body)
	if breakdownResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", breakdownResp.StatusCode, string(breakdownBody))
	}
	var breakdown struct {
		GroupBy string            `json:"group_by"`
		Data    []usage.Breakdown `json:"data"`
	}
	if err := json.Unmarshal(breakdownBody, &breakdown); err != nil {
		t.Fatalf("decode breakdown: %v", err)
	}
	if breakdown.GroupBy != "user" || len(breakdown.Data) != 1 || breakdown.Data[0].Key != "user@example.com" || breakdown.Data[0].BytesDownloaded != 42 {
		t.Fatalf("unexpected breakdown: %+v", breakdown)
	}
}

func TestProviderTransferPayloadValidation(t *testing.T) {
	base := providerTransferPayload{ProviderEventID: "event-1", Direction: usage.ProviderTransferDirectionDownload, Provider: "s3", Bucket: "bucket"}
	for _, tc := range []struct {
		name string
		edit func(*providerTransferPayload)
	}{
		{name: "direction", edit: func(v *providerTransferPayload) { v.Direction = "copy" }},
		{name: "required field", edit: func(v *providerTransferPayload) { v.ProviderEventID = "" }},
		{name: "negative bytes", edit: func(v *providerTransferPayload) { v.BytesTransferred = -1 }},
		{name: "reconciliation status", edit: func(v *providerTransferPayload) { v.ReconciliationStatus = "unknown" }},
		{name: "event time", edit: func(v *providerTransferPayload) { v.EventTime = "not-a-time" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := base
			tc.edit(&value)
			if _, err := providerTransferPayloadToUsage(value); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	value := base
	value.ObjectKey = " /root/object "
	value.HTTPMethod = " get "
	converted, err := providerTransferPayloadToUsage(value)
	if err != nil || converted.ObjectKey != "root/object" || converted.HTTPMethod != "GET" {
		t.Fatalf("normalized provider event = %+v, err=%v", converted, err)
	}
}

func TestProviderTransferHandlerCoversAuthAndDependencyErrors(t *testing.T) {
	valid := &metricsapi.RecordProviderTransferEventsJSONRequestBody{Events: []metricsapi.ProviderTransferEvent{{
		ProviderEventId: "event-1",
		Direction:       metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
		Provider:        "s3",
		Bucket:          "bucket",
	}}}
	server := NewMetricsServer(nil, &metricsIngestFake{})

	response, err := server.RecordProviderTransferEvents(metricsTestContext(context.Background(), "gen3", false, false, nil), metricsapi.RecordProviderTransferEventsRequestObject{Body: valid})
	if err != nil {
		t.Fatalf("missing auth error: %v", err)
	}
	if _, ok := response.(metricsapi.RecordProviderTransferEvents401JSONResponse); !ok {
		t.Fatalf("missing auth response = %T", response)
	}
	response, err = server.RecordProviderTransferEvents(metricsTestContext(context.Background(), "gen3", true, true, nil), metricsapi.RecordProviderTransferEventsRequestObject{})
	if err != nil {
		t.Fatalf("empty body auth error: %v", err)
	}
	if _, ok := response.(metricsapi.RecordProviderTransferEvents403JSONResponse); !ok {
		t.Fatalf("empty body response = %T", response)
	}
	response, err = server.RecordProviderTransferEvents(context.Background(), metricsapi.RecordProviderTransferEventsRequestObject{Body: &metricsapi.RecordProviderTransferEventsJSONRequestBody{Events: []metricsapi.ProviderTransferEvent{{ProviderEventId: "bad", Direction: "copy", Provider: "s3", Bucket: "bucket"}}}})
	if err != nil {
		t.Fatalf("invalid event error: %v", err)
	}
	if _, ok := response.(metricsapi.RecordProviderTransferEvents400JSONResponse); !ok {
		t.Fatalf("invalid event response = %T", response)
	}
	wantErr := errors.New("ingest failed")
	failing := NewMetricsServer(nil, providerErrorIngestor{err: wantErr})
	_, err = failing.RecordProviderTransferEvents(context.Background(), metricsapi.RecordProviderTransferEventsRequestObject{Body: valid})
	if !errors.Is(err, wantErr) {
		t.Fatalf("dependency error = %v, want %v", err, wantErr)
	}
}

func TestTransferReportHandlersPropagateValidationAndDependencyErrors(t *testing.T) {
	wantErr := errors.New("transfer report unavailable")
	server := NewMetricsServer(transferErrorReporter{freshnessErr: wantErr}, nil)
	if _, err := server.GetTransferSummary(context.Background(), metricsapi.GetTransferSummaryRequestObject{}); !errors.Is(err, wantErr) {
		t.Fatalf("summary freshness error = %v", err)
	}
	if _, err := server.GetTransferBreakdown(context.Background(), metricsapi.GetTransferBreakdownRequestObject{}); !errors.Is(err, wantErr) {
		t.Fatalf("breakdown freshness error = %v", err)
	}
	server = NewMetricsServer(transferErrorReporter{summaryErr: wantErr, breakdownErr: wantErr}, nil)
	if _, err := server.GetTransferSummary(context.Background(), metricsapi.GetTransferSummaryRequestObject{}); !errors.Is(err, wantErr) {
		t.Fatalf("summary dependency error = %v", err)
	}
	groupBy := metricsapi.GetTransferBreakdownParamsGroupBy("invalid")
	response, err := server.GetTransferBreakdown(context.Background(), metricsapi.GetTransferBreakdownRequestObject{Params: metricsapi.GetTransferBreakdownParams{GroupBy: &groupBy}})
	if err != nil {
		t.Fatalf("invalid breakdown group error = %v", err)
	}
	if _, ok := response.(metricsapi.GetTransferBreakdown400JSONResponse); !ok {
		t.Fatalf("invalid breakdown response = %T", response)
	}
	groupBy = metricsapi.GetTransferBreakdownParamsGroupBy("scope")
	if _, err := server.GetTransferBreakdown(context.Background(), metricsapi.GetTransferBreakdownRequestObject{Params: metricsapi.GetTransferBreakdownParams{GroupBy: &groupBy}}); !errors.Is(err, wantErr) {
		t.Fatalf("breakdown dependency error = %v", err)
	}
	unauthorized := metricsTestContext(context.Background(), "gen3", false, false, nil)
	server = NewMetricsServer(transferErrorReporter{}, nil)
	if _, err := server.GetTransferSummary(unauthorized, metricsapi.GetTransferSummaryRequestObject{}); err != nil {
		t.Fatalf("unauthorized summary error = %v", err)
	}
}

func TestTransferReportAuthResponsesCoverStatusVariants(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest} {
		if got := getTransferSummaryAuthResponse(ctx, status); got == nil {
			t.Fatalf("summary auth response for %d is nil", status)
		}
		if got := getTransferBreakdownAuthResponse(ctx, status); got == nil {
			t.Fatalf("breakdown auth response for %d is nil", status)
		}
	}
	if generatedTime(nil) != nil {
		t.Fatal("generatedTime(nil) returned a value")
	}
}

func TestMetricsRoutes_TransferAttributionAuthz(t *testing.T) {
	reports := &metricsReporterFake{
		transferSummary:   usage.Summary{BytesDownloaded: 141},
		transferBreakdown: []usage.Breakdown{{Key: "user@example.com", BytesDownloaded: 42}},
	}
	app := newMetricsTestApp(reports, &metricsIngestFake{})

	projectPrivs, _ := json.Marshal(map[string]map[string]bool{
		"/programs/calypr/projects/proj-a": {"read": true},
	})
	globalPrivs, _ := json.Marshal(map[string]map[string]bool{
		"/programs": {"read": true},
	})

	t.Run("project reader can query user metrics inside project", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/breakdown?organization=calypr&project=proj-a&group_by=user&user=user@example.com&allow_stale=true", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		req.Header.Set("X-Test-Privileges", string(projectPrivs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var resp struct {
			Data []usage.Breakdown `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Data) != 1 || resp.Data[0].BytesDownloaded != 42 {
			t.Fatalf("expected only proj-a bytes, got %+v", resp.Data)
		}
	})

	t.Run("project reader cannot query another project", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/summary?organization=calypr&project=proj-b&user=user@example.com&allow_stale=true", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		req.Header.Set("X-Test-Privileges", string(projectPrivs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		if httpResp.StatusCode != http.StatusForbidden {
			body, _ := io.ReadAll(httpResp.Body)
			t.Fatalf("expected 403, got %d body=%s", httpResp.StatusCode, string(body))
		}
	})

	t.Run("project reader can query aggregate metrics for readable scopes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/breakdown?group_by=user&user=user@example.com&allow_stale=true", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		req.Header.Set("X-Test-Privileges", string(projectPrivs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var resp struct {
			Data []usage.Breakdown `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Data) != 1 || resp.Data[0].BytesDownloaded != 42 {
			t.Fatalf("expected aggregate to include only readable scope bytes, got %+v", resp.Data)
		}
	})

	t.Run("global reader can query user globally", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/summary?user=user@example.com&allow_stale=true", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		req.Header.Set("X-Test-Privileges", string(globalPrivs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var summary usage.Summary
		if err := json.Unmarshal(body, &summary); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if summary.BytesDownloaded != 141 {
			t.Fatalf("expected global user bytes 141, got %+v", summary)
		}
	})
}

func TestMetricsRoutes_NoLegacyDownloadAttributionRoutes(t *testing.T) {
	app := fiber.New()
	registerMetricsRoutesForTest(app, &metricsReporterFake{}, &metricsIngestFake{})

	for _, tc := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/index/v1/metrics/download-events"},
		{method: http.MethodPost, path: "/index/v1/metrics/transfer-events"},
		{method: http.MethodGet, path: "/index/v1/metrics/downloads/summary"},
		{method: http.MethodGet, path: "/index/v1/metrics/downloads/breakdown"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("%s %s failed: %v", tc.method, tc.path, err)
		}
		if httpResp.StatusCode != http.StatusNotFound {
			body, _ := io.ReadAll(httpResp.Body)
			t.Fatalf("expected %s %s to be gone with 404, got %d body=%s", tc.method, tc.path, httpResp.StatusCode, string(body))
		}
	}
}
