package internaldrs

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/api/apiutil"
	apimiddleware "github.com/calypr/syfon/internal/api/middleware"
	"github.com/calypr/syfon/internal/api/routeutil"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultInternalListLimit     = 1000
	maxInternalListLimit         = 10000
	maxInternalBulkMissingSHA256 = 10000
	maxInternalBulkOverwrite     = 1000
)

func RegisterInternalRoutes(router fiber.Router, om *core.ObjectManager) {
	router.Get("/", handleInternalListFiber(om))
	router.Get(common.RouteInternalIndex, handleInternalListFiber(om))
	router.Get(routeutil.FiberPath(common.RouteInternalIndexDetail), handleInternalGetFiber(om))

	router.Post(common.RouteInternalIndex, handleInternalCreateFiber(om))
	router.Put(routeutil.FiberPath(common.RouteInternalIndexDetail), func(c fiber.Ctx) error { return handleInternalUpdateFiber(c, om) })
	router.Delete(routeutil.FiberPath(common.RouteInternalIndexDetail), handleInternalDeleteFiber(om))
	router.Post(routeutil.FiberPath(common.RouteInternalIndexControlledAccessRemove), handleInternalRemoveControlledAccessFiber(om))
	router.Delete("/", handleInternalDeleteByQueryFiber(om))
	router.Delete(common.RouteInternalIndex, handleInternalDeleteByQueryFiber(om))

	router.Post(common.RouteInternalBulkHashes, handleInternalBulkHashesFiber(om))
	router.Post(common.RouteInternalBulkSHA256, handleInternalBulkSHA256ValidityFiber(om))
	router.Post(common.RouteInternalBulkSHA256Missing, handleInternalBulkMissingSHA256Fiber(om))
	router.Post(common.RouteInternalBulkCreate, handleInternalBulkCreateFiber(om))
	router.Put("/index/bulk/overwrite", handleInternalBulkOverwriteFiber(om))
	router.Post(common.RouteInternalBulkDocs, handleInternalBulkDocumentsFiber(om))
	router.Post(common.RouteInternalBulkDeleteHashes, handleInternalBulkDeleteFiber(om))
	router.Post(common.RouteInternalRepairScopeAudit, handleInternalScopeRepairAuditFiber(om))
	router.Post(common.RouteInternalRepairScopeApply, handleInternalScopeRepairApplyFiber(om))

	registerInternalTransferRoutes(router, om)
}

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

func handleInternalBulkOverwriteFiber(om *core.ObjectManager) fiber.Handler {
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

		candidates := make([]models.InternalObject, 0, len(req.Records))
		now := time.Now().UTC()
		for i, record := range req.Records {
			record.Organization = &req.Organization
			record.Project = &req.Project
			obj, err := core.InternalRecordToInternalObject(record, now)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString(fmt.Sprintf("Invalid request body: record[%d] invalid: %v", i, err))
			}
			candidates = append(candidates, obj)
		}

		result, err := om.BulkOverwriteObjects(c.Context(), req.Organization, req.Project, candidates)
		if err != nil {
			if errors.Is(err, core.ErrBulkOverwriteConflict) {
				return c.Status(fiber.StatusConflict).SendString(err.Error())
			}
			return apiutil.HandleError(c, err)
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

func handleInternalBulkMissingSHA256Fiber(om *core.ObjectManager) fiber.Handler {
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

		missing, err := om.ListMissingScopedSHA256(c.Context(), req.Organization, req.Project, normalized)
		if err != nil {
			return apiutil.HandleError(c, err)
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

func handleInternalGetFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set(fiber.HeaderCacheControl, "no-store")
		id := c.Params("id")
		obj, err := om.GetObject(c.Context(), id, "read")
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(obj)
	}
}

func handleInternalListFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		hash := c.Query("hash")
		hashType := c.Query("hash_type")
		objectURL := strings.TrimSpace(c.Query("url"))
		if hash != "" {
			hashType, hash = common.ParseHashQuery(hash, hashType)
			filterOrg := strings.TrimSpace(c.Query("organization"))
			filterProject := strings.TrimSpace(c.Query("project"))
			limit, start, offset, err := parseInternalListPaginationFiber(c)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString(err.Error())
			}
			ids, err := om.ListObjectIDsPageByChecksum(c.Context(), hash, hashType, filterOrg, filterProject, "read", start, limit, offset)
			if err != nil {
				return apiutil.HandleError(c, err)
			}
			objs, err := om.GetPreparedScopedObjects(c.Context(), ids, filterOrg, filterProject, "read")
			if err != nil {
				return apiutil.HandleError(c, err)
			}
			records := make([]internalapi.InternalRecord, 0, len(objs))
			for _, o := range objs {
				records = append(records, core.InternalObjectToInternalRecord(o))
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
		var objs []models.InternalObject
		if objectURL != "" {
			var ids []string
			ids, err = om.ListObjectIDsPageByURL(c.Context(), objectURL, filterOrg, filterProject, "read", start, limit, offset)
			if err != nil {
				return apiutil.HandleError(c, err)
			}
			prepareStart := time.Now()
			objs, err = om.GetPreparedScopedObjects(c.Context(), ids, filterOrg, filterProject, "read")
			if err != nil {
				return apiutil.HandleError(c, err)
			}
			listDuration := time.Since(listStart)
			prepareDuration := time.Since(prepareStart)
			log.Printf("INFO: syfon_internal_index_list organization=%s project=%s url_filter=%t start_after=%t limit=%d offset=%d ids=%d records=%d list_ids_ms=%d prepare_scoped_ms=%d duration_ms=%d", filterOrg, filterProject, true, strings.TrimSpace(start) != "", limit, offset, len(ids), len(objs), listDuration.Milliseconds(), prepareDuration.Milliseconds(), time.Since(requestStart).Milliseconds())
		} else {
			objs, err = om.ListPreparedObjectsPageByScope(c.Context(), filterOrg, filterProject, "read", start, limit, offset)
			if err != nil {
				return apiutil.HandleError(c, err)
			}
			listDuration := time.Since(listStart)
			log.Printf("INFO: syfon_internal_index_list organization=%s project=%s url_filter=%t start_after=%t limit=%d offset=%d records=%d list_prepared_ms=%d duration_ms=%d", filterOrg, filterProject, false, strings.TrimSpace(start) != "", limit, offset, len(objs), listDuration.Milliseconds(), time.Since(requestStart).Milliseconds())
		}
		records := make([]internalapi.InternalRecord, 0, len(objs))
		for _, obj := range objs {
			records = append(records, core.InternalObjectToInternalRecord(obj))
		}
		return c.JSON(internalapi.ListRecordsResponse{Records: &records})
	}
}

func handleInternalDeleteFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		if err := om.DeleteObject(c.Context(), id); err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

func handleInternalRemoveControlledAccessFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := strings.TrimSpace(c.Params("id"))
		var req internalapi.ControlledAccessRemoveRequest
		if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Resource) == "" {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		obj, err := om.RemoveObjectControlledAccess(c.Context(), id, req.Resource)
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(core.InternalObjectToInternalRecordResponse(*obj))
	}
}

func handleInternalCreateFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		candidates, err := decodeInternalCreateCandidates(c, time.Now().UTC())
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: " + err.Error())
		}
		if err := om.RegisterObjects(c.Context(), candidates); err != nil {
			return apiutil.HandleError(c, err)
		}

		if strings.HasSuffix(c.Path(), "/bulk") {
			records := make([]internalapi.InternalRecord, len(candidates))
			for i, cand := range candidates {
				records[i] = core.InternalObjectToInternalRecord(cand)
			}
			return c.Status(fiber.StatusCreated).JSON(internalapi.ListRecordsResponse{Records: &records})
		}
		return c.Status(fiber.StatusCreated).JSON(core.InternalObjectToInternalRecordResponse(candidates[0]))
	}
}

func handleInternalDeleteByQueryFiber(om *core.ObjectManager) fiber.Handler {
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

		count, err := om.DeleteBulkByScope(c.Context(), org, project)
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &count})
	}
}

func handleInternalBulkHashesFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.BulkHashesRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}

		normalized := normalizeBulkHashes(req.Hashes)
		res, err := om.GetObjectsByChecksums(c.Context(), normalized, "read")
		if err != nil {
			return apiutil.HandleError(c, err)
		}

		finalRes := make(map[string][]models.InternalObject, len(req.Hashes))
		for i, h := range req.Hashes {
			typ, val := common.ParseHashQuery(h, "")
			matches := []models.InternalObject{}
			if i < len(normalized) {
				matches = res[normalized[i]]
			}
			if typ != "" {
				filtered := make([]models.InternalObject, 0, len(matches))
				for _, m := range matches {
					if common.ObjectHasChecksumTypeAndValue(m, typ, val) {
						filtered = append(filtered, m)
					}
				}
				matches = filtered
			}
			finalRes[h] = matches
		}

		return c.JSON(struct {
			Results map[string][]models.InternalObject
		}{Results: finalRes})
	}
}

func handleInternalBulkSHA256ValidityFiber(om *core.ObjectManager) fiber.Handler {
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

		records, err := om.GetObjectsByChecksums(c.Context(), hashes, "read")
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		for _, hash := range hashes {
			for _, obj := range records[hash] {
				if common.ObjectHasChecksumTypeAndValue(obj, "sha256", hash) {
					out[hash] = true
					break
				}
			}
		}
		return c.JSON(out)
	}
}

func handleInternalBulkCreateFiber(om *core.ObjectManager) fiber.Handler {
	return handleInternalCreateFiber(om)
}

func handleInternalBulkDocumentsFiber(om *core.ObjectManager) fiber.Handler {
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
			ids = append(ids, common.DerefStringSlice(obj.Ids)...)
		}
		if len(ids) == 0 {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: ids are required")
		}

		records, err := om.GetBulkObjects(c.Context(), ids, "read")
		if err != nil {
			return apiutil.HandleError(c, err)
		}

		out := make([]internalapi.InternalRecordResponse, 0, len(records))
		for _, obj := range records {
			out = append(out, core.InternalObjectToInternalRecordResponse(obj))
		}
		return c.JSON(out)
	}
}

func handleInternalBulkDeleteFiber(om *core.ObjectManager) fiber.Handler {
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

		deleted, err := om.DeleteObjectsByChecksums(c.Context(), normalized)
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &deleted})
	}
}

func handleInternalUpdateFiber(c fiber.Ctx, om *core.ObjectManager) error {
	id := c.Params("id")
	var req internalapi.InternalRecord
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: " + err.Error())
	}
	if strings.TrimSpace(req.Did) == "" {
		req.Did = id
	}
	update, err := core.InternalRecordToInternalObject(req, time.Now().UTC())
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid request body: " + err.Error())
	}

	existing, err := om.GetObject(c.Context(), id, "update")
	if err != nil {
		return apiutil.HandleError(c, err)
	}
	if req.Size != nil && *req.Size != existing.Size {
		return apiutil.HandleError(c, fmt.Errorf("%w: object size is immutable", faults.ErrConflict))
	}
	if incomingSHA, ok := common.CanonicalSHA256(update.Checksums); ok {
		storedSHA, stored := common.CanonicalSHA256(existing.Checksums)
		if stored && incomingSHA != storedSHA {
			return apiutil.HandleError(c, fmt.Errorf("%w: object checksum identity is immutable", faults.ErrConflict))
		}
	}
	merged, err := core.MergeInternalObjectUpdate(*existing, update, id, time.Now().UTC())
	if err != nil {
		return apiutil.HandleError(c, err)
	}
	if err := om.ReplaceObjects(c.Context(), []models.InternalObject{merged}); err != nil {
		return apiutil.HandleError(c, err)
	}
	return c.JSON(core.InternalObjectToInternalRecordResponse(merged))
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

func decodeInternalCreateCandidates(c fiber.Ctx, now time.Time) ([]models.InternalObject, error) {
	var bulkReq internalapi.BulkCreateRequest
	candidates := make([]models.InternalObject, 0)
	if err := c.Bind().JSON(&bulkReq); err == nil && len(bulkReq.Records) > 0 {
		for i, r := range bulkReq.Records {
			obj, err := core.InternalRecordToInternalObject(r, now)
			if err != nil {
				return nil, fmt.Errorf("record[%d] invalid: %w", i, err)
			}
			candidates = append(candidates, obj)
		}
		return candidates, nil
	}

	var singleReq internalapi.InternalRecord
	if err := c.Bind().JSON(&singleReq); err == nil && singleReq.Did != "" {
		obj, err := core.InternalRecordToInternalObject(singleReq, now)
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

func normalizeBulkHashes(hashes []string) []string {
	normalized := make([]string, 0, len(hashes))
	for _, h := range hashes {
		_, val := common.ParseHashQuery(h, "")
		normalized = append(normalized, val)
	}
	return normalized
}

func normalizeNonEmptyBulkHashes(hashes []string) []string {
	normalized := make([]string, 0, len(hashes))
	for _, h := range hashes {
		_, val := common.ParseHashQuery(h, "")
		if strings.TrimSpace(val) == "" {
			continue
		}
		normalized = append(normalized, val)
	}
	return normalized
}
