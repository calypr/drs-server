package storage

type RepairOptions struct {
	Organization string
	Project      string
	CheckStorage bool
	Format       string
	Limit        int
	PageSize     int
}

type RepairFindingKind string

const (
	FindingLegacyAccessURLRemovable  RepairFindingKind = "legacy_access_url_removable"
	FindingLegacyAccessURLRewritable RepairFindingKind = "legacy_access_url_rewritable"
	FindingNonCanonicalAccessURL     RepairFindingKind = "non_canonical_access_url"
	FindingMissingControlledAccess   RepairFindingKind = "missing_controlled_access"
	FindingDuplicateSHA256Sibling    RepairFindingKind = "duplicate_sha256_sibling"
	FindingStorageObjectMissing      RepairFindingKind = "storage_object_missing"
	FindingStorageProbeError         RepairFindingKind = "storage_probe_error"
)

type RepairSeverity string

const (
	SeverityInfo  RepairSeverity = "info"
	SeverityWarn  RepairSeverity = "warn"
	SeverityError RepairSeverity = "error"
)

type RepairFinding struct {
	Kind                 RepairFindingKind `json:"kind"`
	Severity             RepairSeverity    `json:"severity"`
	ObjectID             string            `json:"object_id"`
	SHA256               string            `json:"sha256,omitempty"`
	Organization         string            `json:"organization,omitempty"`
	Project              string            `json:"project,omitempty"`
	CurrentAccessURLs    []string          `json:"current_access_urls,omitempty"`
	ProposedCanonicalURL string            `json:"proposed_canonical_url,omitempty"`
	AutoFixable          bool              `json:"auto_fixable"`
	Message              string            `json:"message,omitempty"`
}

type RepairObjectReport struct {
	ObjectID             string          `json:"object_id"`
	SHA256               string          `json:"sha256,omitempty"`
	Organization         string          `json:"organization,omitempty"`
	Project              string          `json:"project,omitempty"`
	CurrentAccessURLs    []string        `json:"current_access_urls,omitempty"`
	ProposedCanonicalURL string          `json:"proposed_canonical_url,omitempty"`
	AutoFixable          bool            `json:"auto_fixable"`
	Findings             []RepairFinding `json:"findings,omitempty"`
}

type RepairReport struct {
	Organization string               `json:"organization,omitempty"`
	Project      string               `json:"project,omitempty"`
	Scanned      int                  `json:"scanned"`
	Objects      []RepairObjectReport `json:"objects,omitempty"`
}

type RepairResult struct {
	Report      RepairReport `json:"report"`
	Mutated     int          `json:"mutated"`
	Skipped     int          `json:"skipped"`
	AutoFixable int          `json:"auto_fixable"`
}
