package lfs

import (
	"net/http"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/lfsapi"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/gofiber/fiber/v3"
)

func LFSRequestMiddleware(opts Options) lfsapi.StrictMiddlewareFunc {
	return func(next lfsapi.StrictHandlerFunc, operationID string) lfsapi.StrictHandlerFunc {
		return func(ctx fiber.Ctx, args interface{}) (interface{}, error) {
			switch operationID {
			case "LfsBatch":
				if !ValidateLFSRequestHeaders(ctx, true, true) {
					return nil, nil
				}
				if !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
				if opts.MaxBatchBodyBytes > 0 && int64(len(ctx.Request().Body())) > opts.MaxBatchBodyBytes {
					WriteLFSError(ctx, http.StatusRequestEntityTooLarge, "batch request body too large", false)
					return nil, nil
				}
				if req, ok := args.(lfsapi.LfsBatchRequestObject); ok && req.Body != nil {
					totalBytes := int64(0)
					for _, in := range req.Body.Objects {
						if in.Size > 0 {
							totalBytes += in.Size
						}
					}
					if !enforceBandwidthLimit(ctx, opts, totalBytes) {
						return nil, nil
					}
				}
			case "LfsStageMetadata":
				if !ValidateLFSRequestHeaders(ctx, false, true) {
					return nil, nil
				}
				if !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
			case "LfsVerify":
				if !ValidateLFSRequestHeaders(ctx, true, true) {
					return nil, nil
				}
				if !enforceRequestLimit(ctx, opts) {
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

func enforceRequestLimit(c fiber.Ctx, opts Options) bool {
	if opts.RequestLimitPerMinute <= 0 {
		return true
	}
	nowMinute := time.Now().UTC().Unix() / 60
	key := requestClientKey(c)
	limitMu.Lock()
	defer limitMu.Unlock()
	win := requestWindowMap[key]
	if win.Minute != nowMinute {
		win.Minute = nowMinute
		win.Count = 0
	}
	win.Count++
	requestWindowMap[key] = win
	if win.Count > opts.RequestLimitPerMinute {
		WriteLFSError(c, http.StatusTooManyRequests, "rate limit exceeded", false)
		return false
	}
	return true
}

func enforceBandwidthLimit(c fiber.Ctx, opts Options, bytes int64) bool {
	if opts.BandwidthLimitBytesPerMinute <= 0 || bytes <= 0 {
		return true
	}
	nowMinute := time.Now().UTC().Unix() / 60
	key := requestClientKey(c)
	limitMu.Lock()
	defer limitMu.Unlock()
	win := bandwidthWindowMap[key]
	if win.Minute != nowMinute {
		win.Minute = nowMinute
		win.Bytes = 0
	}
	if win.Bytes+bytes > opts.BandwidthLimitBytesPerMinute {
		WriteLFSError(c, 509, "bandwidth limit exceeded", false)
		return false
	}
	win.Bytes += bytes
	bandwidthWindowMap[key] = win
	return true
}

func requestClientKey(c fiber.Ctx) string {
	if auth := strings.TrimSpace(c.Get("Authorization")); auth != "" {
		if len(auth) > 64 {
			auth = auth[:64]
		}
		return "auth:" + auth
	}
	return "addr:" + c.IP()
}

func WriteLFSError(c fiber.Ctx, status int, message string, challenge bool) error {
	if challenge {
		c.Set("LFS-Authenticate", `Basic realm="Git LFS"`)
	}
	c.Set("Content-Type", "application/vnd.git-lfs+json")
	payload := lfsapi.LFSErrorResponse{Message: message}
	if reqID := requestmeta.GetRequestID(c.Context()); reqID != "" {
		payload.RequestId = &reqID
	}
	docURL := "https://github.com/git-lfs/git-lfs/blob/main/docs/api"
	payload.DocumentationUrl = &docURL
	return c.Status(status).JSON(payload)
}

func ValidateLFSRequestHeaders(c fiber.Ctx, requireAccept bool, requireContentType bool) bool {
	const mediaType = "application/vnd.git-lfs+json"
	if requireAccept {
		accept := strings.ToLower(strings.TrimSpace(c.Get("Accept")))
		if accept == "" || (!strings.Contains(accept, mediaType) && !strings.Contains(accept, "*/*")) {
			WriteLFSError(c, http.StatusNotAcceptable, "Accept header must include "+mediaType, false)
			return false
		}
	}
	if requireContentType {
		contentType := strings.TrimSpace(c.Get("Content-Type"))
		if contentType == "" {
			WriteLFSError(c, http.StatusUnprocessableEntity, "Content-Type must be "+mediaType, false)
			return false
		}
		if !strings.Contains(strings.ToLower(contentType), mediaType) {
			WriteLFSError(c, http.StatusUnprocessableEntity, "Content-Type must be "+mediaType, false)
			return false
		}
	}
	return true
}
