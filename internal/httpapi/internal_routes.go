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

type internalServer struct {
	objects   *objectrecords.Service
	transfers *domaintransfers.Service
	inspector *projectstorage.Inspector
	cleanup   *projectstorage.ProjectCleanup
	buckets   *buckets.Service
	repair    *scoperepair.Service
}

var _ internalapi.ServerInterface = (*internalServer)(nil)

func (s *internalServer) InternalDownload(c fiber.Ctx, _ string, _ internalapi.InternalDownloadParams) error {
	return handleInternalDownloadFiber(c, s.objects, s.transfers, nil)
}

func (s *internalServer) InternalDownloadPart(c fiber.Ctx, _ string, _ internalapi.InternalDownloadPartParams) error {
	return handleInternalDownloadPartFiber(c, s.objects, s.transfers)
}

func (s *internalServer) InternalInspectObject(c fiber.Ctx) error {
	return handleInternalInspectObjectFiber(s.inspector)(c)
}

func (s *internalServer) InternalInspectObjectBulk(c fiber.Ctx) error {
	return handleInternalInspectObjectBulkFiber(s.inspector)(c)
}

func (s *internalServer) InternalInspectObjectBulkList(c fiber.Ctx) error {
	return handleInternalInspectObjectBulkListFiber(s.inspector)(c)
}

func (s *internalServer) InternalInspectProjectBucket(c fiber.Ctx) error {
	return handleInternalInspectProjectBucketFiber(s.inspector)(c)
}

func (s *internalServer) InternalDeleteProjectBucketObjects(c fiber.Ctx) error {
	return handleInternalDeleteProjectBucketObjectsFiber(s.cleanup)(c)
}

func (s *internalServer) InternalInspectProjectBucketInventory(c fiber.Ctx) error {
	return handleInternalInspectProjectBucketInventoryFiber(s.inspector)(c)
}

func (s *internalServer) InternalInspectProjectRecords(c fiber.Ctx) error {
	return handleInternalInspectProjectRecordsFiber(s.inspector)(c)
}

func (s *internalServer) InternalInspectProjectScopes(c fiber.Ctx, _ internalapi.InternalInspectProjectScopesParams) error {
	return handleInternalInspectProjectScopesFiber(s.buckets)(c)
}

func (s *internalServer) InternalInspectProjectScopesPost(c fiber.Ctx) error {
	return handleInternalInspectProjectScopesFiber(s.buckets)(c)
}

func (s *internalServer) InternalMultipartComplete(c fiber.Ctx) error {
	return handleInternalMultipartCompleteFiber(s.transfers)(c)
}

func (s *internalServer) InternalMultipartInit(c fiber.Ctx) error {
	return handleInternalMultipartInitFiber(s.transfers)(c)
}

func (s *internalServer) InternalMultipartUpload(c fiber.Ctx) error {
	return handleInternalMultipartUploadFiber(s.transfers)(c)
}

func (s *internalServer) InternalDeleteProject(c fiber.Ctx, _, _ string) error {
	return handleInternalDeleteProjectFiber(c, s.cleanup)
}

func (s *internalServer) InternalScopeRepairApply(c fiber.Ctx) error {
	return handleInternalScopeRepairApplyFiber(s.repair)(c)
}

func (s *internalServer) InternalScopeRepairAudit(c fiber.Ctx) error {
	return handleInternalScopeRepairAuditFiber(s.repair)(c)
}

func (s *internalServer) InternalUploadBlank(c fiber.Ctx) error {
	return handleInternalUploadBlankFiber(s.transfers)(c)
}

func (s *internalServer) InternalUploadBulk(c fiber.Ctx) error {
	return handleInternalUploadBulkFiber(s.objects, s.transfers)(c)
}

func (s *internalServer) InternalUploadURL(c fiber.Ctx, _ string, _ internalapi.InternalUploadURLParams) error {
	return handleInternalUploadURLFiber(s.objects, s.transfers)(c)
}

func (s *internalServer) InternalDeleteByQuery(c fiber.Ctx, _ internalapi.InternalDeleteByQueryParams) error {
	return handleInternalDeleteByQueryFiber(s.objects)(c)
}

func (s *internalServer) InternalList(c fiber.Ctx, _ internalapi.InternalListParams) error {
	return handleInternalListFiber(s.objects)(c)
}

func (s *internalServer) InternalCreate(c fiber.Ctx) error {
	return handleInternalCreateFiber(s.objects)(c)
}

func (s *internalServer) InternalBulkCreate(c fiber.Ctx) error {
	return handleInternalBulkCreateFiber(s.objects)(c)
}

func (s *internalServer) InternalBulkDeleteHashes(c fiber.Ctx) error {
	return handleInternalBulkDeleteFiber(s.objects)(c)
}

func (s *internalServer) InternalBulkDocuments(c fiber.Ctx) error {
	return handleInternalBulkDocumentsFiber(s.objects)(c)
}

func (s *internalServer) InternalBulkHashes(c fiber.Ctx) error {
	return handleInternalBulkHashesFiber(s.objects)(c)
}

func (s *internalServer) InternalBulkOverwrite(c fiber.Ctx) error {
	return handleInternalBulkOverwriteFiber(s.objects)(c)
}

func (s *internalServer) InternalBulkMissingSHA256(c fiber.Ctx) error {
	return handleInternalBulkMissingSHA256Fiber(s.objects)(c)
}

func (s *internalServer) InternalBulkSHA256Validity(c fiber.Ctx) error {
	return handleInternalBulkSHA256ValidityFiber(s.objects)(c)
}

func (s *internalServer) InternalDelete(c fiber.Ctx, _ string) error {
	return handleInternalDeleteFiber(s.objects)(c)
}

func (s *internalServer) InternalGet(c fiber.Ctx, _ string) error {
	return handleInternalGetFiber(s.objects)(c)
}

func (s *internalServer) InternalUpdate(c fiber.Ctx, _ string) error {
	return handleInternalUpdateFiber(s.objects)(c)
}

func (s *internalServer) InternalRemoveControlledAccess(c fiber.Ctx, _ string) error {
	return handleInternalRemoveControlledAccessFiber(s.objects)(c)
}

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
