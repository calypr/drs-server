package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func metricsString(value string) *string { return &value }

func metricsInt64(value int64) *int64 { return &value }

func metricsTestContext(base context.Context, mode string, headerSet bool, headerValue bool, privileges map[string]map[string]bool) context.Context {
	session := access.NewSession(mode)
	if headerSet {
		session.AuthHeaderPresent = headerValue
	}
	session.AuthzEnforced = mode == "gen3" || mode == "local"
	session.SetAuthorizations(nil, privileges, session.AuthzEnforced)
	return access.WithSession(base, session)
}

func newMetricsTestApp(reporter usage.Reporter, ingest usage.ProviderEventRecorder) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		mode := c.Get("X-Test-Auth-Mode")
		if mode == "" {
			return c.Next()
		}
		var privileges map[string]map[string]bool
		if raw := c.Get("X-Test-Privileges"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &privileges)
		}
		header := c.Get("X-Test-Auth-Header")
		c.SetContext(metricsTestContext(c.Context(), mode, header != "", header == "true", privileges))
		return c.Next()
	})
	registerMetricsRoutes(app, reporter, ingest)
	return app
}

func setMetricsAuthHeaders(request *http.Request, mode string, header bool, privileges map[string]map[string]bool) {
	request.Header.Set("X-Test-Auth-Mode", mode)
	request.Header.Set("X-Test-Auth-Header", fmt.Sprintf("%t", header))
	if privileges != nil {
		encoded, _ := json.Marshal(privileges)
		request.Header.Set("X-Test-Privileges", string(encoded))
	}
}

type metricsIngestFake struct {
	events []metricsapi.ProviderTransferEvent
	err    error
}

func (f *metricsIngestFake) RecordProviderTransferEvents(_ context.Context, events []metricsapi.ProviderTransferEvent) error {
	if f.err != nil {
		return f.err
	}
	f.events = append(f.events, events...)
	return nil
}

type metricsReporterFake struct {
	files                []metricsapi.FileUsage
	fileUsage            map[string]metricsapi.FileUsage
	scopedFileUsage      map[string]metricsapi.FileUsage
	batch                []metricsapi.FileUsage
	summary              metricsapi.FileUsageSummary
	transferSummary      metricsapi.TransferAttributionSummary
	transferBreakdown    []metricsapi.TransferAttributionBreakdown
	getFileUsageErr      error
	listFileUsageErr     error
	batchErr             error
	summaryErr           error
	scopedFileUsageErr   error
	transferSummaryErr   error
	transferBreakdownErr error
	transferSummaryFn    func(usage.TransferSummaryQuery) (metricsapi.TransferAttributionSummary, error)
	transferBreakdownFn  func(usage.TransferBreakdownQuery) ([]metricsapi.TransferAttributionBreakdown, error)
}

func (f *metricsReporterFake) GetFileUsage(_ context.Context, objectID string) (*metricsapi.FileUsage, error) {
	if f.getFileUsageErr != nil {
		return nil, f.getFileUsageErr
	}
	item, ok := f.fileUsage[objectID]
	if !ok {
		return nil, fmt.Errorf("%w: file usage not found", errorapi.ErrNotFound)
	}
	return &item, nil
}

func (f *metricsReporterFake) ListFileUsageBatch(_ context.Context, query usage.FileUsageBatchQuery) ([]metricsapi.FileUsage, error) {
	if f.batchErr != nil {
		return nil, f.batchErr
	}
	return append([]metricsapi.FileUsage(nil), f.batch...), nil
}

func (f *metricsReporterFake) GetScopedFileUsage(_ context.Context, objectID string, _ usage.ScopeQuery) (*metricsapi.FileUsage, error) {
	if f.scopedFileUsageErr != nil {
		return nil, f.scopedFileUsageErr
	}
	item, ok := f.scopedFileUsage[objectID]
	if !ok {
		return nil, fmt.Errorf("%w: scoped file usage not found", errorapi.ErrNotFound)
	}
	return &item, nil
}

func (f *metricsReporterFake) ListFileUsage(_ context.Context, query usage.FileUsageQuery) ([]metricsapi.FileUsage, error) {
	if f.listFileUsageErr != nil {
		return nil, f.listFileUsageErr
	}
	return append([]metricsapi.FileUsage(nil), f.files...), nil
}

func (f *metricsReporterFake) GetFileUsageSummary(_ context.Context, query usage.FileUsageSummaryQuery) (metricsapi.FileUsageSummary, error) {
	if f.summaryErr != nil {
		return metricsapi.FileUsageSummary{}, f.summaryErr
	}
	return f.summary, nil
}

func (f *metricsReporterFake) GetTransferAttributionSummary(_ context.Context, query usage.TransferSummaryQuery) (metricsapi.TransferAttributionSummary, error) {
	if f.transferSummaryErr != nil {
		return metricsapi.TransferAttributionSummary{}, f.transferSummaryErr
	}
	if f.transferSummaryFn != nil {
		return f.transferSummaryFn(query)
	}
	return f.transferSummary, nil
}

func (f *metricsReporterFake) GetTransferAttributionBreakdown(_ context.Context, query usage.TransferBreakdownQuery) ([]metricsapi.TransferAttributionBreakdown, error) {
	if f.transferBreakdownErr != nil {
		return nil, f.transferBreakdownErr
	}
	if f.transferBreakdownFn != nil {
		return f.transferBreakdownFn(query)
	}
	return append([]metricsapi.TransferAttributionBreakdown(nil), f.transferBreakdown...), nil
}

var _ usage.Reporter = (*metricsReporterFake)(nil)
var _ usage.ProviderEventRecorder = (*metricsIngestFake)(nil)
