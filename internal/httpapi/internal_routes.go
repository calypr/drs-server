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
		return (&internalServer{cleanup: service}).InternalDeleteProject(c, c.Params("organization"), c.Params("project_id"))
	}
}

func RegisterUndocumentedRoutes(router fiber.Router, repair *scoperepair.Service, inspector *projectstorage.Inspector, cleanup *projectstorage.ProjectCleanup) {
	server := &internalServer{repair: repair, inspector: inspector, cleanup: cleanup}
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

const (
	RouteIndex                       = "/index"
	RouteIndexDetail                 = "/index/:id"
	RouteIndexControlledAccessRemove = "/index/:id/controlled-access/remove"
	RouteBulkHashes                  = "/index/bulk/hashes"
	RouteBulkDeleteHashes            = "/index/bulk/delete"
	RouteBulkSHA256                  = "/index/bulk/sha256/validity"
	RouteBulkSHA256Missing           = "/index/bulk/sha256/missing"
	RouteBulkCreate                  = "/index/bulk"
	RouteBulkDocs                    = "/index/bulk/documents"
	RouteBulkOverwrite               = "/index/bulk/overwrite"
)

const (
	RouteDownload          = "/data/download/:file_id"
	RouteDownloadPart      = "/data/download/:file_id/part"
	RouteUpload            = "/data/upload"
	RouteUploadURL         = "/data/upload/:file_id"
	RouteUploadBulk        = "/data/upload/bulk"
	RouteMultipartInit     = "/data/multipart/init"
	RouteMultipartUpload   = "/data/multipart/upload"
	RouteMultipartComplete = "/data/multipart/complete"
)
