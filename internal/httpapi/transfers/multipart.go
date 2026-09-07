package transfers

import (
	"errors"
	"io"
	"strings"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func handleInternalMultipartInitFiber(objectService *objectrecords.Service, transferService *domaintransfers.Service, lifecycle *domaintransfers.MultipartLifecycle) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalMultipartInitRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}

		key := ""
		if req.Guid != nil {
			key = strings.TrimSpace(*req.Guid)
		} else if req.Key != nil {
			key = strings.TrimSpace(*req.Key)
		}
		if key == "" {
			return c.Status(fiber.StatusBadRequest).SendString("key/guid is required")
		}

		if strings.Contains(key, "/") {
			target, err := resolveUploadTarget(c.Context(), transferService, stringValue(req.Organization), stringValue(req.Project), key)
			if err != nil {
				return response.HandleError(c, err)
			}
			internalID := uuid.NewString()
			uploadID, err := lifecycle.Begin(c.Context(), target.Bucket, target.Key)
			if err != nil {
				return response.HandleError(c, err)
			}
			return c.Status(fiber.StatusOK).JSON(internalapi.InternalMultipartInitOutput{
				UploadId: &uploadID,
				Guid:     &internalID,
			})
		}

		internalID := key
		var (
			bucket       string
			multipartKey string
		)
		if objects.LooksLikeSHA256(key) {
			if existing, err := objectService.GetObjectsByChecksum(c.Context(), key, "read"); err == nil && len(existing) > 0 {
				obj := &existing[0]
				internalID = string(obj.Id)
				target, err := transferService.ResolveCanonicalStorageTarget(c.Context(), domaintransfers.CanonicalStorageTargetRequest{
					Object:         obj,
					PreferChecksum: true,
				})
				if err != nil {
					return response.HandleError(c, err)
				}
				bucket, multipartKey = target.Bucket, target.Key
				if bucket == "" || multipartKey == "" {
					return c.Status(fiber.StatusBadRequest).SendString("existing object storage location is not an s3 url")
				}
			} else {
				return c.Status(fiber.StatusBadRequest).SendString("checksum-only multipart init requires an explicit guid or a project-scoped object id")
			}
		} else if _, err := uuid.Parse(key); err != nil {
			internalID = uuid.NewString()
		}

		if bucket == "" {
			target, err := resolveUploadTarget(c.Context(), transferService, stringValue(req.Organization), stringValue(req.Project), internalID)
			if err != nil {
				return response.HandleError(c, err)
			}
			bucket = target.Bucket
			multipartKey = target.Key
		}

		uploadID, err := lifecycle.Begin(c.Context(), bucket, multipartKey)
		if err != nil {
			return response.HandleError(c, err)
		}

		return c.Status(fiber.StatusOK).JSON(internalapi.InternalMultipartInitOutput{
			UploadId: &uploadID,
			Guid:     &internalID,
		})
	}
}

func handleInternalMultipartUploadFiber(lifecycle *domaintransfers.MultipartLifecycle) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalMultipartUploadRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if req.UploadId == "" {
			return c.Status(fiber.StatusBadRequest).SendString("uploadId is required")
		}

		urlStr, err := lifecycle.SignPart(c.Context(), req.UploadId, req.PartNumber)
		if errors.Is(err, domaintransfers.ErrMultipartUploadNotFound) {
			return c.Status(fiber.StatusNotFound).SendString("Upload ID not found")
		}
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(internalapi.InternalMultipartUploadOutput{PresignedUrl: &urlStr})
	}
}

func handleInternalMultipartCompleteFiber(lifecycle *domaintransfers.MultipartLifecycle) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalMultipartCompleteRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return c.Status(fiber.StatusBadRequest).SendString("Invalid request body")
		}
		if req.UploadId == "" {
			return c.Status(fiber.StatusBadRequest).SendString("uploadId is required")
		}

		parts := make([]storage.CompletedPart, len(req.Parts))
		for i, p := range req.Parts {
			parts[i] = storage.CompletedPart{ETag: p.ETag, PartNumber: p.PartNumber}
		}
		if err := lifecycle.Complete(c.Context(), req.UploadId, parts); errors.Is(err, domaintransfers.ErrMultipartUploadNotFound) {
			return c.Status(fiber.StatusNotFound).SendString("Upload ID not found")
		} else if err != nil {
			return response.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusOK)
	}
}
