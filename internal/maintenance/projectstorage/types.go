package projectstorage

import (
	"context"
	"time"

	"github.com/calypr/syfon/internal/objects"
)

// InspectionMode controls the amount of inventory returned by InspectProjectStorage.
type InspectionMode string

const (
	ModeItems   InspectionMode = "items"
	ModeExists  InspectionMode = "exists"
	ModeSummary InspectionMode = "summary"
)

// StorageObject is the provider-neutral inventory value exposed by this
// maintenance service. ObjectURL is always the canonical physical s3:// URL
// for project inventory.
type StorageObject struct {
	ObjectURL   string
	Provider    string
	Bucket      string
	Key         string
	Path        string
	SizeBytes   int64
	MetaSHA256  string
	ETag        string
	LastModTime time.Time
}

type InventoryOptions struct {
	IncludeHead bool
	ExactPrefix bool
	MaxKeys     int32
}

type InspectionOptions struct {
	Mode        InspectionMode
	IncludeHead bool
	PathPrefix  string
}

type Summary struct {
	Provider          string
	Bucket            string
	Prefix            string
	ObjectURL         string
	Exists            bool
	ObjectCount       int
	TotalBytes        int64
	ComputedAt        time.Time
	Mode              InspectionMode
	InventoryComplete bool
	InventoryWarning  string
}

type InspectionResult struct {
	Summary Summary
	Items   []StorageObject
}

type PhysicalScopeReader interface {
	ListPhysicalObjectsByScope(context.Context, string, string, string) ([]objects.Record, error)
}

type ProjectAccessMethod struct {
	Type     string
	AccessID string
	URL      string
	Headers  []string
}

// ProjectRecordAudit is the plain projection used by project-record
// inspection. It intentionally retains physical duplicate rows and carries
// time values until the HTTP adapter chooses its wire format.
type ProjectRecordAudit struct {
	ObjectID      string
	Checksum      string
	Organization  string
	Project       string
	Name          string
	Size          int64
	AccessURLs    []string
	AccessMethods []ProjectAccessMethod
	CreatedTime   time.Time
	UpdatedTime   *time.Time
}

type ListValidationRequest struct {
	ID                string
	ObjectURL         string
	ExpectedSizeBytes *int64
	ExpectedName      string
}

type ListValidationResult struct {
	ID                   string
	ObjectURL            string
	Provider             string
	Bucket               string
	Key                  string
	Path                 string
	Exists               bool
	Status               ProbeStatus
	Error                string
	ErrorKind            string
	SizeBytes            *int64
	ETag                 string
	LastModTime          time.Time
	ValidationStatus     ValidationStatus
	SizeMatch            *bool
	NameMatch            *bool
	ValidationMismatches []string
}

type ProbeStatus string

const (
	ProbePresent     ProbeStatus = "present"
	ProbeNotFound    ProbeStatus = "not_found"
	ProbeForbidden   ProbeStatus = "forbidden"
	ProbeInvalid     ProbeStatus = "invalid"
	ProbeUnsupported ProbeStatus = "unsupported"
	ProbeError       ProbeStatus = "error"
)

type ValidationStatus string

const (
	ValidationNotRequested ValidationStatus = "not_requested"
	ValidationMatched      ValidationStatus = "matched"
	ValidationMismatched   ValidationStatus = "mismatched"
	ValidationUnverifiable ValidationStatus = "unverifiable"
)

type InspectRequest struct {
	ID                string
	Organization      string
	Project           string
	Key               string
	Scheme            string
	ObjectURL         string
	ExpectedSizeBytes *int64
	ExpectedSHA256    string
}

type ObjectMetadata struct {
	ObjectURL   string
	Provider    string
	Bucket      string
	Key         string
	Path        string
	SizeBytes   int64
	MetaSHA256  string
	ETag        string
	LastModTime time.Time
}

type ProbeResult struct {
	ID                   string
	ObjectURL            string
	Provider             string
	Bucket               string
	Key                  string
	Path                 string
	Exists               bool
	Status               ProbeStatus
	Error                string
	ErrorKind            string
	SizeBytes            *int64
	MetaSHA256           string
	ETag                 string
	LastModTime          time.Time
	ValidationStatus     ValidationStatus
	SizeMatch            *bool
	SHA256Match          *bool
	ValidationMismatches []string
}

type DeleteResult struct {
	ObjectURL string
	Status    string
	Error     string
}

// ProjectCleanupResult is the plain result of deleting a project's catalog
// rows followed by its configured bucket scopes. HTTP adapters choose the
// wire representation.
type ProjectCleanupResult struct {
	Organization        string
	ProjectID           string
	DeletedObjects      int
	DeletedBucketScopes int
}

type ErrorKind string

const (
	ErrorInvalidInput      ErrorKind = "invalid_input"
	ErrorScopeNotFound     ErrorKind = "scope_not_found"
	ErrorCredentialMissing ErrorKind = "credential_missing"
	ErrorPermissionDenied  ErrorKind = "permission_denied"
	ErrorObjectNotFound    ErrorKind = "object_not_found"
	ErrorBucketUnavailable ErrorKind = "bucket_unavailable"
	ErrorListingIncomplete ErrorKind = "listing_incomplete"
	ErrorUnsupported       ErrorKind = "unsupported"
)

// Error is a typed maintenance error. HTTP adapters map its Kind to status
// codes; domain callers can use errors.As without importing HTTP packages.
type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "project storage operation failed"
	}
	if e.Message != "" {
		return e.Message
	}
	return string(e.Kind)
}
