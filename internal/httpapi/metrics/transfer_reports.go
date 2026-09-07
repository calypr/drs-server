package metrics

import (
	"context"
	"net/http"
	"time"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/usage"
)

func (s *MetricsServer) GetTransferSummary(ctx context.Context, request metricsapi.GetTransferSummaryRequestObject) (metricsapi.GetTransferSummaryResponseObject, error) {
	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		return getTransferSummaryAuthResponse(statusCode), nil
	}
	filter := transferSummaryParamsToFilter(request.Params)
	freshness, _, err := s.transferFreshness(ctx, filter)
	if err != nil {
		return metricsapi.GetTransferSummary500Response{}, nil
	}
	summary, err := s.reporter.GetTransferAttributionSummary(ctx, usage.TransferSummaryQuery{
		Filter: filter,
		Scope:  access.scopeQuery(),
	})
	if err != nil {
		return metricsapi.GetTransferSummary500Response{}, nil
	}
	generated := toGeneratedTransferSummary(summary)
	generated.Freshness = &freshness
	return metricsapi.GetTransferSummary200JSONResponse(generated), nil
}

func (s *MetricsServer) GetTransferBreakdown(ctx context.Context, request metricsapi.GetTransferBreakdownRequestObject) (metricsapi.GetTransferBreakdownResponseObject, error) {
	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		return getTransferBreakdownAuthResponse(statusCode), nil
	}
	filter := transferBreakdownParamsToFilter(request.Params)
	freshness, _, err := s.transferFreshness(ctx, filter)
	if err != nil {
		return metricsapi.GetTransferBreakdown500Response{}, nil
	}
	groupBy := "scope"
	if request.Params.GroupBy != nil {
		groupBy = string(*request.Params.GroupBy)
	}
	switch groupBy {
	case "scope", "user", "provider", "object":
	default:
		return metricsapi.GetTransferBreakdown400Response{}, nil
	}
	items, err := s.reporter.GetTransferAttributionBreakdown(ctx, usage.TransferBreakdownQuery{
		Filter:  filter,
		GroupBy: groupBy,
		Scope:   access.scopeQuery(),
	})
	if err != nil {
		return metricsapi.GetTransferBreakdown500Response{}, nil
	}
	generatedItems := make([]metricsapi.TransferAttributionBreakdown, 0, len(items))
	for _, item := range items {
		generatedItems = append(generatedItems, toGeneratedTransferBreakdown(item))
	}
	generatedGroupBy := metricsapi.TransferBreakdownResponseGroupBy(groupBy)
	return metricsapi.GetTransferBreakdown200JSONResponse{
		Data:      &generatedItems,
		Freshness: &freshness,
		GroupBy:   &generatedGroupBy,
	}, nil
}

func getTransferSummaryAuthResponse(statusCode int) metricsapi.GetTransferSummaryResponseObject {
	switch statusCode {
	case http.StatusUnauthorized:
		return metricsapi.GetTransferSummary401Response{}
	case http.StatusForbidden:
		return metricsapi.GetTransferSummary403Response{}
	default:
		return metricsapi.GetTransferSummary400Response{}
	}
}

func getTransferBreakdownAuthResponse(statusCode int) metricsapi.GetTransferBreakdownResponseObject {
	switch statusCode {
	case http.StatusUnauthorized:
		return metricsapi.GetTransferBreakdown401Response{}
	case http.StatusForbidden:
		return metricsapi.GetTransferBreakdown403Response{}
	default:
		return metricsapi.GetTransferBreakdown400Response{}
	}
}

func transferSummaryParamsToFilter(params metricsapi.GetTransferSummaryParams) usage.Filter {
	return usage.Filter{
		Organization:         generatedString(params.Organization),
		Project:              generatedString(params.Project),
		Direction:            generatedString(params.Direction),
		ReconciliationStatus: generatedString(params.ReconciliationStatus),
		From:                 generatedTime(params.From),
		To:                   generatedTime(params.To),
		Provider:             generatedString(params.Provider),
		Bucket:               generatedString(params.Bucket),
		SHA256:               generatedString(params.Sha256),
		User:                 generatedString(params.User),
	}
}

func transferBreakdownParamsToFilter(params metricsapi.GetTransferBreakdownParams) usage.Filter {
	return usage.Filter{
		Organization:         generatedString(params.Organization),
		Project:              generatedString(params.Project),
		Direction:            generatedString(params.Direction),
		ReconciliationStatus: generatedString(params.ReconciliationStatus),
		From:                 generatedTime(params.From),
		To:                   generatedTime(params.To),
		Provider:             generatedString(params.Provider),
		Bucket:               generatedString(params.Bucket),
		SHA256:               generatedString(params.Sha256),
		User:                 generatedString(params.User),
	}
}

func generatedString[T ~string](v *T) string {
	if v == nil {
		return ""
	}
	return string(*v)
}

func generatedTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}

func toGeneratedTransferSummary(summary usage.Summary) metricsapi.TransferAttributionSummary {
	return metricsapi.TransferAttributionSummary{
		EventCount:         &summary.EventCount,
		AccessIssuedCount:  &summary.AccessIssuedCount,
		DownloadEventCount: &summary.DownloadEventCount,
		UploadEventCount:   &summary.UploadEventCount,
		BytesRequested:     &summary.BytesRequested,
		BytesDownloaded:    &summary.BytesDownloaded,
		BytesUploaded:      &summary.BytesUploaded,
	}
}

func toGeneratedTransferBreakdown(item usage.Breakdown) metricsapi.TransferAttributionBreakdown {
	return metricsapi.TransferAttributionBreakdown{
		Key:              &item.Key,
		Organization:     &item.Organization,
		Project:          &item.Project,
		Provider:         &item.Provider,
		Bucket:           &item.Bucket,
		Sha256:           &item.SHA256,
		ActorEmail:       &item.ActorEmail,
		ActorSubject:     &item.ActorSubject,
		EventCount:       &item.EventCount,
		BytesRequested:   &item.BytesRequested,
		BytesDownloaded:  &item.BytesDownloaded,
		BytesUploaded:    &item.BytesUploaded,
		LastTransferTime: item.LastTransferTime,
	}
}

func (s *MetricsServer) transferFreshness(ctx context.Context, filter usage.Filter) (metricsapi.TransferMetricsFreshness, bool, error) {
	domainFreshness, err := s.reporter.GetTransferFreshness(ctx, filter)
	if err != nil {
		return metricsapi.TransferMetricsFreshness{}, false, err
	}
	generated := metricsapi.TransferMetricsFreshness{
		IsStale:             &domainFreshness.IsStale,
		MissingBuckets:      &domainFreshness.MissingBuckets,
		RequiredFrom:        domainFreshness.RequiredFrom,
		RequiredTo:          domainFreshness.RequiredTo,
		LatestCompletedSync: domainFreshness.LatestCompletedSync,
	}
	return generated, domainFreshness.IsStale, nil
}
