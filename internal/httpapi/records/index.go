package records

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultInternalListLimit     = 1000
	maxInternalListLimit         = 10000
	maxInternalBulkMissingSHA256 = 10000
	maxInternalBulkOverwrite     = 1000
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

func handleInternalBulkOverwriteFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req bulkOverwriteRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Records) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: organization, project, and records are required")
		}
		if len(req.Records) > maxInternalBulkOverwrite {
			return c.Status(fiber.StatusRequestEntityTooLarge).SendString(fmt.Sprintf("too many records: maximum is %d", maxInternalBulkOverwrite))
		}

		candidates := make([]objects.Record, 0, len(req.Records))
		now := time.Now().UTC()
		for i, record := range req.Records {
			record.Organization = &req.Organization
			record.Project = &req.Project
			obj, err := internalRecordToObject(record, now)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("Invalid request body: record[%d] invalid: %v", i, err))
			}
			candidates = append(candidates, obj)
		}

		result, err := objectService.BulkOverwriteObjects(c.Context(), req.Organization, req.Project, candidates)
		if err != nil {
			if errors.Is(err, objects.ErrBulkOverwriteConflict) {
				return c.Status(fiber.StatusConflict).SendString(err.Error())
			}
			return response.HandleError(c, err)
		}
		return c.JSON(bulkOverwriteResponse{
			Processed:       len(candidates),
			Created:         result.Created,
			Replaced:        result.Replaced,
			DIDMatched:      result.DIDMatched,
			ChecksumMatched: result.ChecksumMatched,
		})
	}
}

func handleInternalBulkMissingSHA256Fiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkMissingSHA256Request
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if strings.TrimSpace(req.Organization) == "" || strings.TrimSpace(req.Project) == "" || len(req.Sha256) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: organization, project, and sha256 values are required")
		}

		normalized, err := normalizeMissingSHA256(req.Sha256)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if len(normalized) > maxInternalBulkMissingSHA256 {
			return c.Status(fiber.StatusRequestEntityTooLarge).SendString(fmt.Sprintf("too many sha256 values: maximum is %d", maxInternalBulkMissingSHA256))
		}

		missing, err := objectService.ListMissingScopedSHA256(c.Context(), req.Organization, req.Project, normalized)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.BulkMissingSHA256Response{Checked: int32(len(normalized)), MissingSha256: missing})
	}
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
		return nil, fmt.Errorf("Invalid request body: sha256 values are required")
	}
	return out, nil
}

func handleInternalGetFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		id := c.Params("id")
		obj, err := objectService.GetObject(c.Context(), id, "read")
		if err != nil {
			return response.HandleError(c, err)
		}
		encoded, err := Encode(*obj)
		if err != nil {
			return response.HandleError(c, err)
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(encoded)
	}
}

func handleInternalListFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		hash := c.Query("hash")
		hashType := c.Query("hash_type")
		objectURL := strings.TrimSpace(c.Query("url"))
		if hash != "" {
			hashType, hash = objects.ParseHashQuery(hash, hashType)
			filterOrg := strings.TrimSpace(c.Query("organization"))
			filterProject := strings.TrimSpace(c.Query("project"))
			limit, start, offset, err := parseInternalListPaginationFiber(c)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString(err.Error())
			}
			ids, err := objectService.ListObjectIDsPageByChecksum(c.Context(), hash, hashType, filterOrg, filterProject, "read", start, limit, offset)
			if err != nil {
				return response.HandleError(c, err)
			}
			objs, err := objectService.GetPreparedScopedObjects(c.Context(), ids, filterOrg, filterProject, "read")
			if err != nil {
				return response.HandleError(c, err)
			}
			records := make([]internalapi.InternalRecord, 0, len(objs))
			for _, o := range objs {
				records = append(records, ToInternalRecord(o))
			}
			return c.JSON(internalapi.ListRecordsResponse{Records: &records})
		}

		filterOrg, filterProject, hasScope, err := parseScopeQueryParts(c.Query("organization"), c.Query("program"), c.Query("project"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if !hasScope {
			filterOrg, filterProject = "", ""
		}
		limit, start, offset, err := parseInternalListPaginationFiber(c)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}

		requestStart := time.Now()
		listStart := time.Now()
		var objs []objects.Record
		if objectURL != "" {
			var ids []string
			ids, err = objectService.ListObjectIDsPageByURL(c.Context(), objectURL, filterOrg, filterProject, "read", start, limit, offset)
			if err != nil {
				return response.HandleError(c, err)
			}
			prepareStart := time.Now()
			objs, err = objectService.GetPreparedScopedObjects(c.Context(), ids, filterOrg, filterProject, "read")
			if err != nil {
				return response.HandleError(c, err)
			}
			listDuration := time.Since(listStart)
			prepareDuration := time.Since(prepareStart)
			log.Printf("INFO: syfon_internal_index_list organization=%s project=%s url_filter=%t start_after=%t limit=%d offset=%d ids=%d records=%d list_ids_ms=%d prepare_scoped_ms=%d duration_ms=%d", filterOrg, filterProject, true, strings.TrimSpace(start) != "", limit, offset, len(ids), len(objs), listDuration.Milliseconds(), prepareDuration.Milliseconds(), time.Since(requestStart).Milliseconds())
		} else {
			objs, err = objectService.ListPreparedObjectsPageByScope(c.Context(), filterOrg, filterProject, "read", start, limit, offset)
			if err != nil {
				return response.HandleError(c, err)
			}
			listDuration := time.Since(listStart)
			log.Printf("INFO: syfon_internal_index_list organization=%s project=%s url_filter=%t start_after=%t limit=%d offset=%d records=%d list_prepared_ms=%d duration_ms=%d", filterOrg, filterProject, false, strings.TrimSpace(start) != "", limit, offset, len(objs), listDuration.Milliseconds(), time.Since(requestStart).Milliseconds())
		}
		records := make([]internalapi.InternalRecord, 0, len(objs))
		for _, obj := range objs {
			records = append(records, ToInternalRecord(obj))
		}
		return c.JSON(internalapi.ListRecordsResponse{Records: &records})
	}
}

func handleInternalDeleteFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		if err := objectService.DeleteObject(c.Context(), id); err != nil {
			return response.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

func handleInternalRemoveControlledAccessFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := strings.TrimSpace(c.Params("id"))
		var req internalapi.ControlledAccessRemoveRequest
		if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Resource) == "" {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		obj, err := objectService.RemoveObjectControlledAccess(c.Context(), id, req.Resource)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(ToInternalRecordResponse(*obj))
	}
}

func handleInternalCreateFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		candidates, err := decodeInternalCreateCandidates(c, time.Now().UTC())
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: " + err.Error())
		}
		if err := objectService.RegisterObjects(c.Context(), candidates); err != nil {
			return response.HandleError(c, err)
		}

		if strings.HasSuffix(c.Path(), "/bulk") {
			records := make([]internalapi.InternalRecord, len(candidates))
			for i, cand := range candidates {
				records[i] = ToInternalRecord(cand)
			}
			return c.Status(fiber.StatusCreated).JSON(internalapi.ListRecordsResponse{Records: &records})
		}
		return c.Status(fiber.StatusCreated).JSON(ToInternalRecordResponse(candidates[0]))
	}
}

func handleInternalDeleteByQueryFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		org, project, hasScope, err := parseScopeQueryParts(c.Query("organization"), c.Query("program"), c.Query("project"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}
		if !hasScope {
			return c.Status(fiber.StatusBadRequest).SendString("No scope specified")
		}

		count, err := objectService.DeleteBulkByScope(c.Context(), org, project)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &count})
	}
}

func handleInternalBulkHashesFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkHashesRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}

		normalized := normalizeBulkHashes(req.Hashes)
		res, err := objectService.GetObjectsByChecksums(c.Context(), normalized, "read")
		if err != nil {
			return response.HandleError(c, err)
		}

		finalRes := make(map[string][]internalapi.InternalRecord, len(req.Hashes))
		for i, h := range req.Hashes {
			typ, val := objects.ParseHashQuery(h, "")
			matches := []objects.Record{}
			if i < len(normalized) {
				matches = res[normalized[i]]
			}
			if typ != "" {
				filtered := make([]objects.Record, 0, len(matches))
				for _, m := range matches {
					if objects.RecordHasChecksumTypeAndValue(m, typ, val) {
						filtered = append(filtered, m)
					}
				}
				matches = filtered
			}
			compatibilityMatches := make([]internalapi.InternalRecord, 0, len(matches))
			for _, match := range matches {
				compatibilityMatches = append(compatibilityMatches, ToInternalRecord(match))
			}
			finalRes[h] = compatibilityMatches
		}

		return c.JSON(struct {
			Results map[string][]internalapi.InternalRecord
		}{Results: finalRes})
	}
}

func handleInternalBulkSHA256ValidityFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkSHA256ValidityRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if req.Sha256 == nil || len(*req.Sha256) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: sha256 values are required")
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
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: sha256 values are required")
		}

		records, err := objectService.GetObjectsByChecksums(c.Context(), hashes, "read")
		if err != nil {
			return response.HandleError(c, err)
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
}

func handleInternalBulkCreateFiber(objectService *objects.Service) fiber.Handler {
	return handleInternalCreateFiber(objectService)
}

func handleInternalBulkDocumentsFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkDocumentsRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}

		var ids []string
		if arr, err := req.AsBulkDocumentsRequest0(); err == nil {
			ids = append(ids, arr...)
		}
		if obj, err := req.AsBulkDocumentsRequest1(); err == nil {
			ids = append(ids, dereferenceStrings(obj.Ids)...)
		}
		if len(ids) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: ids are required")
		}

		records, err := objectService.GetBulkObjects(c.Context(), ids, "read")
		if err != nil {
			return response.HandleError(c, err)
		}

		out := make([]internalapi.InternalRecordResponse, 0, len(records))
		for _, obj := range records {
			out = append(out, ToInternalRecordResponse(obj))
		}
		return c.JSON(out)
	}
}

func handleInternalBulkDeleteFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		var req internalapi.BulkHashesRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if len(req.Hashes) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: hashes are required")
		}

		normalized := normalizeNonEmptyBulkHashes(req.Hashes)
		if len(normalized) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: hashes are required")
		}

		deleted, err := objectService.DeleteObjectsByChecksums(c.Context(), normalized)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &deleted})
	}
}

func handleInternalUpdateFiber(objectService *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		var req internalapi.InternalRecord
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: " + err.Error())
		}
		if strings.TrimSpace(req.Did) == "" {
			req.Did = id
		}
		update, err := internalRecordToObject(req, time.Now().UTC())
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: " + err.Error())
		}

		merged, err := objectService.UpdateRecord(c.Context(), id, update, req.Size, time.Now().UTC())
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(ToInternalRecordResponse(merged))
	}
}

func parseScopeQueryParts(organization, program, project string) (string, string, bool, error) {
	org := strings.TrimSpace(organization)
	if org == "" {
		org = strings.TrimSpace(program)
	}
	project = strings.TrimSpace(project)
	if project != "" && org == "" {
		return "", "", false, fmt.Errorf("organization is required when project is set")
	}
	if org != "" {
		return org, project, true, nil
	}
	return "", "", false, nil
}

func parseInternalListPaginationFiber(c fiber.Ctx) (int, string, int, error) {
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
	offset := 0
	if start == "" {
		rawPage := strings.TrimSpace(c.Query("page"))
		if rawPage != "" {
			page, err := strconv.Atoi(rawPage)
			if err != nil {
				return 0, "", 0, fmt.Errorf("page must be an integer")
			}
			if page < 0 {
				return 0, "", 0, fmt.Errorf("page must be >= 0")
			}
			offset = page * limit
		}
	}
	return limit, start, offset, nil
}

func decodeInternalCreateCandidates(c fiber.Ctx, now time.Time) ([]objects.Record, error) {
	var bulkReq internalapi.BulkCreateRequest
	candidates := make([]objects.Record, 0)
	if err := c.Bind().JSON(&bulkReq); err == nil && len(bulkReq.Records) > 0 {
		for i, r := range bulkReq.Records {
			obj, err := internalRecordToObject(r, now)
			if err != nil {
				return nil, fmt.Errorf("record[%d] invalid: %w", i, err)
			}
			candidates = append(candidates, obj)
		}
		return candidates, nil
	}

	var singleReq internalapi.InternalRecord
	if err := c.Bind().JSON(&singleReq); err == nil && singleReq.Did != "" {
		obj, err := internalRecordToObject(singleReq, now)
		if err != nil {
			return nil, fmt.Errorf("record invalid: %w", err)
		}
		candidates = append(candidates, obj)
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no records found")
	}
	return candidates, nil
}

func internalRecordToObject(value internalapi.InternalRecord, now time.Time) (objects.Record, error) {
	obj, err := FromInternalRecord(value, now)
	if err != nil {
		return objects.Record{}, err
	}
	return objects.EnforceCanonicalProjectScope(obj, recordStringValue(value.Organization), recordStringValue(value.Project))
}

func normalizeBulkHashes(hashes []string) []string {
	normalized := make([]string, 0, len(hashes))
	for _, h := range hashes {
		_, val := objects.ParseHashQuery(h, "")
		normalized = append(normalized, val)
	}
	return normalized
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
