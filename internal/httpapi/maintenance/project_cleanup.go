package maintenance

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/calypr/syfon/internal/faults"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/maintenance/projectstorage"
)

type projectCleanupResponse struct {
	Organization        string `json:"organization"`
	ProjectID           string `json:"project_id"`
	DeletedObjects      int    `json:"deleted_objects"`
	DeletedBucketScopes int    `json:"deleted_bucket_scopes"`
}

func handleInternalDeleteProjectFiber(c fiber.Ctx, service *projectstorage.ProjectCleanup) error {
	if service == nil {
		return response.HandleError(c, &projectstorage.Error{Kind: projectstorage.ErrorUnsupported, Message: "project storage service is not configured"})
	}
	organization := strings.TrimSpace(c.Params("organization"))
	projectID := strings.TrimSpace(c.Params("project_id"))
	if organization == "" || projectID == "" {
		return response.Reject(c, fiber.StatusBadRequest, "organization and project_id are required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return response.HandleError(c, faults.ErrUnauthorized)
	}
	if err := authorizeBucketScopeWrite(c.Context(), organization, projectID, "delete", "update"); err != nil {
		return response.HandleError(c, err)
	}

	result, err := service.DeleteProjectData(c.Context(), organization, projectID)
	if err != nil {
		return response.HandleError(c, err)
	}

	return c.JSON(projectCleanupResponse{
		Organization:        result.Organization,
		ProjectID:           result.ProjectID,
		DeletedObjects:      result.DeletedObjects,
		DeletedBucketScopes: result.DeletedBucketScopes,
	})
}
