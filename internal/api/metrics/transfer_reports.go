package metrics

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/models"
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
	var summary models.TransferAttributionSummary
	if access.hasScopeAggregate() && filter.Organization == "" {
		summary, err = s.getScopedTransferAttributionSummary(ctx, filter, access.scopes)
	} else {
		summary, err = s.database.GetTransferAttributionSummary(ctx, filter)
	}
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
	var items []models.TransferAttributionBreakdown
	if access.hasScopeAggregate() && filter.Organization == "" {
		items, err = s.getScopedTransferAttributionBreakdown(ctx, filter, groupBy, access.scopes)
	} else {
		items, err = s.database.GetTransferAttributionBreakdown(ctx, filter, groupBy)
	}
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

func transferSummaryParamsToFilter(params metricsapi.GetTransferSummaryParams) models.TransferAttributionFilter {
	return models.TransferAttributionFilter{
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

func transferBreakdownParamsToFilter(params metricsapi.GetTransferBreakdownParams) models.TransferAttributionFilter {
	return models.TransferAttributionFilter{
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

func toGeneratedTransferSummary(summary models.TransferAttributionSummary) metricsapi.TransferAttributionSummary {
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

func toGeneratedTransferBreakdown(item models.TransferAttributionBreakdown) metricsapi.TransferAttributionBreakdown {
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

func (s *MetricsServer) transferFreshness(ctx context.Context, filter models.TransferAttributionFilter) (metricsapi.TransferMetricsFreshness, bool, error) {
	stale := false
	missing := make([]string, 0)
	freshness := metricsapi.TransferMetricsFreshness{
		IsStale:        &stale,
		MissingBuckets: &missing,
		RequiredFrom:   filter.From,
		RequiredTo:     filter.To,
	}
	return freshness, stale, nil
}

func (s *MetricsServer) getScopedTransferAttributionSummary(ctx context.Context, filter models.TransferAttributionFilter, scopes []metricsScope) (models.TransferAttributionSummary, error) {
	if scopedStore, ok := s.database.(db.TransferAttributionScopedStore); ok {
		return scopedStore.GetTransferAttributionSummaryByResources(ctx, filter, metricsResources(scopes))
	}

	var out models.TransferAttributionSummary
	for _, scope := range scopes {
		scoped := filter
		scoped.Organization = scope.organization
		scoped.Project = scope.project
		summary, err := s.database.GetTransferAttributionSummary(ctx, scoped)
		if err != nil {
			return models.TransferAttributionSummary{}, err
		}
		out.EventCount += summary.EventCount
		out.AccessIssuedCount += summary.AccessIssuedCount
		out.DownloadEventCount += summary.DownloadEventCount
		out.UploadEventCount += summary.UploadEventCount
		out.BytesRequested += summary.BytesRequested
		out.BytesDownloaded += summary.BytesDownloaded
		out.BytesUploaded += summary.BytesUploaded
	}
	return out, nil
}

func (s *MetricsServer) getScopedTransferAttributionBreakdown(ctx context.Context, filter models.TransferAttributionFilter, groupBy string, scopes []metricsScope) ([]models.TransferAttributionBreakdown, error) {
	if scopedStore, ok := s.database.(db.TransferAttributionScopedStore); ok {
		return scopedStore.GetTransferAttributionBreakdownByResources(ctx, filter, groupBy, metricsResources(scopes))
	}

	byKey := map[string]*models.TransferAttributionBreakdown{}
	for _, scope := range scopes {
		scoped := filter
		scoped.Organization = scope.organization
		scoped.Project = scope.project
		items, err := s.database.GetTransferAttributionBreakdown(ctx, scoped, groupBy)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			key := item.Key
			if key == "" {
				key = item.Organization + "/" + item.Project + "/" + item.Provider + "/" + item.Bucket + "/" + item.SHA256 + "/" + item.ActorEmail + "/" + item.ActorSubject
			}
			merged := byKey[key]
			if merged == nil {
				copy := item
				byKey[key] = &copy
				continue
			}
			merged.EventCount += item.EventCount
			merged.BytesRequested += item.BytesRequested
			merged.BytesDownloaded += item.BytesDownloaded
			merged.BytesUploaded += item.BytesUploaded
			if item.LastTransferTime != nil && (merged.LastTransferTime == nil || item.LastTransferTime.After(*merged.LastTransferTime)) {
				t := *item.LastTransferTime
				merged.LastTransferTime = &t
			}
		}
	}
	out := make([]models.TransferAttributionBreakdown, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTransferTime == nil || out[j].LastTransferTime == nil {
			return out[i].Key < out[j].Key
		}
		if out[i].LastTransferTime.Equal(*out[j].LastTransferTime) {
			return out[i].Key < out[j].Key
		}
		return out[i].LastTransferTime.After(*out[j].LastTransferTime)
	})
	return out, nil
}
