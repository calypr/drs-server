package records

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/httpapi/response"
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
			if errors.Is(err, objectrecords.ErrBulkOverwriteConflict) {
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
