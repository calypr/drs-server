package records

import (
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalCreateFiber(objectService *objectrecords.Service) fiber.Handler {
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

func handleInternalBulkCreateFiber(objectService *objectrecords.Service) fiber.Handler {
	return handleInternalCreateFiber(objectService)
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
