package metrics

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
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
		return metricsapi.ListMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	inactiveSince, err := usage.ParseInactiveSince(time.Now().UTC(), request.Params.InactiveDays)
	if err != nil {
		return metricsapi.ListMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.ListMetricsFiles401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized)), nil
		case http.StatusForbidden:
			return metricsapi.ListMetricsFiles403JSONResponse(metricsAPIError(ctx, http.StatusForbidden)), nil
		default:
			return metricsapi.ListMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
		}
	}

	data, err := s.reporter.ListFileUsage(ctx, usage.FileUsageQuery{
		Scope:         access.scopeQuery(),
		Limit:         limit,
		Offset:        offset,
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return nil, err
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
		return metricsapi.BulkMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}
	hasObjectID := false
	for _, objectID := range request.Body.ObjectIds {
		if strings.TrimSpace(objectID) != "" {
			hasObjectID = true
			break
		}
	}
	if !hasObjectID {
		return metricsapi.BulkMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}
	inactiveSince, err := usage.ParseInactiveSince(time.Now().UTC(), request.Body.InactiveDays)
	if err != nil {
		return metricsapi.BulkMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.BulkMetricsFiles401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized)), nil
		case http.StatusForbidden:
			return metricsapi.BulkMetricsFiles403JSONResponse(metricsAPIError(ctx, http.StatusForbidden)), nil
		default:
			return metricsapi.BulkMetricsFiles400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
		}
	}

	data, err := s.reporter.ListFileUsageBatch(ctx, usage.FileUsageBatchQuery{
		Scope:         access.scopeQuery(),
		ObjectIDs:     request.Body.ObjectIds,
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return nil, err
	}
	items := make([]metricsapi.FileUsage, 0, len(data))
	for _, item := range data {
		items = append(items, toMetricsFileUsage(item))
	}

	log.Printf(
		"INFO: syfon_metrics_files_bulk requested=%d returned=%d scoped=%t aggregate_scopes=%d inactive_days=%t duration_ms=%d",
		len(request.Body.ObjectIds),
		len(items),
		access.isScoped(),
		len(access.scopeQuery().Scopes),
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
		return metricsapi.GetMetricsFile400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.GetMetricsFile401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized)), nil
		case http.StatusForbidden:
			return metricsapi.GetMetricsFile403JSONResponse(metricsAPIError(ctx, http.StatusForbidden)), nil
		default:
			return metricsapi.GetMetricsFile400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
		}
	}

	var fileUsage *usage.FileUsage
	var err error
	scoped := access.isScoped() || access.hasScopeAggregate()
	if scoped {
		fileUsage, err = s.reporter.GetScopedFileUsage(ctx, objectID, access.scopeQuery())
	} else {
		fileUsage, err = s.reporter.GetFileUsage(ctx, objectID)
	}
	if err != nil {
		if scoped && errors.Is(err, errorapi.ErrNotFound) {
			return metricsapi.GetMetricsFile404JSONResponse(metricsAPIError(ctx, http.StatusNotFound)), nil
		}
		return nil, err
	}

	return metricsapi.GetMetricsFile200JSONResponse(toMetricsFileUsage(*fileUsage)), nil
}

func (s *MetricsServer) GetMetricsSummary(ctx context.Context, request metricsapi.GetMetricsSummaryRequestObject) (metricsapi.GetMetricsSummaryResponseObject, error) {
	inactiveSince, err := usage.ParseInactiveSince(time.Now().UTC(), request.Params.InactiveDays)
	if err != nil {
		return metricsapi.GetMetricsSummary400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		switch statusCode {
		case http.StatusUnauthorized:
			return metricsapi.GetMetricsSummary401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized)), nil
		case http.StatusForbidden:
			return metricsapi.GetMetricsSummary403JSONResponse(metricsAPIError(ctx, http.StatusForbidden)), nil
		default:
			return metricsapi.GetMetricsSummary400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
		}
	}

	summary, err := s.reporter.GetFileUsageSummary(ctx, usage.FileUsageSummaryQuery{
		Scope:         access.scopeQuery(),
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return nil, err
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
