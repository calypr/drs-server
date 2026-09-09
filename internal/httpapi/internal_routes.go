package httpapi

import (
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

const (
	RouteInspectObject              = "/data/inspect"
	RouteInspectObjectBulk          = "/data/inspect/bulk"
	RouteInspectObjectBulkList      = "/data/inspect/bulk-list"
	RouteInspectProjectBucket       = "/data/inspect/project-bucket"
	RouteInspectProjectRecords      = "/data/inspect/project-records"
	RouteDeleteProjectBucketObjects = "/data/inspect/project-bucket/delete"
	RouteRepairScopeAudit           = "/data/repair/project-scope/audit"
	RouteRepairScopeApply           = "/data/repair/project-scope/apply"
)

func projectCleanupHandler(server *internalServer) fiber.Handler {
	return func(c fiber.Ctx) error {
		return server.InternalDeleteProject(c, c.Params("organization"), c.Params("project_id"))
	}
}

func registerMaintenanceRoutes(router fiber.Router, server *internalServer) {
	router.Post(RouteRepairScopeAudit, server.InternalScopeRepairAudit)
	router.Post(RouteRepairScopeApply, server.InternalScopeRepairApply)
	router.Post(RouteInspectObject, server.InternalInspectObject)
	router.Post(RouteInspectObjectBulk, server.InternalInspectObjectBulk)
	router.Post(RouteInspectObjectBulkList, server.InternalInspectObjectBulkList)
	router.Post(RouteInspectProjectBucket, server.InternalInspectProjectBucket)
	router.Post(RouteInspectProjectRecords, server.InternalInspectProjectRecords)
	router.Post(RouteDeleteProjectBucketObjects, server.InternalDeleteProjectBucketObjects)
}

type internalServer struct {
	objects   *objectrecords.Service
	transfers *domaintransfers.Service
	inspector *projectstorage.Inspector
	cleanup   *projectstorage.ProjectCleanup
	buckets   *buckets.Service
	repair    *scoperepair.Service
}

var _ internalapi.ServerInterface = (*internalServer)(nil)
