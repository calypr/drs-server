package httpapi

import (
	"strings"
	"time"

	generated "github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
	drsapi "github.com/calypr/syfon/internal/httpapi/drs"
	"github.com/calypr/syfon/internal/objects"
)

func fromInternalRecord(value generated.InternalRecord, now time.Time) (objects.Record, error) {
	size := int64(0)
	if value.Size != nil {
		size = *value.Size
	}

	record := objects.Record{
		Id:          objects.RecordID(value.Did),
		Size:        size,
		CreatedTime: parseRecordTime(value.CreatedTime, time.Time{}),
		Version:     value.Version,
		Description: value.Description,
	}
	if value.UpdatedTime != nil {
		updated := parseRecordTime(value.UpdatedTime, time.Time{})
		record.UpdatedTime = &updated
	}
	if value.Hashes != nil {
		record.Checksums = make([]objects.Checksum, 0, len(*value.Hashes))
		for typ, checksum := range *value.Hashes {
			record.Checksums = append(record.Checksums, objects.Checksum{Type: typ, Checksum: checksum})
		}
	}
	if value.ControlledAccess != nil {
		controlled := clientaccess.NormalizeAccessResources(*value.ControlledAccess)
		record.ControlledAccess = &controlled
	}
	if value.AccessMethods != nil {
		methods := drsapi.FromGeneratedAccessMethods(*value.AccessMethods)
		record.AccessMethods = &methods
	}
	if value.NameAliases != nil {
		record.NameAliases = append([]string(nil), (*value.NameAliases)...)
	}
	return objects.NormalizeRecord(record, now)
}

func toInternalRecord(record objects.Record) generated.InternalRecord {
	createdTime := record.CreatedTime.Format(time.RFC3339)
	name := ""
	if record.Name != nil {
		name = *record.Name
	}
	nameAliases := objects.NormalizeNameAliases(name, record.NameAliases)
	result := generated.InternalRecord{
		Did:           string(record.Id),
		Size:          &record.Size,
		CreatedTime:   &createdTime,
		Description:   record.Description,
		Name:          record.Name,
		NameAliases:   &nameAliases,
		Version:       record.Version,
		AccessMethods: drsapi.ToGeneratedAccessMethods(record.AccessMethods),
	}
	if controlled := record.ControlledAccess; controlled != nil {
		values := append([]string(nil), (*controlled)...)
		result.ControlledAccess = &values
	}
	if record.UpdatedTime != nil {
		updatedTime := record.UpdatedTime.Format(time.RFC3339)
		result.UpdatedTime = &updatedTime
	}
	if len(record.Checksums) > 0 {
		hashes := make(generated.HashInfo)
		for _, checksum := range record.Checksums {
			hashes[checksum.Type] = checksum.Checksum
		}
		result.Hashes = &hashes
	}
	return result
}

func toInternalRecordResponse(record objects.Record) generated.InternalRecordResponse {
	value := toInternalRecord(record)
	return generated.InternalRecordResponse{
		Did:              value.Did,
		AccessMethods:    value.AccessMethods,
		ControlledAccess: value.ControlledAccess,
		Size:             value.Size,
		CreatedTime:      value.CreatedTime,
		Description:      value.Description,
		Name:             value.Name,
		NameAliases:      value.NameAliases,
		Version:          value.Version,
		UpdatedTime:      value.UpdatedTime,
		Hashes:           value.Hashes,
		Organization:     value.Organization,
		Project:          value.Project,
	}
}

func parseRecordTime(raw *string, fallback time.Time) time.Time {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return fallback.UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(*raw)); err == nil {
			return parsed.UTC()
		}
	}
	return fallback.UTC()
}

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
