package response

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

type publicError interface {
	PublicMessage() string
}

// APIError is the generated error envelope shared by every Syfon HTTP API.
type APIError = errorapi.APIError

func HandleError(c fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}

	code, ok := faults.CodeOf(err)
	if !ok {
		code = faults.CodeInternal
	}
	category, categoryOK := faults.CategoryOf(err)
	if !categoryOK {
		category, categoryOK = faults.CategoryForCode(code)
	}
	if !categoryOK {
		category = faults.CategoryInternal
	}
	status := statusForCategory(category)
	msg := err.Error()

	switch category {
	case faults.CategoryNotFound:
		msg = "Resource not found"
	case faults.CategoryUnauthorized:
		if code == faults.CodeUnauthorized {
			code = faults.CodeAccessDenied
			category = faults.CategoryForbidden
			status = http.StatusForbidden
			if access.IsGen3Mode(c.Context()) && !access.HasAuthHeader(c.Context()) {
				code = faults.CodeAuthenticationRequired
				category = faults.CategoryUnauthorized
				status = http.StatusUnauthorized
			}
		}
		msg = "Unauthorized"
		var publicErr publicError
		if status == http.StatusForbidden && errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case faults.CategoryForbidden:
		msg = "Forbidden"
		var publicErr publicError
		if errors.As(err, &publicErr) {
			msg = publicErr.PublicMessage()
		}
	case faults.CategoryRateLimited:
		msg = "Rate limit exceeded"
	case faults.CategoryUnavailable:
		msg = "Service unavailable"
	case faults.CategoryInvalidInput:
		switch code {
		case faults.CodeNoValidSHA256:
			msg = "A valid SHA256 checksum is required"
		case faults.CodeAccessMethodsRequired:
			msg = err.Error()
		}
	case faults.CategoryInternal:
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

	return sendWithCategory(c, code, category, status, msg, requestID)
}

func Reject(c fiber.Ctx, status int, msg string) error {
	requestID := requestid.GetRequestID(c.Context())
	if status >= 500 {
		slog.Error("request failed", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	} else {
		slog.Warn("request rejected", "request_id", requestID, "method", c.Method(), "path", c.Path(), "status", status, "msg", msg)
	}
	code := codeForStatus(status)
	if status == http.StatusUnauthorized {
		code = faults.CodeAuthenticationRequired
	} else if status == http.StatusForbidden {
		code = faults.CodeAccessDenied
	}
	category, _ := faults.CategoryForCode(code)
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

func send(c fiber.Ctx, code faults.Code, status int, msg, requestID string) error {
	category, _ := faults.CategoryForCode(code)
	return sendWithCategory(c, code, category, status, msg, requestID)
}

func sendWithCategory(c fiber.Ctx, code faults.Code, category faults.Category, status int, msg, requestID string) error {
	payload := NewAPIErrorWithCategory(c.Context(), code, category, status, msg)
	if requestID != "" {
		payload.RequestId = &requestID
	}
	return c.Status(status).JSON(payload)
}

// NewAPIError builds the shared wire payload for Fiber and generated handlers.
func NewAPIError(ctx context.Context, code faults.Code, status int, msg string) APIError {
	category, _ := faults.CategoryForCode(code)
	return NewAPIErrorWithCategory(ctx, code, category, status, msg)
}

func NewAPIErrorWithCategory(ctx context.Context, code faults.Code, category faults.Category, status int, msg string) APIError {
	msg = strings.TrimSpace(msg)
	if status >= http.StatusInternalServerError {
		msg = publicStatusText(status)
	}
	if msg == "" {
		msg = publicStatusText(status)
	}
	payload := APIError{Code: code, Category: category, Status: status, Message: msg, Msg: &msg, StatusCode: &status}
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
	category, ok := faults.CategoryForCode(code)
	if !ok {
		return http.StatusInternalServerError
	}
	return statusForCategory(category)
}

func statusForCategory(category faults.Category) int {
	switch category {
	case faults.CategoryInvalidInput:
		return http.StatusBadRequest
	case faults.CategoryUnauthorized:
		return http.StatusUnauthorized
	case faults.CategoryForbidden:
		return http.StatusForbidden
	case faults.CategoryNotFound:
		return http.StatusNotFound
	case faults.CategoryConflict:
		return http.StatusConflict
	case faults.CategoryRateLimited:
		return http.StatusTooManyRequests
	case faults.CategoryUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
