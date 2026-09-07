package transfers

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/calypr/syfon/apigen/server/internalapi"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/faults"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/storage/address"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

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

func handleInternalDownloadFiber(c fiber.Ctx, objectService *objects.Service, transferService *domaintransfers.Service, fileCounters usage.FileCounterRecorder) error {
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

func handleInternalDownloadPartFiber(c fiber.Ctx, objectService *objects.Service, transferService *domaintransfers.Service) error {
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

func handleInternalUploadURLFiber(objectService *objects.Service, transferService *domaintransfers.Service) fiber.Handler {
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

func handleInternalUploadBulkFiber(objectService *objects.Service, transferService *domaintransfers.Service) fiber.Handler {
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

func handleInternalMultipartInitFiber(objectService *objects.Service, transferService *domaintransfers.Service, lifecycle *domaintransfers.MultipartLifecycle) fiber.Handler {
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
