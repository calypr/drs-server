package metrics

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/usage"
)

func (s *MetricsServer) ListMetricsFiles(ctx context.Context, request metricsapi.ListMetricsFilesRequestObject) (metricsapi.ListMetricsFilesResponseObject, error) {
	limit := 200
	if request.Params.Limit != nil {
		limit = *request.Params.Limit
	}
	offset := 0
	if request.Params.Offset != nil {
		offset = *request.Params.Offset
	}

	if limit < 1 || limit > 1000 || offset < 0 {
		return metricsapi.ListMetricsFiles400Response{}, nil
	}

	inactiveSince, err := parseInactiveSince(request.Params.InactiveDays)
	if err != nil {
		return metricsapi.ListMetricsFiles400Response{}, nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.ListMetricsFiles401Response{}, nil
		case http.StatusForbidden:
			return metricsapi.ListMetricsFiles403Response{}, nil
		default:
			return metricsapi.ListMetricsFiles400Response{}, nil
		}
	}

	data, err := s.reporter.ListFileUsage(ctx, usage.FileUsageQuery{
		Scope:         access.scopeQuery(),
		Limit:         limit,
		Offset:        offset,
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return metricsapi.ListMetricsFiles500Response{}, nil
	}

	items := make([]metricsapi.FileUsage, 0, len(data))
	for _, v := range data {
		items = append(items, toMetricsFileUsage(v))
	}

	return metricsapi.ListMetricsFiles200JSONResponse{
		Data:   &items,
		Limit:  &limit,
		Offset: &offset,
	}, nil
}

func (s *MetricsServer) BulkMetricsFiles(ctx context.Context, request metricsapi.BulkMetricsFilesRequestObject) (metricsapi.BulkMetricsFilesResponseObject, error) {
	started := time.Now()
	if request.Body == nil {
		return metricsapi.BulkMetricsFiles400Response{}, nil
	}
	objectIDs := uniqueNonEmptyStrings(request.Body.ObjectIds)
	if len(objectIDs) == 0 {
		return metricsapi.BulkMetricsFiles400Response{}, nil
	}
	inactiveSince, err := parseInactiveSince(request.Body.InactiveDays)
	if err != nil {
		return metricsapi.BulkMetricsFiles400Response{}, nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.BulkMetricsFiles401Response{}, nil
		case http.StatusForbidden:
			return metricsapi.BulkMetricsFiles403Response{}, nil
		default:
			return metricsapi.BulkMetricsFiles400Response{}, nil
		}
	}

	readableObjectIDs, err := s.readableBulkObjectIDs(ctx, access, objectIDs)
	if err != nil {
		return metricsapi.BulkMetricsFiles500Response{}, nil
	}
	data, err := s.reporter.ListFileUsageByObjectIDs(ctx, readableObjectIDs)
	if err != nil {
		return metricsapi.BulkMetricsFiles500Response{}, nil
	}
	items := make([]metricsapi.FileUsage, 0, len(data))
	for _, usage := range data {
		if !usageMatchesInactiveFilter(usage, inactiveSince) {
			continue
		}
		items = append(items, toMetricsFileUsage(usage))
	}

	log.Printf(
		"INFO: syfon_metrics_files_bulk requested=%d returned=%d scoped=%t aggregate_scopes=%d inactive_days=%t duration_ms=%d",
		len(objectIDs),
		len(items),
		access.isScoped(),
		len(access.scopes),
		request.Body.InactiveDays != nil,
		time.Since(started).Milliseconds(),
	)
	return metricsapi.BulkMetricsFiles200JSONResponse{
		Data: &items,
	}, nil
}

func (s *MetricsServer) GetMetricsFile(ctx context.Context, request metricsapi.GetMetricsFileRequestObject) (metricsapi.GetMetricsFileResponseObject, error) {
	objectID := request.ObjectId
	if objectID == "" {
		return metricsapi.GetMetricsFile400Response{}, nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.GetMetricsFile401Response{}, nil
		case http.StatusForbidden:
			return metricsapi.GetMetricsFile403Response{}, nil
		default:
			return metricsapi.GetMetricsFile400Response{}, nil
		}
	}

	if access.isScoped() || access.hasScopeAggregate() {
		inside, err := s.objectInScope(ctx, objectID, access)
		if err != nil {
			return metricsapi.GetMetricsFile500Response{}, nil
		}
		if !inside {
			return metricsapi.GetMetricsFile404Response{}, nil
		}
	}

	fileUsage, err := s.reporter.GetFileUsage(ctx, objectID)
	if err != nil {
		if errors.Is(err, faults.ErrNotFound) {
			return metricsapi.GetMetricsFile404Response{}, nil
		}
		return metricsapi.GetMetricsFile500Response{}, nil
	}

	return metricsapi.GetMetricsFile200JSONResponse(toMetricsFileUsage(*fileUsage)), nil
}

func (s *MetricsServer) GetMetricsSummary(ctx context.Context, request metricsapi.GetMetricsSummaryRequestObject) (metricsapi.GetMetricsSummaryResponseObject, error) {
	inactiveSince, err := parseInactiveSince(request.Params.InactiveDays)
	if err != nil {
		return metricsapi.GetMetricsSummary400Response{}, nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.GetMetricsSummary401Response{}, nil
		case http.StatusForbidden:
			return metricsapi.GetMetricsSummary403Response{}, nil
		default:
			return metricsapi.GetMetricsSummary400Response{}, nil
		}
	}

	summary, err := s.reporter.GetFileUsageSummary(ctx, usage.FileUsageSummaryQuery{
		Scope:         access.scopeQuery(),
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return metricsapi.GetMetricsSummary500Response{}, nil
	}

	return metricsapi.GetMetricsSummary200JSONResponse{
		TotalFiles:              &summary.TotalFiles,
		TotalUploads:            &summary.TotalUploads,
		TotalDownloads:          &summary.TotalDownloads,
		InactiveFileCount:       &summary.InactiveFileCount,
		RecordCount:             scopedSummaryInt64(access, summary.RecordCount),
		RecordLatestUpdatedTime: scopedSummaryTime(access, summary.RecordLatestUpdatedTime),
	}, nil
}

func (s *MetricsServer) readableBulkObjectIDs(ctx context.Context, access metricsAccess, objectIDs []string) ([]string, error) {
	return s.reporter.ListReadableObjectIDs(ctx, access.scopeQuery(), objectIDs)
}

func (s *MetricsServer) objectInScope(ctx context.Context, objectID string, access metricsAccess) (bool, error) {
	items, err := s.reporter.ListReadableObjectIDs(ctx, access.scopeQuery(), []string{objectID})
	if err != nil {
		if errors.Is(err, faults.ErrNotFound) || errors.Is(err, faults.ErrUnauthorized) {
			return false, nil
		}
		return false, err
	}
	return len(items) > 0, nil
}

func usageMatchesInactiveFilter(usage usage.FileUsage, inactiveSince *time.Time) bool {
	if inactiveSince == nil {
		return true
	}
	return usage.LastDownloadTime == nil || usage.LastDownloadTime.Before(*inactiveSince)
}

func uniqueNonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func scopedSummaryInt64(access metricsAccess, value int64) *int64 {
	if !access.isScoped() {
		return nil
	}
	return &value
}

func scopedSummaryTime(access metricsAccess, value *time.Time) *time.Time {
	if !access.isScoped() || value == nil {
		return nil
	}
	return value
}

func parseInactiveSince(inactiveDays *int) (*time.Time, error) {
	if inactiveDays == nil {
		return nil, nil
	}
	days := *inactiveDays
	if days < 0 {
		return nil, errors.New("inactive_days must be a non-negative integer")
	}
	t := time.Now().UTC().AddDate(0, 0, -days)
	return &t, nil
}

func toMetricsFileUsage(v usage.FileUsage) metricsapi.FileUsage {
	return metricsapi.FileUsage{
		ObjectId:         &v.ObjectID,
		Name:             &v.Name,
		Size:             &v.Size,
		UploadCount:      &v.UploadCount,
		DownloadCount:    &v.DownloadCount,
		LastUploadTime:   v.LastUploadTime,
		LastDownloadTime: v.LastDownloadTime,
		LastAccessTime:   v.LastAccessTime,
	}
}
