package apidocs

import (
	"log"

	"github.com/gofiber/fiber/v3"
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
		return sendInternalServerError(c, "OpenAPI spec file not found: "+err.Error())
	}
	c.Set("Content-Type", "application/yaml")
	if err := c.Send(merged); err != nil {
		log.Printf("write merged openapi spec response: %v", err)
		return err
	}
	return nil
}

func handleLFSOpenAPISpec(c fiber.Ctx) error {
	specBytes, err := loadSpecBytesByName("lfs.openapi.yaml")
	if err != nil {
		return sendInternalServerError(c, "LFS OpenAPI spec file not found: "+err.Error())
	}
	c.Set("Content-Type", "application/yaml")
	if err := c.Send(specBytes); err != nil {
		log.Printf("write lfs openapi spec response: %v", err)
		return err
	}
	return nil
}

func handleBucketOpenAPISpec(c fiber.Ctx) error {
	specBytes, err := loadSpecBytesByName("bucket.openapi.yaml")
	if err != nil {
		return sendInternalServerError(c, "Bucket OpenAPI spec file not found: "+err.Error())
	}
	c.Set("Content-Type", "application/yaml")
	if err := c.Send(specBytes); err != nil {
		log.Printf("write bucket openapi spec response: %v", err)
		return err
	}
	return nil
}

func handleInternalOpenAPISpec(c fiber.Ctx) error {
	specBytes, err := loadSpecBytesByName("internal.openapi.yaml")
	if err != nil {
		return sendInternalServerError(c, "Internal OpenAPI spec file not found: "+err.Error())
	}
	c.Set("Content-Type", "application/yaml")
	if err := c.Send(specBytes); err != nil {
		log.Printf("write internal openapi spec response: %v", err)
		return err
	}
	return nil
}

func sendInternalServerError(c fiber.Ctx, message string) error {
	c.Set("Content-Type", "text/plain; charset=utf-8")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.Status(fiber.StatusInternalServerError).SendString(message + "\n")
}
