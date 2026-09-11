package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/requestid"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/gofiber/fiber/v3"
)

// baseURLKey is intentionally private to this HTTP adapter.  A request's
// reverse-proxy prefix is protocol state and must not leak into transfers or
// the object domain.
type baseURLKey struct{}

type windowCounter struct {
	Minute int64
	Count  int
}

type windowBytes struct {
	Minute int64
	Bytes  int64
}

var (
	limitMu            sync.Mutex
	requestWindowMap   = map[string]windowCounter{}
	bandwidthWindowMap = map[string]windowBytes{}
)

// lfsRequestMiddleware applies the legacy per-operation media and limiter
// checks before generated strict decoding invokes the handler.
func lfsRequestMiddleware(opts LFSOptions) lfsapi.StrictMiddlewareFunc {
	return func(next lfsapi.StrictHandlerFunc, operationID string) lfsapi.StrictHandlerFunc {
		return func(ctx fiber.Ctx, args interface{}) (interface{}, error) {
			switch operationID {
			case "LfsBatch":
				if !validateLFSRequestHeaders(ctx, true, true) || !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
				if opts.MaxBatchBodyBytes > 0 && int64(len(ctx.Request().Body())) > opts.MaxBatchBodyBytes {
					_ = writeLFSError(ctx, http.StatusRequestEntityTooLarge, "batch request body too large", false)
					return nil, nil
				}
				if request, ok := args.(lfsapi.LfsBatchRequestObject); ok && request.Body != nil {
					var totalBytes int64
					for _, object := range request.Body.Objects {
						if object.Size > 0 {
							totalBytes += object.Size
						}
					}
					if !enforceBandwidthLimit(ctx, opts, totalBytes) {
						return nil, nil
					}
				}
			case "LfsStageMetadata":
				if !validateLFSMetadataHeaders(ctx) || !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
			case "LfsVerify":
				if !validateLFSRequestHeaders(ctx, true, true) || !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
			case "LfsUploadProxy":
				if !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
			}
			return next(ctx, args)
		}
	}
}

// validateLFSMetadataHeaders accepts both content types supported by the
// generated metadata request decoder.  The other LFS operations require the
// vendor media type exclusively.
func validateLFSMetadataHeaders(c fiber.Ctx) bool {
	const mediaType = "application/vnd.git-lfs+json"
	contentType := strings.ToLower(strings.TrimSpace(c.Get("Content-Type")))
	if contentType == "" || (!strings.Contains(contentType, mediaType) && !strings.Contains(contentType, "application/json")) {
		_ = writeLFSError(c, http.StatusUnprocessableEntity, "Content-Type must be "+mediaType, false)
		return false
	}
	return true
}

func enforceRequestLimit(c fiber.Ctx, opts LFSOptions) bool {
	if opts.RequestLimitPerMinute <= 0 {
		return true
	}
	nowMinute := time.Now().UTC().Unix() / 60
	key := requestClientKey(c)
	limitMu.Lock()
	defer limitMu.Unlock()
	window := requestWindowMap[key]
	if window.Minute != nowMinute {
		window = windowCounter{Minute: nowMinute}
	}
	window.Count++
	requestWindowMap[key] = window
	if window.Count > opts.RequestLimitPerMinute {
		_ = writeLFSError(c, http.StatusTooManyRequests, "rate limit exceeded", false)
		return false
	}
	return true
}

func enforceBandwidthLimit(c fiber.Ctx, opts LFSOptions, bytes int64) bool {
	if opts.BandwidthLimitBytesPerMinute <= 0 || bytes <= 0 {
		return true
	}
	nowMinute := time.Now().UTC().Unix() / 60
	key := requestClientKey(c)
	limitMu.Lock()
	defer limitMu.Unlock()
	window := bandwidthWindowMap[key]
	if window.Minute != nowMinute {
		window = windowBytes{Minute: nowMinute}
	}
	if window.Bytes+bytes > opts.BandwidthLimitBytesPerMinute {
		_ = writeLFSError(c, 509, "bandwidth limit exceeded", false)
		return false
	}
	window.Bytes += bytes
	bandwidthWindowMap[key] = window
	return true
}

func requestClientKey(c fiber.Ctx) string {
	authorization := strings.TrimSpace(c.Get("Authorization"))
	if authorization != "" {
		if len(authorization) > 64 {
			authorization = authorization[:64]
		}
		return "auth:" + authorization
	}
	return "addr:" + c.IP()
}

// writeLFSError presents the generated Git LFS error shape and protocol
// media type while retaining request-id and optional Basic challenge headers.
func writeLFSError(c fiber.Ctx, status int, message string, challenge bool) error {
	if challenge {
		c.Set("LFS-Authenticate", `Basic realm="Git LFS"`)
	}
	c.Set("Content-Type", "application/vnd.git-lfs+json")
	payload := lfsapi.LFSErrorResponse{Message: message}
	if requestID := requestid.GetRequestID(c.Context()); requestID != "" {
		payload.RequestId = &requestID
	}
	documentationURL := "https://github.com/git-lfs/git-lfs/blob/main/docs/api"
	payload.DocumentationUrl = &documentationURL
	return c.Status(status).JSON(payload, "application/vnd.git-lfs+json")
}

// validateLFSRequestHeaders validates the media contract for a strict LFS
// operation.  It intentionally accepts */* for clients that use a generic
// Accept header.
func validateLFSRequestHeaders(c fiber.Ctx, requireAccept, requireContentType bool) bool {
	const mediaType = "application/vnd.git-lfs+json"
	if requireAccept {
		accept := strings.ToLower(strings.TrimSpace(c.Get("Accept")))
		if accept == "" || (!strings.Contains(accept, mediaType) && !strings.Contains(accept, "*/*")) {
			_ = writeLFSError(c, http.StatusNotAcceptable, "Accept header must include "+mediaType, false)
			return false
		}
	}
	if requireContentType {
		contentType := strings.ToLower(strings.TrimSpace(c.Get("Content-Type")))
		if contentType == "" || !strings.Contains(contentType, mediaType) {
			_ = writeLFSError(c, http.StatusUnprocessableEntity, "Content-Type must be "+mediaType, false)
			return false
		}
	}
	return true
}

type LFSOptions struct {
	MaxBatchObjects              int
	MaxBatchBodyBytes            int64
	RequestLimitPerMinute        int
	BandwidthLimitBytesPerMinute int64
}

func registerLFSRoutes(router fiber.Router, service *transferlfs.Service, opts LFSOptions) {
	server := &lfsServer{opts: opts, service: service}
	strict := lfsapi.NewStrictHandler(server, []lfsapi.StrictMiddlewareFunc{
		lfsRequestMiddleware(opts),
	})
	router.Use(func(c fiber.Ctx) error {
		c.SetContext(context.WithValue(c.Context(), baseURLKey{}, c.BaseURL()))
		err := c.Next()
		if c.Method() == http.MethodPost && (c.Path() == "/objects/batch" || strings.HasSuffix(c.Path(), "/verify")) {
			c.Set(fiber.HeaderContentType, "application/vnd.git-lfs+json")
		}
		return err
	})
	lfsapi.RegisterHandlers(router, strict)
}

type lfsServer struct {
	opts    LFSOptions
	service *transferlfs.Service
}

func (s *lfsServer) LfsBatch(ctx context.Context, request lfsapi.LfsBatchRequestObject) (lfsapi.LfsBatchResponseObject, error) {
	baseURL, _ := ctx.Value(baseURLKey{}).(string)
	req := request.Body
	if req == nil {
		return lfsapi.LfsBatch500ApplicationVndGitLfsPlusJSONResponse{Message: "missing request body"}, nil
	}
	req.Operation = lfsapi.BatchRequestOperation(strings.ToLower(strings.TrimSpace(string(req.Operation))))
	if req.Operation != "download" && req.Operation != "upload" {
		return lfsapi.LfsBatch422ApplicationVndGitLfsPlusJSONResponse{Message: "operation must be 'download' or 'upload'"}, nil
	}
	if len(req.Objects) == 0 {
		return lfsapi.LfsBatch422ApplicationVndGitLfsPlusJSONResponse{Message: "objects cannot be empty"}, nil
	}
	if s.opts.MaxBatchObjects > 0 && len(req.Objects) > s.opts.MaxBatchObjects {
		return lfsapi.LfsBatch413ApplicationVndGitLfsPlusJSONResponse{Message: "batch contains too many objects"}, nil
	}

	responseObjects := make([]lfsapi.BatchResponseObject, len(req.Objects))
	valid := make([]transferlfs.BatchObject, 0, len(req.Objects))
	validIndexes := make([]int, 0, len(req.Objects))
	for index, input := range req.Objects {
		responseObjects[index] = lfsapi.BatchResponseObject{Oid: input.Oid, Size: input.Size}
		if input.Size < 0 {
			responseObjects[index].Size = 0
			responseObjects[index].Error = &lfsapi.ObjectError{Code: http.StatusBadRequest, Message: "size must be non-negative"}
			continue
		}
		oid := objects.NormalizeOID(input.Oid)
		if oid == "" {
			responseObjects[index].Error = &lfsapi.ObjectError{Code: http.StatusBadRequest, Message: "invalid oid"}
			continue
		}
		responseObjects[index].Oid = oid
		valid = append(valid, transferlfs.BatchObject{OID: oid, Size: input.Size})
		validIndexes = append(validIndexes, index)
	}
	batch, err := s.service.Batch(ctx, transferlfs.BatchRequest{Operation: string(req.Operation), Objects: valid})
	if err != nil {
		return lfsapi.LfsBatch500ApplicationVndGitLfsPlusJSONResponse{Message: lfsInternalError(ctx, "batch", http.StatusInternalServerError, err)}, nil
	}
	for index, item := range batch.Objects {
		responseIndex := validIndexes[index]
		responseObjects[responseIndex].Size = item.Size
		if item.Err != nil {
			responseObjects[responseIndex].Error = batchErrToObjectError(ctx, item.Err, req.Operation == "download")
			continue
		}
		if req.Operation == "download" {
			responseObjects[responseIndex].Actions = &lfsapi.BatchActions{Download: &lfsapi.Action{Href: item.DownloadURL}}
		} else if !item.Existing {
			oid := responseObjects[responseIndex].Oid
			responseObjects[responseIndex].Actions = &lfsapi.BatchActions{Upload: &lfsapi.Action{Href: baseURL + "/info/lfs/objects/" + oid}, Verify: &lfsapi.Action{Href: baseURL + "/info/lfs/verify"}}
		}
	}
	transfer := "basic"
	hashAlgorithm := "sha256"
	return lfsapi.LfsBatch200ApplicationVndGitLfsPlusJSONResponse{Transfer: &transfer, Objects: responseObjects, HashAlgo: &hashAlgorithm}, nil
}

func (s *lfsServer) LfsVerify(ctx context.Context, request lfsapi.LfsVerifyRequestObject) (lfsapi.LfsVerifyResponseObject, error) {
	if request.Body == nil {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "missing request body"}, nil
	}
	oid := objects.NormalizeOID(request.Body.Oid)
	if oid == "" {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "invalid oid"}, nil
	}
	if request.Body.Size < 0 {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "size must be non-negative"}, nil
	}
	if err := s.service.Verify(ctx, oid); err != nil {
		var candidateErr *transferlfs.MetadataCandidateError
		if errors.As(err, &candidateErr) {
			return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: err.Error()}, nil
		}
		if errorapi.IsNotFoundError(err) {
			return lfsapi.LfsVerify404ApplicationVndGitLfsPlusJSONResponse{Message: "Object not found"}, nil
		}
		return lfsapi.LfsVerify500ApplicationVndGitLfsPlusJSONResponse{Message: lfsInternalError(ctx, "verify", http.StatusInternalServerError, err)}, nil
	}
	return lfsapi.LfsVerify200Response{}, nil
}

func (s *lfsServer) LfsStageMetadata(ctx context.Context, request lfsapi.LfsStageMetadataRequestObject) (lfsapi.LfsStageMetadataResponseObject, error) {
	var input *lfsapi.MetadataSubmitRequest
	if request.JSONBody != nil {
		input = request.JSONBody
	} else if request.ApplicationVndGitLfsPlusJSONBody != nil {
		input = request.ApplicationVndGitLfsPlusJSONBody
	}
	if input == nil || len(input.Candidates) == 0 {
		return lfsapi.LfsStageMetadata400JSONResponse{Message: "candidates cannot be empty"}, nil
	}
	for index, candidate := range input.Candidates {
		if candidate.Size != nil && *candidate.Size < 0 {
			return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] size must be non-negative", index)}, nil
		}
	}
	if err := s.service.Stage(ctx, input.Candidates); err != nil {
		var stageErr *transferlfs.MetadataStageError
		if errors.As(err, &stageErr) {
			if stageErr.MissingSHA {
				return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] missing canonical sha256", stageErr.Index)}, nil
			}
			return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] invalid: %v", stageErr.Index, stageErr)}, nil
		}
		return lfsapi.LfsStageMetadata500JSONResponse{Message: lfsInternalError(ctx, "stage metadata", http.StatusInternalServerError, err)}, nil
	}
	return lfsapi.LfsStageMetadata200JSONResponse{Staged: int32(len(input.Candidates))}, nil
}

func (s *lfsServer) LfsUploadProxy(ctx context.Context, request lfsapi.LfsUploadProxyRequestObject) (lfsapi.LfsUploadProxyResponseObject, error) {
	oid := objects.NormalizeOID(request.Oid)
	if oid == "" {
		return lfsapi.LfsUploadProxy400TextResponse("invalid oid"), nil
	}
	if err := s.service.UploadProxy(ctx, oid, request.Body); err != nil {
		if errors.Is(err, errorapi.ErrBucketNotConfigured) {
			return lfsapi.LfsUploadProxy507TextResponse(lfsInternalError(ctx, "upload", http.StatusInsufficientStorage, err)), nil
		}
		return lfsapi.LfsUploadProxy500TextResponse(lfsInternalError(ctx, "upload", http.StatusInternalServerError, err)), nil
	}
	return lfsapi.LfsUploadProxy200Response{}, nil
}

func batchErrToObjectError(ctx context.Context, err error, download bool) *lfsapi.ObjectError {
	if download {
		var lookupErr *transferlfs.DownloadLookupError
		if errors.As(err, &lookupErr) {
			err = lookupErr.Err
		}
	}
	if errors.Is(err, errorapi.ErrObjectLocationUnavailable) {
		return &lfsapi.ObjectError{Code: http.StatusNotFound, Message: "no object location available"}
	}
	if errors.Is(err, errorapi.ErrBucketNotConfigured) {
		return &lfsapi.ObjectError{Code: http.StatusInsufficientStorage, Message: lfsInternalError(ctx, "batch", http.StatusInsufficientStorage, err)}
	}
	if errorapi.IsNotFoundError(err) {
		return &lfsapi.ObjectError{Code: http.StatusNotFound, Message: "object not found"}
	}
	if errors.Is(err, errorapi.ErrAccessDenied) {
		return &lfsapi.ObjectError{Code: http.StatusForbidden, Message: "forbidden"}
	}
	return &lfsapi.ObjectError{Code: http.StatusInternalServerError, Message: lfsInternalError(ctx, "batch", http.StatusInternalServerError, err)}
}

func lfsInternalError(ctx context.Context, operation string, status int, err error) string {
	slog.Error("lfs request failed", "request_id", requestid.GetRequestID(ctx), "operation", operation, "status", status, "err", err)
	return http.StatusText(status)
}
