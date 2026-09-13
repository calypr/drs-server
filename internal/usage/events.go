package usage

import (
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/metricsapi"
)

const (
	TransferEventAccessIssued = "access_issued"

	ProviderTransferDirectionDownload = "download"
	ProviderTransferDirectionUpload   = "upload"

	ProviderTransferMatched   = "matched"
	ProviderTransferAmbiguous = "ambiguous"
	ProviderTransferUnmatched = "unmatched"
)

type Grant struct {
	AccessGrantID string
	FirstIssuedAt time.Time
	LastIssuedAt  time.Time
	IssueCount    int64
	ObjectID      string
	SHA256        string
	ObjectSize    int64
	Organization  string
	Project       string
	AccessID      string
	Provider      string
	Bucket        string
	StorageURL    string
	ActorEmail    string
	ActorSubject  string
	AuthMode      string
}

type Event struct {
	EventID           string
	AccessGrantID     string
	EventType         string
	Direction         string
	EventTime         time.Time
	RequestID         string
	ObjectID          string
	SHA256            string
	ObjectSize        int64
	Organization      string
	Project           string
	AccessID          string
	Provider          string
	Bucket            string
	StorageURL        string
	RangeStart        *int64
	RangeEnd          *int64
	BytesRequested    int64
	BytesCompleted    int64
	ActorEmail        string
	ActorSubject      string
	AuthMode          string
	ClientName        string
	ClientVersion     string
	TransferSessionID string
}

// NormalizeProviderEvent validates and canonicalizes one provider event before
// it reaches a persistence writer.
func NormalizeProviderEvent(event metricsapi.ProviderTransferEvent) (metricsapi.ProviderTransferEvent, error) {
	event.ProviderEventId = strings.TrimSpace(event.ProviderEventId)
	normalizedDirection := strings.ToLower(strings.TrimSpace(string(event.Direction)))
	switch normalizedDirection {
	case ProviderTransferDirectionDownload, ProviderTransferDirectionUpload:
		event.Direction = metricsapi.ProviderTransferDirection(normalizedDirection)
	default:
		return metricsapi.ProviderTransferEvent{}, fmt.Errorf("invalid direction")
	}
	if event.ProviderEventId == "" || strings.TrimSpace(event.Provider) == "" || strings.TrimSpace(event.Bucket) == "" {
		return metricsapi.ProviderTransferEvent{}, fmt.Errorf("provider_event_id, provider, and bucket are required")
	}
	if event.BytesTransferred < 0 {
		return metricsapi.ProviderTransferEvent{}, fmt.Errorf("bytes_transferred cannot be negative")
	}
	if event.ReconciliationStatus != nil {
		normalizedStatus := strings.TrimSpace(string(*event.ReconciliationStatus))
		switch normalizedStatus {
		case ProviderTransferMatched, ProviderTransferAmbiguous, ProviderTransferUnmatched:
			status := metricsapi.ProviderTransferReconciliationStatus(normalizedStatus)
			event.ReconciliationStatus = &status
		case "":
			event.ReconciliationStatus = nil
		default:
			return metricsapi.ProviderTransferEvent{}, fmt.Errorf("invalid reconciliation_status")
		}
	}
	if event.EventTime == nil {
		now := time.Now().UTC()
		event.EventTime = &now
	} else {
		when := event.EventTime.UTC()
		event.EventTime = &when
	}
	normalizeProviderString(&event.AccessGrantId)
	normalizeProviderString(&event.RequestId)
	normalizeProviderString(&event.ProviderRequestId)
	normalizeProviderString(&event.ObjectId)
	normalizeProviderString(&event.Sha256)
	normalizeProviderString(&event.Organization)
	normalizeProviderString(&event.Project)
	normalizeProviderString(&event.AccessId)
	event.Provider = strings.TrimSpace(event.Provider)
	event.Bucket = strings.TrimSpace(event.Bucket)
	normalizeProviderString(&event.ObjectKey)
	if event.ObjectKey != nil {
		value := strings.TrimLeft(*event.ObjectKey, "/")
		event.ObjectKey = &value
	}
	normalizeProviderString(&event.StorageUrl)
	if event.HttpMethod != nil {
		value := strings.ToUpper(strings.TrimSpace(*event.HttpMethod))
		event.HttpMethod = &value
	}
	normalizeProviderString(&event.RequesterPrincipal)
	normalizeProviderString(&event.SourceIp)
	normalizeProviderString(&event.UserAgent)
	normalizeProviderString(&event.RawEventRef)
	normalizeProviderString(&event.ActorEmail)
	normalizeProviderString(&event.ActorSubject)
	normalizeProviderString(&event.AuthMode)
	return event, nil
}

func normalizeProviderString(value **string) {
	if *value != nil {
		normalized := strings.TrimSpace(**value)
		*value = &normalized
	}
}
