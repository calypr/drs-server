package objects

import (
	"context"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
)

// RecordValidationError identifies the invalid record within an internal API request.
type RecordValidationError struct {
	Index int
	cause error
}

func (e *RecordValidationError) Error() string {
	return e.cause.Error()
}

func (e *RecordValidationError) Unwrap() error {
	return e.cause
}

func invalidInternalRecord(index int, cause error) error {
	return &RecordValidationError{Index: index, cause: cause}
}

func (s *Service) BulkOverwriteRecords(ctx context.Context, organization, project string, records []internalapi.InternalRecord) (internalapi.BulkOverwriteResponse, error) {
	candidates := make([]drs.DrsObject, len(records))
	for i, record := range records {
		candidate, err := objectFromInternalRecord(record)
		if err != nil {
			return internalapi.BulkOverwriteResponse{}, invalidInternalRecord(i, err)
		}
		candidates[i] = candidate
	}

	result, err := s.BulkOverwriteObjects(ctx, organization, project, candidates)
	if err != nil {
		return internalapi.BulkOverwriteResponse{}, err
	}
	return internalapi.BulkOverwriteResponse{
		Processed:       len(candidates),
		Created:         result.Created,
		Replaced:        result.Replaced,
		DidMatched:      result.DIDMatched,
		ChecksumMatched: result.ChecksumMatched,
	}, nil
}

func (s *Service) LookupRecordsByChecksums(ctx context.Context, hashes []string, requiredMethod string) (map[string][]internalapi.InternalRecord, error) {
	queries := make([]ChecksumQuery, len(hashes))
	for i, hash := range hashes {
		queries[i] = ChecksumQuery{Value: hash}
	}
	matches, err := s.LookupChecksumQueries(ctx, queries, requiredMethod)
	if err != nil {
		return nil, err
	}

	result := make(map[string][]internalapi.InternalRecord, len(hashes))
	for i, hash := range hashes {
		var records []drs.DrsObject
		if i < len(matches) {
			records = matches[i]
		}
		result[hash] = internalRecordsFromObjects(records)
	}
	return result, nil
}

func (s *Service) GetRecord(ctx context.Context, id, requiredMethod string) (internalapi.InternalRecordResponse, error) {
	record, err := s.GetObject(ctx, id, requiredMethod)
	if err != nil {
		return internalapi.InternalRecordResponse{}, err
	}
	return internalRecordResponseFromObject(*record), nil
}

func (s *Service) ListRecords(ctx context.Context, query RecordListQuery) (internalapi.ListRecordsResponse, error) {
	records, err := s.ListObjects(ctx, query)
	if err != nil {
		return internalapi.ListRecordsResponse{}, err
	}
	result := internalRecordsFromObjects(records)
	return internalapi.ListRecordsResponse{Records: &result}, nil
}

func (s *Service) GetRecords(ctx context.Context, ids []string, requiredMethod string) ([]internalapi.InternalRecord, error) {
	records, err := s.GetBulkObjects(ctx, ids, requiredMethod)
	if err != nil {
		return nil, err
	}
	return internalRecordsFromObjects(records), nil
}

func (s *Service) CreateRecords(ctx context.Context, records []internalapi.InternalRecord) ([]internalapi.InternalRecord, error) {
	now := time.Now().UTC()
	prepared := make([]drs.DrsObject, len(records))
	for i, value := range records {
		record, err := objectFromInternalRecord(value)
		if err != nil {
			return nil, invalidInternalRecord(i, err)
		}
		scope, err := scopeFromInternalRecord(value)
		if err != nil {
			return nil, invalidInternalRecord(i, err)
		}
		record, err = enforceCanonicalProjectScope(record, scope.Organization, scope.Project)
		if err != nil {
			return nil, invalidInternalRecord(i, err)
		}
		prepared[i] = materializeRecordTime(record, now)
	}
	if err := s.RegisterObjects(ctx, prepared); err != nil {
		return nil, err
	}
	created := internalRecordsFromObjects(prepared)
	for i := range created {
		created[i].Name = nil
	}
	return created, nil
}

func (s *Service) RemoveRecordControlledAccess(ctx context.Context, id, resource string) (internalapi.InternalRecord, error) {
	record, err := s.RemoveObjectControlledAccess(ctx, id, resource)
	if err != nil {
		return internalapi.InternalRecord{}, err
	}
	return internalRecordFromObject(*record), nil
}

func (s *Service) UpdateRecord(ctx context.Context, id string, value internalapi.InternalRecord) (internalapi.InternalRecord, error) {
	if strings.TrimSpace(value.Did) == "" {
		value.Did = id
	}
	update, err := objectFromInternalRecord(value)
	if err != nil {
		return internalapi.InternalRecord{}, invalidInternalRecord(0, err)
	}
	scope, err := scopeFromInternalRecord(value)
	if err != nil {
		return internalapi.InternalRecord{}, invalidInternalRecord(0, err)
	}
	merged, err := s.UpdateObjectMetadata(ctx, id, update, scope, value.Size)
	if err != nil {
		return internalapi.InternalRecord{}, err
	}
	return internalRecordFromObject(merged), nil
}

func objectFromInternalRecord(value internalapi.InternalRecord) (drs.DrsObject, error) {
	size := int64(0)
	if value.Size != nil {
		size = *value.Size
	}

	record := drs.DrsObject{
		Id:            value.Did,
		Size:          size,
		CreatedTime:   parseInternalRecordTime(value.CreatedTime),
		Version:       value.Version,
		Description:   value.Description,
		Name:          value.Name,
		NameAliases:   value.NameAliases,
		AccessMethods: value.AccessMethods,
	}
	if value.UpdatedTime != nil {
		updated := parseInternalRecordTime(value.UpdatedTime)
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
	return NormalizeRecord(record, time.Time{})
}

func internalRecordFromObject(record drs.DrsObject) internalapi.InternalRecord {
	createdTime := record.CreatedTime.Format(time.RFC3339)
	name := ""
	if record.Name != nil {
		name = *record.Name
	}
	var aliases []string
	if record.NameAliases != nil {
		aliases = *record.NameAliases
	}
	nameAliases := NormalizeNameAliases(name, aliases)
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
	if record.ControlledAccess != nil {
		values := append([]string(nil), (*record.ControlledAccess)...)
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

func internalRecordsFromObjects(records []drs.DrsObject) []internalapi.InternalRecord {
	result := make([]internalapi.InternalRecord, len(records))
	for i, record := range records {
		result[i] = internalRecordFromObject(record)
	}
	return result
}

func internalRecordResponseFromObject(record drs.DrsObject) internalapi.InternalRecordResponse {
	id := record.Id
	response := internalapi.InternalRecordResponse{
		Id:               &id,
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
		aliases := NormalizeNameAliases(name, *record.NameAliases)
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

func scopeFromInternalRecord(value internalapi.InternalRecord) (Scope, error) {
	organization, project := "", ""
	if value.Organization != nil {
		organization = *value.Organization
	}
	if value.Project != nil {
		project = *value.Project
	}
	return NewScope(organization, project)
}

func parseInternalRecordTime(raw *string) time.Time {
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
