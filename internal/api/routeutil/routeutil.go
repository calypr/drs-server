package routeutil

import (
	"context"
	"net/http"
	"regexp"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

type pathParamsKey struct{}

var routeParamPattern = regexp.MustCompile(`\{([^{}]+)\}`)

// FiberPath converts mux-style route templates to fiber path templates.
func FiberPath(pattern string) string {
	return routeParamPattern.ReplaceAllString(pattern, ":$1")
}

// WithPathParams attaches extracted path parameters to the request context.
func WithPathParams(r *http.Request, params map[string]string) *http.Request {
	if len(params) == 0 {
		return r
	}
	copyParams := make(map[string]string, len(params))
	for k, v := range params {
		copyParams[k] = v
	}
	return r.WithContext(context.WithValue(r.Context(), pathParamsKey{}, copyParams))
}

// Handler adapts a standard net/http handler to fiber while preserving path params.
func Handler(h any, paramNames ...string) fiber.Handler {
	return func(c fiber.Ctx) error {
		params := make(map[string]string, len(paramNames))
		for _, name := range paramNames {
			params[name] = c.Params(name)
		}

		// Use fiber's adaptor to convert http.Handler to fiber.Handler
		// But we need to inject our path params first.
		
		var httpHandler http.Handler
		switch handler := h.(type) {
		case http.Handler:
			httpHandler = handler
		case func(http.ResponseWriter, *http.Request):
			httpHandler = http.HandlerFunc(handler)
		default:
			return c.SendStatus(fiber.StatusInternalServerError)
		}

		return adaptor.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req := WithPathParams(r, params)
			httpHandler.ServeHTTP(w, req)
		}))(c)
	}
}
