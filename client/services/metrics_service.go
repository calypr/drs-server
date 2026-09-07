package services

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/syfon/apigen/client/metricsapi"
	"github.com/calypr/syfon/internal/models"
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
		return metricsapi.FileUsageSummary{}, fmt.Errorf("failed to get metrics summary: %d", resp.StatusCode())
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
		return nil, fmt.Errorf("failed to list metrics files: %d", resp.StatusCode())
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
		return metricsapi.FileUsage{}, fmt.Errorf("failed to get file metrics: %d", resp.StatusCode())
	}
	return *resp.JSON200, nil
}

func (s *MetricsService) TransferSummary(ctx context.Context, opts TransferMetricsOptions) (models.TransferAttributionSummary, error) {
	params, err := transferSummaryParams(opts)
	if err != nil {
		return models.TransferAttributionSummary{}, err
	}
	resp, err := s.gen.GetTransferSummaryWithResponse(ctx, params)
	if err != nil {
		return models.TransferAttributionSummary{}, err
	}
	if resp.JSON200 == nil {
		return models.TransferAttributionSummary{}, fmt.Errorf("failed to get transfer metrics summary: %d", resp.StatusCode())
	}
	return generatedTransferSummaryToModel(*resp.JSON200), nil
}

type TransferBreakdownResponse struct {
	GroupBy   string                                `json:"group_by"`
	Data      []models.TransferAttributionBreakdown `json:"data"`
	Freshness *models.TransferMetricsFreshness      `json:"freshness,omitempty"`
}

func (s *MetricsService) TransferBreakdown(ctx context.Context, opts TransferMetricsOptions) (TransferBreakdownResponse, error) {
	params, err := transferBreakdownParams(opts)
	if err != nil {
		return TransferBreakdownResponse{}, err
	}
	resp, err := s.gen.GetTransferBreakdownWithResponse(ctx, params)
	if err != nil {
		return TransferBreakdownResponse{}, err
	}
	if resp.JSON200 == nil {
		return TransferBreakdownResponse{}, fmt.Errorf("failed to get transfer metrics breakdown: %d", resp.StatusCode())
	}
	out := TransferBreakdownResponse{}
	if resp.JSON200.GroupBy != nil {
		out.GroupBy = string(*resp.JSON200.GroupBy)
	}
	if resp.JSON200.Data != nil {
		out.Data = make([]models.TransferAttributionBreakdown, 0, len(*resp.JSON200.Data))
		for _, item := range *resp.JSON200.Data {
			out.Data = append(out.Data, generatedTransferBreakdownToModel(item))
		}
	}
	out.Freshness = generatedFreshnessToModel(resp.JSON200.Freshness)
	return out, nil
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

func generatedTransferSummaryToModel(v metricsapi.TransferAttributionSummary) models.TransferAttributionSummary {
	return models.TransferAttributionSummary{
		EventCount:         int64Val(v.EventCount),
		AccessIssuedCount:  int64Val(v.AccessIssuedCount),
		DownloadEventCount: int64Val(v.DownloadEventCount),
		UploadEventCount:   int64Val(v.UploadEventCount),
		BytesRequested:     int64Val(v.BytesRequested),
		BytesDownloaded:    int64Val(v.BytesDownloaded),
		BytesUploaded:      int64Val(v.BytesUploaded),
		Freshness:          generatedFreshnessToModel(v.Freshness),
	}
}

func generatedFreshnessToModel(v *metricsapi.TransferMetricsFreshness) *models.TransferMetricsFreshness {
	if v == nil {
		return nil
	}
	out := &models.TransferMetricsFreshness{
		IsStale:             boolVal(v.IsStale),
		LatestCompletedSync: v.LatestCompletedSync,
		RequiredFrom:        v.RequiredFrom,
		RequiredTo:          v.RequiredTo,
	}
	if v.MissingBuckets != nil {
		out.MissingBuckets = append(out.MissingBuckets, (*v.MissingBuckets)...)
	}
	return out
}

func generatedTransferBreakdownToModel(v metricsapi.TransferAttributionBreakdown) models.TransferAttributionBreakdown {
	return models.TransferAttributionBreakdown{
		Key:              stringVal(v.Key),
		Organization:     stringVal(v.Organization),
		Project:          stringVal(v.Project),
		Provider:         stringVal(v.Provider),
		Bucket:           stringVal(v.Bucket),
		SHA256:           stringVal(v.Sha256),
		ActorEmail:       stringVal(v.ActorEmail),
		ActorSubject:     stringVal(v.ActorSubject),
		EventCount:       int64Val(v.EventCount),
		BytesRequested:   int64Val(v.BytesRequested),
		BytesDownloaded:  int64Val(v.BytesDownloaded),
		BytesUploaded:    int64Val(v.BytesUploaded),
		LastTransferTime: v.LastTransferTime,
	}
}

func stringVal(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func int64Val(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func boolVal(v *bool) bool {
	return v != nil && *v
}
