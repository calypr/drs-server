package transfers

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestRouteRegistrationLeavesMaintenanceGapBetweenTransferGroups(t *testing.T) {
	app := fiber.New()
	RegisterObjectRoutes(app, nil, nil, nil)
	app.Post("/data/maintenance/sentinel", func(fiber.Ctx) error { return nil })
	RegisterBulkAndMultipartRoutes(app, nil, nil)

	var postPaths []string
	for _, methodRoutes := range app.Stack() {
		for _, route := range methodRoutes {
			if route.Method == http.MethodPost {
				postPaths = append(postPaths, route.Path)
			}
		}
	}

	want := []string{
		RouteUpload,
		"/data/maintenance/sentinel",
		RouteUploadBulk,
		RouteMultipartInit,
		RouteMultipartUpload,
		RouteMultipartComplete,
	}
	if len(postPaths) != len(want) {
		t.Fatalf("unexpected POST route count: got %v want %v", postPaths, want)
	}
	for i := range want {
		if postPaths[i] != want[i] {
			t.Fatalf("unexpected POST route order: got %v want %v", postPaths, want)
		}
	}
}
