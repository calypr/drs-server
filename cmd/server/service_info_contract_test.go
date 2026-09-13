package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"testing"

	"github.com/calypr/syfon/internal/httpapi"
	"github.com/calypr/syfon/internal/httpapi/apidocs"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gofiber/fiber/v3"
)

func TestServiceInfoResponseMatchesServedOpenAPIContract(t *testing.T) {
	app := fiber.New()
	httpapi.RegisterRoutes(app, httpapi.Dependencies{
		ServiceInfo: serviceInfoForConfig(testServiceInfoConfig()),
	}, httpapi.Options{Docs: true, GA4GH: true, MaxBulkRequestLength: 100})

	request := func(requestPath string) []byte {
		t.Helper()
		response, err := app.Test(httptest.NewRequest(http.MethodGet, requestPath, nil))
		if err != nil {
			t.Fatalf("GET %s: %v", requestPath, err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read GET %s: %v", requestPath, err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, body = %s", requestPath, response.StatusCode, body)
		}
		return body
	}

	specBody := request(apidocs.RouteOpenAPISpec)
	errorSpecBody := request(apidocs.RouteErrorSpec)
	loader := openapi3.NewLoader()
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		switch path.Base(location.Path) {
		case "openapi.yaml":
			return specBody, nil
		case "error.openapi.yaml":
			return errorSpecBody, nil
		}
		return nil, fmt.Errorf("unexpected external OpenAPI reference %q", location.String())
	}
	spec, err := loader.LoadFromData(specBody)
	if err != nil {
		t.Fatalf("parse served OpenAPI document: %v", err)
	}
	serviceInfoPath := spec.Paths.Find("/service-info")
	if serviceInfoPath == nil || serviceInfoPath.Get == nil {
		t.Fatal("served OpenAPI document has no service-info GET operation")
	}
	response := serviceInfoPath.Get.Responses.Value("200")
	if response == nil || response.Value == nil {
		t.Fatal("served OpenAPI document has no service-info 200 response")
	}
	media := response.Value.Content.Get("application/json")
	if media == nil || media.Schema == nil || media.Schema.Value == nil {
		t.Fatal("served OpenAPI document has no service-info JSON schema")
	}

	var responseBody any
	if err := json.Unmarshal(request("/ga4gh/drs/v1/service-info"), &responseBody); err != nil {
		t.Fatalf("decode service-info response: %v", err)
	}
	if err := media.Schema.Value.VisitJSON(responseBody); err != nil {
		t.Fatalf("service-info response violates served OpenAPI schema: %v", err)
	}
}
