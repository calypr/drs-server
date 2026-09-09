package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type metricsQueryContextKey struct{}

type metricsQueryParams struct {
	organization string
	program      string
	project      string
}

type metricsServer struct {
	reporter usage.Reporter
	ingestor usage.ProviderEventRecorder
}

func newMetricsServer(reporter usage.Reporter, ingestor usage.ProviderEventRecorder) *metricsServer {
	return &metricsServer{
		reporter: reporter,
		ingestor: ingestor,
	}
}

func registerMetricsRoutes(router fiber.Router, reporter usage.Reporter, ingestor usage.ProviderEventRecorder) {
	router.Use(func(c fiber.Ctx) error {
		params := metricsQueryParams{
			organization: strings.TrimSpace(c.Query("organization")),
			program:      strings.TrimSpace(c.Query("program")),
			project:      strings.TrimSpace(c.Query("project")),
		}
		c.SetContext(context.WithValue(c.Context(), metricsQueryContextKey{}, params))
		return c.Next()
	})

	server := newMetricsServer(reporter, ingestor)
	strict := metricsapi.NewStrictHandler(server, nil)
	metricsapi.RegisterHandlers(router, strict)
}

func (s *metricsServer) checkAuth(ctx context.Context) (metricsAccess, int, bool) {
	organization, project, _, err := parseScopeQuery(ctx)
	if err != nil {
		return metricsAccess{}, http.StatusBadRequest, false
	}
	if access.IsAuthzEnforced(ctx) && access.MissingGen3AuthHeader(ctx) {
		return metricsAccess{}, http.StatusUnauthorized, false
	}
	scope, err := usage.ResolveMetricsScope(ctx, usage.ScopeSelection{
		Organization: organization,
		Project:      project,
	})
	if err != nil {
		return metricsAccess{}, http.StatusForbidden, false
	}
	return metricsAccess{
		organization: scope.Organization,
		project:      scope.Project,
		scope:        scope,
	}, 0, true
}

// metricsAccess is the HTTP response/status projection of an authorized
// usage scope. Authorization and scope construction live in usage.
type metricsAccess struct {
	organization string
	project      string
	scope        usage.ScopeQuery
}

func (a metricsAccess) scopeQuery() usage.ScopeQuery {
	if a.scope.Organization != "" || a.scope.Project != "" || len(a.scope.Scopes) > 0 || len(a.scope.Resources) > 0 {
		return a.scope
	}
	return usage.ScopeQuery{Organization: a.organization, Project: a.project}
}

func (a metricsAccess) isScoped() bool {
	return strings.TrimSpace(a.scopeQuery().Organization) != ""
}

func (a metricsAccess) hasScopeAggregate() bool {
	query := a.scopeQuery()
	return !a.isScoped() && len(query.Scopes) > 0
}

func parseScopeQuery(ctx context.Context) (string, string, bool, error) {
	params, _ := ctx.Value(metricsQueryContextKey{}).(metricsQueryParams)
	organization := strings.TrimSpace(params.organization)
	if organization == "" {
		organization = strings.TrimSpace(params.program)
	}
	project := strings.TrimSpace(params.project)
	if project != "" && organization == "" {
		return "", "", false, fmt.Errorf("organization is required when project is set")
	}
	if organization != "" {
		return organization, project, true, nil
	}
	return "", "", false, nil
}

func metricsAPIError(ctx context.Context, status int) metricsapi.APIError {
	return middleware.NewAPIError(ctx, metricsErrorCode(status), status, http.StatusText(status))
}

func metricsErrorCode(status int) errorapi.ErrorCode {
	switch status {
	case http.StatusUnauthorized:
		return errorapi.ErrorCodeAuthenticationRequired
	case http.StatusForbidden:
		return errorapi.ErrorCodeAccessDenied
	default:
		return errorapi.CodeForStatus(status)
	}
}

func (s *metricsServer) ListMetricsFiles(ctx context.Context, request metricsapi.ListMetricsFilesRequestObject) (metricsapi.ListMetricsFilesResponseObject, error) {
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

func (s *metricsServer) BulkMetricsFiles(ctx context.Context, request metricsapi.BulkMetricsFilesRequestObject) (metricsapi.BulkMetricsFilesResponseObject, error) {
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

func (s *metricsServer) GetMetricsFile(ctx context.Context, request metricsapi.GetMetricsFileRequestObject) (metricsapi.GetMetricsFileResponseObject, error) {
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

func (s *metricsServer) GetMetricsSummary(ctx context.Context, request metricsapi.GetMetricsSummaryRequestObject) (metricsapi.GetMetricsSummaryResponseObject, error) {
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

func (s *metricsServer) RecordProviderTransferEvents(ctx context.Context, request metricsapi.RecordProviderTransferEventsRequestObject) (metricsapi.RecordProviderTransferEventsResponseObject, error) {
	statusCode, ok := checkProviderMetricsIngestAuth(ctx, request.Body)
	if !ok {
		return recordProviderTransferEventsAuthResponse(ctx, statusCode), nil
	}
	if request.Body == nil || len(request.Body.Events) == 0 {
		return metricsapi.RecordProviderTransferEvents400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}
	events := make([]usage.ProviderEvent, 0, len(request.Body.Events))
	for _, item := range request.Body.Events {
		ev, err := providerTransferGeneratedEventToUsage(item)
		if err != nil {
			return metricsapi.RecordProviderTransferEvents400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
		}
		events = append(events, ev)
	}
	if err := s.ingestor.RecordProviderTransferEvents(ctx, events); err != nil {
		return nil, err
	}
	recorded := len(events)
	return metricsapi.RecordProviderTransferEvents201JSONResponse{Recorded: &recorded}, nil
}

func checkProviderMetricsIngestAuth(ctx context.Context, body *metricsapi.RecordProviderTransferEventsJSONRequestBody) (int, bool) {
	if !access.IsGen3Mode(ctx) {
		return 0, true
	}
	if access.MissingGen3AuthHeader(ctx) {
		return http.StatusUnauthorized, false
	}
	if body == nil || len(body.Events) == 0 {
		return http.StatusForbidden, false
	}
	for _, item := range body.Events {
		resource, ok := providerTransferResource(strings.TrimSpace(generatedString(item.Organization)), strings.TrimSpace(generatedString(item.Project)))
		if !ok {
			return http.StatusForbidden, false
		}
		if !access.HasAnyMethodAccess(ctx, []string{resource}, "create", "update") {
			return http.StatusForbidden, false
		}
	}
	return 0, true
}

func providerTransferResource(organization, project string) (string, bool) {
	resource, err := clientaccess.ResourcePath(strings.TrimSpace(organization), strings.TrimSpace(project))
	if err != nil || strings.TrimSpace(resource) == "" {
		return "", false
	}
	return resource, true
}

func providerTransferGeneratedEventToUsage(item metricsapi.ProviderTransferEvent) (usage.ProviderEvent, error) {
	when := time.Time{}
	if item.EventTime != nil {
		when = *item.EventTime
	}
	objectSize := int64(0)
	if item.ObjectSize != nil {
		objectSize = *item.ObjectSize
	}
	httpStatus := 0
	if item.HttpStatus != nil {
		httpStatus = *item.HttpStatus
	}
	event := usage.ProviderEvent{
		ProviderEventID:      item.ProviderEventId,
		AccessGrantID:        generatedString(item.AccessGrantId),
		Direction:            string(item.Direction),
		EventTime:            when,
		RequestID:            generatedString(item.RequestId),
		ProviderRequestID:    generatedString(item.ProviderRequestId),
		ObjectID:             generatedString(item.ObjectId),
		SHA256:               generatedString(item.Sha256),
		ObjectSize:           objectSize,
		Organization:         generatedString(item.Organization),
		Project:              generatedString(item.Project),
		AccessID:             generatedString(item.AccessId),
		Provider:             item.Provider,
		Bucket:               item.Bucket,
		ObjectKey:            generatedString(item.ObjectKey),
		StorageURL:           generatedString(item.StorageUrl),
		RangeStart:           item.RangeStart,
		RangeEnd:             item.RangeEnd,
		BytesTransferred:     item.BytesTransferred,
		HTTPMethod:           generatedString(item.HttpMethod),
		HTTPStatus:           httpStatus,
		RequesterPrincipal:   generatedString(item.RequesterPrincipal),
		SourceIP:             generatedString(item.SourceIp),
		UserAgent:            generatedString(item.UserAgent),
		RawEventRef:          generatedString(item.RawEventRef),
		ActorEmail:           generatedString(item.ActorEmail),
		ActorSubject:         generatedString(item.ActorSubject),
		AuthMode:             generatedString(item.AuthMode),
		ReconciliationStatus: generatedString(item.ReconciliationStatus),
	}
	return usage.NormalizeProviderEvent(event)
}

func recordProviderTransferEventsAuthResponse(ctx context.Context, statusCode int) metricsapi.RecordProviderTransferEventsResponseObject {
	switch statusCode {
	case http.StatusUnauthorized:
		return metricsapi.RecordProviderTransferEvents401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized))
	case http.StatusForbidden:
		return metricsapi.RecordProviderTransferEvents403JSONResponse(metricsAPIError(ctx, http.StatusForbidden))
	default:
		return metricsapi.RecordProviderTransferEvents400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest))
	}
}

func (s *metricsServer) GetTransferSummary(ctx context.Context, request metricsapi.GetTransferSummaryRequestObject) (metricsapi.GetTransferSummaryResponseObject, error) {
	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		return getTransferSummaryAuthResponse(ctx, statusCode), nil
	}
	filter := transferSummaryParamsToFilter(request.Params)
	freshness, _, err := s.transferFreshness(ctx, filter)
	if err != nil {
		return nil, err
	}
	summary, err := s.reporter.GetTransferAttributionSummary(ctx, usage.TransferSummaryQuery{
		Filter: filter,
		Scope:  access.scopeQuery(),
	})
	if err != nil {
		return nil, err
	}
	generated := toGeneratedTransferSummary(summary)
	generated.Freshness = &freshness
	return metricsapi.GetTransferSummary200JSONResponse(generated), nil
}

func (s *metricsServer) GetTransferBreakdown(ctx context.Context, request metricsapi.GetTransferBreakdownRequestObject) (metricsapi.GetTransferBreakdownResponseObject, error) {
	access, statusCode, ok := s.checkAuth(ctx)
	if !ok {
		return getTransferBreakdownAuthResponse(ctx, statusCode), nil
	}
	filter := transferBreakdownParamsToFilter(request.Params)
	freshness, _, err := s.transferFreshness(ctx, filter)
	if err != nil {
		return nil, err
	}
	groupBy := "scope"
	if request.Params.GroupBy != nil {
		groupBy = string(*request.Params.GroupBy)
	}
	switch groupBy {
	case "scope", "user", "provider", "object":
	default:
		return metricsapi.GetTransferBreakdown400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}
	items, err := s.reporter.GetTransferAttributionBreakdown(ctx, usage.TransferBreakdownQuery{
		Filter:  filter,
		GroupBy: groupBy,
		Scope:   access.scopeQuery(),
	})
	if err != nil {
		return nil, err
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

func getTransferSummaryAuthResponse(ctx context.Context, statusCode int) metricsapi.GetTransferSummaryResponseObject {
	switch statusCode {
	case http.StatusUnauthorized:
		return metricsapi.GetTransferSummary401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized))
	case http.StatusForbidden:
		return metricsapi.GetTransferSummary403JSONResponse(metricsAPIError(ctx, http.StatusForbidden))
	default:
		return metricsapi.GetTransferSummary400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest))
	}
}

func getTransferBreakdownAuthResponse(ctx context.Context, statusCode int) metricsapi.GetTransferBreakdownResponseObject {
	switch statusCode {
	case http.StatusUnauthorized:
		return metricsapi.GetTransferBreakdown401JSONResponse(metricsAPIError(ctx, http.StatusUnauthorized))
	case http.StatusForbidden:
		return metricsapi.GetTransferBreakdown403JSONResponse(metricsAPIError(ctx, http.StatusForbidden))
	default:
		return metricsapi.GetTransferBreakdown400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest))
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

func (s *metricsServer) transferFreshness(ctx context.Context, filter usage.Filter) (metricsapi.TransferMetricsFreshness, bool, error) {
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
