package usage

import (
	"fmt"
	"strings"
	"time"
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

type ProviderEvent struct {
	ProviderEventID      string
	AccessGrantID        string
	Direction            string
	EventTime            time.Time
	RequestID            string
	ProviderRequestID    string
	ObjectID             string
	SHA256               string
	ObjectSize           int64
	Organization         string
	Project              string
	AccessID             string
	Provider             string
	Bucket               string
	ObjectKey            string
	StorageURL           string
	RangeStart           *int64
	RangeEnd             *int64
	BytesTransferred     int64
	HTTPMethod           string
	HTTPStatus           int
	RequesterPrincipal   string
	SourceIP             string
	UserAgent            string
	RawEventRef          string
	ActorEmail           string
	ActorSubject         string
	AuthMode             string
	ReconciliationStatus string
}

// NormalizeProviderEvent validates and canonicalizes one provider event before
// it reaches a persistence writer.
func NormalizeProviderEvent(event ProviderEvent) (ProviderEvent, error) {
	event.ProviderEventID = strings.TrimSpace(event.ProviderEventID)
	event.AccessGrantID = strings.TrimSpace(event.AccessGrantID)
	event.Direction = strings.ToLower(strings.TrimSpace(event.Direction))
	switch event.Direction {
	case ProviderTransferDirectionDownload, ProviderTransferDirectionUpload:
	default:
		return ProviderEvent{}, fmt.Errorf("invalid direction")
	}
	if event.ProviderEventID == "" || strings.TrimSpace(event.Provider) == "" || strings.TrimSpace(event.Bucket) == "" {
		return ProviderEvent{}, fmt.Errorf("provider_event_id, provider, and bucket are required")
	}
	if event.BytesTransferred < 0 {
		return ProviderEvent{}, fmt.Errorf("bytes_transferred cannot be negative")
	}
	event.ReconciliationStatus = strings.TrimSpace(event.ReconciliationStatus)
	switch event.ReconciliationStatus {
	case "", ProviderTransferMatched, ProviderTransferAmbiguous, ProviderTransferUnmatched:
	default:
		return ProviderEvent{}, fmt.Errorf("invalid reconciliation_status")
	}
	if event.EventTime.IsZero() {
		event.EventTime = time.Now().UTC()
	} else {
		event.EventTime = event.EventTime.UTC()
	}
	event.RequestID = strings.TrimSpace(event.RequestID)
	event.ProviderRequestID = strings.TrimSpace(event.ProviderRequestID)
	event.ObjectID = strings.TrimSpace(event.ObjectID)
	event.SHA256 = strings.TrimSpace(event.SHA256)
	event.Organization = strings.TrimSpace(event.Organization)
	event.Project = strings.TrimSpace(event.Project)
	event.AccessID = strings.TrimSpace(event.AccessID)
	event.Provider = strings.TrimSpace(event.Provider)
	event.Bucket = strings.TrimSpace(event.Bucket)
	event.ObjectKey = strings.TrimLeft(strings.TrimSpace(event.ObjectKey), "/")
	event.StorageURL = strings.TrimSpace(event.StorageURL)
	event.HTTPMethod = strings.ToUpper(strings.TrimSpace(event.HTTPMethod))
	event.RequesterPrincipal = strings.TrimSpace(event.RequesterPrincipal)
	event.SourceIP = strings.TrimSpace(event.SourceIP)
	event.UserAgent = strings.TrimSpace(event.UserAgent)
	event.RawEventRef = strings.TrimSpace(event.RawEventRef)
	event.ActorEmail = strings.TrimSpace(event.ActorEmail)
	event.ActorSubject = strings.TrimSpace(event.ActorSubject)
	event.AuthMode = strings.TrimSpace(event.AuthMode)
	return event, nil
}
