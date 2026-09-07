package metrics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/metricsapi"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/usage"
)

type providerTransferPayload struct {
	ProviderEventID      string `json:"provider_event_id"`
	AccessGrantID        string `json:"access_grant_id"`
	Direction            string `json:"direction"`
	EventTime            string `json:"event_time"`
	RequestID            string `json:"request_id"`
	ProviderRequestID    string `json:"provider_request_id"`
	ObjectID             string `json:"object_id"`
	SHA256               string `json:"sha256"`
	ObjectSize           int64  `json:"object_size"`
	Organization         string `json:"organization"`
	Project              string `json:"project"`
	AccessID             string `json:"access_id"`
	Provider             string `json:"provider"`
	Bucket               string `json:"bucket"`
	ObjectKey            string `json:"object_key"`
	StorageURL           string `json:"storage_url"`
	RangeStart           *int64 `json:"range_start"`
	RangeEnd             *int64 `json:"range_end"`
	BytesTransferred     int64  `json:"bytes_transferred"`
	HTTPMethod           string `json:"http_method"`
	HTTPStatus           int    `json:"http_status"`
	RequesterPrincipal   string `json:"requester_principal"`
	SourceIP             string `json:"source_ip"`
	UserAgent            string `json:"user_agent"`
	RawEventRef          string `json:"raw_event_ref"`
	ActorEmail           string `json:"actor_email"`
	ActorSubject         string `json:"actor_subject"`
	AuthMode             string `json:"auth_mode"`
	ReconciliationStatus string `json:"reconciliation_status"`
}

func (s *MetricsServer) RecordProviderTransferEvents(ctx context.Context, request metricsapi.RecordProviderTransferEventsRequestObject) (metricsapi.RecordProviderTransferEventsResponseObject, error) {
	statusCode, ok := checkProviderMetricsIngestAuth(ctx, request.Body)
	if !ok {
		return recordProviderTransferEventsAuthResponse(statusCode), nil
	}
	if request.Body == nil || len(request.Body.Events) == 0 {
		return metricsapi.RecordProviderTransferEvents400Response{}, nil
	}
	events := make([]usage.ProviderEvent, 0, len(request.Body.Events))
	for _, item := range request.Body.Events {
		ev, err := providerTransferPayloadToUsage(providerTransferGeneratedEventToPayload(item))
		if err != nil {
			return metricsapi.RecordProviderTransferEvents400Response{}, nil
		}
		events = append(events, ev)
	}
	if err := s.ingestor.RecordProviderTransferEvents(ctx, events); err != nil {
		return metricsapi.RecordProviderTransferEvents500Response{}, nil
	}
	recorded := len(events)
	return metricsapi.RecordProviderTransferEvents201JSONResponse{Recorded: &recorded}, nil
}

func checkProviderMetricsIngestAuth(ctx context.Context, body *metricsapi.RecordProviderTransferEventsJSONRequestBody) (int, bool) {
	if !access.IsGen3Mode(ctx) {
		return 0, true
	}
	if apimiddleware.MissingGen3AuthHeader(ctx) {
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

func providerTransferPayloadToUsage(item providerTransferPayload) (usage.ProviderEvent, error) {
	direction := strings.ToLower(strings.TrimSpace(item.Direction))
	switch direction {
	case usage.ProviderTransferDirectionDownload, usage.ProviderTransferDirectionUpload:
	default:
		return usage.ProviderEvent{}, errors.New("invalid direction")
	}
	if strings.TrimSpace(item.ProviderEventID) == "" || strings.TrimSpace(item.Provider) == "" || strings.TrimSpace(item.Bucket) == "" {
		return usage.ProviderEvent{}, errors.New("provider_event_id, provider, and bucket are required")
	}
	if item.BytesTransferred < 0 {
		return usage.ProviderEvent{}, errors.New("bytes_transferred cannot be negative")
	}
	status := strings.TrimSpace(item.ReconciliationStatus)
	switch status {
	case "", usage.ProviderTransferMatched, usage.ProviderTransferAmbiguous, usage.ProviderTransferUnmatched:
	default:
		return usage.ProviderEvent{}, errors.New("invalid reconciliation_status")
	}
	when := time.Now().UTC()
	if strings.TrimSpace(item.EventTime) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(item.EventTime))
		if err != nil {
			return usage.ProviderEvent{}, errors.New("invalid event_time")
		}
		when = parsed.UTC()
	}
	return usage.ProviderEvent{
		ProviderEventID:      strings.TrimSpace(item.ProviderEventID),
		AccessGrantID:        strings.TrimSpace(item.AccessGrantID),
		Direction:            direction,
		EventTime:            when,
		RequestID:            strings.TrimSpace(item.RequestID),
		ProviderRequestID:    strings.TrimSpace(item.ProviderRequestID),
		ObjectID:             strings.TrimSpace(item.ObjectID),
		SHA256:               strings.TrimSpace(item.SHA256),
		ObjectSize:           item.ObjectSize,
		Organization:         strings.TrimSpace(item.Organization),
		Project:              strings.TrimSpace(item.Project),
		AccessID:             strings.TrimSpace(item.AccessID),
		Provider:             strings.TrimSpace(item.Provider),
		Bucket:               strings.TrimSpace(item.Bucket),
		ObjectKey:            strings.TrimLeft(strings.TrimSpace(item.ObjectKey), "/"),
		StorageURL:           strings.TrimSpace(item.StorageURL),
		RangeStart:           item.RangeStart,
		RangeEnd:             item.RangeEnd,
		BytesTransferred:     item.BytesTransferred,
		HTTPMethod:           strings.ToUpper(strings.TrimSpace(item.HTTPMethod)),
		HTTPStatus:           item.HTTPStatus,
		RequesterPrincipal:   strings.TrimSpace(item.RequesterPrincipal),
		SourceIP:             strings.TrimSpace(item.SourceIP),
		UserAgent:            strings.TrimSpace(item.UserAgent),
		RawEventRef:          strings.TrimSpace(item.RawEventRef),
		ActorEmail:           strings.TrimSpace(item.ActorEmail),
		ActorSubject:         strings.TrimSpace(item.ActorSubject),
		AuthMode:             strings.TrimSpace(item.AuthMode),
		ReconciliationStatus: status,
	}, nil
}

func recordProviderTransferEventsAuthResponse(statusCode int) metricsapi.RecordProviderTransferEventsResponseObject {
	switch statusCode {
	case http.StatusUnauthorized:
		return metricsapi.RecordProviderTransferEvents401Response{}
	case http.StatusForbidden:
		return metricsapi.RecordProviderTransferEvents403Response{}
	default:
		return metricsapi.RecordProviderTransferEvents400Response{}
	}
}

func providerTransferGeneratedEventToPayload(item metricsapi.ProviderTransferEvent) providerTransferPayload {
	out := providerTransferPayload{
		ProviderEventID:  item.ProviderEventId,
		Direction:        string(item.Direction),
		Provider:         item.Provider,
		Bucket:           item.Bucket,
		BytesTransferred: item.BytesTransferred,
	}
	if item.AccessGrantId != nil {
		out.AccessGrantID = *item.AccessGrantId
	}
	if item.EventTime != nil {
		out.EventTime = item.EventTime.Format(time.RFC3339Nano)
	}
	if item.RequestId != nil {
		out.RequestID = *item.RequestId
	}
	if item.ProviderRequestId != nil {
		out.ProviderRequestID = *item.ProviderRequestId
	}
	if item.ObjectId != nil {
		out.ObjectID = *item.ObjectId
	}
	if item.Sha256 != nil {
		out.SHA256 = *item.Sha256
	}
	if item.ObjectSize != nil {
		out.ObjectSize = *item.ObjectSize
	}
	if item.Organization != nil {
		out.Organization = *item.Organization
	}
	if item.Project != nil {
		out.Project = *item.Project
	}
	if item.AccessId != nil {
		out.AccessID = *item.AccessId
	}
	if item.ObjectKey != nil {
		out.ObjectKey = *item.ObjectKey
	}
	if item.StorageUrl != nil {
		out.StorageURL = *item.StorageUrl
	}
	out.RangeStart = item.RangeStart
	out.RangeEnd = item.RangeEnd
	if item.HttpMethod != nil {
		out.HTTPMethod = *item.HttpMethod
	}
	if item.HttpStatus != nil {
		out.HTTPStatus = *item.HttpStatus
	}
	if item.RequesterPrincipal != nil {
		out.RequesterPrincipal = *item.RequesterPrincipal
	}
	if item.SourceIp != nil {
		out.SourceIP = *item.SourceIp
	}
	if item.UserAgent != nil {
		out.UserAgent = *item.UserAgent
	}
	if item.RawEventRef != nil {
		out.RawEventRef = *item.RawEventRef
	}
	if item.ActorEmail != nil {
		out.ActorEmail = *item.ActorEmail
	}
	if item.ActorSubject != nil {
		out.ActorSubject = *item.ActorSubject
	}
	if item.AuthMode != nil {
		out.AuthMode = *item.AuthMode
	}
	if item.ReconciliationStatus != nil {
		out.ReconciliationStatus = string(*item.ReconciliationStatus)
	}
	return out
}
