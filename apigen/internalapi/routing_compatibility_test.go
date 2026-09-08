package internalapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type routingCompatibilityServer struct {
	ServerInterface
	called bool
	params InternalListParams
}

func (s *routingCompatibilityServer) InternalList(c fiber.Ctx, params InternalListParams) error {
	s.called = true
	s.params = params
	return c.SendStatus(fiber.StatusNoContent)
}

func TestRawQueryCompatibilityDefersValidationToOperation(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		raw        bool
		wantStatus int
		wantCalled bool
		wantLimit  *int
	}{
		{name: "malformed default", query: "?limit=bad", wantStatus: http.StatusBadRequest},
		{name: "malformed raw", query: "?limit=bad", raw: true, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "empty default", query: "?limit=", wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "empty raw", query: "?limit=", raw: true, wantStatus: http.StatusNoContent, wantCalled: true},
		{name: "multiple default", query: "?limit=1&limit=2", wantStatus: http.StatusNoContent, wantCalled: true, wantLimit: intPointer(1)},
		{name: "multiple raw", query: "?limit=1&limit=2", raw: true, wantStatus: http.StatusNoContent, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &routingCompatibilityServer{}
			app := fiber.New()
			options := FiberServerOptions{}
			if tt.raw {
				options.RawQueryOperations = map[string]bool{"InternalList": true}
			}
			RegisterHandlersWithOptions(app, server, options)

			request := httptest.NewRequest(http.MethodGet, "/index"+tt.query, nil)
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.wantStatus)
			}
			if server.called != tt.wantCalled {
				t.Fatalf("handler called = %t, want %t", server.called, tt.wantCalled)
			}
			if tt.wantLimit == nil {
				if server.params.Limit != nil {
					t.Fatalf("limit = %v, want nil", *server.params.Limit)
				}
				return
			}
			if server.params.Limit == nil || *server.params.Limit != *tt.wantLimit {
				t.Fatalf("limit = %v, want %d", server.params.Limit, *tt.wantLimit)
			}
		})
	}
}

func TestDefaultQueryBindingRetainsCurrentErrors(t *testing.T) {
	server := &routingCompatibilityServer{}
	app := fiber.New()
	RegisterHandlers(app, server)

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/index?limit=bad", nil))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	if server.called {
		t.Fatal("handler called for malformed default query")
	}
}

func intPointer(value int) *int {
	return &value
}
