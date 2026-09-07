package models

import "github.com/calypr/syfon/apigen/server/drs"

// InternalObject is the primary DRS domain model. It wraps the GA4GH DrsObject
// and adds Syfon-specific authorization metadata.
type InternalObject struct {
	drs.DrsObject
	NameAliases    []string               `json:"name_aliases,omitempty"`
	Authorizations map[string][]string    `json:"-"`
	Properties     map[string]interface{} `json:"-"`
	// PublicRead is internal policy state. It is deliberately excluded from
	// the wire representation and is populated by the database projection.
	PublicRead bool `json:"-"`
	// PublicReadPolicyKnown distinguishes an explicit protected policy from a
	// legacy row whose policy table entry has not been backfilled.
	PublicReadPolicyKnown bool `json:"-"`
}

// DrsObjectWithAuthz is an alias for InternalObject retained for older Go call sites.
type DrsObjectWithAuthz = InternalObject

func (o InternalObject) External() drs.DrsObject {
	return o.DrsObject
}
