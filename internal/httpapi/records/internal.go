package records

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	generated "github.com/calypr/syfon/apigen/server/internalapi"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/objects"
)

// FromInternalRecord translates the generated internal-index record into a
// domain record. Scope policy is applied by the core caller after translation.
func FromInternalRecord(value generated.InternalRecord, now time.Time) (objects.Record, error) {
	id := strings.TrimSpace(value.Did)
	if id == "" {
		return objects.Record{}, fmt.Errorf("did is required")
	}

	record := objects.Record{
		Id:          objects.RecordID(id),
		Size:        int64Value(value.Size),
		CreatedTime: parseRecordTime(value.CreatedTime, now),
		Version:     stringPointerOrDefault(value.Version, "1"),
		Description: value.Description,
		Properties:  map[string]json.RawMessage{},
	}
	updated := parseRecordTime(value.UpdatedTime, record.CreatedTime)
	record.UpdatedTime = &updated
	if value.Name != nil && strings.TrimSpace(*value.Name) != "" {
		record.Name = normalizedRecordName(value.Name)
	}
	if value.Hashes != nil {
		record.Checksums = make([]objects.Checksum, 0, len(*value.Hashes))
		for typ, checksum := range *value.Hashes {
			if objects.NormalizeChecksumType(typ) == "sha256" {
				if normalized := syfoncommon.NormalizeOid(checksum); normalized != "" {
					typ, checksum = "sha256", normalized
				}
			}
			record.Checksums = append(record.Checksums, objects.Checksum{Type: typ, Checksum: checksum})
		}
	}
	if value.ControlledAccess != nil {
		controlled := syfoncommon.NormalizeAccessResources(*value.ControlledAccess)
		record.ControlledAccess = &controlled
		record.Authorizations = syfoncommon.ControlledAccessToAuthzMap(controlled)
	}
	if value.AccessMethods != nil {
		methods := fromGeneratedAccessMethods(*value.AccessMethods)
		record.AccessMethods = &methods
	}
	record.NameAliases = objects.NormalizeNameAliases(recordStringValue(record.Name), dereferenceStrings(value.NameAliases))
	return record, nil
}

// ToInternalRecord translates a domain record to the generated internal-index
// response model.
func ToInternalRecord(record objects.Record) generated.InternalRecord {
	result := generated.InternalRecord{
		Did:           string(record.Id),
		Size:          &record.Size,
		CreatedTime:   stringPtr(record.CreatedTime.Format(time.RFC3339)),
		Description:   record.Description,
		Name:          record.Name,
		NameAliases:   stringSlicePtr(objects.NormalizeNameAliases(recordStringValue(record.Name), record.NameAliases)),
		Version:       record.Version,
		AccessMethods: toGeneratedAccessMethods(record.AccessMethods),
	}
	if controlled := record.ControlledAccess; controlled != nil {
		values := append([]string(nil), (*controlled)...)
		result.ControlledAccess = &values
	} else if resources := syfoncommon.AuthzMapToControlledAccess(record.Authorizations); len(resources) > 0 {
		result.ControlledAccess = &resources
	}
	if record.UpdatedTime != nil {
		result.UpdatedTime = stringPtr(record.UpdatedTime.Format(time.RFC3339))
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

// ToInternalRecordResponse translates a domain record to the generated
// internal-index response envelope.
func ToInternalRecordResponse(record objects.Record) generated.InternalRecordResponse {
	value := ToInternalRecord(record)
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

func normalizedRecordName(value *string) *string {
	if value == nil {
		return nil
	}
	base := objects.CleanToBasename(*value)
	if base == "" {
		return nil
	}
	return &base
}

func int64Value(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func stringPointerOrDefault(value *string, fallback string) *string {
	if value != nil {
		return value
	}
	return &fallback
}

func recordStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func dereferenceStrings(value *[]string) []string {
	if value == nil {
		return nil
	}
	return append([]string(nil), (*value)...)
}

func stringPtr(value string) *string { return &value }

func stringSlicePtr(value []string) *[]string { return &value }
