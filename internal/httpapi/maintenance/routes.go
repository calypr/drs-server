package maintenance

import (
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

const (
	RouteInspectObject                 = "/data/inspect"
	RouteInspectObjectBulk             = "/data/inspect/bulk"
	RouteInspectObjectBulkList         = "/data/inspect/bulk-list"
	RouteInspectProjectBucket          = "/data/inspect/project-bucket"
	RouteInspectProjectBucketInventory = "/data/inspect/project-bucket/inventory"
	RouteInspectProjectRecords         = "/data/inspect/project-records"
	RouteInspectProjectScopes          = "/data/inspect/project-scopes"
	RouteDeleteProjectBucketObjects    = "/data/inspect/project-bucket/delete"
	RouteProjectCleanup                = "/data/projects/:organization/:project_id"
	RouteRepairScopeAudit              = "/data/repair/project-scope/audit"
	RouteRepairScopeApply              = "/data/repair/project-scope/apply"
)

// ProjectCleanupHandler returns the project cleanup handler.
func ProjectCleanupHandler(service *projectstorage.ProjectCleanup) fiber.Handler {
	return func(c fiber.Ctx) error {
		return handleInternalDeleteProjectFiber(c, service)
	}
}

func RegisterUndocumentedRoutes(router fiber.Router, repair *scoperepair.Service, inspector *projectstorage.Inspector, cleanup *projectstorage.ProjectCleanup) {
	router.Post(RouteRepairScopeAudit, handleInternalScopeRepairAuditFiber(repair))
	router.Post(RouteRepairScopeApply, handleInternalScopeRepairApplyFiber(repair))
	router.Post(RouteInspectObject, handleInternalInspectObjectFiber(inspector))
	router.Post(RouteInspectObjectBulk, handleInternalInspectObjectBulkFiber(inspector))
	router.Post(RouteInspectObjectBulkList, handleInternalInspectObjectBulkListFiber(inspector))
	router.Post(RouteInspectProjectBucket, handleInternalInspectProjectBucketFiber(inspector))
	router.Post(RouteInspectProjectRecords, handleInternalInspectProjectRecordsFiber(inspector))
	router.Post(RouteDeleteProjectBucketObjects, handleInternalDeleteProjectBucketObjectsFiber(cleanup))
}
