package httpapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/internalapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func (s *internalServer) InternalBulkOverwrite(c fiber.Ctx) error {
	var req internalapi.BulkOverwriteRequest
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Records) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: organization, project, and records are required")
	}
	if len(req.Records) > maxInternalBulkOverwrite {
		return Reject(c, fiber.StatusRequestEntityTooLarge, fmt.Sprintf("too many records: maximum is %d", maxInternalBulkOverwrite))
	}
	scope, err := objects.NewScope(req.Organization, req.Project)
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, err.Error())
	}

	candidates := make([]objects.Record, 0, len(req.Records))
	for i, record := range req.Records {
		obj, err := fromInternalRecord(record, time.Time{})
		if err != nil {
			return Reject(c, fiber.StatusBadRequest, fmt.Sprintf("Invalid request body: record[%d] invalid: %v", i, err))
		}
		candidates = append(candidates, obj)
	}

	result, err := s.objects.BulkOverwriteObjects(c.Context(), scope.Organization, scope.Project, candidates)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(internalapi.BulkOverwriteResponse{
		Processed:       len(candidates),
		Created:         result.Created,
		Replaced:        result.Replaced,
		DidMatched:      result.DIDMatched,
		ChecksumMatched: result.ChecksumMatched,
	})
}

func (s *internalServer) InternalBulkMissingSHA256(c fiber.Ctx) error {
	var req internalapi.BulkMissingSHA256Request
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Sha256) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: organization, project, and sha256 values are required")
	}

	normalized := make([]string, 0, len(req.Sha256))
	seen := make(map[string]struct{}, len(req.Sha256))
	for _, raw := range req.Sha256 {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		value, ok := objects.NormalizeSHA256Query(raw)
		if !ok {
			return Reject(c, fiber.StatusBadRequest, fmt.Sprintf("invalid sha256 checksum %q", raw))
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	if len(normalized) == 0 {
		return Reject(c, fiber.StatusBadRequest, "invalid request body: sha256 values are required")
	}
	if len(normalized) > maxInternalBulkMissingSHA256 {
		return Reject(c, fiber.StatusRequestEntityTooLarge, fmt.Sprintf("too many sha256 values: maximum is %d", maxInternalBulkMissingSHA256))
	}

	missing, err := s.objects.ListMissingScopedSHA256(c.Context(), req.Organization, req.Project, normalized)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(internalapi.BulkMissingSHA256Response{Checked: int32(len(normalized)), MissingSha256: missing})
}

func (s *internalServer) InternalBulkHashes(c fiber.Ctx) error {
	var req internalapi.BulkHashesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	queries := make([]objects.ChecksumQuery, 0, len(req.Hashes))
	for _, raw := range req.Hashes {
		typ, value := objects.ParseHashQuery(raw, "")
		queries = append(queries, objects.ChecksumQuery{Type: typ, Value: value})
	}
	matches, err := s.objects.LookupChecksumQueries(c.Context(), queries, "read")
	if err != nil {
		return HandleError(c, err)
	}

	finalRes := make(map[string][]internalapi.InternalRecord, len(req.Hashes))
	for i, h := range req.Hashes {
		var records []objects.Record
		if i < len(matches) {
			records = matches[i].Records
		}
		compatibilityMatches := make([]internalapi.InternalRecord, 0, len(records))
		for _, match := range records {
			compatibilityMatches = append(compatibilityMatches, toInternalRecord(match))
		}
		finalRes[h] = compatibilityMatches
	}

	return c.JSON(struct {
		Results map[string][]internalapi.InternalRecord
	}{Results: finalRes})
}

func (s *internalServer) InternalBulkSHA256Validity(c fiber.Ctx) error {
	var req internalapi.BulkSHA256ValidityRequest
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Sha256 == nil || len(*req.Sha256) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: sha256 values are required")
	}

	hashes := make([]string, 0, len(*req.Sha256))
	out := make(map[string]bool, len(*req.Sha256))
	for _, raw := range *req.Sha256 {
		hash := strings.TrimSpace(raw)
		if hash == "" {
			continue
		}
		hashes = append(hashes, hash)
		out[hash] = false
	}
	if len(hashes) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: sha256 values are required")
	}

	records, err := s.objects.GetObjectsByChecksums(c.Context(), hashes, "read")
	if err != nil {
		return HandleError(c, err)
	}
	for _, hash := range hashes {
		for _, obj := range records[hash] {
			if objects.RecordHasChecksumTypeAndValue(obj, "sha256", hash) {
				out[hash] = true
				break
			}
		}
	}
	return c.JSON(out)
}

func (s *internalServer) InternalDelete(c fiber.Ctx, _ string) error {
	id := c.Params("id")
	if err := s.objects.DeleteObject(c.Context(), id, objects.DeleteOptions{}); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *internalServer) InternalDeleteByQuery(c fiber.Ctx, _ internalapi.InternalDeleteByQueryParams) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	scope, err := scopeFromQuery(c.Query("organization"), c.Query("program"), c.Query("project"))
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, err.Error())
	}
	if scope.Organization == "" {
		return Reject(c, fiber.StatusBadRequest, "No scope specified")
	}

	count, err := s.objects.DeleteBulkByScope(c.Context(), scope.Organization, scope.Project)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &count})
}

func (s *internalServer) InternalBulkDeleteHashes(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}

	var req internalapi.BulkHashesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(req.Hashes) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: hashes are required")
	}

	normalized := make([]string, 0, len(req.Hashes))
	for _, h := range req.Hashes {
		_, val := objects.ParseHashQuery(h, "")
		if strings.TrimSpace(val) == "" {
			continue
		}
		normalized = append(normalized, val)
	}
	if len(normalized) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: hashes are required")
	}

	deleted, err := s.objects.DeleteObjectsByChecksums(c.Context(), normalized)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &deleted})
}

const (
	defaultInternalListLimit     = 1000
	maxInternalListLimit         = 10000
	maxInternalBulkMissingSHA256 = 10000
	maxInternalBulkOverwrite     = 1000
)

func (s *internalServer) InternalGet(c fiber.Ctx, _ string) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	id := c.Params("id")
	obj, err := s.objects.GetObject(c.Context(), id, "read")
	if err != nil {
		return HandleError(c, err)
	}
	encoded, err := json.Marshal(projectGet(*obj))
	if err != nil {
		return HandleError(c, err)
	}
	c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return c.Send(encoded)
}

func (s *internalServer) InternalList(c fiber.Ctx, _ internalapi.InternalListParams) error {
	var (
		limit int
		start string
		page  int
		err   error
	)
	hash := c.Query("hash")
	if hash != "" {
		// Preserve the checksum branch's existing error precedence: raw
		// integer syntax is rejected before the typed scope is validated.
		limit, start, page, err = parseInternalListPageFiber(c)
		if err != nil {
			return Reject(c, fiber.StatusBadRequest, err.Error())
		}
	}

	scope, err := scopeFromQuery(c.Query("organization"), c.Query("program"), c.Query("project"))
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, err.Error())
	}
	if hash == "" {
		limit, start, page, err = parseInternalListPageFiber(c)
	}
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, err.Error())
	}

	query := objects.RecordListQuery{
		Scope:          scope,
		ObjectURL:      strings.TrimSpace(c.Query("url")),
		StartAfter:     start,
		Limit:          limit,
		Page:           page,
		RequiredMethod: "read",
	}
	if hash != "" {
		hashType, hashValue := objects.ParseHashQuery(hash, c.Query("hash_type"))
		query.Checksum = &objects.ChecksumQuery{Type: hashType, Value: hashValue}
	}
	objs, err := s.objects.ListPreparedPage(c.Context(), query)
	if err != nil {
		return HandleError(c, err)
	}
	records := make([]internalapi.InternalRecord, 0, len(objs))
	for _, obj := range objs {
		records = append(records, toInternalRecord(obj))
	}
	return c.JSON(internalapi.ListRecordsResponse{Records: &records})
}

func scopeFromQuery(organization, program, project string) (objects.Scope, error) {
	org := strings.TrimSpace(organization)
	if org == "" {
		org = strings.TrimSpace(program)
	}
	return objects.NewScope(org, project)
}

func (s *internalServer) InternalBulkDocuments(c fiber.Ctx) error {
	var req internalapi.BulkDocumentsRequest
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	var ids []string
	if arr, err := req.AsBulkDocumentsRequest0(); err == nil {
		ids = append(ids, arr...)
	}
	if obj, err := req.AsBulkDocumentsRequest1(); err == nil {
		if obj.Ids != nil {
			ids = append(ids, (*obj.Ids)...)
		}
	}
	if len(ids) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: ids are required")
	}

	records, err := s.objects.GetBulkObjects(c.Context(), ids, "read")
	if err != nil {
		return HandleError(c, err)
	}

	out := make([]internalapi.InternalRecordResponse, 0, len(records))
	for _, obj := range records {
		out = append(out, toInternalRecordResponse(obj))
	}
	return c.JSON(out)
}

func parseInternalListPageFiber(c fiber.Ctx) (int, string, int, error) {
	limit := defaultInternalListLimit
	rawLimit := strings.TrimSpace(c.Query("limit"))
	if rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil {
			return 0, "", 0, fmt.Errorf("limit must be an integer")
		}
		if parsed < 0 {
			return 0, "", 0, fmt.Errorf("limit must be >= 0")
		}
		limit = parsed
	}
	if limit > maxInternalListLimit {
		limit = maxInternalListLimit
	}

	start := strings.TrimSpace(c.Query("start"))
	page := 0
	if start == "" {
		rawPage := strings.TrimSpace(c.Query("page"))
		if rawPage != "" {
			parsedPage, err := strconv.Atoi(rawPage)
			if err != nil {
				return 0, "", 0, fmt.Errorf("page must be an integer")
			}
			if parsedPage < 0 {
				return 0, "", 0, fmt.Errorf("page must be >= 0")
			}
			page = parsedPage
		}
	}
	return limit, start, page, nil
}

func (s *internalServer) InternalCreate(c fiber.Ctx) error {
	candidates, err := decodeInternalCreateCandidates(c)
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if err := s.objects.RegisterScopedObjects(c.Context(), candidates); err != nil {
		return HandleError(c, err)
	}

	if strings.HasSuffix(c.Path(), "/bulk") {
		records := make([]internalapi.InternalRecord, len(candidates))
		for i, scoped := range candidates {
			records[i] = toInternalRecord(scoped.Record)
		}
		return c.Status(fiber.StatusCreated).JSON(internalapi.ListRecordsResponse{Records: &records})
	}
	return c.Status(fiber.StatusCreated).JSON(toInternalRecordResponse(candidates[0].Record))
}

func (s *internalServer) InternalBulkCreate(c fiber.Ctx) error {
	return s.InternalCreate(c)
}

func decodeInternalCreateCandidates(c fiber.Ctx) ([]objects.ScopedRecord, error) {
	var bulkReq internalapi.BulkCreateRequest
	candidates := make([]objects.ScopedRecord, 0)
	if err := c.Bind().JSON(&bulkReq); err == nil && len(bulkReq.Records) > 0 {
		for i, r := range bulkReq.Records {
			obj, err := fromInternalRecord(r, time.Time{})
			if err != nil {
				return nil, fmt.Errorf("record[%d] invalid: %w", i, err)
			}
			scope, err := internalRecordScope(r)
			if err != nil {
				return nil, fmt.Errorf("record[%d] invalid: %w", i, err)
			}
			candidates = append(candidates, objects.ScopedRecord{Record: obj, Scope: scope})
		}
		return candidates, nil
	}

	var singleReq internalapi.InternalRecord
	if err := c.Bind().JSON(&singleReq); err == nil && singleReq.Did != "" {
		obj, err := fromInternalRecord(singleReq, time.Time{})
		if err != nil {
			return nil, fmt.Errorf("record invalid: %w", err)
		}
		scope, err := internalRecordScope(singleReq)
		if err != nil {
			return nil, fmt.Errorf("record invalid: %w", err)
		}
		candidates = append(candidates, objects.ScopedRecord{Record: obj, Scope: scope})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no records found")
	}
	return candidates, nil
}

func internalRecordScope(value internalapi.InternalRecord) (objects.Scope, error) {
	organization, project := "", ""
	if value.Organization != nil {
		organization = *value.Organization
	}
	if value.Project != nil {
		project = *value.Project
	}
	return objects.NewScope(organization, project)
}

func (s *internalServer) InternalRemoveControlledAccess(c fiber.Ctx, _ string) error {
	id := strings.TrimSpace(c.Params("id"))
	var req internalapi.ControlledAccessRemoveRequest
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Resource) == "" {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	obj, err := s.objects.RemoveObjectControlledAccess(c.Context(), id, req.Resource)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(toInternalRecordResponse(*obj))
}

func (s *internalServer) InternalUpdate(c fiber.Ctx, _ string) error {
	id := c.Params("id")
	var req internalapi.InternalRecord
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if strings.TrimSpace(req.Did) == "" {
		req.Did = id
	}
	scope, err := internalRecordScope(req)
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	update, err := fromInternalRecord(req, time.Time{})
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	merged, err := s.objects.UpdateRecordInScope(c.Context(), id, scope, update, req.Size)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(toInternalRecordResponse(merged))
}

func fromInternalRecord(value internalapi.InternalRecord, now time.Time) (objects.Record, error) {
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
		methods := drsFromGeneratedAccessMethods(*value.AccessMethods)
		record.AccessMethods = &methods
	}
	if value.NameAliases != nil {
		record.NameAliases = append([]string(nil), (*value.NameAliases)...)
	}
	return objects.NormalizeRecord(record, now)
}

func toInternalRecord(record objects.Record) internalapi.InternalRecord {
	createdTime := record.CreatedTime.Format(time.RFC3339)
	name := ""
	if record.Name != nil {
		name = *record.Name
	}
	nameAliases := objects.NormalizeNameAliases(name, record.NameAliases)
	result := internalapi.InternalRecord{
		Did:           string(record.Id),
		Size:          &record.Size,
		CreatedTime:   &createdTime,
		Description:   record.Description,
		Name:          record.Name,
		NameAliases:   &nameAliases,
		Version:       record.Version,
		AccessMethods: drsToGeneratedAccessMethods(record.AccessMethods),
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

func toInternalRecordResponse(record objects.Record) internalapi.InternalRecordResponse {
	value := toInternalRecord(record)
	return internalapi.InternalRecordResponse{
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
