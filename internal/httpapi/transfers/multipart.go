package transfers

import (
	"errors"
	"io"

	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func handleInternalMultipartInitFiber(transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalMultipartInitRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		result, err := transferService.BeginMultipart(c.Context(), domaintransfers.MultipartInitRequest{GUID: req.Guid, Key: req.Key, Organization: req.Organization, Project: req.Project})
		if err != nil {
			return middleware.HandleError(c, err)
		}
		return c.Status(fiber.StatusOK).JSON(internalapi.InternalMultipartInitOutput{UploadId: &result.UploadID, Guid: &result.GUID})
	}
}

func handleInternalMultipartUploadFiber(transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalMultipartUploadRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		if req.UploadId == "" {
			return middleware.Reject(c, fiber.StatusBadRequest, "uploadId is required")
		}
		urlStr, err := transferService.SignMultipartPart(c.Context(), req.UploadId, req.PartNumber)
		if err != nil {
			return middleware.HandleError(c, err)
		}
		return c.JSON(internalapi.InternalMultipartUploadOutput{PresignedUrl: &urlStr})
	}
}

func handleInternalMultipartCompleteFiber(transferService *domaintransfers.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var req internalapi.InternalMultipartCompleteRequest
		if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		if req.UploadId == "" {
			return middleware.Reject(c, fiber.StatusBadRequest, "uploadId is required")
		}
		parts := make([]storage.CompletedPart, len(req.Parts))
		for i, part := range req.Parts {
			parts[i] = storage.CompletedPart{ETag: part.ETag, PartNumber: part.PartNumber}
		}
		if err := transferService.CompleteMultipart(c.Context(), req.UploadId, parts); err != nil {
			return middleware.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusOK)
	}
}
