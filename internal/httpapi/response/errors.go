package response

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

type publicError interface {
	PublicMessage() string
}

// APIError is the stable error envelope returned by Syfon HTTP APIs.
type APIError struct {
	Code      faults.Code `json:"code"`
	Status    int         `json:"status"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id,omitempty"`
	Msg       string      `json:"msg,omitempty"`
}

func HandleError(c fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}

	code, ok := faults.CodeOf(err)
	if !ok {
		switch {
		case errors.Is(err, objects.ErrNoValidSHA256), errors.Is(err, objects.ErrAccessMethodsRequired):
			code = faults.CodeInvalidInput
		default:
			code = faults.CodeInternal
		}
	}
	status := statusForCode(code)
	msg := err.Error()

	switch code {
	case faults.CodeNotFound:
		msg = "Resource not found"
	case faults.CodeUnauthorized:
		status = http.StatusForbidden
		code = faults.CodeForbidden
		if access.IsGen3Mode(c.Context()) && !access.HasAuthHeader(c.Context()) {
			status = http.StatusUnauthorized
			code = faults.CodeUnauthorized
		}
		msg = "Unauthorized"
		var publicErr publicError
		if status == http.StatusForbidden && errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case faults.CodeForbidden:
		msg = "Forbidden"
	case faults.CodeRateLimited:
		msg = "Rate limit exceeded"
	case faults.CodeUnavailable:
		msg = "Service unavailable"
	case faults.CodeInvalidInput:
		switch {
		case errors.Is(err, objects.ErrNoValidSHA256):
			msg = "A valid SHA256 checksum is required"
		case errors.Is(err, objects.ErrAccessMethodsRequired):
			msg = err.Error()
		}
	case faults.CodeInternal:
		msg = http.StatusText(http.StatusInternalServerError)
	}
	if status >= http.StatusInternalServerError {
		msg = http.StatusText(status)
	}

	requestID := requestid.GetRequestID(c.Context())
	if status >= 500 {
		slog.Error("request failed", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "err", err)
	} else {
		slog.Warn("request rejected", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg, "err", err)
	}

	return send(c, code, status, msg, requestID)
}

func Reject(c fiber.Ctx, status int, msg string) error {
	requestID := requestid.GetRequestID(c.Context())
	if status >= 500 {
		slog.Error("request failed", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	} else {
		slog.Warn("request rejected", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	}
	return send(c, codeForStatus(status), status, msg, requestID)
}

// FiberErrorHandler converts errors returned through Fiber into the same API
// error contract used by explicit handler rejections.
func FiberErrorHandler(c fiber.Ctx, err error) error {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return Reject(c, fiberErr.Code, fiberErr.Message)
	}
	return HandleError(c, err)
}

func send(c fiber.Ctx, code faults.Code, status int, msg, requestID string) error {
	payload := NewAPIError(c.Context(), code, status, msg)
	if requestID != "" {
		payload.RequestID = requestID
	}
	return c.Status(status).JSON(payload)
}

// NewAPIError builds the shared wire payload for Fiber and generated handlers.
func NewAPIError(ctx context.Context, code faults.Code, status int, msg string) APIError {
	msg = strings.TrimSpace(msg)
	if status >= http.StatusInternalServerError {
		msg = http.StatusText(status)
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return APIError{
		Code:      code,
		Status:    status,
		Message:   msg,
		RequestID: requestid.GetRequestID(ctx),
		Msg:       msg,
	}
}

func codeForStatus(status int) faults.Code {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusRequestEntityTooLarge:
		return faults.CodeInvalidInput
	case http.StatusUnauthorized:
		return faults.CodeUnauthorized
	case http.StatusForbidden:
		return faults.CodeForbidden
	case http.StatusNotFound:
		return faults.CodeNotFound
	case http.StatusConflict:
		return faults.CodeConflict
	case http.StatusTooManyRequests:
		return faults.CodeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return faults.CodeUnavailable
	default:
		if status >= http.StatusInternalServerError {
			return faults.CodeInternal
		}
		return faults.CodeRequestFailed
	}
}

func statusForCode(code faults.Code) int {
	switch code {
	case faults.CodeInvalidInput:
		return http.StatusBadRequest
	case faults.CodeUnauthorized:
		return http.StatusUnauthorized
	case faults.CodeForbidden:
		return http.StatusForbidden
	case faults.CodeNotFound:
		return http.StatusNotFound
	case faults.CodeConflict:
		return http.StatusConflict
	case faults.CodeRateLimited:
		return http.StatusTooManyRequests
	case faults.CodeUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
