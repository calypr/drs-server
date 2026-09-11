package httpapi

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
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

	candidates := make([]drs.DrsObject, len(req.Records))
	for i, record := range req.Records {
		candidate, err := fromInternalRecord(record)
		if err != nil {
			return Reject(c, fiber.StatusBadRequest, fmt.Sprintf("Invalid request body: record[%d] invalid: %v", i, err))
		}
		candidates[i] = candidate
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

	queries := make([]objects.ChecksumQuery, len(req.Hashes))
	for i, raw := range req.Hashes {
		queries[i] = objects.ChecksumQuery{Value: raw}
	}
	matches, err := s.objects.LookupChecksumQueries(c.Context(), queries, "read")
	if err != nil {
		return HandleError(c, err)
	}
	result := make(map[string][]internalapi.InternalRecord, len(req.Hashes))
	for i, hash := range req.Hashes {
		var records []drs.DrsObject
		if i < len(matches) {
			records = matches[i]
		}
		converted := make([]internalapi.InternalRecord, len(records))
		for j, record := range records {
			converted[j] = toInternalRecord(record)
		}
		result[hash] = converted
	}
	return c.JSON(struct {
		Results map[string][]internalapi.InternalRecord
	}{Results: result})
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
		out[hash] = len(records[hash]) > 0
	}
	return c.JSON(out)
}

func (s *internalServer) InternalDelete(c fiber.Ctx, _ string) error {
	id := c.Params("id")
	if err := s.objects.DeleteObject(c.Context(), id); err != nil {
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
	record, err := s.objects.GetObject(c.Context(), id, "read")
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(toInternalRecordResponse(*record))
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
	objects, err := s.objects.ListObjects(c.Context(), query)
	if err != nil {
		return HandleError(c, err)
	}
	records := make([]internalapi.InternalRecord, len(objects))
	for i, object := range objects {
		records[i] = toInternalRecord(object)
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

	objects, err := s.objects.GetBulkObjects(c.Context(), ids, "read")
	if err != nil {
		return HandleError(c, err)
	}
	records := make([]internalapi.InternalRecord, len(objects))
	for i, object := range objects {
		records[i] = toInternalRecord(object)
	}
	return c.JSON(records)
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
	candidates, err := decodeInternalCreateObjects(c)
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	created, err := s.objects.RegisterScopedObjects(c.Context(), candidates)
	if err != nil {
		return HandleError(c, err)
	}
	converted := make([]internalapi.InternalRecord, len(created))
	for i, record := range created {
		converted[i] = toInternalRecord(record)
		converted[i].Name = nil
	}

	if strings.HasSuffix(c.Path(), "/bulk") {
		return c.Status(fiber.StatusCreated).JSON(internalapi.ListRecordsResponse{Records: &converted})
	}
	return c.Status(fiber.StatusCreated).JSON(converted[0])
}

func (s *internalServer) InternalBulkCreate(c fiber.Ctx) error {
	return s.InternalCreate(c)
}

func decodeInternalCreateObjects(c fiber.Ctx) ([]objects.ScopedObject, error) {
	var request internalapi.BulkCreateRequest
	bulk := c.Bind().JSON(&request) == nil && len(request.Records) > 0
	if !bulk {
		var single internalapi.InternalRecord
		if err := c.Bind().JSON(&single); err != nil || single.Did == "" {
			return nil, fmt.Errorf("no records found")
		}
		request.Records = []internalapi.InternalRecord{single}
	}
	candidates := make([]objects.ScopedObject, len(request.Records))
	for i, value := range request.Records {
		record, err := fromInternalRecord(value)
		if err == nil {
			var scope objects.Scope
			scope, err = scopeFromInternalRecord(value)
			if err == nil {
				candidates[i] = objects.ScopedObject{Object: record, Scope: scope}
			}
		}
		if err != nil {
			if bulk {
				return nil, fmt.Errorf("record[%d] invalid: %w", i, err)
			}
			return nil, fmt.Errorf("record invalid: %w", err)
		}
	}
	return candidates, nil
}

func (s *internalServer) InternalRemoveControlledAccess(c fiber.Ctx, _ string) error {
	id := strings.TrimSpace(c.Params("id"))
	var req internalapi.ControlledAccessRemoveRequest
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Resource) == "" {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	record, err := s.objects.RemoveObjectControlledAccess(c.Context(), id, req.Resource)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(toInternalRecord(*record))
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
	scope, err := scopeFromInternalRecord(req)
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	update, err := fromInternalRecord(req)
	if err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	merged, err := s.objects.UpdateObjectMetadata(c.Context(), id, update, scope, req.Size)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(toInternalRecord(merged))
}
