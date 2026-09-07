package usage

import "time"

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
