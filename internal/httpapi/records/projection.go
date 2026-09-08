package records

import (
	"time"

	"github.com/calypr/syfon/internal/objects"
)

type getResponse struct {
	ID               string                  `json:"id,omitempty"`
	DID              string                  `json:"did"`
	Checksums        []objects.Checksum      `json:"checksums,omitempty"`
	Hashes           map[string]string       `json:"hashes,omitempty"`
	AccessMethods    *[]objects.AccessMethod `json:"access_methods,omitempty"`
	ControlledAccess *[]string               `json:"controlled_access,omitempty"`
	Created          string                  `json:"created_time,omitempty"`
	Updated          *string                 `json:"updated_time,omitempty"`
	Name             *string                 `json:"name,omitempty"`
	NameAliases      *[]string               `json:"name_aliases,omitempty"`
	Description      *string                 `json:"description,omitempty"`
	Size             int64                   `json:"size,omitempty"`
}

func projectGet(record objects.Record) getResponse {
	response := getResponse{
		ID:               string(record.Id),
		DID:              string(record.Id),
		Checksums:        record.Checksums,
		AccessMethods:    record.AccessMethods,
		ControlledAccess: record.ControlledAccess,
		Name:             record.Name,
		Description:      record.Description,
	}
	if !record.CreatedTime.IsZero() {
		response.Created = record.CreatedTime.Format(time.RFC3339)
	}
	if record.UpdatedTime != nil {
		updated := record.UpdatedTime.Format(time.RFC3339)
		response.Updated = &updated
	}
	if record.Size > 0 {
		response.Size = record.Size
	}
	if len(record.NameAliases) > 0 {
		name := ""
		if record.Name != nil {
			name = *record.Name
		}
		aliases := objects.NormalizeNameAliases(name, record.NameAliases)
		response.NameAliases = &aliases
	}
	for _, checksum := range record.Checksums {
		if checksum.Type == "" || checksum.Checksum == "" {
			continue
		}
		if response.Hashes == nil {
			response.Hashes = make(map[string]string)
		}
		response.Hashes[checksum.Type] = checksum.Checksum
	}
	return response
}
