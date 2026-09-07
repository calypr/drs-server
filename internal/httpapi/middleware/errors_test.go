package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	clientapierror "github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

type publicMessageError struct {
	message string
}

func (e publicMessageError) Error() string {
	return "internal authorization detail"
}

func (e publicMessageError) Unwrap() error {
	return errorapi.ErrAccessDenied
}

func (e publicMessageError) PublicMessage() string {
	return e.message
}

func TestHandleError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		ctx          context.Context
		wantStatus   int
		wantCode     errorapi.ErrorCode
		wantCategory errorapi.ErrorCategory
		wantMessage  string
	}{
		{name: "nil", wantStatus: http.StatusOK},
		{name: "unknown", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: errorapi.ErrorCodeInternalError, wantCategory: errorapi.ErrorCategoryInternalError, wantMessage: "Internal Server Error"},
		{name: "not found", err: errorapi.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: errorapi.ErrorCodeNotFound, wantCategory: errorapi.ErrorCategoryNotFound, wantMessage: "Resource not found"},
		{name: "legacy unauthorized", err: errorapi.ErrUnauthorized, wantStatus: http.StatusForbidden, wantCode: errorapi.ErrorCodeAccessDenied, wantCategory: errorapi.ErrorCategoryForbidden, wantMessage: "Unauthorized"},
		{name: "legacy unauthorized without credentials", err: errorapi.ErrUnauthorized, ctx: sessionContext("gen3", false), wantStatus: http.StatusUnauthorized, wantCode: errorapi.ErrorCodeAuthenticationRequired, wantCategory: errorapi.ErrorCategoryUnauthorized, wantMessage: "Unauthorized"},
		{name: "authentication required", err: errorapi.ErrAuthenticationRequired, wantStatus: http.StatusUnauthorized, wantCode: errorapi.ErrorCodeAuthenticationRequired, wantCategory: errorapi.ErrorCategoryUnauthorized, wantMessage: "Unauthorized"},
		{name: "access denied", err: errorapi.ErrAccessDenied, wantStatus: http.StatusForbidden, wantCode: errorapi.ErrorCodeAccessDenied, wantCategory: errorapi.ErrorCategoryForbidden, wantMessage: "Forbidden"},
		{name: "public access denied message", err: publicMessageError{message: "object is outside your grants"}, wantStatus: http.StatusForbidden, wantCode: errorapi.ErrorCodeAccessDenied, wantCategory: errorapi.ErrorCategoryForbidden, wantMessage: "object is outside your grants"},
		{name: "forbidden", err: errorapi.ErrForbidden, wantStatus: http.StatusForbidden, wantCode: errorapi.ErrorCodeForbidden, wantCategory: errorapi.ErrorCategoryForbidden, wantMessage: "Forbidden"},
		{name: "conflict", err: errorapi.ErrConflict, wantStatus: http.StatusConflict, wantCode: errorapi.ErrorCodeConflict, wantCategory: errorapi.ErrorCategoryConflict, wantMessage: "conflict"},
		{name: "invalid input", err: errorapi.ErrInvalidInput, wantStatus: http.StatusBadRequest, wantCode: errorapi.ErrorCodeInvalidInput, wantCategory: errorapi.ErrorCategoryInvalidInput, wantMessage: "invalid input"},
		{name: "rate limited", err: errorapi.ErrRateLimited, wantStatus: http.StatusTooManyRequests, wantCode: errorapi.ErrorCodeRateLimited, wantCategory: errorapi.ErrorCategoryRateLimited, wantMessage: "Rate limit exceeded"},
		{name: "unavailable", err: errorapi.ErrUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: errorapi.ErrorCodeUnavailable, wantCategory: errorapi.ErrorCategoryUnavailable, wantMessage: "Service Unavailable"},
		{name: "invalid checksum", err: errorapi.ErrNoValidSHA256, wantStatus: http.StatusBadRequest, wantCode: errorapi.ErrorCodeNoValidSha256, wantCategory: errorapi.ErrorCategoryInvalidInput, wantMessage: "A valid SHA256 checksum is required"},
		{name: "missing access methods", err: errorapi.ErrAccessMethodsRequired, wantStatus: http.StatusBadRequest, wantCode: errorapi.ErrorCodeAccessMethodsRequired, wantCategory: errorapi.ErrorCategoryInvalidInput, wantMessage: errorapi.ErrAccessMethodsRequired.Error()},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				if tc.ctx != nil {
					c.SetContext(tc.ctx)
				}
				return HandleError(c, tc.err)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			if tc.err != nil {
				var body errorapi.APIError
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatalf("decode response body: %v", err)
				}
				if body.Code != tc.wantCode || body.Category != tc.wantCategory || body.Status != tc.wantStatus || body.StatusCode == nil || *body.StatusCode != tc.wantStatus || body.Message != tc.wantMessage || body.Msg == nil || *body.Msg != tc.wantMessage {
					t.Fatalf("unexpected error body: %+v", body)
				}
			}
		})
	}
}

func TestHandleErrorCarriesWrappedFaultCodeAndDetail(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return HandleError(c, errorapi.ErrObjectSizeImmutable)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	var body errorapi.APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body.Code != errorapi.ErrorCodeObjectSizeImmutable || body.Category != errorapi.ErrorCategoryConflict || body.Status != http.StatusConflict || body.Message != "object size is immutable" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestReject(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		message      string
		wantStatus   int
		wantCode     errorapi.ErrorCode
		wantCategory errorapi.ErrorCategory
	}{
		{name: "client rejection", status: http.StatusBadRequest, message: "bucket is required", wantStatus: http.StatusBadRequest, wantCode: errorapi.ErrorCodeInvalidInput, wantCategory: errorapi.ErrorCategoryInvalidInput},
		{name: "authentication rejection", status: http.StatusUnauthorized, message: "Unauthorized", wantStatus: http.StatusUnauthorized, wantCode: errorapi.ErrorCodeAuthenticationRequired, wantCategory: errorapi.ErrorCategoryUnauthorized},
		{name: "authorization rejection", status: http.StatusForbidden, message: "Forbidden", wantStatus: http.StatusForbidden, wantCode: errorapi.ErrorCodeAccessDenied, wantCategory: errorapi.ErrorCategoryForbidden},
		{name: "server rejection", status: http.StatusInternalServerError, message: "dependency unavailable", wantStatus: http.StatusInternalServerError, wantCode: errorapi.ErrorCodeInternalError, wantCategory: errorapi.ErrorCategoryInternalError},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			app := fiber.New()
			app.Get("/", func(c fiber.Ctx) error {
				return Reject(c, tc.status, tc.message)
			})

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, resp.StatusCode)
			}
			var body errorapi.APIError
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode response body: %v", err)
			}
			wantMessage := tc.message
			if tc.status >= http.StatusInternalServerError {
				wantMessage = http.StatusText(tc.status)
			}
			if body.Code != tc.wantCode || body.Category != tc.wantCategory || body.Status != tc.wantStatus || body.Message != wantMessage {
				t.Fatalf("unexpected error body: %+v", body)
			}
		})
	}
}

func TestRejectIncludesRequestID(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		c.SetContext(requestid.WithRequestID(c.Context(), "request-123"))
		return Reject(c, http.StatusTooManyRequests, "try later")
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	var body errorapi.APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body.RequestId == nil || *body.RequestId != "request-123" || body.Code != "rate_limited" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestErrorEnvelopeRoundTripsThroughClient(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		c.SetContext(requestid.WithRequestID(c.Context(), "request-456"))
		return Reject(c, http.StatusConflict, "object already exists")
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	apiErr := clientapierror.FromResponse(resp, body)
	if !errors.Is(apiErr, errorapi.ErrConflict) {
		t.Fatalf("expected conflict sentinel, got %v", apiErr)
	}
	if apiErr.Code != "conflict" || apiErr.Status != http.StatusConflict || apiErr.Message != "object already exists" || apiErr.RequestID != "request-456" {
		t.Fatalf("unexpected client API error: %+v", apiErr)
	}
}

func sessionContext(mode string, authHeader bool) context.Context {
	session := access.NewSession(mode)
	session.AuthHeaderPresent = authHeader
	return access.WithSession(context.Background(), session)
}
