package services

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
)

type MetricsService struct {
	gen metricsapi.ClientWithResponsesInterface
}

func NewMetricsService(gen metricsapi.ClientWithResponsesInterface) *MetricsService {
	return &MetricsService{gen: gen}
}

func (s *MetricsService) Summary(ctx context.Context, opts MetricsSummaryOptions) (metricsapi.FileUsageSummary, error) {
	params := &metricsapi.GetMetricsSummaryParams{
		Organization: stringPtr[metricsapi.Organization](opts.Organization),
		Project:      stringPtr[metricsapi.Project](opts.ProjectID),
	}
	if opts.InactiveDays > 0 {
		params.InactiveDays = &opts.InactiveDays
	}
	resp, err := s.gen.GetMetricsSummaryWithResponse(ctx, params)
	if err != nil {
		return metricsapi.FileUsageSummary{}, err
	}
	if resp.JSON200 == nil {
		return metricsapi.FileUsageSummary{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *MetricsService) Files(ctx context.Context, opts MetricsFilesOptions) ([]metricsapi.FileUsage, error) {
	params := &metricsapi.ListMetricsFilesParams{
		Organization: stringPtr[metricsapi.Organization](opts.Organization),
		Project:      stringPtr[metricsapi.Project](opts.ProjectID),
	}
	if opts.Limit > 0 {
		params.Limit = &opts.Limit
	}
	if opts.Offset > 0 {
		params.Offset = &opts.Offset
	}
	if opts.InactiveDays > 0 {
		params.InactiveDays = &opts.InactiveDays
	}

	resp, err := s.gen.ListMetricsFilesWithResponse(ctx, params)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	if resp.JSON200.Data == nil {
		return []metricsapi.FileUsage{}, nil
	}
	return *resp.JSON200.Data, nil
}

func (s *MetricsService) File(ctx context.Context, objectID string) (metricsapi.FileUsage, error) {
	resp, err := s.gen.GetMetricsFileWithResponse(ctx, objectID, nil)
	if err != nil {
		return metricsapi.FileUsage{}, err
	}
	if resp.JSON200 == nil {
		return metricsapi.FileUsage{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *MetricsService) TransferSummary(ctx context.Context, opts TransferMetricsOptions) (metricsapi.TransferAttributionSummary, error) {
	params, err := transferSummaryParams(opts)
	if err != nil {
		return metricsapi.TransferAttributionSummary{}, err
	}
	resp, err := s.gen.GetTransferSummaryWithResponse(ctx, params)
	if err != nil {
		return metricsapi.TransferAttributionSummary{}, err
	}
	if resp.JSON200 == nil {
		return metricsapi.TransferAttributionSummary{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *MetricsService) TransferBreakdown(ctx context.Context, opts TransferMetricsOptions) (metricsapi.TransferBreakdownResponse, error) {
	params, err := transferBreakdownParams(opts)
	if err != nil {
		return metricsapi.TransferBreakdownResponse{}, err
	}
	resp, err := s.gen.GetTransferBreakdownWithResponse(ctx, params)
	if err != nil {
		return metricsapi.TransferBreakdownResponse{}, err
	}
	if resp.JSON200 == nil {
		return metricsapi.TransferBreakdownResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func transferSummaryParams(opts TransferMetricsOptions) (*metricsapi.GetTransferSummaryParams, error) {
	from, err := optionalMetricsTime(opts.From)
	if err != nil {
		return nil, err
	}
	to, err := optionalMetricsTime(opts.To)
	if err != nil {
		return nil, err
	}
	return &metricsapi.GetTransferSummaryParams{
		Organization:         stringPtr[metricsapi.Organization](opts.Organization),
		Project:              stringPtr[metricsapi.Project](opts.ProjectID),
		Direction:            stringPtr[metricsapi.Direction](opts.Direction),
		ReconciliationStatus: stringPtr[metricsapi.ReconciliationStatus](opts.ReconciliationStatus),
		From:                 from,
		To:                   to,
		Provider:             stringPtr[metricsapi.Provider](opts.Provider),
		Bucket:               stringPtr[metricsapi.Bucket](opts.Bucket),
		Sha256:               stringPtr[metricsapi.SHA256](opts.SHA256),
		User:                 stringPtr[metricsapi.User](opts.User),
		AllowStale:           boolPtr[metricsapi.AllowStale](opts.AllowStale),
	}, nil
}

func transferBreakdownParams(opts TransferMetricsOptions) (*metricsapi.GetTransferBreakdownParams, error) {
	summary, err := transferSummaryParams(opts)
	if err != nil {
		return nil, err
	}
	return &metricsapi.GetTransferBreakdownParams{
		Organization:         summary.Organization,
		Project:              summary.Project,
		Direction:            summary.Direction,
		ReconciliationStatus: summary.ReconciliationStatus,
		From:                 summary.From,
		To:                   summary.To,
		Provider:             summary.Provider,
		Bucket:               summary.Bucket,
		Sha256:               summary.Sha256,
		User:                 summary.User,
		AllowStale:           summary.AllowStale,
		GroupBy:              stringPtr[metricsapi.GetTransferBreakdownParamsGroupBy](opts.GroupBy),
	}, nil
}

func optionalMetricsTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, fmt.Errorf("parse metrics time %q: %w", raw, err)
	}
	u := t.UTC()
	return &u, nil
}

func stringPtr[T ~string](raw string) *T {
	if raw == "" {
		return nil
	}
	v := T(raw)
	return &v
}

func boolPtr[T ~bool](raw bool) *T {
	if !raw {
		return nil
	}
	v := T(raw)
	return &v
}
