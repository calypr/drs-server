package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

type publicError interface {
	PublicMessage() string
}

// ClassifyError converts an application error into the public API error
// envelope. The original error remains available to the boundary logger and
// is never used as a response message for server failures.
func ClassifyError(ctx context.Context, err error) errorapi.APIError {
	if err == nil {
		return errorapi.APIError{}
	}

	code, ok := errorapi.CodeOf(err)
	if !ok {
		code = errorapi.ErrorCodeInternalError
	}
	category, categoryOK := errorapi.CategoryOf(err)
	if !categoryOK {
		category, categoryOK = errorapi.CategoryForCode(code)
	}
	if !categoryOK {
		category = errorapi.ErrorCategoryInternalError
	}
	status := statusForCategory(category)
	msg := err.Error()

	switch category {
	case errorapi.ErrorCategoryNotFound:
		msg = "Resource not found"
		if code == errorapi.ErrorCodeMultipartUploadNotFound {
			msg = "Upload ID not found"
		}
	case errorapi.ErrorCategoryUnauthorized:
		if code == errorapi.ErrorCodeUnauthorized {
			code = errorapi.ErrorCodeAccessDenied
			category = errorapi.ErrorCategoryForbidden
			status = http.StatusForbidden
			if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
				code = errorapi.ErrorCodeAuthenticationRequired
				category = errorapi.ErrorCategoryUnauthorized
				status = http.StatusUnauthorized
			}
		}
		msg = "Unauthorized"
		var publicErr publicError
		if status == http.StatusForbidden && errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case errorapi.ErrorCategoryForbidden:
		msg = "Forbidden"
		var publicErr publicError
		if errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case errorapi.ErrorCategoryRateLimited:
		msg = "Rate limit exceeded"
	case errorapi.ErrorCategoryUnavailable:
		msg = "Service unavailable"
	case errorapi.ErrorCategoryInvalidInput:
		switch code {
		case errorapi.ErrorCodeNoValidSha256:
			msg = "A valid SHA256 checksum is required"
		case errorapi.ErrorCodeAccessMethodsRequired:
			msg = err.Error()
		default:
			var publicErr publicError
			if errors.As(err, &publicErr) {
				msg = publicErr.PublicMessage()
			}
		}
	case errorapi.ErrorCategoryConflict:
		var publicErr publicError
		if errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case errorapi.ErrorCategoryInternalError:
		msg = http.StatusText(http.StatusInternalServerError)
	}
	return newAPIError(ctx, code, category, status, msg)
}

// logError records the original cause separately from its public API value.
func logError(c fiber.Ctx, err error, payload errorapi.APIError) {
	requestID := requestid.GetRequestID(c.Context())
	args := []any{
		"request_id", requestID,
		"method", c.Method(),
		"path", c.Path(),
		"status", payload.Status,
		"code", payload.Code,
		"category", payload.Category,
		"err", err,
	}
	if payload.Status >= http.StatusInternalServerError {
		slog.Error("request failed", args...)
	} else {
		slog.Warn("request rejected", args...)
	}
}

func HandleError(c fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}

	payload := ClassifyError(c.Context(), err)
	logError(c, err, payload)
	return c.Status(payload.Status).JSON(payload)
}

func Reject(c fiber.Ctx, status int, msg string) error {
	requestID := requestid.GetRequestID(c.Context())
	if status >= 500 {
		slog.Error("request failed", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	} else {
		slog.Warn("request rejected", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	}
	code := errorapi.CodeForStatus(status)
	if status == http.StatusUnauthorized {
		code = errorapi.ErrorCodeAuthenticationRequired
	} else if status == http.StatusForbidden {
		code = errorapi.ErrorCodeAccessDenied
	}
	category, _ := errorapi.CategoryForCode(code)
	return sendWithCategory(c, code, category, status, msg, requestID)
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

func sendWithCategory(c fiber.Ctx, code errorapi.ErrorCode, category errorapi.ErrorCategory, status int, msg, requestID string) error {
	payload := newAPIError(c.Context(), code, category, status, msg)
	if requestID != "" {
		payload.RequestId = &requestID
	}
	return c.Status(status).JSON(payload)
}

// NewAPIError builds the shared wire payload for Fiber and generated handlers.
func NewAPIError(ctx context.Context, code errorapi.ErrorCode, status int, msg string) errorapi.APIError {
	category, _ := errorapi.CategoryForCode(code)
	return newAPIError(ctx, code, category, status, msg)
}

func newAPIError(ctx context.Context, code errorapi.ErrorCode, category errorapi.ErrorCategory, status int, msg string) errorapi.APIError {
	msg = strings.TrimSpace(msg)
	if status >= http.StatusInternalServerError {
		msg = publicStatusText(status)
	}
	if msg == "" {
		msg = publicStatusText(status)
	}
	payload := errorapi.APIError{Code: code, Category: category, Status: status, Message: msg, Msg: &msg, StatusCode: &status}
	if requestID := requestid.GetRequestID(ctx); requestID != "" {
		payload.RequestId = &requestID
	}
	return payload
}

func publicStatusText(status int) string {
	if message := http.StatusText(status); message != "" {
		return message
	}
	return http.StatusText(http.StatusInternalServerError)
}

func statusForCategory(category errorapi.ErrorCategory) int {
	switch category {
	case errorapi.ErrorCategoryInvalidInput:
		return http.StatusBadRequest
	case errorapi.ErrorCategoryUnauthorized:
		return http.StatusUnauthorized
	case errorapi.ErrorCategoryForbidden:
		return http.StatusForbidden
	case errorapi.ErrorCategoryNotFound:
		return http.StatusNotFound
	case errorapi.ErrorCategoryConflict:
		return http.StatusConflict
	case errorapi.ErrorCategoryRateLimited:
		return http.StatusTooManyRequests
	case errorapi.ErrorCategoryUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
