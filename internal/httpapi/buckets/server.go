package buckets

import (
	"github.com/calypr/syfon/apigen/bucketapi"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	"github.com/gofiber/fiber/v3"
)

type bucketServer struct {
	bucketService         *domainbuckets.Service
	projectCleanupHandler fiber.Handler
}

func (s *bucketServer) DeleteBucketScope(c fiber.Ctx, bucket string, params bucketapi.DeleteBucketScopeParams) error {
	return s.deleteBucketScopeRequest(c, bucket, params)
}

func (s *bucketServer) DeleteProjectData(c fiber.Ctx, organization, projectID string) error {
	if s.projectCleanupHandler == nil {
		return fiber.ErrNotFound
	}
	return s.projectCleanupHandler(c)
}
