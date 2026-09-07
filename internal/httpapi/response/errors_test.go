package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	clientapierror "github.com/calypr/syfon/client/apierror"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
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
	return faults.ErrUnauthorized
}

func (e publicMessageError) PublicMessage() string {
	return e.message
}

func TestHandleError(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		ctx         context.Context
		wantStatus  int
		wantCode    faults.Code
		wantMessage string
	}{
		{name: "nil", wantStatus: http.StatusOK},
		{name: "unknown", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantCode: "internal_error", wantMessage: "Internal Server Error"},
		{name: "not found", err: faults.ErrNotFound, wantStatus: http.StatusNotFound, wantCode: "not_found", wantMessage: "Resource not found"},
		{name: "unauthorized", err: faults.ErrUnauthorized, wantStatus: http.StatusForbidden, wantCode: "forbidden", wantMessage: "Unauthorized"},
		{name: "unauthorized gen3 without header", err: faults.ErrUnauthorized, ctx: sessionContext("gen3", false), wantStatus: http.StatusUnauthorized, wantCode: "unauthorized", wantMessage: "Unauthorized"},
		{name: "public unauthorized message", err: publicMessageError{message: "object is outside your grants"}, wantStatus: http.StatusForbidden, wantCode: "forbidden", wantMessage: "object is outside your grants"},
		{name: "public unauthorized message gen3 without header", err: publicMessageError{message: "object is outside your grants"}, ctx: sessionContext("gen3", false), wantStatus: http.StatusUnauthorized, wantCode: "unauthorized", wantMessage: "Unauthorized"},
		{name: "public unauthorized message gen3 with header", err: publicMessageError{message: "object is outside your grants"}, ctx: sessionContext("gen3", true), wantStatus: http.StatusForbidden, wantCode: "forbidden", wantMessage: "object is outside your grants"},
		{name: "forbidden", err: faults.ErrForbidden, wantStatus: http.StatusForbidden, wantCode: "forbidden", wantMessage: "Forbidden"},
		{name: "conflict", err: faults.ErrConflict, wantStatus: http.StatusConflict, wantCode: "conflict", wantMessage: "conflict"},
		{name: "invalid input", err: faults.ErrInvalidInput, wantStatus: http.StatusBadRequest, wantCode: "invalid_input", wantMessage: "invalid input"},
		{name: "rate limited", err: faults.ErrRateLimited, wantStatus: http.StatusTooManyRequests, wantCode: "rate_limited", wantMessage: "Rate limit exceeded"},
		{name: "unavailable", err: faults.ErrUnavailable, wantStatus: http.StatusServiceUnavailable, wantCode: "unavailable", wantMessage: "Service Unavailable"},
		{name: "invalid checksum", err: objects.ErrNoValidSHA256, wantStatus: http.StatusBadRequest, wantCode: "invalid_input", wantMessage: "A valid SHA256 checksum is required"},
		{name: "missing access methods", err: objects.ErrAccessMethodsRequired, wantStatus: http.StatusBadRequest, wantCode: "invalid_input", wantMessage: objects.ErrAccessMethodsRequired.Error()},
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
				var body APIError
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatalf("decode response body: %v", err)
				}
				if body.Code != tc.wantCode || body.Status != tc.wantStatus || body.Message != tc.wantMessage || body.Msg != tc.wantMessage {
					t.Fatalf("unexpected error body: %+v", body)
				}
			}
		})
	}
}

func TestHandleErrorCarriesWrappedFaultCodeAndDetail(t *testing.T) {
	app := fiber.New()
	app.Get("/", func(c fiber.Ctx) error {
		return HandleError(c, fmt.Errorf("%w: object size is immutable", faults.ErrConflict))
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}
	var body APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body.Code != "conflict" || body.Status != http.StatusConflict || body.Message != "conflict: object size is immutable" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func TestReject(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		message    string
		wantStatus int
		wantCode   faults.Code
	}{
		{name: "client rejection", status: http.StatusBadRequest, message: "bucket is required", wantStatus: http.StatusBadRequest, wantCode: "invalid_input"},
		{name: "server rejection", status: http.StatusInternalServerError, message: "dependency unavailable", wantStatus: http.StatusInternalServerError, wantCode: "internal_error"},
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
			var body APIError
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode response body: %v", err)
			}
			wantMessage := tc.message
			if tc.status >= http.StatusInternalServerError {
				wantMessage = http.StatusText(tc.status)
			}
			if body.Code != tc.wantCode || body.Status != tc.wantStatus || body.Message != wantMessage {
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
	var body APIError
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	if body.RequestID != "request-123" || body.Code != "rate_limited" {
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
	if !errors.Is(apiErr, clientapierror.ErrConflict) {
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
