package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/metricsapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type metricsServer struct {
	reporter usage.Reporter
	ingestor usage.ProviderEventRecorder
}

func registerMetricsRoutes(router fiber.Router, reporter usage.Reporter, ingestor usage.ProviderEventRecorder) {
	router.Use(func(c fiber.Ctx) error {
		if strings.TrimSpace(c.Query("organization")) == "" {
			if program := strings.TrimSpace(c.Query("program")); program != "" {
				query := c.Request().URI().QueryArgs()
				query.Set("organization", program)
				c.Request().URI().SetQueryString(query.String())
			}
		}
		return c.Next()
	})

	server := &metricsServer{reporter: reporter, ingestor: ingestor}
	strict := metricsapi.NewStrictHandler(server, nil)
	metricsapi.RegisterHandlers(router, strict)
}

func (s *metricsServer) checkAuth(ctx context.Context, organization, project string) (usage.ScopeQuery, int, bool) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if project != "" && organization == "" {
		return usage.ScopeQuery{}, http.StatusBadRequest, false
	}
	if access.IsAuthzEnforced(ctx) && access.MissingGen3AuthHeader(ctx) {
		return usage.ScopeQuery{}, http.StatusUnauthorized, false
	}
	scope, err := usage.ResolveMetricsScope(ctx, usage.ScopeSelection{
		Organization: organization,
		Project:      project,
	})
	if err != nil {
		return usage.ScopeQuery{}, http.StatusForbidden, false
	}
	return scope, 0, true
}

func metricsAPIError(ctx context.Context, status int) metricsapi.APIError {
	return NewAPIError(ctx, metricsErrorCode(status), status, http.StatusText(status))
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

	scope, statusCode, ok := s.checkAuth(ctx, generatedString(request.Params.Organization), generatedString(request.Params.Project))
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
		Scope:         scope,
		Limit:         limit,
		Offset:        offset,
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return nil, err
	}

	return metricsapi.ListMetricsFiles200JSONResponse{
		Data:   &data,
		Limit:  &limit,
		Offset: &offset,
	}, nil
}

func (s *metricsServer) BulkMetricsFiles(ctx context.Context, request metricsapi.BulkMetricsFilesRequestObject) (metricsapi.BulkMetricsFilesResponseObject, error) {
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

	scope, statusCode, ok := s.checkAuth(ctx, generatedString(request.Params.Organization), generatedString(request.Params.Project))
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
		Scope:         scope,
		ObjectIDs:     request.Body.ObjectIds,
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return nil, err
	}
	return metricsapi.BulkMetricsFiles200JSONResponse{
		Data: &data,
	}, nil
}

func (s *metricsServer) GetMetricsFile(ctx context.Context, request metricsapi.GetMetricsFileRequestObject) (metricsapi.GetMetricsFileResponseObject, error) {
	objectID := request.ObjectId
	if objectID == "" {
		return metricsapi.GetMetricsFile400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	scope, statusCode, ok := s.checkAuth(ctx, generatedString(request.Params.Organization), generatedString(request.Params.Project))
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

	var fileUsage *metricsapi.FileUsage
	var err error
	scoped := strings.TrimSpace(scope.Organization) != "" || len(scope.Scopes) > 0
	if scoped {
		fileUsage, err = s.reporter.GetScopedFileUsage(ctx, objectID, scope)
	} else {
		fileUsage, err = s.reporter.GetFileUsage(ctx, objectID)
	}
	if err != nil {
		if scoped && errors.Is(err, errorapi.ErrNotFound) {
			return metricsapi.GetMetricsFile404JSONResponse(metricsAPIError(ctx, http.StatusNotFound)), nil
		}
		return nil, err
	}

	return metricsapi.GetMetricsFile200JSONResponse(*fileUsage), nil
}

func (s *metricsServer) GetMetricsSummary(ctx context.Context, request metricsapi.GetMetricsSummaryRequestObject) (metricsapi.GetMetricsSummaryResponseObject, error) {
	inactiveSince, err := usage.ParseInactiveSince(time.Now().UTC(), request.Params.InactiveDays)
	if err != nil {
		return metricsapi.GetMetricsSummary400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}

	scope, statusCode, ok := s.checkAuth(ctx, generatedString(request.Params.Organization), generatedString(request.Params.Project))
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
		Scope:         scope,
		InactiveSince: inactiveSince,
	})
	if err != nil {
		return nil, err
	}

	return metricsapi.GetMetricsSummary200JSONResponse(summary), nil
}

func (s *metricsServer) RecordProviderTransferEvents(ctx context.Context, request metricsapi.RecordProviderTransferEventsRequestObject) (metricsapi.RecordProviderTransferEventsResponseObject, error) {
	statusCode, ok := checkProviderMetricsIngestAuth(ctx, request.Body)
	if !ok {
		return recordProviderTransferEventsAuthResponse(ctx, statusCode), nil
	}
	if request.Body == nil || len(request.Body.Events) == 0 {
		return metricsapi.RecordProviderTransferEvents400JSONResponse(metricsAPIError(ctx, http.StatusBadRequest)), nil
	}
	events := make([]metricsapi.ProviderTransferEvent, 0, len(request.Body.Events))
	for _, item := range request.Body.Events {
		ev, err := usage.NormalizeProviderEvent(item)
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
		resource, err := clientaccess.ResourcePath(generatedString(item.Organization), generatedString(item.Project))
		if err != nil || strings.TrimSpace(resource) == "" {
			return http.StatusForbidden, false
		}
		if !access.HasAnyMethodAccess(ctx, []string{resource}, "create", "update") {
			return http.StatusForbidden, false
		}
	}
	return 0, true
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
	scope, statusCode, ok := s.checkAuth(ctx, generatedString(request.Params.Organization), generatedString(request.Params.Project))
	if !ok {
		return getTransferSummaryAuthResponse(ctx, statusCode), nil
	}
	filter := transferSummaryParamsToFilter(request.Params)
	freshness := transferMetricsFreshness(filter)
	summary, err := s.reporter.GetTransferAttributionSummary(ctx, usage.TransferSummaryQuery{
		Filter: filter,
		Scope:  scope,
	})
	if err != nil {
		return nil, err
	}
	generated := metricsapi.GetTransferSummary200JSONResponse(summary)
	generated.Freshness = &freshness
	return generated, nil
}

func (s *metricsServer) GetTransferBreakdown(ctx context.Context, request metricsapi.GetTransferBreakdownRequestObject) (metricsapi.GetTransferBreakdownResponseObject, error) {
	scope, statusCode, ok := s.checkAuth(ctx, generatedString(request.Params.Organization), generatedString(request.Params.Project))
	if !ok {
		return getTransferBreakdownAuthResponse(ctx, statusCode), nil
	}
	filter := transferBreakdownParamsToFilter(request.Params)
	freshness := transferMetricsFreshness(filter)
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
		Scope:   scope,
	})
	if err != nil {
		return nil, err
	}
	generatedGroupBy := metricsapi.TransferBreakdownResponseGroupBy(groupBy)
	return metricsapi.GetTransferBreakdown200JSONResponse{
		Data:      &items,
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

func transferMetricsFreshness(filter usage.Filter) metricsapi.TransferMetricsFreshness {
	isStale := false
	missingBuckets := []string{}
	return metricsapi.TransferMetricsFreshness{
		IsStale:             &isStale,
		MissingBuckets:      &missingBuckets,
		RequiredFrom:        filter.From,
		RequiredTo:          filter.To,
		LatestCompletedSync: nil,
	}
}

func generatedTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	t := v.UTC()
	return &t
}
