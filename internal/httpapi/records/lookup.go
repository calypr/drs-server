package records

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
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
			return middleware.HandleError(c, err)
		}
		encoded, err := json.Marshal(projectGet(*obj))
		if err != nil {
			return middleware.HandleError(c, err)
		}
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
		return c.Send(encoded)
	}
}

func handleInternalListFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
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
		objs, err := objectService.ListPreparedPage(c.Context(), query)
		if err != nil {
			return middleware.HandleError(c, err)
		}
		records := make([]internalapi.InternalRecord, 0, len(objs))
		for _, obj := range objs {
			records = append(records, ToInternalRecord(obj))
		}
		return c.JSON(internalapi.ListRecordsResponse{Records: &records})
	}
}

func scopeFromQuery(organization, program, project string) (objects.Scope, error) {
	org := strings.TrimSpace(organization)
	if org == "" {
		org = strings.TrimSpace(program)
	}
	return objects.NewScope(org, project)
}

func handleInternalBulkDocumentsFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
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

		records, err := objectService.GetBulkObjects(c.Context(), ids, "read")
		if err != nil {
			return middleware.HandleError(c, err)
		}

		out := make([]internalapi.InternalRecordResponse, 0, len(records))
		for _, obj := range records {
			out = append(out, ToInternalRecordResponse(obj))
		}
		return c.JSON(out)
	}
}

func parseInternalListPaginationFiber(c fiber.Ctx) (int, string, int, error) {
	limit, start, page, err := parseInternalListPageFiber(c)
	if err != nil {
		return 0, "", 0, err
	}
	offset := 0
	if page != 0 && limit != 0 {
		maxInt := int(^uint(0) >> 1)
		if page > maxInt/limit {
			return 0, "", 0, fmt.Errorf("page offset is too large")
		}
		offset = page * limit
	}
	return limit, start, offset, nil
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
