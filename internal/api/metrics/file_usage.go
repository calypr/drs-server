package metrics

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/metricsapi"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
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

	var data []models.FileUsage
	if access.isScoped() {
		if scopedStore, ok := s.database.(db.FileUsageScopedLister); ok {
			data, err = scopedStore.ListFileUsagePageByScope(ctx, access.organization, access.project, limit, offset, inactiveSince)
		} else {
			data, _, err = listScopedFileUsage(ctx, s.database, s.objects, access.organization, access.project, limit, offset, inactiveSince)
		}
	} else if access.hasScopeAggregate() {
		if scopedStore, ok := s.database.(db.FileUsageScopedLister); ok {
			data, err = scopedStore.ListFileUsagePageByResources(ctx, metricsResources(access.scopes), false, limit, offset, inactiveSince)
		} else {
			data, _, err = listMultiScopedFileUsage(ctx, s.database, s.objects, access.scopes, limit, offset, inactiveSince)
		}
	} else {
		data, err = s.database.ListFileUsage(ctx, limit, offset, inactiveSince)
	}
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
	data, err := s.database.ListFileUsageByObjectIDs(ctx, readableObjectIDs)
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

	if access.isScoped() {
		inside, err := objectInScope(ctx, s.objects, objectID, access.organization, access.project)
		if err != nil {
			return metricsapi.GetMetricsFile500Response{}, nil
		}
		if !inside {
			return metricsapi.GetMetricsFile404Response{}, nil
		}
	} else if access.hasScopeAggregate() {
		inside, err := objectInAnyScope(ctx, s.objects, objectID, access.scopes)
		if err != nil {
			return metricsapi.GetMetricsFile500Response{}, nil
		}
		if !inside {
			return metricsapi.GetMetricsFile404Response{}, nil
		}
	}

	usage, err := s.database.GetFileUsage(ctx, objectID)
	if err != nil {
		if errors.Is(err, faults.ErrNotFound) {
			return metricsapi.GetMetricsFile404Response{}, nil
		}
		return metricsapi.GetMetricsFile500Response{}, nil
	}

	return metricsapi.GetMetricsFile200JSONResponse(toMetricsFileUsage(*usage)), nil
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

	var summary models.FileUsageSummary
	if access.isScoped() {
		if scopedStore, ok := s.database.(db.FileUsageScopedLister); ok {
			summary, err = scopedStore.GetFileUsageSummaryByScope(ctx, access.organization, access.project, inactiveSince)
			if err == nil {
				recordSummary, recordErr := scopedStore.GetProjectRecordSummaryByScope(ctx, access.organization, access.project)
				if recordErr != nil {
					err = recordErr
				} else {
					summary.RecordCount = recordSummary.RecordCount
					summary.RecordLatestUpdatedTime = recordSummary.RecordLatestUpdatedTime
				}
			}
		} else {
			_, summary, err = listScopedFileUsage(ctx, s.database, s.objects, access.organization, access.project, 0, 0, inactiveSince)
			summary.RecordCount = summary.TotalFiles
		}
	} else if access.hasScopeAggregate() {
		if scopedStore, ok := s.database.(db.FileUsageScopedLister); ok {
			summary, err = scopedStore.GetFileUsageSummaryByResources(ctx, metricsResources(access.scopes), false, inactiveSince)
		} else {
			_, summary, err = listMultiScopedFileUsage(ctx, s.database, s.objects, access.scopes, 0, 0, inactiveSince)
		}
	} else {
		summary, err = s.database.GetFileUsageSummary(ctx, inactiveSince)
	}
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
	if !access.isScoped() && !access.hasScopeAggregate() {
		return objectIDs, nil
	}
	readable := make(map[string]struct{}, len(objectIDs))
	if access.isScoped() {
		ids, err := s.objects.ListObjectIDsByScope(ctx, access.organization, access.project, "read")
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			readable[strings.TrimSpace(id)] = struct{}{}
		}
	} else {
		for _, scope := range access.scopes {
			ids, err := s.objects.ListObjectIDsByScope(ctx, scope.organization, scope.project, "read")
			if err != nil {
				return nil, err
			}
			for _, id := range ids {
				readable[strings.TrimSpace(id)] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(objectIDs))
	for _, objectID := range objectIDs {
		if _, ok := readable[objectID]; ok {
			out = append(out, objectID)
		}
	}
	return out, nil
}

func usageMatchesInactiveFilter(usage models.FileUsage, inactiveSince *time.Time) bool {
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

func toMetricsFileUsage(v models.FileUsage) metricsapi.FileUsage {
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

func collectScopedUsage(ctx context.Context, database db.MetricsStore, objects metricsObjectReader, organization, project string, inactiveSince *time.Time) ([]models.FileUsage, models.FileUsageSummary, error) {
	ids, err := objects.ListObjectIDsByScope(ctx, organization, project, "read")
	if err != nil {
		return nil, models.FileUsageSummary{}, err
	}
	sort.Strings(ids)

	summary := models.FileUsageSummary{TotalFiles: int64(len(ids))}
	usages := make([]models.FileUsage, 0, len(ids))
	bulkUsage, err := database.ListFileUsageByObjectIDs(ctx, ids)
	if err != nil {
		return nil, models.FileUsageSummary{}, err
	}
	usageByID := make(map[string]models.FileUsage, len(bulkUsage))
	for _, usage := range bulkUsage {
		usageByID[usage.ObjectID] = usage
	}
	for _, id := range ids {
		usage, ok := usageByID[id]
		if !ok {
			if inactiveSince != nil {
				summary.InactiveFileCount++
			}
			obj, objErr := objects.GetObject(ctx, id, "read")
			if objErr != nil {
				if errors.Is(objErr, faults.ErrNotFound) || errors.Is(objErr, faults.ErrUnauthorized) {
					continue
				}
				return nil, models.FileUsageSummary{}, objErr
			}
			usages = append(usages, models.FileUsage{
				ObjectID: id,
				Name:     common.StringVal(obj.Name),
				Size:     obj.Size,
			})
			continue
		}
		summary.TotalUploads += usage.UploadCount
		summary.TotalDownloads += usage.DownloadCount
		if inactiveSince != nil && (usage.LastDownloadTime == nil || usage.LastDownloadTime.Before(*inactiveSince)) {
			summary.InactiveFileCount++
		}
		if inactiveSince != nil && usage.LastDownloadTime != nil && !usage.LastDownloadTime.Before(*inactiveSince) {
			continue
		}
		usages = append(usages, usage)
	}
	return usages, summary, nil
}

func listScopedFileUsage(ctx context.Context, database db.MetricsStore, objects metricsObjectReader, organization, project string, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, models.FileUsageSummary, error) {
	usages, summary, err := collectScopedUsage(ctx, database, objects, organization, project, inactiveSince)
	if err != nil {
		return nil, models.FileUsageSummary{}, err
	}
	if limit <= 0 {
		return usages, summary, nil
	}
	if offset >= len(usages) {
		return []models.FileUsage{}, summary, nil
	}
	end := offset + limit
	if end > len(usages) {
		end = len(usages)
	}
	return usages[offset:end], summary, nil
}

func listMultiScopedFileUsage(ctx context.Context, database db.MetricsStore, objects metricsObjectReader, scopes []metricsScope, limit, offset int, inactiveSince *time.Time) ([]models.FileUsage, models.FileUsageSummary, error) {
	byID := map[string]models.FileUsage{}
	var summary models.FileUsageSummary
	for _, scope := range scopes {
		usages, scopedSummary, err := collectScopedUsage(ctx, database, objects, scope.organization, scope.project, inactiveSince)
		if err != nil {
			return nil, models.FileUsageSummary{}, err
		}
		summary.TotalFiles += scopedSummary.TotalFiles
		summary.TotalUploads += scopedSummary.TotalUploads
		summary.TotalDownloads += scopedSummary.TotalDownloads
		summary.InactiveFileCount += scopedSummary.InactiveFileCount
		for _, usage := range usages {
			byID[usage.ObjectID] = usage
		}
	}
	usages := make([]models.FileUsage, 0, len(byID))
	for _, usage := range byID {
		usages = append(usages, usage)
	}
	sort.Slice(usages, func(i, j int) bool {
		return usages[i].ObjectID < usages[j].ObjectID
	})
	if limit <= 0 {
		return usages, summary, nil
	}
	if offset >= len(usages) {
		return []models.FileUsage{}, summary, nil
	}
	end := offset + limit
	if end > len(usages) {
		end = len(usages)
	}
	return usages[offset:end], summary, nil
}

func objectInScope(ctx context.Context, objects metricsObjectReader, objectID, organization, project string) (bool, error) {
	obj, err := objects.GetObject(ctx, objectID, "read")
	if err != nil {
		if errors.Is(err, faults.ErrNotFound) || errors.Is(err, faults.ErrUnauthorized) {
			return false, nil
		}
		return false, err
	}
	if strings.TrimSpace(organization) == "" {
		return true, nil
	}
	projects, ok := obj.Authorizations[organization]
	if !ok {
		return false, nil
	}
	if strings.TrimSpace(project) == "" || len(projects) == 0 {
		return true, nil
	}
	for _, p := range projects {
		if p == project {
			return true, nil
		}
	}
	return false, nil
}

func objectInAnyScope(ctx context.Context, objects metricsObjectReader, objectID string, scopes []metricsScope) (bool, error) {
	for _, scope := range scopes {
		inside, err := objectInScope(ctx, objects, objectID, scope.organization, scope.project)
		if err != nil || inside {
			return inside, err
		}
	}
	return false, nil
}
