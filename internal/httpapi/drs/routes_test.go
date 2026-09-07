package drs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/gofiber/fiber/v3"
)

func TestRegisterDRSRoutesKeepsStaticRoutesBeforeDynamicRoutes(t *testing.T) {
	app := fiber.New()
	RegisterDRSRoutes(app, nil, nil, generated.Service{})

	var getPaths []string
	for _, routes := range app.Stack() {
		for _, route := range routes {
			if route.Method == http.MethodGet {
				getPaths = append(getPaths, route.Path)
			}
		}
	}
	checksumIndex := indexOf(getPaths, "/objects/checksum/:checksum")
	objectIndex := indexOf(getPaths, "/objects/:object_id")
	if checksumIndex < 0 || objectIndex < 0 || checksumIndex > objectIndex {
		t.Fatalf("GET route order = %v", getPaths)
	}
}

func TestRegisterDRSRoutesPreservesAliasesAndOptions(t *testing.T) {
	app := fiber.New()
	RegisterDRSRoutes(app, nil, nil, generated.Service{})

	want := map[string]bool{
		"POST /objects/register":                     false,
		"POST /objects/access":                       false,
		"POST /objects/delete":                       false,
		"PUT /objects/delete":                        false,
		"POST /objects/access-methods":               false,
		"PUT /objects/access-methods":                false,
		"POST /objects/:object_id":                   false,
		"DELETE /objects/:object_id":                 false,
		"POST /objects/:object_id/delete":            false,
		"PUT /objects/:object_id/delete":             false,
		"GET /objects/:object_id/access/:access_id":  false,
		"POST /objects/:object_id/access/:access_id": false,
		"POST /objects/:object_id/access-methods":    false,
		"PUT /objects/:object_id/access-methods":     false,
		"OPTIONS /objects":                           false,
		"OPTIONS /objects/:object_id":                false,
	}
	for _, routes := range app.Stack() {
		for _, route := range routes {
			key := route.Method + " " + route.Path
			if _, ok := want[key]; ok {
				want[key] = true
			}
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("missing route %s", route)
		}
	}

	for _, methodPath := range []struct {
		method string
		path   string
	}{
		{method: http.MethodOptions, path: "/objects"},
		{method: http.MethodOptions, path: "/objects/object-1"},
	} {
		resp, err := app.Test(httptest.NewRequest(methodPath.method, methodPath.path, nil))
		if err != nil {
			t.Fatalf("request %s %s failed: %v", methodPath.method, methodPath.path, err)
		}
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("%s %s status = %d, want %d", methodPath.method, methodPath.path, resp.StatusCode, http.StatusNoContent)
		}
	}
}

func TestUnsupportedChecksumRoutesReturnDRSError(t *testing.T) {
	app := fiber.New()
	RegisterDRSRoutes(app, nil, nil, generated.Service{})

	for _, path := range []string{"/objects/checksums", "/objects/object-1/checksums"} {
		resp, err := app.Test(httptest.NewRequest(http.MethodPut, path, nil))
		if err != nil {
			t.Fatalf("request %s failed: %v", path, err)
		}
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want %d", path, resp.StatusCode, http.StatusNotFound)
		}
		var body generated.Error
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s response: %v", path, err)
		}
		if body.Msg == nil || *body.Msg != "Checksum addition is not supported" {
			t.Errorf("%s body = %+v", path, body)
		}
	}
}

func indexOf(values []string, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}
