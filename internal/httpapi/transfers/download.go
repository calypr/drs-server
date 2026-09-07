package transfers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func firstSupportedAccessURL(obj *objects.Record) string {
	if obj == nil || obj.AccessMethods == nil {
		return ""
	}
	for _, method := range *obj.AccessMethods {
		if method.AccessUrl == nil || strings.TrimSpace(method.AccessUrl.Url) == "" {
			continue
		}
		scheme := address.SchemeFromURL(method.AccessUrl.Url)
		if scheme != "" && address.ProviderFromScheme(scheme) == "" {
			continue
		}
		return method.AccessUrl.Url
	}
	return ""
}

func handleInternalDownloadFiber(c fiber.Ctx, objectService *objectrecords.Service, transferService *domaintransfers.Service, fileCounters usage.FileCounterRecorder) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	fileID := c.Params("file_id")

	obj, err := objectService.GetObject(c.Context(), fileID, "read")
	if err != nil {
		return response.HandleError(c, err)
	}

	objectURL := firstSupportedAccessURL(obj)
	if objectURL == "" {
		return c.Status(fiber.StatusNotFound).SendString("No supported cloud location found for this file")
	}

	opts := storage.AccessOptions{}
	if expStr := c.Query("expires_in"); expStr != "" {
		if exp, err := strconv.Atoi(expStr); err == nil {
			opts.ExpiresIn = time.Duration(exp) * time.Second
		}
	}
	if obj.Name != nil {
		opts.DownloadFilename = storage.DownloadFilename(*obj.Name)
	}
	if opts.ExpiresIn <= 0 {
		opts.ExpiresIn = time.Duration(config.DefaultSigningExpirySeconds) * time.Second
	}

	signedURL, err := transferService.SignObjectURL(c.Context(), obj, objectURL, opts)
	if err != nil {
		return response.HandleError(c, err)
	}

	if fileCounters == nil {
		return response.HandleError(c, errors.New("file usage recorder is not configured"))
	}
	if err := fileCounters.RecordFileDownload(c.Context(), string(obj.Id)); err != nil {
		return response.HandleError(c, err)
	}
	if err := transferService.RecordAccessIssued(c.Context(), domaintransfers.AccessRequest{
		Object:     obj,
		Direction:  usage.ProviderTransferDirectionDownload,
		StorageURL: objectURL,
	}); err != nil {
		return response.HandleError(c, err)
	}

	if c.Query("redirect") == "true" {
		return c.Redirect().To(signedURL)
	}

	return c.JSON(internalapi.InternalSignedURL{Url: &signedURL})
}

func handleInternalDownloadPartFiber(c fiber.Ctx, objectService *objectrecords.Service, transferService *domaintransfers.Service) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	fileID := c.Params("file_id")
	startStr := c.Query("start")
	endStr := c.Query("end")

	if startStr == "" || endStr == "" {
		return c.Status(fiber.StatusBadRequest).SendString("Missing 'start' or 'end' query parameter")
	}

	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid 'start' parameter")
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return c.Status(fiber.StatusBadRequest).SendString("Invalid 'end' parameter")
	}

	obj, err := objectService.GetObject(c.Context(), fileID, "read")
	if err != nil {
		return response.HandleError(c, err)
	}

	objectURL := firstSupportedAccessURL(obj)
	if objectURL == "" {
		return c.Status(fiber.StatusNotFound).SendString("No supported cloud location found for this file")
	}

	bucketID := ""
	if b, _, ok := address.ParseS3URL(objectURL); ok {
		bucketID = b
	}

	opts := storage.AccessOptions{ExpiresIn: time.Duration(config.DefaultSigningExpirySeconds) * time.Second}
	if obj.Name != nil {
		opts.DownloadFilename = storage.DownloadFilename(*obj.Name)
	}
	signedURL, err := transferService.SignObjectDownloadPart(c.Context(), obj, bucketID, objectURL, start, end, opts)
	if err != nil {
		return response.HandleError(c, err)
	}
	if err := transferService.RecordAccessIssued(c.Context(), domaintransfers.AccessRequest{
		Object:         obj,
		Direction:      usage.ProviderTransferDirectionDownload,
		StorageURL:     objectURL,
		RangeStart:     &start,
		RangeEnd:       &end,
		BytesRequested: end - start + 1,
	}); err != nil {
		return response.HandleError(c, err)
	}

	return c.JSON(internalapi.InternalSignedURL{Url: &signedURL})
}
