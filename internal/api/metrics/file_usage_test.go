package metrics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/models"
	"github.com/calypr/syfon/internal/testutils"
	"github.com/gofiber/fiber/v3"
)

type metricsObjectReaderAdapter struct {
	db *testutils.MockDatabase
}

func (a metricsObjectReaderAdapter) ListObjectIDsByScope(ctx context.Context, organization, project, requiredMethod string) ([]string, error) {
	return a.db.ListObjectIDsByScope(ctx, organization, project)
}

func (a metricsObjectReaderAdapter) GetObject(ctx context.Context, id, requiredMethod string) (*models.InternalObject, error) {
	return a.db.GetObject(ctx, id)
}

func TestMetricsRoutes_ListAndSummary(t *testing.T) {
	now := time.Now().UTC()
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"sha-1": {Id: "sha-1", Name: common.Ptr("f1"), Size: 1},
			"sha-2": {Id: "sha-2", Name: common.Ptr("f2"), Size: 2},
		},
		Usage: map[string]models.FileUsage{
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
		},
	}

	app := fiber.New()
	RegisterMetricsRoutes(app, db)

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
	RegisterMetricsRoutes(app, &testutils.MockDatabase{})

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
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"obj-a": {Id: "obj-a", Name: common.Ptr("a.txt"), Size: 10},
			"obj-b": {Id: "obj-b", Name: common.Ptr("b.txt"), Size: 20},
			"obj-c": {Id: "obj-c", Name: common.Ptr("c.txt"), Size: 30},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-a": {"cbds": {"end_to_end_test"}},
			"obj-b": {"cbds": {"end_to_end_test"}},
			"obj-c": {"other": {"project"}},
		},
		Usage: map[string]models.FileUsage{
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
		},
	}
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
	RegisterMetricsRoutes(app, db)

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
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"scoped-1": {Id: "scoped-1", Name: common.Ptr("f1"), Size: 1},
			"other-1":  {Id: "other-1", Name: common.Ptr("f2"), Size: 2},
		},
		ObjectAuthz: map[string]map[string][]string{
			"scoped-1": {"cbds": {"end_to_end_test"}},
			"other-1":  {"other": {"other"}},
		},
		Usage: map[string]models.FileUsage{
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
		},
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		// Mock auth values from headers for testing
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
	RegisterMetricsRoutes(app, db)

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
	db := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"scoped-1": {Id: "scoped-1", Name: common.Ptr("f1"), Size: 1},
			"other-1":  {Id: "other-1", Name: common.Ptr("f2"), Size: 2},
		},
		ObjectAuthz: map[string]map[string][]string{
			"scoped-1": {"cbds": {"end_to_end_test"}},
			"other-1":  {"other": {"other"}},
		},
		Usage: map[string]models.FileUsage{
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
		},
	}
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
	RegisterMetricsRoutes(app, db)

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
	objects := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"obj-a": {Id: "obj-a", CreatedTime: now, UpdatedTime: &now},
			"obj-b": {Id: "obj-b", CreatedTime: now, UpdatedTime: &now},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-a": {"org1": {"p1"}},
			"obj-b": {"org2": {"p2"}},
		},
	}

	adapter := metricsObjectReaderAdapter{db: objects}

	inside, err := objectInScope(context.Background(), adapter, "obj-a", "org1", "p1")
	if err != nil || !inside {
		t.Fatalf("expected obj-a in scope org1/p1, inside=%v err=%v", inside, err)
	}
	inside, err = objectInScope(context.Background(), adapter, "obj-b", "org1", "p1")
	if err != nil {
		t.Fatalf("objectInScope error: %v", err)
	}
	if inside {
		t.Fatalf("expected obj-b outside org1/p1")
	}

	inside, err = objectInAnyScope(context.Background(), adapter, "obj-b", []metricsScope{{organization: "org1", project: "p1"}, {organization: "org2", project: "p2"}})
	if err != nil || !inside {
		t.Fatalf("expected obj-b in one of scopes, inside=%v err=%v", inside, err)
	}
}

func TestListMultiScopedFileUsage_DeduplicatesAcrossScopes(t *testing.T) {
	now := time.Now().UTC()
	objects := &testutils.MockDatabase{
		Objects: map[string]*drs.DrsObject{
			"obj-a": {Id: "obj-a", CreatedTime: now, UpdatedTime: &now},
		},
		ObjectAuthz: map[string]map[string][]string{
			"obj-a": {"org1": {"p1"}},
		},
		Usage: map[string]models.FileUsage{
			"obj-a": {ObjectID: "obj-a", UploadCount: 1, DownloadCount: 2},
		},
	}

	adapter := metricsObjectReaderAdapter{db: objects}
	usages, summary, err := listMultiScopedFileUsage(context.Background(), objects, adapter, []metricsScope{{organization: "org1", project: "p1"}, {organization: "org1", project: "p1"}}, 0, 0, nil)
	if err != nil {
		t.Fatalf("listMultiScopedFileUsage error: %v", err)
	}
	if len(usages) != 1 || usages[0].ObjectID != "obj-a" {
		t.Fatalf("expected one deduplicated usage record, got %+v", usages)
	}
	if summary.TotalFiles < 1 {
		t.Fatalf("expected summary total files > 0, got %+v", summary)
	}
}
