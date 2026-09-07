package objects

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	clientaccess "github.com/calypr/syfon/client/access"
)

// EnforceCanonicalProjectScope adds the exact project resource to a record's
// controlled-access set. It is the domain-side normalization used by record
// adapters before registration or overwrite.
func EnforceCanonicalProjectScope(obj Record, organization, project string) (Record, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if project != "" && organization == "" {
		return Record{}, fmt.Errorf("organization is required when project is set")
	}
	if organization == "" || project == "" {
		return obj, nil
	}

	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return Record{}, err
	}
	controlled := append(AccessResources(&obj), resource)
	controlled = clientaccess.NormalizeAccessResources(controlled)
	obj.ControlledAccess = &controlled
	obj.Authorizations = clientaccess.ControlledAccessToAuthzMap(controlled)
	return obj, nil
}

// MergeRecordUpdate applies the mutable fields from update while retaining
// immutable identity and existing fields that were omitted by the caller.
func MergeRecordUpdate(existing Record, update Record, id string, now time.Time) (Record, error) {
	merged := existing
	merged.Id = RecordID(id)
	merged.UpdatedTime = &now
	if update.Properties != nil {
		if merged.Properties == nil {
			merged.Properties = make(map[string]json.RawMessage, len(update.Properties))
		}
		for key, value := range update.Properties {
			merged.Properties[key] = value
		}
	}

	if update.Name != nil {
		name := CleanToBasename(*update.Name)
		if name == "" {
			merged.Name = nil
		} else {
			merged.Name = objectStringPtr(name)
		}
	}
	if update.Description != nil {
		merged.Description = update.Description
	}
	if update.MimeType != nil {
		merged.MimeType = update.MimeType
	}
	if update.Version != nil {
		merged.Version = update.Version
	}
	if update.Aliases != nil {
		merged.Aliases = update.Aliases
	}
	if update.Authorizations != nil {
		merged.Authorizations = update.Authorizations
	}
	if update.ControlledAccess != nil {
		merged.ControlledAccess = update.ControlledAccess
		merged.Authorizations = clientaccess.ControlledAccessToAuthzMap(*update.ControlledAccess)
	}
	if update.AccessMethods != nil {
		merged.AccessMethods = update.AccessMethods
	}
	if update.Checksums != nil {
		merged.Checksums = MergeAdditionalChecksums(existing.Checksums, update.Checksums)
	}

	return merged, nil
}
