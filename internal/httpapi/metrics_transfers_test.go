package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/client/apierror"
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

func TestMetricsRoutes_ProviderTransferBoundaryErrors(t *testing.T) {
	const validBody = `{"events":[{"provider_event_id":"event-1","direction":"download","provider":"s3","bucket":"bucket","organization":"org","project":"project"}]}`

	tests := []struct {
		name        string
		body        string
		mode        string
		authHeader  string
		ingestError error
		requestID   string
		wantStatus  int
	}{
		{name: "empty body", wantStatus: http.StatusBadRequest},
		{name: "malformed body", body: `{"events":`, wantStatus: http.StatusBadRequest},
		{name: "missing auth", body: validBody, mode: "gen3", wantStatus: http.StatusUnauthorized},
		{name: "authorization denied", body: validBody, mode: "gen3", authHeader: "true", wantStatus: http.StatusForbidden},
		{name: "invalid normalized event", body: `{"events":[{"provider_event_id":"event-1","direction":"copy","provider":"s3","bucket":"bucket"}]}`, wantStatus: http.StatusBadRequest},
		{name: "ingestor failure", body: validBody, ingestError: errors.New("ingest failed"), requestID: "metrics-ingest", wantStatus: http.StatusInternalServerError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newMetricsTestApp(&metricsReporterFake{}, &metricsIngestFake{err: test.ingestError})
			req := httptest.NewRequest(http.MethodPost, "/index/v1/metrics/provider-transfer-events", strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			setMetricsAuthHeaders(req, test.mode, test.authHeader == "true", nil)
			if test.requestID != "" {
				req.Header.Set("X-Request-Id", test.requestID)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != test.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", test.wantStatus, resp.StatusCode, body)
			}
			if test.requestID != "" {
				decoded := apierror.FromResponse(resp, body)
				if decoded.Status != test.wantStatus || decoded.RequestID != test.requestID {
					t.Fatalf("unexpected error envelope: %+v body=%s", decoded, body)
				}
				if got := resp.Header.Get("X-Request-Id"); got != test.requestID {
					t.Fatalf("expected request ID %q, got %q", test.requestID, got)
				}
			}
		})
	}
}

func TestMetricsRoutes_TransferReportBoundaryErrors(t *testing.T) {
	assertServerError := func(t *testing.T, app *fiber.App, req *http.Request, requestID string) {
		t.Helper()
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d body=%s", resp.StatusCode, body)
		}
		decoded := apierror.FromResponse(resp, body)
		if decoded.Status != http.StatusInternalServerError || decoded.RequestID != requestID || decoded.Message != http.StatusText(http.StatusInternalServerError) {
			t.Fatalf("unexpected error envelope: %+v body=%s", decoded, body)
		}
		if got := resp.Header.Get("X-Request-Id"); got != requestID {
			t.Fatalf("expected request ID %q, got %q", requestID, got)
		}
	}

	for _, test := range []struct {
		name, path, requestID string
		reporter              *metricsReporterFake
	}{
		{name: "summary dependency failure", path: "/index/v1/metrics/transfers/summary", requestID: "metrics-summary", reporter: &metricsReporterFake{transferSummaryErr: errors.New("summary unavailable")}},
		{name: "breakdown dependency failure", path: "/index/v1/metrics/transfers/breakdown?group_by=scope", requestID: "metrics-breakdown", reporter: &metricsReporterFake{transferBreakdownErr: errors.New("breakdown unavailable")}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			app := newMetricsTestApp(test.reporter, &metricsIngestFake{})
			req := httptest.NewRequest(http.MethodGet, test.path, nil)
			req.Header.Set("X-Request-Id", test.requestID)
			assertServerError(t, app, req, test.requestID)
		})
	}

	t.Run("invalid breakdown group does not query reporter", func(t *testing.T) {
		called := false
		reporter := &metricsReporterFake{transferBreakdownFn: func(usage.TransferBreakdownQuery) ([]metricsapi.TransferAttributionBreakdown, error) {
			called = true
			return nil, errors.New("reporter should not run")
		}}
		app := newMetricsTestApp(reporter, &metricsIngestFake{})
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/transfers/breakdown?group_by=invalid", nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 400, got %d body=%s", resp.StatusCode, body)
		}
		if called {
			t.Fatal("reporter was called for invalid breakdown group")
		}
	})

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
