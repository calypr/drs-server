package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func TestMetricsRoutes_TransferAttribution(t *testing.T) {
	ingest := &metricsIngestFake{}
	reports := &metricsReporterFake{
		transferSummary:   metricsapi.TransferAttributionSummary{EventCount: metricsInt64(1), DownloadEventCount: metricsInt64(1), BytesDownloaded: metricsInt64(42)},
		transferBreakdown: []metricsapi.TransferAttributionBreakdown{{Key: metricsString("user@example.com"), BytesDownloaded: metricsInt64(42)}},
	}
	app := fiber.New()
	registerMetricsRoutes(app, reports, ingest)

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
		"object_key":"root/sha-1",
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
	event := ingest.events[0]
	if event.ProviderEventId != "event-download-1" || generatedString(event.AccessGrantId) != "grant-1" || generatedString(event.ObjectId) != "did-1" || generatedString(event.ObjectKey) != "root/sha-1" || generatedString(event.HttpMethod) != "GET" || event.HttpStatus == nil || *event.HttpStatus != 200 || event.RangeStart == nil || *event.RangeStart != 0 || event.RangeEnd == nil || *event.RangeEnd != 41 {
		t.Fatalf("unexpected provider transfer event: %+v", event)
	}

	summaryReq := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/summary?organization=calypr&project=proj-a&direction=download&from=2026-04-01T00:00:00Z&to=2026-04-30T00:00:00Z&allow_stale=true", nil)
	summaryResp, err := app.Test(summaryReq)
	if err != nil {
		t.Fatalf("summary request failed: %v", err)
	}
	summaryBody, _ := io.ReadAll(summaryResp.Body)
	if summaryResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", summaryResp.StatusCode, string(summaryBody))
	}
	var summary metricsapi.TransferAttributionSummary
	if err := json.Unmarshal(summaryBody, &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.EventCount == nil || *summary.EventCount != 1 || summary.DownloadEventCount == nil || *summary.DownloadEventCount != 1 || summary.BytesDownloaded == nil || *summary.BytesDownloaded != 42 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.Freshness == nil || summary.Freshness.IsStale == nil || *summary.Freshness.IsStale || summary.Freshness.MissingBuckets == nil || len(*summary.Freshness.MissingBuckets) != 0 || summary.Freshness.LatestCompletedSync != nil {
		t.Fatalf("unexpected transfer freshness: %+v", summary.Freshness)
	}
	wantFrom, _ := time.Parse(time.RFC3339, "2026-04-01T00:00:00Z")
	wantTo, _ := time.Parse(time.RFC3339, "2026-04-30T00:00:00Z")
	if summary.Freshness.RequiredFrom == nil || !summary.Freshness.RequiredFrom.Equal(wantFrom) || summary.Freshness.RequiredTo == nil || !summary.Freshness.RequiredTo.Equal(wantTo) {
		t.Fatalf("unexpected transfer freshness bounds: %+v", summary.Freshness)
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
		GroupBy string                                    `json:"group_by"`
		Data    []metricsapi.TransferAttributionBreakdown `json:"data"`
	}
	if err := json.Unmarshal(breakdownBody, &breakdown); err != nil {
		t.Fatalf("decode breakdown: %v", err)
	}
	if breakdown.GroupBy != "user" || len(breakdown.Data) != 1 || breakdown.Data[0].Key == nil || *breakdown.Data[0].Key != "user@example.com" || breakdown.Data[0].BytesDownloaded == nil || *breakdown.Data[0].BytesDownloaded != 42 {
		t.Fatalf("unexpected breakdown: %+v", breakdown)
	}
}

func TestProviderTransferHandlerCoversAuthAndDependencyErrors(t *testing.T) {
	valid := &metricsapi.RecordProviderTransferEventsJSONRequestBody{Events: []metricsapi.ProviderTransferEvent{{
		ProviderEventId: "event-1",
		Direction:       metricsapi.ProviderTransferDirection(usage.ProviderTransferDirectionDownload),
		Provider:        "s3",
		Bucket:          "bucket",
	}}}
	server := &metricsServer{ingestor: &metricsIngestFake{}}

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
	failing := &metricsServer{ingestor: &metricsIngestFake{err: wantErr}}
	_, err = failing.RecordProviderTransferEvents(context.Background(), metricsapi.RecordProviderTransferEventsRequestObject{Body: valid})
	if !errors.Is(err, wantErr) {
		t.Fatalf("dependency error = %v, want %v", err, wantErr)
	}
}

func TestTransferReportHandlersPropagateValidationAndDependencyErrors(t *testing.T) {
	wantErr := errors.New("transfer report unavailable")
	server := &metricsServer{reporter: &metricsReporterFake{transferSummaryErr: wantErr, transferBreakdownErr: wantErr}}
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
	server = &metricsServer{reporter: &metricsReporterFake{}}
	if _, err := server.GetTransferSummary(unauthorized, metricsapi.GetTransferSummaryRequestObject{}); err != nil {
		t.Fatalf("unauthorized summary error = %v", err)
	}
}

func TestMetricsRoutes_TransferAttributionAuthz(t *testing.T) {
	reports := &metricsReporterFake{
		transferSummary:   metricsapi.TransferAttributionSummary{BytesDownloaded: metricsInt64(141)},
		transferBreakdown: []metricsapi.TransferAttributionBreakdown{{Key: metricsString("user@example.com"), BytesDownloaded: metricsInt64(42)}},
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
			Data []metricsapi.TransferAttributionBreakdown `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Data) != 1 || resp.Data[0].BytesDownloaded == nil || *resp.Data[0].BytesDownloaded != 42 {
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
			Data []metricsapi.TransferAttributionBreakdown `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Data) != 1 || resp.Data[0].BytesDownloaded == nil || *resp.Data[0].BytesDownloaded != 42 {
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
		var summary metricsapi.TransferAttributionSummary
		if err := json.Unmarshal(body, &summary); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if summary.BytesDownloaded == nil || *summary.BytesDownloaded != 141 {
			t.Fatalf("expected global user bytes 141, got %+v", summary)
		}
	})
}
