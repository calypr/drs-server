package records

import (
	"github.com/calypr/syfon/apigen/internalapi"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

type RecordsServer struct {
	objects *objectrecords.Service
}

func NewRecordsServer(objectService *objectrecords.Service) *RecordsServer {
	return &RecordsServer{objects: objectService}
}

func (s *RecordsServer) InternalDeleteByQuery(c fiber.Ctx, _ internalapi.InternalDeleteByQueryParams) error {
	return handleInternalDeleteByQueryFiber(s.objects)(c)
}

func (s *RecordsServer) InternalList(c fiber.Ctx, _ internalapi.InternalListParams) error {
	return handleInternalListFiber(s.objects)(c)
}

func (s *RecordsServer) InternalCreate(c fiber.Ctx) error {
	return handleInternalCreateFiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkCreate(c fiber.Ctx) error {
	return handleInternalBulkCreateFiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkDeleteHashes(c fiber.Ctx) error {
	return handleInternalBulkDeleteFiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkDocuments(c fiber.Ctx) error {
	return handleInternalBulkDocumentsFiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkHashes(c fiber.Ctx) error {
	return handleInternalBulkHashesFiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkOverwrite(c fiber.Ctx) error {
	return handleInternalBulkOverwriteFiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkMissingSHA256(c fiber.Ctx) error {
	return handleInternalBulkMissingSHA256Fiber(s.objects)(c)
}

func (s *RecordsServer) InternalBulkSHA256Validity(c fiber.Ctx) error {
	return handleInternalBulkSHA256ValidityFiber(s.objects)(c)
}

func (s *RecordsServer) InternalDelete(c fiber.Ctx, _ string) error {
	return handleInternalDeleteFiber(s.objects)(c)
}

func (s *RecordsServer) InternalGet(c fiber.Ctx, _ string) error {
	return handleInternalGetFiber(s.objects)(c)
}

func (s *RecordsServer) InternalUpdate(c fiber.Ctx, _ string) error {
	return handleInternalUpdateFiber(s.objects)(c)
}

func (s *RecordsServer) InternalRemoveControlledAccess(c fiber.Ctx, _ string) error {
	return handleInternalRemoveControlledAccessFiber(s.objects)(c)
}
