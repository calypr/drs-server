package httpapi

import (
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/objects"
)

// fromInternalRecord is the one internal API boundary codec. Core object
// operations receive the generated DRS model directly.
func fromInternalRecord(value internalapi.InternalRecord) (drs.DrsObject, error) {
	size := int64(0)
	if value.Size != nil {
		size = *value.Size
	}

	record := drs.DrsObject{
		Id:            value.Did,
		Size:          size,
		CreatedTime:   parseRecordTime(value.CreatedTime),
		Version:       value.Version,
		Description:   value.Description,
		Name:          value.Name,
		NameAliases:   value.NameAliases,
		AccessMethods: value.AccessMethods,
	}
	if value.UpdatedTime != nil {
		updated := parseRecordTime(value.UpdatedTime)
		record.UpdatedTime = &updated
	}
	if value.Hashes != nil {
		record.Checksums = make([]drs.Checksum, 0, len(*value.Hashes))
		for typ, checksum := range *value.Hashes {
			record.Checksums = append(record.Checksums, drs.Checksum{Type: typ, Checksum: checksum})
		}
	}
	if value.ControlledAccess != nil {
		controlled := clientaccess.NormalizeAccessResources(*value.ControlledAccess)
		record.ControlledAccess = &controlled
	}
	return objects.NormalizeRecord(record, time.Time{})
}

func toInternalRecord(record drs.DrsObject) internalapi.InternalRecord {
	createdTime := record.CreatedTime.Format(time.RFC3339)
	name := ""
	if record.Name != nil {
		name = *record.Name
	}
	var aliases []string
	if record.NameAliases != nil {
		aliases = *record.NameAliases
	}
	nameAliases := objects.NormalizeNameAliases(name, aliases)
	size := record.Size
	result := internalapi.InternalRecord{
		Did:           record.Id,
		Size:          &size,
		CreatedTime:   &createdTime,
		Description:   record.Description,
		Name:          record.Name,
		NameAliases:   &nameAliases,
		Version:       record.Version,
		AccessMethods: record.AccessMethods,
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
		hashes := make(internalapi.HashInfo)
		for _, checksum := range record.Checksums {
			hashes[checksum.Type] = checksum.Checksum
		}
		result.Hashes = &hashes
	}
	return result
}

// projectGet keeps the internal GET response's historical omissions and id
// key while using the generated response model.
func projectGet(record drs.DrsObject) internalapi.InternalRecordResponse {
	response := internalapi.InternalRecordResponse{
		Id:               stringPointer(record.Id),
		Did:              record.Id,
		AccessMethods:    record.AccessMethods,
		ControlledAccess: record.ControlledAccess,
		Name:             record.Name,
		Description:      record.Description,
	}
	if record.Checksums != nil {
		checksums := append([]drs.Checksum(nil), record.Checksums...)
		response.Checksums = &checksums
	}
	if !record.CreatedTime.IsZero() {
		created := record.CreatedTime.Format(time.RFC3339)
		response.CreatedTime = &created
	}
	if record.UpdatedTime != nil {
		updated := record.UpdatedTime.Format(time.RFC3339)
		response.UpdatedTime = &updated
	}
	if record.Size > 0 {
		size := record.Size
		response.Size = &size
	}
	if record.NameAliases != nil && len(*record.NameAliases) > 0 {
		name := ""
		if record.Name != nil {
			name = *record.Name
		}
		aliases := objects.NormalizeNameAliases(name, *record.NameAliases)
		response.NameAliases = &aliases
	}
	for _, checksum := range record.Checksums {
		if checksum.Type == "" || checksum.Checksum == "" {
			continue
		}
		if response.Hashes == nil {
			hashes := make(internalapi.HashInfo)
			response.Hashes = &hashes
		}
		(*response.Hashes)[checksum.Type] = checksum.Checksum
	}
	return response
}

func stringPointer(value string) *string {
	return &value
}

func parseRecordTime(raw *string) time.Time {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.999999", "2006-01-02 15:04:05.999999", "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(*raw)); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}
