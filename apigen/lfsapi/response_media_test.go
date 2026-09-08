package lfsapi

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestGeneratedResponsesPreserveDeclaredMediaType(t *testing.T) {
	app := fiber.New()
	app.Get("/error", func(c fiber.Ctx) error {
		return (LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "invalid size"}).VisitLfsVerifyResponse(c)
	})
	app.Get("/json", func(c fiber.Ctx) error {
		return (LfsStageMetadata200JSONResponse{Staged: 1}).VisitLfsStageMetadataResponse(c)
	})
	for _, tc := range []struct {
		path, mediaType string
		status          int
	}{
		{"/error", "application/vnd.git-lfs+json", 400},
		{"/json", "application/json", 200},
	} {
		response, err := app.Test(httptest.NewRequest("GET", tc.path, nil))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != tc.status || response.Header.Get("Content-Type") != tc.mediaType {
			t.Fatalf("%s: status=%d mediaType=%q", tc.path, response.StatusCode, response.Header.Get("Content-Type"))
		}
	}
}
