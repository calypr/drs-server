package buckets

import (
	"github.com/calypr/syfon/apigen/bucketapi"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	"github.com/gofiber/fiber/v3"
)

const (
	RouteBuckets      = "/data/buckets"
	RouteBucketDetail = "/data/buckets/:bucket"
	RouteBucketScopes = "/data/buckets/:bucket/scopes"
)

func RegisterRoutes(router fiber.Router, bucketService *domainbuckets.Service, projectCleanupHandler fiber.Handler) {
	bucketapi.RegisterHandlers(router, &bucketServer{
		bucketService:         bucketService,
		projectCleanupHandler: projectCleanupHandler,
	})
}
