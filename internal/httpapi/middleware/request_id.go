package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/gofiber/fiber/v3"
)

const requestIDHeader = "X-Request-Id"

type RequestIDMiddleware struct {
	logger *slog.Logger
}

func NewRequestIDMiddleware(logger *slog.Logger) *RequestIDMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return &RequestIDMiddleware{logger: logger}
}

func (m *RequestIDMiddleware) FiberMiddleware() fiber.Handler {
	return func(c fiber.Ctx) error {
		requestID := strings.TrimSpace(c.Get(requestIDHeader))
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := requestmeta.WithRequestID(c.Context(), requestID)
		c.SetContext(ctx)

		c.Set(requestIDHeader, requestID)

		start := time.Now()
		m.logger.Debug("request start", "request_id", requestID, "method", c.Method(), "path", c.Path())

		err := c.Next()

		status := c.Response().StatusCode()
		m.logger.Debug(
			fmt.Sprintf("[%d] %s %s", status, c.Method(), c.Path()),
			"request_id", requestID,
			"status", status,
			"duration_ms", time.Since(start).Milliseconds(),
		)

		return err
	}
}

func newRequestID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "rid-fallback"
	}
	return hex.EncodeToString(b[:])
}
