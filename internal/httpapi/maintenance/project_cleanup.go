package maintenance

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/calypr/syfon/apigen/errorapi"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
)

type projectCleanupResponse struct {
	Organization        string `json:"organization"`
	ProjectID           string `json:"project_id"`
	DeletedObjects      int    `json:"deleted_objects"`
	DeletedBucketScopes int    `json:"deleted_bucket_scopes"`
}

func handleInternalDeleteProjectFiber(c fiber.Ctx, service *projectstorage.ProjectCleanup) error {
	if service == nil {
		return apimiddleware.HandleError(c, errorapi.Define(errorapi.ErrorCodeStorageUnavailable, errorapi.ErrorCategoryUnavailable, "project storage service is not configured"))
	}
	organization := strings.TrimSpace(c.Params("organization"))
	projectID := strings.TrimSpace(c.Params("project_id"))
	if organization == "" || projectID == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "organization and project_id are required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	result, err := service.DeleteProjectDataAuthorized(c.Context(), organization, projectID)
	if err != nil {
		return apimiddleware.HandleError(c, err)
	}

	return c.JSON(projectCleanupResponse{
		Organization:        result.Organization,
		ProjectID:           result.ProjectID,
		DeletedObjects:      result.DeletedObjects,
		DeletedBucketScopes: result.DeletedBucketScopes,
	})
}
