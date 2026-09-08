package lfs

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestWriteLFSErrorPreservesLFSContentType(t *testing.T) {
	app := fiber.New()
	app.Get("/error", func(c fiber.Ctx) error {
		return WriteLFSError(c, http.StatusTooManyRequests, "rate limit exceeded", false)
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/error", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusTooManyRequests)
	}
	if got := response.Header.Get("Content-Type"); got != "application/vnd.git-lfs+json" {
		t.Fatalf("Content-Type = %q, want application/vnd.git-lfs+json", got)
	}
}
