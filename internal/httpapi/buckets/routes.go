package buckets

import (
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	"github.com/gofiber/fiber/v3"
)

const (
	RouteBuckets      = "/data/buckets"
	RouteBucketDetail = "/data/buckets/:bucket"
	RouteBucketScopes = "/data/buckets/:bucket/scopes"
)

func RegisterRoutes(router fiber.Router, bucketService *domainbuckets.Service) {
	router.Get(RouteBuckets, func(c fiber.Ctx) error { return handleInternalBucketsFiber(c, bucketService) })
	router.Put(RouteBuckets, func(c fiber.Ctx) error { return handleInternalPutBucketFiber(c, bucketService) })
	router.Delete(RouteBucketDetail, func(c fiber.Ctx) error { return handleInternalDeleteBucketFiber(c, bucketService) })
	router.Get(RouteBucketScopes, func(c fiber.Ctx) error { return handleInternalListBucketScopesFiber(c, bucketService) })
	router.Post(RouteBucketScopes, func(c fiber.Ctx) error { return handleInternalCreateBucketScopeFiber(c, bucketService) })
	router.Delete(RouteBucketScopes, func(c fiber.Ctx) error { return handleInternalDeleteBucketScopeFiber(c, bucketService) })
}
