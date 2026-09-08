package records

import (
	"github.com/calypr/syfon/apigen/internalapi"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalDeleteFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		if err := objectService.DeleteObject(c.Context(), id); err != nil {
			return apimiddleware.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

func handleInternalDeleteByQueryFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return apimiddleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		scope, err := scopeFromQuery(c.Query("organization"), c.Query("program"), c.Query("project"))
		if err != nil {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, err.Error())
		}
		if scope.Organization == "" {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "No scope specified")
		}

		count, err := objectService.DeleteBulkByScope(c.Context(), scope.Organization, scope.Project)
		if err != nil {
			return apimiddleware.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &count})
	}
}

func handleInternalBulkDeleteFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return apimiddleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}

		var req internalapi.BulkHashesRequest
		if err := c.Bind().JSON(&req); err != nil {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		if len(req.Hashes) == 0 {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: hashes are required")
		}

		normalized := normalizeNonEmptyBulkHashes(req.Hashes)
		if len(normalized) == 0 {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: hashes are required")
		}

		deleted, err := objectService.DeleteObjectsByChecksums(c.Context(), normalized)
		if err != nil {
			return apimiddleware.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &deleted})
	}
}
