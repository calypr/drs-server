package lfs

import (
	"context"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/requestid"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/gofiber/fiber/v3"
	"net/http"
	"strings"
	"sync"
	"time"
)

// baseURLKey is intentionally private to this HTTP adapter.  A request's
// reverse-proxy prefix is protocol state and must not leak into transfers or
// the object domain.
type baseURLKey struct{}

// WithBaseURL attaches the Fiber request base URL to a request context.
func WithBaseURL(ctx context.Context, baseURL string) context.Context {
	return context.WithValue(ctx, baseURLKey{}, baseURL)
}

// GetBaseURL returns the request base URL previously attached by the route
// adapter.
func GetBaseURL(ctx context.Context) string {
	value, _ := ctx.Value(baseURLKey{}).(string)
	return value
}

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

// ResetLFSLimitersForTest clears the process-global limiter windows.
func ResetLFSLimitersForTest() {
	limitMu.Lock()
	defer limitMu.Unlock()
	requestWindowMap = map[string]windowCounter{}
	bandwidthWindowMap = map[string]windowBytes{}
}

// LFSRequestMiddleware applies the legacy per-operation media and limiter
// checks before generated strict decoding invokes the handler.
func LFSRequestMiddleware(opts Options) lfsapi.StrictMiddlewareFunc {
	return func(next lfsapi.StrictHandlerFunc, operationID string) lfsapi.StrictHandlerFunc {
		return func(ctx fiber.Ctx, args interface{}) (interface{}, error) {
			switch operationID {
			case "LfsBatch":
				if !ValidateLFSRequestHeaders(ctx, true, true) || !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
				if opts.MaxBatchBodyBytes > 0 && int64(len(ctx.Request().Body())) > opts.MaxBatchBodyBytes {
					_ = WriteLFSError(ctx, http.StatusRequestEntityTooLarge, "batch request body too large", false)
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
				if !ValidateLFSMetadataHeaders(ctx) || !enforceRequestLimit(ctx, opts) {
					return nil, nil
				}
			case "LfsVerify":
				if !ValidateLFSRequestHeaders(ctx, true, true) || !enforceRequestLimit(ctx, opts) {
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

// ValidateLFSMetadataHeaders accepts both content types supported by the
// generated metadata request decoder.  The other LFS operations require the
// vendor media type exclusively.
func ValidateLFSMetadataHeaders(c fiber.Ctx) bool {
	const mediaType = "application/vnd.git-lfs+json"
	contentType := strings.ToLower(strings.TrimSpace(c.Get("Content-Type")))
	if contentType == "" || (!strings.Contains(contentType, mediaType) && !strings.Contains(contentType, "application/json")) {
		_ = WriteLFSError(c, http.StatusUnprocessableEntity, "Content-Type must be "+mediaType, false)
		return false
	}
	return true
}

func enforceRequestLimit(c fiber.Ctx, opts Options) bool {
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
		_ = WriteLFSError(c, http.StatusTooManyRequests, "rate limit exceeded", false)
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
	window := bandwidthWindowMap[key]
	if window.Minute != nowMinute {
		window = windowBytes{Minute: nowMinute}
	}
	if window.Bytes+bytes > opts.BandwidthLimitBytesPerMinute {
		_ = WriteLFSError(c, 509, "bandwidth limit exceeded", false)
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

// WriteLFSError presents the generated Git LFS error shape and protocol
// media type while retaining request-id and optional Basic challenge headers.
func WriteLFSError(c fiber.Ctx, status int, message string, challenge bool) error {
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

// ValidateLFSRequestHeaders validates the media contract for a strict LFS
// operation.  It intentionally accepts */* for clients that use a generic
// Accept header.
func ValidateLFSRequestHeaders(c fiber.Ctx, requireAccept, requireContentType bool) bool {
	const mediaType = "application/vnd.git-lfs+json"
	if requireAccept {
		accept := strings.ToLower(strings.TrimSpace(c.Get("Accept")))
		if accept == "" || (!strings.Contains(accept, mediaType) && !strings.Contains(accept, "*/*")) {
			_ = WriteLFSError(c, http.StatusNotAcceptable, "Accept header must include "+mediaType, false)
			return false
		}
	}
	if requireContentType {
		contentType := strings.ToLower(strings.TrimSpace(c.Get("Content-Type")))
		if contentType == "" || !strings.Contains(contentType, mediaType) {
			_ = WriteLFSError(c, http.StatusUnprocessableEntity, "Content-Type must be "+mediaType, false)
			return false
		}
	}
	return true
}

type Options struct {
	MaxBatchObjects              int
	MaxBatchBodyBytes            int64
	RequestLimitPerMinute        int
	BandwidthLimitBytesPerMinute int64
}

type Dependencies struct {
	Service *transferlfs.Service
}

// DefaultOptions returns the historical Git LFS limits.
func DefaultOptions() Options {
	return Options{
		MaxBatchObjects:              1000,
		MaxBatchBodyBytes:            10 * 1024 * 1024,
		RequestLimitPerMinute:        1200,
		BandwidthLimitBytesPerMinute: 0,
	}
}

func RegisterLFSRoutes(router fiber.Router, deps Dependencies, opts ...Options) {
	effective := DefaultOptions()
	if len(opts) > 0 {
		effective = opts[0]
	}
	server := NewLFSServer(deps.Service, effective)
	strict := lfsapi.NewStrictHandler(server, []lfsapi.StrictMiddlewareFunc{
		LFSRequestMiddleware(effective),
	})
	router.Use(func(c fiber.Ctx) error {
		c.SetContext(WithBaseURL(c.Context(), c.BaseURL()))
		return c.Next()
	})
	lfsapi.RegisterHandlers(router, strict)
}
