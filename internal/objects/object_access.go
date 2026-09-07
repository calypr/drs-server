package objects

import (
	syfoncommon "github.com/calypr/syfon/common"
)

// AccessResources returns the normalized controlled-access resources attached
// to a record.
func AccessResources(obj *Record) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return syfoncommon.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return syfoncommon.AuthzMapToList(obj.Authorizations)
}
