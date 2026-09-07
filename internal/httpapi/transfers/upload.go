package transfers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/faults"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func handleInternalUploadBlankFiber(transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		var req internalapi.InternalUploadBlankRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
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

		target, err := resolveUploadTarget(c.Context(), transferService, stringValue(req.Organization), stringValue(req.Project), guid)
		if err != nil {
			return response.HandleError(c, err)
		}

		signedURL, err := transferService.SignURL(c.Context(), target.URL, storage.AccessOptions{Method: http.MethodPut})
		if err != nil {
			return response.HandleError(c, err)
		}

		return c.Status(fiber.StatusCreated).JSON(internalapi.InternalUploadBlankOutput{
			Url:    &signedURL,
			Guid:   &guid,
			Bucket: &target.Bucket,
		})
	}
}

func handleInternalUploadURLFiber(objectService *objectrecords.Service, transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		if apimiddleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		fileID := c.Params("file_id")
		var params internalapi.InternalUploadURLParams
		if err := c.Bind().Query(&params); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid query parameters")
		}

		obj, err := objectService.GetObject(c.Context(), fileID, "update")
		if err != nil && !errors.Is(err, faults.ErrNotFound) {
			return response.HandleError(c, err)
		}

		var (
			bucket string
			key    = fileID
		)
		if obj != nil {
			var target domaintransfers.CanonicalStorageTarget
			if strings.TrimSpace(stringValue(params.Organization)) != "" {
				target, err = resolveUploadTarget(c.Context(), transferService, stringValue(params.Organization), stringValue(params.Project), uploadKeyForExistingObject(obj, params))
			} else {
				target, err = transferService.ResolveCanonicalStorageTarget(c.Context(), domaintransfers.CanonicalStorageTargetRequest{
					Object:         obj,
					Key:            strings.TrimSpace(stringValue(params.Key)),
					PreferChecksum: true,
				})
			}
			if err != nil {
				return response.HandleError(c, err)
			}
			signedURL, err := transferService.SignURL(c.Context(), target.URL, storage.AccessOptions{Method: http.MethodPut})
			if err != nil {
				return response.HandleError(c, err)
			}
			if err := transferService.RecordAccessIssued(c.Context(), domaintransfers.AccessRequest{
				Object:     obj,
				Scope:      uploadAttributionScope(params),
				Direction:  usage.ProviderTransferDirectionUpload,
				StorageURL: target.URL,
			}); err != nil {
				return response.HandleError(c, err)
			}
			return c.JSON(internalapi.InternalSignedURL{Url: &signedURL})
		} else {
			if requestedKey := strings.Trim(strings.TrimSpace(firstNonEmpty(stringValue(params.Key), c.Query("key"))), "/"); requestedKey != "" {
				key = requestedKey
			}
			target, err := resolveUploadTarget(c.Context(), transferService, stringValue(params.Organization), stringValue(params.Project), key)
			if err != nil {
				return response.HandleError(c, err)
			}
			bucket = target.Bucket
			key = target.Key
		}

		urlStr := address.BucketToURL(bucket, key)
		signedURL, err := transferService.SignURL(c.Context(), urlStr, storage.AccessOptions{Method: http.MethodPut})
		if err != nil {
			return response.HandleError(c, err)
		}
		if obj != nil {
			if err := transferService.RecordAccessIssued(c.Context(), domaintransfers.AccessRequest{
				Object:     obj,
				Direction:  usage.ProviderTransferDirectionUpload,
				StorageURL: urlStr,
			}); err != nil {
				return response.HandleError(c, err)
			}
		}

		return c.JSON(internalapi.InternalSignedURL{Url: &signedURL})
	}
}

func uploadAttributionScope(params internalapi.InternalUploadURLParams) *domaintransfers.AccessScope {
	if params.Organization == nil && params.Project == nil {
		return nil
	}
	return &domaintransfers.AccessScope{
		Organization: stringValue(params.Organization),
		Project:      stringValue(params.Project),
	}
}

func uploadKeyForExistingObject(obj *objects.Record, params internalapi.InternalUploadURLParams) string {
	if key := strings.Trim(strings.TrimSpace(stringValue(params.Key)), "/"); key != "" {
		return key
	}
	if obj != nil {
		if sha, ok := objects.CanonicalSHA256(obj.Checksums); ok {
			return sha
		}
	}
	return ""
}

func resolveUploadTarget(ctx context.Context, transferService *domaintransfers.Service, organization, project, key string) (domaintransfers.CanonicalStorageTarget, error) {
	key = strings.Trim(strings.TrimSpace(key), "/")
	return transferService.ResolveScopedUploadTarget(ctx, organization, project, key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func handleInternalUploadBulkFiber(objectService *objectrecords.Service, transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalUploadBulkRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if len(req.Requests) == 0 {
			empty := []internalapi.InternalUploadBulkResult{}
			return c.JSON(internalapi.InternalUploadBulkOutput{Results: &empty})
		}

		results := make([]internalapi.InternalUploadBulkResult, 0, len(req.Requests))
		for _, item := range req.Requests {
			res := internalapi.InternalUploadBulkResult{FileId: item.FileId, Key: item.Key}
			if item.FileId == "" {
				errMsg := "FileId is required"
				res.Error = &errMsg
				res.Status = http.StatusBadRequest
				results = append(results, res)
				continue
			}

			obj, err := objectService.GetObject(c.Context(), item.FileId, "update")
			if err != nil {
				errMsg := err.Error()
				res.Error = &errMsg
				switch {
				case errors.Is(err, faults.ErrUnauthorized):
					res.Status = http.StatusUnauthorized
				case errors.Is(err, faults.ErrNotFound):
					res.Status = http.StatusNotFound
				default:
					res.Status = http.StatusInternalServerError
				}
				results = append(results, res)
				continue
			}

			target, err := transferService.ResolveCanonicalStorageTarget(c.Context(), domaintransfers.CanonicalStorageTargetRequest{
				Object:         obj,
				Key:            strings.TrimSpace(stringValue(item.Key)),
				PreferChecksum: true,
			})
			if err != nil {
				errMsg := err.Error()
				res.Error = &errMsg
				res.Status = http.StatusBadRequest
				results = append(results, res)
				continue
			}
			bucket := target.Bucket
			key := target.Key
			if key == "" {
				key = string(obj.Id)
			}
			signedURL, err := transferService.SignURL(c.Context(), target.URL, storage.AccessOptions{Method: http.MethodPut})
			if err != nil {
				errMsg := err.Error()
				res.Error = &errMsg
				res.Status = http.StatusInternalServerError
			} else if err := transferService.RecordAccessIssued(c.Context(), domaintransfers.AccessRequest{
				Object:     obj,
				Direction:  usage.ProviderTransferDirectionUpload,
				StorageURL: target.URL,
			}); err != nil {
				errMsg := err.Error()
				res.Error = &errMsg
				res.Status = http.StatusInternalServerError
			} else {
				res.Url = &signedURL
				res.Bucket = &bucket
				res.Key = &key
				res.Status = http.StatusOK
			}
			results = append(results, res)
		}

		status := fiber.StatusOK
		for _, r := range results {
			if r.Error != nil {
				status = fiber.StatusMultiStatus
				break
			}
		}
		return c.Status(status).JSON(internalapi.InternalUploadBulkOutput{Results: &results})
	}
}
