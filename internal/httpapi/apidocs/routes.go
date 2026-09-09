package apidocs

import "github.com/gofiber/fiber/v3"

const (
	RouteSwaggerUI    = "/index/swagger"
	RouteSwaggerUIAlt = "/index/swagger/"
	RouteOpenAPISpec  = "/index/openapi.yaml"
	RouteLFSSpec      = "/index/openapi-lfs.yaml"
	RouteBucketSpec   = "/index/openapi-bucket.yaml"
	RouteInternalSpec = "/index/openapi-internal.yaml"
	RouteErrorSpec    = "/index/error.openapi.yaml"
)

// RegisterSwaggerRoutes adds Swagger/OpenAPI docs endpoints.
func RegisterSwaggerRoutes(router fiber.Router) {
	router.Get(RouteSwaggerUI, handleSwaggerUI)
	router.Get(RouteSwaggerUIAlt, handleSwaggerUI)
	router.Get(RouteOpenAPISpec, handleOpenAPISpec)
	router.Get(RouteLFSSpec, handleNamedOpenAPISpec("lfs.openapi.yaml", "LFS"))
	router.Get(RouteBucketSpec, handleNamedOpenAPISpec("bucket.openapi.yaml", "Bucket"))
	router.Get(RouteInternalSpec, handleNamedOpenAPISpec("internal.openapi.yaml", "Internal"))
	router.Get(RouteErrorSpec, handleNamedOpenAPISpec("error.openapi.yaml", "Error"))
}
