package maintenance

import (
	"context"
	"strings"

	sycommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/maintenance/scoperepair"
	"github.com/gofiber/fiber/v3"
)

func authorizeStorageCleanupScope(ctx context.Context, organization, project string, methods ...string) error {
	if !access.IsAuthzEnforced(ctx) {
		return nil
	}
	resource, err := sycommon.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if access.HasMethodAccess(ctx, methods[0], []string{"/programs", "/data_file"}) || access.HasAnyMethodAccess(ctx, []string{resource}, methods...) {
		return nil
	}
	return faults.ErrUnauthorized
}

func handleInternalScopeRepairAuditFiber(svc *scoperepair.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if middleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		var req scoperepair.Options
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return response.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		req.Organization = strings.TrimSpace(req.Organization)
		req.Project = strings.TrimSpace(req.Project)
		req.CheckStorage = true
		if req.Organization == "" || req.Project == "" {
			return response.Reject(c, fiber.StatusBadRequest, "organization and project are required")
		}
		if err := authorizeStorageCleanupScope(c.Context(), req.Organization, req.Project, "read"); err != nil {
			return response.HandleError(c, err)
		}
		report, err := svc.Audit(c.Context(), req)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(report)
	}
}

func handleInternalScopeRepairApplyFiber(svc *scoperepair.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if middleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}
		var req scoperepair.Options
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return response.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
		req.Organization = strings.TrimSpace(req.Organization)
		req.Project = strings.TrimSpace(req.Project)
		req.CheckStorage = true
		if req.Organization == "" || req.Project == "" {
			return response.Reject(c, fiber.StatusBadRequest, "organization and project are required")
		}
		if err := authorizeStorageCleanupScope(c.Context(), req.Organization, req.Project, "read"); err != nil {
			return response.HandleError(c, err)
		}
		if err := authorizeStorageCleanupScope(c.Context(), req.Organization, req.Project, "update"); err != nil {
			return response.HandleError(c, err)
		}
		result, err := svc.Apply(c.Context(), req)
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(result)
	}
}
