package apiutil

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/requestmeta"
	"github.com/gofiber/fiber/v3"
)

type publicError interface {
	PublicMessage() string
}

// HandleError maps core/domain errors to standardized Fiber HTTP responses.
// It handles logging and request ID extraction automatically.
func HandleError(c fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}

	status := http.StatusInternalServerError
	msg := err.Error()

	switch {
	case errors.Is(err, faults.ErrNotFound):
		status = http.StatusNotFound
		msg = "Resource not found"
	case errors.Is(err, faults.ErrUnauthorized):
		status = http.StatusForbidden
		if access.IsGen3Mode(c.Context()) && !access.HasAuthHeader(c.Context()) {
			status = http.StatusUnauthorized
		}
		msg = "Unauthorized"
		var publicErr publicError
		if status == http.StatusForbidden && errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case errors.Is(err, faults.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, faults.ErrInvalidInput):
		status = http.StatusBadRequest
	case errors.Is(err, common.ErrNoValidSHA256):
		status = http.StatusBadRequest
		msg = "A valid SHA256 checksum is required"
	case errors.Is(err, common.ErrAccessMethodsRequired):
		status = http.StatusBadRequest
		msg = err.Error()
	}

	requestID := requestmeta.GetRequestID(c.Context())
	if status >= 500 {
		slog.Error("request failed", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "err", err)
	} else {
		slog.Warn("request rejected", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg, "err", err)
	}

	return c.Status(status).SendString(msg)
}

// Reject returns an explicit route-level rejection and logs the public reason.
// Use this for validation failures that are not represented by a domain error.
func Reject(c fiber.Ctx, status int, msg string) error {
	requestID := requestmeta.GetRequestID(c.Context())
	if status >= 500 {
		slog.Error("request failed", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	} else {
		slog.Warn("request rejected", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	}
	return c.Status(status).SendString(msg)
}
