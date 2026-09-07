package records

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
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

func handleInternalBulkOverwriteFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
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

		candidates := make([]objects.Record, 0, len(req.Records))
		now := time.Now().UTC()
		for i, record := range req.Records {
			record.Organization = &req.Organization
			record.Project = &req.Project
			obj, err := internalRecordToObject(record, now)
			if err != nil {
				return middleware.Reject(c, fiber.StatusBadRequest, fmt.Sprintf("Invalid request body: record[%d] invalid: %v", i, err))
			}
			candidates = append(candidates, obj)
		}

		result, err := objectService.BulkOverwriteObjects(c.Context(), req.Organization, req.Project, candidates)
		if err != nil {
			if errors.Is(err, errorapi.ErrBulkOverwriteConflict) {
				return middleware.Reject(c, fiber.StatusConflict, err.Error())
			}
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
}
