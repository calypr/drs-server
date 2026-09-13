package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

const requestIDHeader = "X-Request-Id"

// RequestIDHandler adds the request ID to context and response headers while
// recording the request lifecycle at the HTTP boundary.
func RequestIDHandler(logger *slog.Logger) fiber.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c fiber.Ctx) error {
		requestID := strings.TrimSpace(c.Get(requestIDHeader))
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := requestid.WithRequestID(c.Context(), requestID)
		c.SetContext(ctx)

		c.Set(requestIDHeader, requestID)

		start := time.Now()
		logger.Debug("request start", "request_id", requestID, "method", c.Method(), "path", c.Path())

		err := c.Next()

		status := pendingResponseStatus(c, err)
		logger.Debug(
			fmt.Sprintf("[%d] %s %s", status, c.Method(), c.Path()),
			"request_id", requestID,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		return err
	}
}

func pendingResponseStatus(c fiber.Ctx, err error) int {
	if err == nil {
		return c.Response().StatusCode()
	}
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Code
	}
	return ClassifyError(c.Context(), err).Status
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rid-fallback"
	}
	return hex.EncodeToString(b[:])
}
