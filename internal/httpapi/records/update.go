package records

import (
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalRemoveControlledAccessFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := strings.TrimSpace(c.Params("id"))
		var req internalapi.ControlledAccessRemoveRequest
		if err := c.Bind().JSON(&req); err != nil || strings.TrimSpace(req.Resource) == "" {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		obj, err := objectService.RemoveObjectControlledAccess(c.Context(), id, req.Resource)
		if err != nil {
			return middleware.HandleError(c, err)
		}
		return c.JSON(ToInternalRecordResponse(*obj))
	}
}

func handleInternalUpdateFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		var req internalapi.InternalRecord
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
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

		merged, err := objectService.UpdateRecordInScope(c.Context(), id, scope, update, req.Size)
		if err != nil {
			return middleware.HandleError(c, err)
		}
		return c.JSON(ToInternalRecordResponse(merged))
	}
}
