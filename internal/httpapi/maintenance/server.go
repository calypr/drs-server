package maintenance

import (
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

type MaintenanceServer struct {
	inspector *projectstorage.Inspector
	buckets   *buckets.Service
}

func NewMaintenanceServer(inspector *projectstorage.Inspector, bucketService *buckets.Service) *MaintenanceServer {
	return &MaintenanceServer{inspector: inspector, buckets: bucketService}
}

func (s *MaintenanceServer) InternalInspectProjectBucketInventory(c fiber.Ctx) error {
	return handleInternalInspectProjectBucketInventoryFiber(s.inspector)(c)
}

func (s *MaintenanceServer) InternalInspectProjectScopes(c fiber.Ctx, _ internalapi.InternalInspectProjectScopesParams) error {
	return handleInternalInspectProjectScopesFiber(s.buckets)(c)
}

func (s *MaintenanceServer) InternalInspectProjectScopesPost(c fiber.Ctx) error {
	return handleInternalInspectProjectScopesFiber(s.buckets)(c)
}
