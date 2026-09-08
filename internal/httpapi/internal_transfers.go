package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
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

// TransfersServer is retained as a small generated-test adapter. Production
// routes bind the same operations on internalServer.
type TransfersServer struct{ transfers *domaintransfers.Service }

func NewTransfersServer(transfers *domaintransfers.Service) *TransfersServer {
	return &TransfersServer{transfers: transfers}
}
func (s *TransfersServer) InternalDownload(c fiber.Ctx, _ string, _ internalapi.InternalDownloadParams) error {
	return handleInternalDownloadFiber(c, nil, s.transfers, nil)
}
func (s *TransfersServer) InternalDownloadPart(c fiber.Ctx, _ string, _ internalapi.InternalDownloadPartParams) error {
	return handleInternalDownloadPartFiber(c, nil, s.transfers)
}
func (s *TransfersServer) InternalMultipartComplete(c fiber.Ctx) error {
	return handleInternalMultipartCompleteFiber(s.transfers)(c)
}
func (s *TransfersServer) InternalMultipartInit(c fiber.Ctx) error {
	return handleInternalMultipartInitFiber(s.transfers)(c)
}
func (s *TransfersServer) InternalMultipartUpload(c fiber.Ctx) error {
	return handleInternalMultipartUploadFiber(s.transfers)(c)
}
func (s *TransfersServer) InternalUploadBlank(c fiber.Ctx) error {
	return handleInternalUploadBlankFiber(s.transfers)(c)
}
func (s *TransfersServer) InternalUploadBulk(c fiber.Ctx) error {
	return handleInternalUploadBulkFiber(nil, s.transfers)(c)
}
func (s *TransfersServer) InternalUploadURL(c fiber.Ctx, _ string, _ internalapi.InternalUploadURLParams) error {
	return handleInternalUploadURLFiber(nil, s.transfers)(c)
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
