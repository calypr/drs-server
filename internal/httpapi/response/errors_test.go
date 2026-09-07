package response

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/objects"
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
		name       string
		err        error
		ctx        context.Context
		wantStatus int
		wantBody   string
	}{
		{name: "nil", wantStatus: http.StatusOK},
		{name: "unknown", err: errors.New("database unavailable"), wantStatus: http.StatusInternalServerError, wantBody: "database unavailable"},
		{name: "not found", err: faults.ErrNotFound, wantStatus: http.StatusNotFound, wantBody: "Resource not found"},
		{name: "unauthorized", err: faults.ErrUnauthorized, wantStatus: http.StatusForbidden, wantBody: "Unauthorized"},
		{name: "unauthorized gen3 without header", err: faults.ErrUnauthorized, ctx: sessionContext("gen3", false), wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "public unauthorized message", err: publicMessageError{message: "object is outside your grants"}, wantStatus: http.StatusForbidden, wantBody: "object is outside your grants"},
		{name: "public unauthorized message gen3 without header", err: publicMessageError{message: "object is outside your grants"}, ctx: sessionContext("gen3", false), wantStatus: http.StatusUnauthorized, wantBody: "Unauthorized"},
		{name: "public unauthorized message gen3 with header", err: publicMessageError{message: "object is outside your grants"}, ctx: sessionContext("gen3", true), wantStatus: http.StatusForbidden, wantBody: "object is outside your grants"},
		{name: "conflict", err: faults.ErrConflict, wantStatus: http.StatusConflict, wantBody: "conflict"},
		{name: "invalid input", err: faults.ErrInvalidInput, wantStatus: http.StatusBadRequest, wantBody: "invalid input"},
		{name: "invalid checksum", err: objects.ErrNoValidSHA256, wantStatus: http.StatusBadRequest, wantBody: "A valid SHA256 checksum is required"},
		{name: "missing access methods", err: objects.ErrAccessMethodsRequired, wantStatus: http.StatusBadRequest, wantBody: objects.ErrAccessMethodsRequired.Error()},
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
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if got := string(body); got != tc.wantBody {
				t.Fatalf("expected body %q, got %q", tc.wantBody, got)
			}
		})
	}
}

func TestReject(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		message    string
		wantStatus int
		wantBody   string
	}{
		{name: "client rejection", status: http.StatusBadRequest, message: "bucket is required", wantStatus: http.StatusBadRequest, wantBody: "bucket is required"},
		{name: "server rejection", status: http.StatusInternalServerError, message: "dependency unavailable", wantStatus: http.StatusInternalServerError, wantBody: "dependency unavailable"},
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
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read response body: %v", err)
			}
			if got := string(body); got != tc.wantBody {
				t.Fatalf("expected body %q, got %q", tc.wantBody, got)
			}
		})
	}
}

func sessionContext(mode string, authHeader bool) context.Context {
	session := access.NewSession(mode)
	session.AuthHeaderPresent = authHeader
	return access.WithSession(context.Background(), session)
}
