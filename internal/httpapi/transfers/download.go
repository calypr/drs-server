package transfers

import (
	"errors"
	"strconv"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/config"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func handleInternalDownloadFiber(c fiber.Ctx, objectService *objectrecords.Service, transferService *domaintransfers.Service, counters interface{}) error {
	if transferService != nil {
		if recorder, ok := counters.(usage.FileCounterRecorder); ok {
			transferService.BindLegacyDependencies(objectService, recorder)
		} else {
			transferService.BindLegacyDependencies(objectService, nil)
		}
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	expires := parseDownloadExpiry(c.Query("expires_in"))
	result, err := transferService.Download(c.Context(), domaintransfers.DownloadRequest{ObjectID: c.Params("file_id"), ExpiresIn: expires, Accounting: domaintransfers.AccountingDownloadBeforeEvent})
	if err != nil {
		return mapDownloadError(c, err)
	}
	if c.Query("redirect") == "true" {
		return c.Redirect().To(result.URL)
	}
	return c.JSON(internalapi.InternalSignedURL{Url: &result.URL})
}

func handleInternalDownloadPartFiber(c fiber.Ctx, objectService *objectrecords.Service, transferService *domaintransfers.Service) error {
	if transferService != nil {
		transferService.BindLegacyDependencies(objectService, nil)
	}
	c.Set(fiber.HeaderCacheControl, "no-store")
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	startStr, endStr := c.Query("start"), c.Query("end")
	if startStr == "" || endStr == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "Missing 'start' or 'end' query parameter")
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid 'start' parameter")
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid 'end' parameter")
	}
	result, err := transferService.Download(c.Context(), domaintransfers.DownloadRequest{ObjectID: c.Params("file_id"), ExpiresIn: time.Duration(config.DefaultSigningExpirySeconds) * time.Second, Range: &storage.ByteRange{Start: start, End: end}, Accounting: domaintransfers.AccountingEventOnly})
	if err != nil {
		return mapDownloadError(c, err)
	}
	return c.JSON(internalapi.InternalSignedURL{Url: &result.URL})
}

func parseDownloadExpiry(raw string) time.Duration {
	if raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return time.Duration(config.DefaultSigningExpirySeconds) * time.Second
}

func mapDownloadError(c fiber.Ctx, err error) error {
	if errors.Is(err, errorapi.ErrObjectLocationUnavailable) {
		return apimiddleware.Reject(c, fiber.StatusNotFound, "No supported cloud location found for this file")
	}
	return apimiddleware.HandleError(c, err)
}
