package transfers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/internalapi"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func handleInternalUploadBlankFiber(transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return apimiddleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		var req internalapi.InternalUploadBlankRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		guid := ""
		if req.Guid != nil {
			guid = strings.TrimSpace(*req.Guid)
		}
		if guid == "" {
			guid = uuid.New().String()
		} else if _, err := uuid.Parse(guid); err != nil {
			guid = uuid.New().String()
		}
		result, err := transferService.UploadURL(c.Context(), domaintransfers.UploadRequest{Organization: stringValue(req.Organization), Project: stringValue(req.Project), Key: guid})
		if err != nil {
			return apimiddleware.HandleError(c, err)
		}
		bucket := result.Target.PhysicalBucket
		return c.Status(fiber.StatusCreated).JSON(internalapi.InternalUploadBlankOutput{Url: &result.URL, Guid: &guid, Bucket: &bucket})
	}
}

func handleInternalUploadURLFiber(objectService interface{}, transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if transferService != nil {
			if objects, ok := objectService.(domaintransfers.ObjectPort); ok {
				transferService.BindLegacyDependencies(objects, nil)
			}
		}
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return apimiddleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
		}
		var params internalapi.InternalUploadURLParams
		if err := c.Bind().Query(&params); err != nil {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid query parameters")
		}
		request := domaintransfers.UploadRequest{ObjectID: c.Params("file_id"), Organization: stringValue(params.Organization), Project: stringValue(params.Project), Key: stringValue(params.Key), Scope: uploadScope(params.Organization, params.Project)}
		if params.ExpiresIn != nil {
			request.ExpiresIn = time.Duration(*params.ExpiresIn) * time.Second
		}
		result, err := transferService.UploadURL(c.Context(), request)
		if err != nil {
			return apimiddleware.HandleError(c, err)
		}
		return c.JSON(internalapi.InternalSignedURL{Url: &result.URL})
	}
}

func handleInternalUploadBulkFiber(objectService interface{}, transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if transferService != nil {
			if objects, ok := objectService.(domaintransfers.ObjectPort); ok {
				transferService.BindLegacyDependencies(objects, nil)
			}
		}
		var req internalapi.InternalUploadBulkRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		if len(req.Requests) == 0 {
			empty := []internalapi.InternalUploadBulkResult{}
			return c.JSON(internalapi.InternalUploadBulkOutput{Results: &empty})
		}
		requests := make([]domaintransfers.UploadRequest, len(req.Requests))
		for i, item := range req.Requests {
			requests[i] = domaintransfers.UploadRequest{ObjectID: item.FileId, Organization: stringValue(item.Organization), Project: stringValue(item.Project), Key: stringValue(item.Key)}
			if item.ExpiresIn != nil {
				requests[i].ExpiresIn = time.Duration(*item.ExpiresIn) * time.Second
			}
		}
		results := transferService.UploadBulk(c.Context(), requests)
		out := make([]internalapi.InternalUploadBulkResult, len(results))
		status := fiber.StatusOK
		for i, result := range results {
			item := req.Requests[i]
			out[i] = internalapi.InternalUploadBulkResult{FileId: item.FileId, Key: item.Key, Status: http.StatusOK}
			if result.Target.Key != "" {
				out[i].Key = &result.Target.Key
			}
			if result.URL != "" {
				out[i].Url = &result.URL
			}
			if result.Target.PhysicalBucket != "" {
				bucket := result.Target.PhysicalBucket
				out[i].Bucket = &bucket
			}
			if result.Err != nil {
				setBulkUploadError(c, &out[i], result.Err)
				status = fiber.StatusMultiStatus
			}
		}
		return c.Status(status).JSON(internalapi.InternalUploadBulkOutput{Results: &out})
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func uploadScope(organization, project *string) *domaintransfers.AccessScope {
	if organization == nil && project == nil {
		return nil
	}
	return &domaintransfers.AccessScope{Organization: stringValue(organization), Project: stringValue(project)}
}

// resolveUploadTarget remains the multipart adapter seam until the multipart
// consumer migration moves target resolution into its lifecycle service.
func resolveUploadTarget(ctx context.Context, transferService *domaintransfers.Service, organization, project, key string) (domaintransfers.CanonicalStorageTarget, error) {
	return transferService.ResolveScopedUploadTarget(ctx, organization, project, key)
}

func setBulkUploadError(c fiber.Ctx, result *internalapi.InternalUploadBulkResult, err error) {
	payload := apimiddleware.ClassifyError(c.Context(), err)
	apimiddleware.LogError(c, err, payload)
	result.Error = &payload.Message
	result.Status = int32(payload.Status)
}
