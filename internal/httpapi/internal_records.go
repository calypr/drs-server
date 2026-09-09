package httpapi

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

type bulkOverwriteRequest struct {
	Organization string                       `json:"organization"`
	Project      string                       `json:"project"`
	Records      []internalapi.InternalRecord `json:"records"`
}

type bulkOverwriteResponse struct {
	Processed       int `json:"processed"`
	Created         int `json:"created"`
	Replaced        int `json:"replaced"`
	DIDMatched      int `json:"did_matched"`
	ChecksumMatched int `json:"checksum_matched"`
}

func (s *internalServer) InternalBulkOverwrite(c fiber.Ctx) error {
	var req bulkOverwriteRequest
	if err := c.Bind().JSON(&req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Records) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: organization, project, and records are required")
	}
	if len(req.Records) > maxInternalBulkOverwrite {
		return middleware.Reject(c, fiber.StatusRequestEntityTooLarge, fmt.Sprintf("too many records: maximum is %d", maxInternalBulkOverwrite))
	}
	scope, err := objects.NewScope(req.Organization, req.Project)
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, err.Error())
	}

	candidates := make([]objects.Record, 0, len(req.Records))
	for i, record := range req.Records {
		obj, err := FromInternalRecord(record, time.Time{})
		if err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, fmt.Sprintf("Invalid request body: record[%d] invalid: %v", i, err))
		}
		candidates = append(candidates, obj)
	}

	result, err := s.objects.BulkOverwriteObjects(c.Context(), scope.Organization, scope.Project, candidates)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(bulkOverwriteResponse{
		Processed:       len(candidates),
		Created:         result.Created,
		Replaced:        result.Replaced,
		DIDMatched:      result.DIDMatched,
		ChecksumMatched: result.ChecksumMatched,
	})
}

func (s *internalServer) InternalBulkMissingSHA256(c fiber.Ctx) error {
	var req internalapi.BulkMissingSHA256Request
	if err := c.Bind().JSON(&req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Sha256) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: organization, project, and sha256 values are required")
	}

	normalized, err := normalizeMissingSHA256(req.Sha256)
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, err.Error())
	}
	if len(normalized) > maxInternalBulkMissingSHA256 {
		return middleware.Reject(c, fiber.StatusRequestEntityTooLarge, fmt.Sprintf("too many sha256 values: maximum is %d", maxInternalBulkMissingSHA256))
	}

	missing, err := s.objects.ListMissingScopedSHA256(c.Context(), req.Organization, req.Project, normalized)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(internalapi.BulkMissingSHA256Response{Checked: int32(len(normalized)), MissingSha256: missing})
}

func normalizeMissingSHA256(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		value = strings.TrimPrefix(strings.ToLower(value), "sha256:")
		if value == "" {
			continue
		}
		if len(value) != 64 {
			return nil, fmt.Errorf("invalid sha256 checksum %q", raw)
		}
		if _, err := hex.DecodeString(value); err != nil {
			return nil, fmt.Errorf("invalid sha256 checksum %q", raw)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("invalid request body: sha256 values are required")
	}
	return out, nil
}

func (s *internalServer) InternalBulkHashes(c fiber.Ctx) error {
	var req internalapi.BulkHashesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	queries := make([]objects.ChecksumQuery, 0, len(req.Hashes))
	for _, raw := range req.Hashes {
		typ, value := objects.ParseHashQuery(raw, "")
		queries = append(queries, objects.ChecksumQuery{Type: typ, Value: value})
	}
	matches, err := s.objects.LookupChecksumQueries(c.Context(), queries, "read")
	if err != nil {
		return middleware.HandleError(c, err)
	}

	finalRes := make(map[string][]internalapi.InternalRecord, len(req.Hashes))
	for i, h := range req.Hashes {
		var records []objects.Record
		if i < len(matches) {
			records = matches[i].Records
		}
		compatibilityMatches := make([]internalapi.InternalRecord, 0, len(records))
		for _, match := range records {
			compatibilityMatches = append(compatibilityMatches, ToInternalRecord(match))
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
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if req.Sha256 == nil || len(*req.Sha256) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: sha256 values are required")
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
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: sha256 values are required")
	}

	records, err := s.objects.GetObjectsByChecksums(c.Context(), hashes, "read")
	if err != nil {
		return middleware.HandleError(c, err)
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

func normalizeNonEmptyBulkHashes(hashes []string) []string {
	normalized := make([]string, 0, len(hashes))
	for _, h := range hashes {
		_, val := objects.ParseHashQuery(h, "")
		if strings.TrimSpace(val) == "" {
			continue
		}
		normalized = append(normalized, val)
	}
	return normalized
}

func (s *internalServer) InternalDelete(c fiber.Ctx, _ string) error {
	id := c.Params("id")
	if err := s.objects.DeleteObject(c.Context(), id); err != nil {
		return middleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *internalServer) InternalDeleteByQuery(c fiber.Ctx, _ internalapi.InternalDeleteByQueryParams) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	scope, err := scopeFromQuery(c.Query("organization"), c.Query("program"), c.Query("project"))
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, err.Error())
	}
	if scope.Organization == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "No scope specified")
	}

	count, err := s.objects.DeleteBulkByScope(c.Context(), scope.Organization, scope.Project)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &count})
}

func (s *internalServer) InternalBulkDeleteHashes(c fiber.Ctx) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}

	var req internalapi.BulkHashesRequest
	if err := c.Bind().JSON(&req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(req.Hashes) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: hashes are required")
	}

	normalized := normalizeNonEmptyBulkHashes(req.Hashes)
	if len(normalized) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: hashes are required")
	}

	deleted, err := s.objects.DeleteObjectsByChecksums(c.Context(), normalized)
	if err != nil {
		return middleware.HandleError(c, err)
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
		return middleware.HandleError(c, err)
	}
	encoded, err := json.Marshal(projectGet(*obj))
	if err != nil {
		return middleware.HandleError(c, err)
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
			return middleware.Reject(c, fiber.StatusBadRequest, err.Error())
		}
	}

	var scope objects.Scope
	if hash == "" {
		scope, err = scopeFromQuery(c.Query("organization"), c.Query("program"), c.Query("project"))
		if err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, err.Error())
		}
		limit, start, page, err = parseInternalListPageFiber(c)
	} else {
		scope, err = scopeFromQuery(c.Query("organization"), c.Query("program"), c.Query("project"))
	}
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, err.Error())
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
		return middleware.HandleError(c, err)
	}
	records := make([]internalapi.InternalRecord, 0, len(objs))
	for _, obj := range objs {
		records = append(records, ToInternalRecord(obj))
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
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	var ids []string
	if arr, err := req.AsBulkDocumentsRequest0(); err == nil {
		ids = append(ids, arr...)
	}
	if obj, err := req.AsBulkDocumentsRequest1(); err == nil {
		ids = append(ids, dereferenceStrings(obj.Ids)...)
	}
	if len(ids) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: ids are required")
	}

	records, err := s.objects.GetBulkObjects(c.Context(), ids, "read")
	if err != nil {
		return middleware.HandleError(c, err)
	}

	out := make([]internalapi.InternalRecordResponse, 0, len(records))
	for _, obj := range records {
		out = append(out, ToInternalRecordResponse(obj))
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
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if err := s.objects.RegisterScopedObjects(c.Context(), candidates); err != nil {
		return middleware.HandleError(c, err)
	}

	if strings.HasSuffix(c.Path(), "/bulk") {
		records := make([]internalapi.InternalRecord, len(candidates))
		for i, scoped := range candidates {
			records[i] = ToInternalRecord(scoped.Record)
		}
		return c.Status(fiber.StatusCreated).JSON(internalapi.ListRecordsResponse{Records: &records})
	}
	return c.Status(fiber.StatusCreated).JSON(ToInternalRecordResponse(candidates[0].Record))
}

func (s *internalServer) InternalBulkCreate(c fiber.Ctx) error {
	candidates, err := decodeInternalCreateCandidates(c)
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if err := s.objects.RegisterScopedObjects(c.Context(), candidates); err != nil {
		return middleware.HandleError(c, err)
	}
	records := make([]internalapi.InternalRecord, len(candidates))
	for i, scoped := range candidates {
		records[i] = ToInternalRecord(scoped.Record)
	}
	return c.Status(fiber.StatusCreated).JSON(internalapi.ListRecordsResponse{Records: &records})
}

func decodeInternalCreateCandidates(c fiber.Ctx) ([]objects.ScopedRecord, error) {
	var bulkReq internalapi.BulkCreateRequest
	candidates := make([]objects.ScopedRecord, 0)
	if err := c.Bind().JSON(&bulkReq); err == nil && len(bulkReq.Records) > 0 {
		for i, r := range bulkReq.Records {
			obj, err := FromInternalRecord(r, time.Time{})
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
		obj, err := FromInternalRecord(singleReq, time.Time{})
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
	return objects.NewScope(recordStringValue(value.Organization), recordStringValue(value.Project))
}

func (s *internalServer) InternalRemoveControlledAccess(c fiber.Ctx, _ string) error {
	id := strings.TrimSpace(c.Params("id"))
	var req internalapi.ControlledAccessRemoveRequest
	if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Resource) == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	obj, err := s.objects.RemoveObjectControlledAccess(c.Context(), id, req.Resource)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(ToInternalRecordResponse(*obj))
}

func (s *internalServer) InternalUpdate(c fiber.Ctx, _ string) error {
	id := c.Params("id")
	var req internalapi.InternalRecord
	if err := recordsDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if strings.TrimSpace(req.Did) == "" {
		req.Did = id
	}
	scope, err := internalRecordScope(req)
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	update, err := FromInternalRecord(req, time.Time{})
	if err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	merged, err := s.objects.UpdateRecordInScope(c.Context(), id, scope, update, req.Size)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(ToInternalRecordResponse(merged))
}
