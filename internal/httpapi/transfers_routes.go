package httpapi

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

func (s *internalServer) InternalDownload(c fiber.Ctx, _ string, _ internalapi.InternalDownloadParams) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	if access.MissingGen3AuthHeader(c.Context()) {
		return middleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	expires := time.Duration(config.DefaultSigningExpirySeconds) * time.Second
	if raw := c.Query("expires_in"); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			expires = time.Duration(seconds) * time.Second
		}
	}
	result, err := s.transfers.Download(c.Context(), domaintransfers.DownloadRequest{ObjectID: c.Params("file_id"), ExpiresIn: expires, Accounting: domaintransfers.AccountingDownloadBeforeEvent})
	if err != nil {
		return mapDownloadError(c, err)
	}
	if c.Query("redirect") == "true" {
		return c.Redirect().To(result.URL)
	}
	return c.JSON(internalapi.InternalSignedURL{Url: &result.URL})
}

func (s *internalServer) InternalDownloadPart(c fiber.Ctx, _ string, _ internalapi.InternalDownloadPartParams) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	if access.MissingGen3AuthHeader(c.Context()) {
		return middleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	startStr, endStr := c.Query("start"), c.Query("end")
	if startStr == "" || endStr == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "Missing 'start' or 'end' query parameter")
	}
	start, err := strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid 'start' parameter")
	}
	end, err := strconv.ParseInt(endStr, 10, 64)
	if err != nil || end < start {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid 'end' parameter")
	}
	result, err := s.transfers.Download(c.Context(), domaintransfers.DownloadRequest{ObjectID: c.Params("file_id"), ExpiresIn: time.Duration(config.DefaultSigningExpirySeconds) * time.Second, Range: &domaintransfers.ByteRange{Start: start, End: end}, Accounting: domaintransfers.AccountingEventOnly})
	if err != nil {
		return mapDownloadError(c, err)
	}
	return c.JSON(internalapi.InternalSignedURL{Url: &result.URL})
}

func mapDownloadError(c fiber.Ctx, err error) error {
	if errors.Is(err, errorapi.ErrObjectLocationUnavailable) {
		return middleware.Reject(c, fiber.StatusNotFound, "No supported cloud location found for this file")
	}
	return middleware.HandleError(c, err)
}

func (s *internalServer) InternalMultipartInit(c fiber.Ctx) error {
	var req internalapi.InternalMultipartInitRequest
	if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	result, err := s.transfers.BeginMultipart(c.Context(), domaintransfers.MultipartInitRequest{GUID: req.Guid, Key: req.Key, Organization: req.Organization, Project: req.Project})
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.Status(fiber.StatusOK).JSON(internalapi.InternalMultipartInitOutput{UploadId: &result.UploadID, Guid: &result.GUID})
}

func (s *internalServer) InternalMultipartUpload(c fiber.Ctx) error {
	var req internalapi.InternalMultipartUploadRequest
	if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if req.UploadId == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "uploadId is required")
	}
	urlStr, err := s.transfers.SignMultipartPart(c.Context(), req.UploadId, req.PartNumber)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(internalapi.InternalMultipartUploadOutput{PresignedUrl: &urlStr})
}

func (s *internalServer) InternalMultipartComplete(c fiber.Ctx) error {
	var req internalapi.InternalMultipartCompleteRequest
	if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if req.UploadId == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "uploadId is required")
	}
	parts := make([]domaintransfers.CompletedPart, len(req.Parts))
	for i, part := range req.Parts {
		parts[i] = domaintransfers.CompletedPart{ETag: part.ETag, PartNumber: part.PartNumber}
	}
	if err := s.transfers.CompleteMultipart(c.Context(), req.UploadId, parts); err != nil {
		return middleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusOK)
}

func (s *internalServer) InternalUploadBlank(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalUploadBlankRequest
	if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
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
	result, err := s.transfers.UploadURL(c.Context(), domaintransfers.UploadRequest{Organization: stringValue(req.Organization), Project: stringValue(req.Project), Key: guid})
	if err != nil {
		return middleware.HandleError(c, err)
	}
	bucket := result.Target.PhysicalBucket
	return c.Status(fiber.StatusCreated).JSON(internalapi.InternalUploadBlankOutput{Url: &result.URL, Guid: &guid, Bucket: &bucket})
}

func (s *internalServer) InternalUploadURL(c fiber.Ctx, _ string, params internalapi.InternalUploadURLParams) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	request := domaintransfers.UploadRequest{ObjectID: c.Params("file_id"), Organization: stringValue(params.Organization), Project: stringValue(params.Project), Key: stringValue(params.Key), Scope: uploadScope(params.Organization, params.Project)}
	if params.ExpiresIn != nil {
		request.ExpiresIn = time.Duration(*params.ExpiresIn) * time.Second
	}
	result, err := s.transfers.UploadURL(c.Context(), request)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(internalapi.InternalSignedURL{Url: &result.URL})
}

func (s *internalServer) InternalUploadBulk(c fiber.Ctx) error {
	var req internalapi.InternalUploadBulkRequest
	if err := c.Bind().JSON(&req); err != nil && !errors.Is(err, io.EOF) {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
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
	results := s.transfers.UploadBulk(c.Context(), requests)
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
			payload := middleware.ClassifyError(c.Context(), result.Err)
			middleware.LogError(c, result.Err, payload)
			out[i].Error = &payload.Message
			out[i].Status = int32(payload.Status)
			status = fiber.StatusMultiStatus
		}
	}
	return c.Status(status).JSON(internalapi.InternalUploadBulkOutput{Results: &out})
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
