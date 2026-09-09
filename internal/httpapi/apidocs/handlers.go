package apidocs

import (
	"fmt"
	"log"
	"strings"

	"github.com/gofiber/fiber/v3"
)

const (
	RouteSwaggerUI    = "/index/swagger"
	RouteSwaggerUIAlt = "/index/swagger/"
	RouteOpenAPISpec  = "/index/openapi.yaml"
	RouteLFSSpec      = "/index/openapi-lfs.yaml"
	RouteBucketSpec   = "/index/openapi-bucket.yaml"
	RouteInternalSpec = "/index/openapi-internal.yaml"
	RouteErrorSpec    = "/index/error.openapi.yaml"
)

const swaggerUIHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>DRS Server API Docs</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.onload = function() {
      window.ui = SwaggerUIBundle({
        url: "` + RouteOpenAPISpec + `",
        dom_id: "#swagger-ui"
      });
    };
  </script>
</body>
</html>
`

func handleSwaggerUI(c fiber.Ctx) error {
	c.Set("Content-Type", "text/html; charset=utf-8")
	if err := c.SendString(swaggerUIHTML); err != nil {
		log.Printf("write swagger ui response: %v", err)
		return err
	}
	return nil
}

func handleOpenAPISpec(c fiber.Ctx) error {
	merged, err := buildMergedOpenAPISpec()
	if err != nil {
		return fmt.Errorf("OpenAPI spec file not found: %w", err)
	}
	c.Set("Content-Type", "application/yaml")
	if err := c.Send(merged); err != nil {
		log.Printf("write merged openapi spec response: %v", err)
		return err
	}
	return nil
}

func handleNamedOpenAPISpec(name, label string) fiber.Handler {
	return func(c fiber.Ctx) error {
		specBytes, err := loadSpecBytesByName(name)
		if err != nil {
			return fmt.Errorf("%s OpenAPI spec file not found: %w", label, err)
		}
		c.Set("Content-Type", "application/yaml")
		if err := c.Send(specBytes); err != nil {
			log.Printf("write %s openapi spec response: %v", strings.ToLower(label), err)
			return err
		}
		return nil
	}
}

func RegisterSwaggerRoutes(router fiber.Router) {
	router.Get(RouteSwaggerUI, handleSwaggerUI)
	router.Get(RouteSwaggerUIAlt, handleSwaggerUI)
	router.Get(RouteOpenAPISpec, handleOpenAPISpec)
	router.Get(RouteLFSSpec, handleNamedOpenAPISpec("lfs.openapi.yaml", "LFS"))
	router.Get(RouteBucketSpec, handleNamedOpenAPISpec("bucket.openapi.yaml", "Bucket"))
	router.Get(RouteInternalSpec, handleNamedOpenAPISpec("internal.openapi.yaml", "Internal"))
	router.Get(RouteErrorSpec, handleNamedOpenAPISpec("error.openapi.yaml", "Error"))
}
