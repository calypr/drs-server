package apidocs

import "github.com/gofiber/fiber/v3"

const (
	RouteSwaggerUI    = "/index/swagger"
	RouteSwaggerUIAlt = "/index/swagger/"
	RouteOpenAPISpec  = "/index/openapi.yaml"
	RouteLFSSpec      = "/index/openapi-lfs.yaml"
	RouteBucketSpec   = "/index/openapi-bucket.yaml"
	RouteInternalSpec = "/index/openapi-internal.yaml"
)

// RegisterSwaggerRoutes adds Swagger/OpenAPI docs endpoints.
func RegisterSwaggerRoutes(router fiber.Router) {
	router.Get(RouteSwaggerUI, handleSwaggerUI)
	router.Get(RouteSwaggerUIAlt, handleSwaggerUI)
	router.Get(RouteOpenAPISpec, handleOpenAPISpec)
	router.Get(RouteLFSSpec, handleLFSOpenAPISpec)
	router.Get(RouteBucketSpec, handleBucketOpenAPISpec)
	router.Get(RouteInternalSpec, handleInternalOpenAPISpec)
}
