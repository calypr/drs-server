package metrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type metricsOptimizedReportStore struct {
	*metricsReportFake
}

func (s metricsOptimizedReportStore) ListFileUsagePageByScope(context.Context, string, string, int, int, *time.Time) ([]usage.FileUsage, error) {
	return []usage.FileUsage{{ObjectID: "org/"}}, nil
}

func (s metricsOptimizedReportStore) ListFileUsagePageByResources(context.Context, []string, bool, int, int, *time.Time) ([]usage.FileUsage, error) {
	return nil, nil
}

func (s metricsOptimizedReportStore) GetFileUsageSummaryByScope(context.Context, string, string, *time.Time) (usage.FileUsageSummary, error) {
	return usage.FileUsageSummary{}, nil
}

func (s metricsOptimizedReportStore) GetFileUsageSummaryByResources(context.Context, []string, bool, *time.Time) (usage.FileUsageSummary, error) {
	return usage.FileUsageSummary{}, nil
}

func (s metricsOptimizedReportStore) GetProjectRecordSummaryByScope(context.Context, string, string) (usage.FileUsageSummary, error) {
	return usage.FileUsageSummary{}, nil
}

func TestMetricsRoutes_ListAndSummary(t *testing.T) {
	now := time.Now().UTC()
	objectReader := newMetricsObjectReader(map[string]*objects.Record{
		"sha-1": {Id: "sha-1", Name: metricsStringPtr("f1"), Size: 1},
		"sha-2": {Id: "sha-2", Name: metricsStringPtr("f2"), Size: 2},
	}, nil)
	state := &metricsTransferState{}
	ingest := &metricsIngestFake{state: state}
	reports := newMetricsReport(objectReader, map[string]usage.FileUsage{
		"sha-1": {
			ObjectID:      "sha-1",
			Name:          "f1",
			Size:          1,
			UploadCount:   1,
			DownloadCount: 3,
			LastDownloadTime: func() *time.Time {
				t := now.AddDate(0, 0, -10)
				return &t
			}(),
		},
		"sha-2": {
			ObjectID:      "sha-2",
			Name:          "f2",
			Size:          2,
			UploadCount:   1,
			DownloadCount: 0,
		},
	}, state)

	app := fiber.New()
	registerMetricsRoutesForTest(app, ingest, reports, objectReader)

	t.Run("list", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?limit=10&offset=0&inactive_days=365", nil)
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var resp map[string]any
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := resp["data"]; !ok {
			t.Fatalf("expected data field in response: %v", resp)
		}
	})

	t.Run("summary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?inactive_days=365", nil)
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var resp metricsapi.FileUsageSummary
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.TotalFiles == nil || *resp.TotalFiles != 2 {
			t.Fatalf("expected total files 2, got %+v", resp.TotalFiles)
		}
	})
}

func TestMetricsRoutes_GetNotFoundAndValidation(t *testing.T) {
	app := fiber.New()
	objectReader := newMetricsObjectReader(nil, nil)
	state := &metricsTransferState{}
	registerMetricsRoutesForTest(app, &metricsIngestFake{state: state}, newMetricsReport(objectReader, nil, state), objectReader)

	req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/missing", nil)
	httpResp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d body=%s", httpResp.StatusCode, string(body))
	}

	req2 := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?limit=0", nil)
	httpResp2, err := app.Test(req2)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body2, _ := io.ReadAll(httpResp2.Body)
	if httpResp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", httpResp2.StatusCode, string(body2))
	}
}

func TestMetricsRoutes_BulkFiles(t *testing.T) {
	oldDownload := time.Now().UTC().AddDate(0, 0, -40)
	recentDownload := time.Now().UTC().AddDate(0, 0, -2)
	objectReader := newMetricsObjectReader(map[string]*objects.Record{
		"obj-a": {Id: "obj-a", Name: metricsStringPtr("a.txt"), Size: 10},
		"obj-b": {Id: "obj-b", Name: metricsStringPtr("b.txt"), Size: 20},
		"obj-c": {Id: "obj-c", Name: metricsStringPtr("c.txt"), Size: 30},
	}, map[string]map[string][]string{
		"obj-a": {"cbds": {"end_to_end_test"}},
		"obj-b": {"cbds": {"end_to_end_test"}},
		"obj-c": {"other": {"project"}},
	})
	state := &metricsTransferState{}
	ingest := &metricsIngestFake{state: state}
	reports := newMetricsReport(objectReader, map[string]usage.FileUsage{
		"obj-a": {
			ObjectID:         "obj-a",
			Name:             "a.txt",
			Size:             10,
			DownloadCount:    3,
			LastDownloadTime: &oldDownload,
		},
		"obj-b": {
			ObjectID:         "obj-b",
			Name:             "b.txt",
			Size:             20,
			DownloadCount:    7,
			LastDownloadTime: &recentDownload,
		},
		"obj-c": {
			ObjectID:      "obj-c",
			Name:          "c.txt",
			Size:          30,
			DownloadCount: 11,
		},
	}, state)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if mode := c.Get("X-Test-Auth-Mode"); mode != "" {
			var privs map[string]map[string]bool
			if privsJSON := c.Get("X-Test-Privileges"); privsJSON != "" {
				_ = json.Unmarshal([]byte(privsJSON), &privs)
			}
			headerRaw := c.Get("X-Test-Auth-Header")
			ctx := metricsTestContext(c.Context(), mode, headerRaw != "", headerRaw == "true", privs)
			c.SetContext(ctx)
		}
		return c.Next()
	})
	registerMetricsRoutesForTest(app, ingest, reports, objectReader)

	req := httptest.NewRequest(http.MethodPost, "/index/v1/metrics/files/bulk?organization=cbds&project=end_to_end_test", strings.NewReader(`{"object_ids":["obj-a","obj-b","obj-c","missing","obj-a"],"inactive_days":30}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Test-Auth-Mode", "gen3")
	req.Header.Set("X-Test-Auth-Header", "true")
	privs, _ := json.Marshal(map[string]map[string]bool{
		"/programs/cbds/projects/end_to_end_test": {"read": true},
	})
	req.Header.Set("X-Test-Privileges", string(privs))
	httpResp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, _ := io.ReadAll(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
	}
	var resp metricsapi.MetricsListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data == nil || len(*resp.Data) != 1 {
		t.Fatalf("expected only inactive scoped object, got %s", string(body))
	}
	if got := *(*resp.Data)[0].ObjectId; got != "obj-a" {
		t.Fatalf("expected obj-a, got %q", got)
	}
}

func TestMetricsSummaryAuthzAndScope(t *testing.T) {
	objectReader := newMetricsObjectReader(map[string]*objects.Record{
		"scoped-1": {Id: "scoped-1", Name: metricsStringPtr("f1"), Size: 1},
		"other-1":  {Id: "other-1", Name: metricsStringPtr("f2"), Size: 2},
	}, map[string]map[string][]string{
		"scoped-1": {"cbds": {"end_to_end_test"}},
		"other-1":  {"other": {"other"}},
	})
	state := &metricsTransferState{}
	ingest := &metricsIngestFake{state: state}
	reports := newMetricsReport(objectReader, map[string]usage.FileUsage{
		"scoped-1": {
			ObjectID:      "scoped-1",
			UploadCount:   2,
			DownloadCount: 3,
		},
		"other-1": {
			ObjectID:      "other-1",
			UploadCount:   7,
			DownloadCount: 11,
		},
	}, state)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if mode := c.Get("X-Test-Auth-Mode"); mode != "" {
			var privs map[string]map[string]bool
			if privsJSON := c.Get("X-Test-Privileges"); privsJSON != "" {
				_ = json.Unmarshal([]byte(privsJSON), &privs)
			}
			headerRaw := c.Get("X-Test-Auth-Header")
			ctx := metricsTestContext(c.Context(), mode, headerRaw != "", headerRaw == "true", privs)
			c.SetContext(ctx)
		}
		return c.Next()
	})
	registerMetricsRoutesForTest(app, ingest, reports, objectReader)

	t.Run("scope reader can access scoped summary", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?organization=cbds&project=end_to_end_test", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		privs, _ := json.Marshal(map[string]map[string]bool{
			"/programs/cbds/projects/end_to_end_test": {"read": true},
		})
		req.Header.Set("X-Test-Privileges", string(privs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var resp metricsapi.FileUsageSummary
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.TotalFiles == nil || resp.TotalUploads == nil || resp.TotalDownloads == nil || *resp.TotalFiles != 1 || *resp.TotalUploads != 2 || *resp.TotalDownloads != 3 {
			t.Fatalf("unexpected scoped summary: %+v", resp)
		}
		if resp.RecordCount == nil || *resp.RecordCount != 1 {
			t.Fatalf("expected exact scoped record count, got %+v", resp.RecordCount)
		}
	})

	t.Run("missing auth header returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary?organization=cbds&project=end_to_end_test", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "false")
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d body=%s", httpResp.StatusCode, string(body))
		}
	})

	t.Run("program reader can access global summary via /programs read", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/summary", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		privs, _ := json.Marshal(map[string]map[string]bool{
			"/programs": {"read": true},
		})
		req.Header.Set("X-Test-Privileges", string(privs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
	})
}

func TestMetricsFilesAuthzAndScope(t *testing.T) {
	objectReader := newMetricsObjectReader(map[string]*objects.Record{
		"scoped-1": {Id: "scoped-1", Name: metricsStringPtr("f1"), Size: 1},
		"other-1":  {Id: "other-1", Name: metricsStringPtr("f2"), Size: 2},
	}, map[string]map[string][]string{
		"scoped-1": {"cbds": {"end_to_end_test"}},
		"other-1":  {"other": {"other"}},
	})
	state := &metricsTransferState{}
	ingest := &metricsIngestFake{state: state}
	reports := newMetricsReport(objectReader, map[string]usage.FileUsage{
		"scoped-1": {
			ObjectID:      "scoped-1",
			Name:          "f1",
			Size:          1,
			UploadCount:   2,
			DownloadCount: 3,
		},
		"other-1": {
			ObjectID:      "other-1",
			Name:          "f2",
			Size:          2,
			UploadCount:   7,
			DownloadCount: 11,
		},
	}, state)
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if mode := c.Get("X-Test-Auth-Mode"); mode != "" {
			var privs map[string]map[string]bool
			if privsJSON := c.Get("X-Test-Privileges"); privsJSON != "" {
				_ = json.Unmarshal([]byte(privsJSON), &privs)
			}
			ctx := metricsTestContext(c.Context(), mode, c.Get("X-Test-Auth-Header") == "true", c.Get("X-Test-Auth-Header") == "true", privs)
			c.SetContext(ctx)
		}
		return c.Next()
	})
	registerMetricsRoutesForTest(app, ingest, reports, objectReader)

	t.Run("scoped list returns only scoped objects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files?organization=cbds&project=end_to_end_test&limit=10&offset=0", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		privs, _ := json.Marshal(map[string]map[string]bool{
			"/programs/cbds/projects/end_to_end_test": {"read": true},
		})
		req.Header.Set("X-Test-Privileges", string(privs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
		var resp map[string]any
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		data, ok := resp["data"].([]any)
		if !ok {
			t.Fatalf("missing data field: %v", resp)
		}
		if len(data) != 1 {
			t.Fatalf("expected 1 scoped item, got %d payload=%v", len(data), resp)
		}
		first, ok := data[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected data item type: %T", data[0])
		}
		if first["object_id"] != "scoped-1" {
			t.Fatalf("expected scoped-1, got %v", first["object_id"])
		}
	})

	t.Run("scoped object lookup outside scope returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/other-1?organization=cbds&project=end_to_end_test", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		privs, _ := json.Marshal(map[string]map[string]bool{
			"/programs/cbds/projects/end_to_end_test": {"read": true},
		})
		req.Header.Set("X-Test-Privileges", string(privs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d body=%s", httpResp.StatusCode, string(body))
		}
	})

	t.Run("global object lookup allowed via /programs read", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/index/v1/metrics/files/other-1", nil)
		req.Header.Set("X-Test-Auth-Mode", "gen3")
		req.Header.Set("X-Test-Auth-Header", "true")
		privs, _ := json.Marshal(map[string]map[string]bool{
			"/programs": {"read": true},
		})
		req.Header.Set("X-Test-Privileges", string(privs))
		httpResp, err := app.Test(req)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", httpResp.StatusCode, string(body))
		}
	})
}

func TestFileUsageScopeHelpers(t *testing.T) {
	now := time.Now().UTC()
	objectReader := newMetricsObjectReader(map[string]*objects.Record{
		"obj-a": {Id: "obj-a", CreatedTime: now, UpdatedTime: &now},
		"obj-b": {Id: "obj-b", CreatedTime: now, UpdatedTime: &now},
	}, map[string]map[string][]string{
		"obj-a": {"org1": {"p1"}},
		"obj-b": {"org2": {"p2"}},
	})
	state := &metricsTransferState{}
	ingest := &metricsIngestFake{state: state}
	reports := newMetricsReport(objectReader, nil, state)

	service := usage.NewService(usage.Dependencies{
		Reports: reports,
		Objects: objectReader,
	})
	server := NewMetricsServer(service.Reports(), ingest)
	access := metricsAccess{organization: "org1", project: "p1"}

	inside, err := server.objectInScope(context.Background(), "obj-a", access)
	if err != nil || !inside {
		t.Fatalf("expected obj-a in scope org1/p1, inside=%v err=%v", inside, err)
	}
	inside, err = server.objectInScope(context.Background(), "obj-b", access)
	if err != nil {
		t.Fatalf("objectInScope error: %v", err)
	}
	if inside {
		t.Fatalf("expected obj-b outside org1/p1")
	}
}

func TestFileUsageScopeHelpersUseUnpagedObjectMembership(t *testing.T) {
	ids := make([]string, 1001)
	records := make(map[string]*objects.Record, len(ids))
	authorizations := make(map[string]map[string][]string, len(ids))
	fileUsage := make(map[string]usage.FileUsage, len(ids))
	for i := range ids {
		ids[i] = fmt.Sprintf("object-%04d", i)
		records[ids[i]] = &objects.Record{Id: objects.RecordID(ids[i])}
		authorizations[ids[i]] = map[string][]string{"org": {"project"}}
		fileUsage[ids[i]] = usage.FileUsage{ObjectID: ids[i]}
	}
	objectReader := newMetricsObjectReader(records, authorizations)
	reports := newMetricsReport(objectReader, fileUsage, &metricsTransferState{})
	service := usage.NewService(usage.Dependencies{
		Reports: reports,
		Objects: objectReader,
	})
	server := NewMetricsServer(service.Reports(), nil)
	readable, err := server.readableBulkObjectIDs(context.Background(), metricsAccess{organization: "org", project: "project"}, []string{"object-1000"})
	if err != nil {
		t.Fatalf("readableBulkObjectIDs error: %v", err)
	}
	if len(readable) != 1 || readable[0] != "object-1000" {
		t.Fatalf("readable IDs = %v, want [object-1000]", readable)
	}

	orgReader := newMetricsObjectReader(
		map[string]*objects.Record{"project-object": {Id: "project-object"}},
		map[string]map[string][]string{
			"project-object": {"org": {"project"}},
		},
	)
	orgReports := newMetricsReport(orgReader, nil, &metricsTransferState{})
	optimized := usage.NewService(usage.Dependencies{
		Reports: metricsOptimizedReportStore{metricsReportFake: orgReports},
		Objects: orgReader,
	})
	orgServer := NewMetricsServer(optimized.Reports(), nil)
	inside, err := orgServer.objectInScope(context.Background(), "project-object", metricsAccess{organization: "org"})
	if err != nil {
		t.Fatalf("organization-wide objectInScope error: %v", err)
	}
	if !inside {
		t.Fatal("organization-wide access did not match project object")
	}
}

func TestListMultiScopedFileUsage_DeduplicatesAcrossScopes(t *testing.T) {
	now := time.Now().UTC()
	objectReader := newMetricsObjectReader(map[string]*objects.Record{
		"obj-a": {Id: "obj-a", CreatedTime: now, UpdatedTime: &now},
	}, map[string]map[string][]string{
		"obj-a": {"org1": {"p1"}},
	})
	state := &metricsTransferState{}
	reports := newMetricsReport(objectReader, map[string]usage.FileUsage{
		"obj-a": {ObjectID: "obj-a", UploadCount: 1, DownloadCount: 2},
	}, state)

	service := usage.NewService(usage.Dependencies{
		Reports: reports,
		Objects: objectReader,
	})
	usages, err := service.ListFileUsage(context.Background(), usage.FileUsageQuery{
		Scope: usage.ScopeQuery{
			Scopes: []usage.Scope{{Organization: "org1", Project: "p1"}, {Organization: "org1", Project: "p1"}},
		},
	})
	if err != nil {
		t.Fatalf("ListFileUsage error: %v", err)
	}
	if len(usages) != 1 || usages[0].ObjectID != "obj-a" {
		t.Fatalf("expected one deduplicated usage record, got %+v", usages)
	}
}
