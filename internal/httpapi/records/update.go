package records

import (
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/httpapi/response"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalRemoveControlledAccessFiber(objectService *objectrecords.Service) fiber.Handler {
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

func handleInternalUpdateFiber(objectService *objectrecords.Service) fiber.Handler {
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
