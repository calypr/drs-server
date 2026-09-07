package records

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

const (
	defaultInternalListLimit     = 1000
	maxInternalListLimit         = 10000
	maxInternalBulkMissingSHA256 = 10000
	maxInternalBulkOverwrite     = 1000
)

func handleInternalGetFiber(objectService *objectrecords.Service) fiber.Handler {
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

func handleInternalListFiber(objectService *objectrecords.Service) fiber.Handler {
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

func handleInternalBulkDocumentsFiber(objectService *objectrecords.Service) fiber.Handler {
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
