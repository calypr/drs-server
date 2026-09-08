package transfers

import (
	"github.com/calypr/syfon/apigen/internalapi"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

type TransfersServer struct {
	objects   *objectrecords.Service
	transfers *domaintransfers.Service
	counters  usage.FileCounterRecorder
	multipart *domaintransfers.MultipartLifecycle
}

func NewTransfersServer(objectService *objectrecords.Service, transferService *domaintransfers.Service, fileCounters usage.FileCounterRecorder) *TransfersServer {
	return &TransfersServer{
		objects:   objectService,
		transfers: transferService,
		counters:  fileCounters,
		multipart: domaintransfers.NewMultipartLifecycle(transferService),
	}
}

func (s *TransfersServer) InternalDownload(c fiber.Ctx, _ string, _ internalapi.InternalDownloadParams) error {
	return handleInternalDownloadFiber(c, s.objects, s.transfers, s.counters)
}

func (s *TransfersServer) InternalDownloadPart(c fiber.Ctx, _ string, _ internalapi.InternalDownloadPartParams) error {
	return handleInternalDownloadPartFiber(c, s.objects, s.transfers)
}

func (s *TransfersServer) InternalMultipartComplete(c fiber.Ctx) error {
	return handleInternalMultipartCompleteFiber(s.multipart)(c)
}

func (s *TransfersServer) InternalMultipartInit(c fiber.Ctx) error {
	return handleInternalMultipartInitFiber(s.objects, s.transfers, s.multipart)(c)
}

func (s *TransfersServer) InternalMultipartUpload(c fiber.Ctx) error {
	return handleInternalMultipartUploadFiber(s.multipart)(c)
}

func (s *TransfersServer) InternalUploadBlank(c fiber.Ctx) error {
	return handleInternalUploadBlankFiber(s.transfers)(c)
}

func (s *TransfersServer) InternalUploadBulk(c fiber.Ctx) error {
	return handleInternalUploadBulkFiber(s.objects, s.transfers)(c)
}

func (s *TransfersServer) InternalUploadURL(c fiber.Ctx, _ string, _ internalapi.InternalUploadURLParams) error {
	return handleInternalUploadURLFiber(s.objects, s.transfers)(c)
}
