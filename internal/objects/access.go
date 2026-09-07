package objects

import (
	clientaccess "github.com/calypr/syfon/client/access"
)

// AccessResources returns the normalized controlled-access resources attached
// to a record.
func AccessResources(obj *Record) []string {
	if obj == nil {
		return nil
	}
	if obj.ControlledAccess != nil {
		return clientaccess.NormalizeAccessResources(*obj.ControlledAccess)
	}
	return clientaccess.AuthzMapToList(obj.Authorizations)
}
