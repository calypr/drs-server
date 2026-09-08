// Package objects owns the values used to describe persisted DRS records and
// their checksum-derived views.  It deliberately has no dependency on the
// generated HTTP contract: adapters translate at the boundary.
package objects

import (
	"fmt"
	"strings"
	"time"
)

// Scope identifies the optional organization/project boundary for a record
// query or mutation. An empty Scope represents an unscoped operation.
type Scope struct {
	Organization string
	Project      string
}

// ChecksumQuery is an algorithm-qualified checksum lookup.
type ChecksumQuery struct {
	Type  string
	Value string
}

// ChecksumMatches keeps one checksum query's result separate from the other
// queries in an ordered batch.
type ChecksumMatches struct {
	Query   ChecksumQuery
	Records []Record
}

// RecordListQuery describes the selection and pagination policy for a record
// listing. Only domain values cross into the records service.
type RecordListQuery struct {
	Scope          Scope
	Checksum       *ChecksumQuery
	ObjectURL      string
	StartAfter     string
	Limit          int
	Page           int
	RequiredMethod string
}

// ScopedRecord pairs a record with the scope supplied by its caller.
type ScopedRecord struct {
	Record Record
	Scope  Scope
}

// NewScope validates and normalizes an organization/project scope. A project
// cannot be supplied without an organization; an omitted scope is valid.
func NewScope(organization, project string) (Scope, error) {
	scope := Scope{
		Organization: strings.TrimSpace(organization),
		Project:      strings.TrimSpace(project),
	}
	if scope.Project != "" && scope.Organization == "" {
		return Scope{}, fmt.Errorf("organization is required when project is set")
	}
	return scope, nil
}

// RecordID identifies one physical persisted record.  Two records may refer
// to the same content while retaining distinct record IDs.
type RecordID string

// ContentID identifies content by its canonical checksum.
type ContentID string

// Checksum is the persistence-independent checksum value carried by a record.
type Checksum struct {
	Type     string `json:"type"`
	Checksum string `json:"checksum"`
}

// AccessURL is the URL and optional request headers for one access method.
type AccessURL struct {
	Headers *[]string `json:"headers,omitempty"`
	Url     string    `json:"url"`
}

// AccessAuthorizations describes optional authorization issuers attached to
// an access method.  The fields mirror the DRS contract without importing it.
type AccessAuthorizations struct {
	BearerAuthIssuers   *[]string `json:"bearer_auth_issuers,omitempty"`
	DrsObjectId         *string   `json:"drs_object_id,omitempty"`
	PassportAuthIssuers *[]string `json:"passport_auth_issuers,omitempty"`
	SupportedTypes      *[]string `json:"supported_types,omitempty"`
}

// AccessMethod describes one physical access route for a record.
type AccessMethod struct {
	AccessId       *string               `json:"access_id,omitempty"`
	AccessUrl      *AccessURL            `json:"access_url,omitempty"`
	Authorizations *AccessAuthorizations `json:"authorizations,omitempty"`
	Available      *bool                 `json:"available,omitempty"`
	Cloud          *string               `json:"cloud,omitempty"`
	Region         *string               `json:"region,omitempty"`
	Type           string                `json:"type"`
}

// Content is a nested bundle entry.  It is intentionally independent of the
// generated API's ContentsObject so persistence and object services can share
// it without importing HTTP code.
type Content struct {
	Contents *[]Content `json:"contents,omitempty"`
	DrsUri   *[]string  `json:"drs_uri,omitempty"`
	Id       *string    `json:"id,omitempty"`
	Name     string     `json:"name"`
}

// Candidate is the plain request value accepted by object registration and
// LFS metadata staging. HTTP adapters translate generated request models into
// this value before it crosses into core or persistence.
type Candidate struct {
	AccessMethods    *[]AccessMethod `json:"access_methods,omitempty"`
	Aliases          *[]string       `json:"aliases,omitempty"`
	Checksums        *[]Checksum     `json:"checksums,omitempty"`
	Contents         *[]Content      `json:"contents,omitempty"`
	ControlledAccess *[]string       `json:"controlled_access,omitempty"`
	Description      *string         `json:"description,omitempty"`
	MimeType         *string         `json:"mime_type,omitempty"`
	Name             *string         `json:"name,omitempty"`
	Size             *int64          `json:"size,omitempty"`
}

// Record is one physical object record. ControlledAccess is the sole modeled
// scope state carried by the record.
type Record struct {
	Id                    RecordID        `json:"id"`
	AccessMethods         *[]AccessMethod `json:"access_methods,omitempty"`
	Aliases               *[]string       `json:"aliases,omitempty"`
	Checksums             []Checksum      `json:"checksums"`
	Contents              *[]Content      `json:"contents,omitempty"`
	ControlledAccess      *[]string       `json:"controlled_access,omitempty"`
	CreatedTime           time.Time       `json:"created_time"`
	Description           *string         `json:"description,omitempty"`
	MimeType              *string         `json:"mime_type,omitempty"`
	Name                  *string         `json:"name,omitempty"`
	NameAliases           []string        `json:"name_aliases,omitempty"`
	Project               string          `json:"project"`
	PublicRead            bool            `json:"-"`
	PublicReadPolicyKnown bool            `json:"-"`
	SelfUri               string          `json:"self_uri"`
	Size                  int64           `json:"size"`
	UpdatedTime           *time.Time      `json:"updated_time,omitempty"`
	Version               *string         `json:"version,omitempty"`
}

// CanonicalContent is the prepared same-content view returned by
// checksum-aware reads.  Record is the merged presentation and Records keeps
// the physical replicas available to callers that need them.
type CanonicalContent struct {
	ContentID ContentID
	Record    Record
	Records   []Record
}
