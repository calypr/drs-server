package repair

import (
	"context"
	"errors"

	"github.com/calypr/syfon/apigen/client/bucketapi"
	"github.com/calypr/syfon/apigen/client/internalapi"
)

type IndexAPI interface {
	List(ctx context.Context, opts ListRecordsOptions) (internalapi.ListRecordsResponse, error)
	Update(ctx context.Context, did string, rec internalapi.InternalRecord) (internalapi.InternalRecordResponse, error)
}

type ListRecordsOptions struct {
	Limit        int
	Start        string
	Organization string
	Project      string
}

type BucketsAPI interface {
	List(ctx context.Context) (bucketapi.BucketsResponse, error)
	ListScopes(ctx context.Context, bucket string) ([]bucketapi.BucketScopeResponse, error)
}

// StorageInspector probes one storage object for the repair workflow.
//
// The repair package owns this narrow contract so it can be implemented
// directly by an in-process server adapter without carrying the internal API
// transport contract into the repair service.
type StorageInspector interface {
	Inspect(ctx context.Context, req StorageInspectRequest) (StorageInspectResult, error)
}

type StorageInspectRequest struct {
	Organization string
	Project      string
	ObjectURL    string
}

type StorageInspectResult struct {
	ObjectURL string
}

// ErrStorageObjectNotFound marks the one storage inspection failure that
// repair reports separately from other probe failures.
var ErrStorageObjectNotFound = errors.New("storage object not found")

type Options struct {
	Organization string
	Project      string
	CheckStorage bool
	Format       string
	Limit        int
	PageSize     int
}

type FindingKind string

const (
	FindingLegacyAccessURLRemovable  FindingKind = "legacy_access_url_removable"
	FindingLegacyAccessURLRewritable FindingKind = "legacy_access_url_rewritable"
	FindingNonCanonicalAccessURL     FindingKind = "non_canonical_access_url"
	FindingMissingControlledAccess   FindingKind = "missing_controlled_access"
	FindingDuplicateSHA256Sibling    FindingKind = "duplicate_sha256_sibling"
	FindingStorageObjectMissing      FindingKind = "storage_object_missing"
	FindingStorageProbeError         FindingKind = "storage_probe_error"
)

type Severity string

const (
	SeverityInfo  Severity = "info"
	SeverityWarn  Severity = "warn"
	SeverityError Severity = "error"
)

type Finding struct {
	Kind                 FindingKind `json:"kind"`
	Severity             Severity    `json:"severity"`
	ObjectID             string      `json:"object_id"`
	SHA256               string      `json:"sha256,omitempty"`
	Organization         string      `json:"organization,omitempty"`
	Project              string      `json:"project,omitempty"`
	CurrentAccessURLs    []string    `json:"current_access_urls,omitempty"`
	ProposedCanonicalURL string      `json:"proposed_canonical_url,omitempty"`
	AutoFixable          bool        `json:"auto_fixable"`
	Message              string      `json:"message,omitempty"`
}

type ObjectReport struct {
	ObjectID             string    `json:"object_id"`
	SHA256               string    `json:"sha256,omitempty"`
	Organization         string    `json:"organization,omitempty"`
	Project              string    `json:"project,omitempty"`
	CurrentAccessURLs    []string  `json:"current_access_urls,omitempty"`
	ProposedCanonicalURL string    `json:"proposed_canonical_url,omitempty"`
	AutoFixable          bool      `json:"auto_fixable"`
	Findings             []Finding `json:"findings,omitempty"`
}

type Report struct {
	Organization string         `json:"organization,omitempty"`
	Project      string         `json:"project,omitempty"`
	Scanned      int            `json:"scanned"`
	Objects      []ObjectReport `json:"objects,omitempty"`
}

type ApplyResult struct {
	Report      Report `json:"report"`
	Mutated     int    `json:"mutated"`
	Skipped     int    `json:"skipped"`
	AutoFixable int    `json:"auto_fixable"`
}

func (r Report) FindingCount() int {
	return len(r.Objects)
}

type Service struct {
	index     IndexAPI
	buckets   BucketsAPI
	inspector StorageInspector
}

func NewService(index IndexAPI, buckets BucketsAPI, inspector StorageInspector) *Service {
	return &Service{index: index, buckets: buckets, inspector: inspector}
}

type scopeTarget struct {
	Resource     string
	Organization string
	Project      string
	Bucket       string
	Prefix       string
}

type auditState struct {
	report  Report
	objects []*auditedObject
}

type auditedObject struct {
	record         internalapi.InternalRecord
	sha256         string
	currentURLs    []string
	scope          scopeTarget
	scopeKnown     bool
	scopeAmbiguous bool
	inferredScope  string
	canonicalURL   string
	findings       []Finding
	updated        *internalapi.InternalRecord
}
