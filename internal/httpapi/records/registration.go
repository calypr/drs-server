package records

import (
	"fmt"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalCreateFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		candidates, err := decodeInternalCreateCandidates(c)
		if err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		if err := objectService.RegisterScopedObjects(c.Context(), candidates); err != nil {
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
}

func handleInternalBulkCreateFiber(objectService *objectrecords.Service) fiber.Handler {
	return handleInternalCreateFiber(objectService)
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
