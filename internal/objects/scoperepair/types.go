package scoperepair

import (
	"context"
	"errors"

	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

type PreparedRecordQuery struct {
	Limit        int
	Start        string
	Organization string
	Project      string
}

type PreparedRecordReader interface {
	ListPrepared(context.Context, PreparedRecordQuery) ([]objects.Record, error)
}

type ReferenceWriter interface {
	Update(context.Context, objects.RecordID, objects.Record) error
}

// ScopeReader returns only S3 credentials and the scopes attached to each
// credential. The adapter performs any credential-id/bucket alias resolution.
type ScopeReader interface {
	ListCredentials(context.Context) ([]buckets.Credential, error)
	ListScopes(context.Context, string) ([]buckets.Scope, error)
}

type StorageInspectRequest struct {
	Organization string
	Project      string
	ObjectURL    string
}

type StorageInspectResult struct {
	ObjectURL string
}

type StorageProbe interface {
	Inspect(context.Context, StorageInspectRequest) (StorageInspectResult, error)
}

var ErrStorageObjectNotFound = errors.New("storage object not found")

type DuplicateCollapser interface {
	Collapse(context.Context, string, string) (int, error)
}

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
