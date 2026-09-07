// Package objects owns the values used to describe persisted DRS records and
// their checksum-derived views.  It deliberately has no dependency on the
// generated HTTP contract: adapters translate at the boundary.
package objects

import (
	"encoding/json"
	"time"
)

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

// Record is one physical object record.  Extension fields are retained only
// while the value is in memory; SQL hydration preserves the historical
// behavior and does not claim to persist unknown properties.
type Record struct {
	Id                    RecordID                   `json:"id"`
	AccessMethods         *[]AccessMethod            `json:"access_methods,omitempty"`
	Aliases               *[]string                  `json:"aliases,omitempty"`
	Authorizations        map[string][]string        `json:"-"`
	Checksums             []Checksum                 `json:"checksums"`
	Contents              *[]Content                 `json:"contents,omitempty"`
	ControlledAccess      *[]string                  `json:"controlled_access,omitempty"`
	CreatedTime           time.Time                  `json:"created_time"`
	Description           *string                    `json:"description,omitempty"`
	MimeType              *string                    `json:"mime_type,omitempty"`
	Name                  *string                    `json:"name,omitempty"`
	NameAliases           []string                   `json:"name_aliases,omitempty"`
	Properties            map[string]json.RawMessage `json:"-"`
	Project               string                     `json:"project"`
	PublicRead            bool                       `json:"-"`
	PublicReadPolicyKnown bool                       `json:"-"`
	SelfUri               string                     `json:"self_uri"`
	Size                  int64                      `json:"size"`
	UpdatedTime           *time.Time                 `json:"updated_time,omitempty"`
	Version               *string                    `json:"version,omitempty"`
}

// CanonicalContent is the prepared same-content view returned by
// checksum-aware reads.  Record is the merged presentation and Records keeps
// the physical replicas available to callers that need them.
type CanonicalContent struct {
	ContentID ContentID
	Record    Record
	Records   []Record
}
