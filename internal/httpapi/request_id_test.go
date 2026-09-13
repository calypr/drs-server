package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/internal/requestid"
	"github.com/gofiber/fiber/v3"
)

func TestRequestIDHandler_GeneratesAndPropagates(t *testing.T) {
	handler := RequestIDHandler(nil)
	app := fiber.New()
	app.Use(handler)
	app.Get("/", func(c fiber.Ctx) error {
		if requestid.GetRequestID(c.Context()) == "" {
			t.Fatalf("expected request id in context")
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(requestIDHeader) == "" {
		t.Fatalf("expected %s response header", requestIDHeader)
	}
}

func TestRequestIDHandlerLogsPendingErrorStatus(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	app := fiber.New(fiber.Config{ErrorHandler: FiberErrorHandler})
	app.Use(RequestIDHandler(logger))
	app.Get("/ordinary", func(fiber.Ctx) error {
		return errors.New("provider detail")
	})
	app.Get("/teapot", func(fiber.Ctx) error {
		return fiber.NewError(http.StatusTeapot, "short and stout")
	})

	for _, test := range []struct {
		path       string
		wantStatus int
		wantLog    string
	}{
		{path: "/ordinary", wantStatus: http.StatusInternalServerError, wantLog: `msg="[500] GET /ordinary"`},
		{path: "/teapot", wantStatus: http.StatusTeapot, wantLog: `msg="[418] GET /teapot"`},
	} {
		t.Run(test.path, func(t *testing.T) {
			resp, err := app.Test(httptest.NewRequest(http.MethodGet, test.path, nil))
			if err != nil {
				t.Fatalf("test request failed: %v", err)
			}
			if resp.StatusCode != test.wantStatus {
				t.Fatalf("expected status %d, got %d", test.wantStatus, resp.StatusCode)
			}
			if !strings.Contains(logs.String(), test.wantLog) {
				t.Fatalf("completion log %q missing %q", logs.String(), test.wantLog)
			}
		})
	}
}

func TestRequestIDHandler_UsesIncomingHeader(t *testing.T) {
	handler := RequestIDHandler(nil)
	const incoming = "rid-test-123"
	app := fiber.New()
	app.Use(handler)
	app.Get("/", func(c fiber.Ctx) error {
		if got := requestid.GetRequestID(c.Context()); got != incoming {
			t.Fatalf("expected request id %q in context, got %q", incoming, got)
		}
		return c.SendStatus(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(requestIDHeader, incoming)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("test request failed: %v", err)
	}

	if got := resp.Header.Get(requestIDHeader); got != incoming {
		t.Fatalf("expected response header %q, got %q", incoming, got)
	}
}
