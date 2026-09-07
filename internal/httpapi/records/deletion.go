package records

import (
	"github.com/calypr/syfon/apigen/server/internalapi"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleInternalDeleteFiber(objectService *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("id")
		if err := objectService.DeleteObject(c.Context(), id); err != nil {
			return response.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

func handleInternalDeleteByQueryFiber(objectService *objectrecords.Service) fiber.Handler {
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

		count, err := objectService.DeleteBulkByScope(c.Context(), org, project)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &count})
	}
}

func handleInternalBulkDeleteFiber(objectService *objectrecords.Service) fiber.Handler {
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

		deleted, err := objectService.DeleteObjectsByChecksums(c.Context(), normalized)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.DeleteByQueryResponse{Deleted: &deleted})
	}
}
