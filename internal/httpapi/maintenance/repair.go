package maintenance

import (
	"strings"

	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	"github.com/gofiber/fiber/v3"
)

func handleInternalScopeRepairAuditFiber(svc *scoperepair.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if middleware.MissingGen3AuthHeader(c.Context()) {
			return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		var req scoperepair.Options
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		req.Organization = strings.TrimSpace(req.Organization)
		req.Project = strings.TrimSpace(req.Project)
		req.CheckStorage = true
		if req.Organization == "" || req.Project == "" {
			return middleware.Reject(c, fiber.StatusBadRequest, "organization and project are required")
		}
		report, err := svc.AuditAuthorized(c.Context(), req)
		if err != nil {
			return middleware.HandleError(c, err)
		}
		return c.JSON(report)
	}
}

func handleInternalScopeRepairApplyFiber(svc *scoperepair.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if middleware.MissingGen3AuthHeader(c.Context()) {
			return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		var req scoperepair.Options
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		req.Organization = strings.TrimSpace(req.Organization)
		req.Project = strings.TrimSpace(req.Project)
		req.CheckStorage = true
		if req.Organization == "" || req.Project == "" {
			return middleware.Reject(c, fiber.StatusBadRequest, "organization and project are required")
		}
		result, err := svc.ApplyAuthorized(c.Context(), req)
		if err != nil {
			return middleware.HandleError(c, err)
		}
		return c.JSON(result)
	}
}
