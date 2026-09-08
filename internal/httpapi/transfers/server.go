package transfers

import (
	"github.com/calypr/syfon/apigen/internalapi"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

type TransfersServer struct {
	transfers *domaintransfers.Service
}

func NewTransfersServer(transferService *domaintransfers.Service) *TransfersServer {
	return &TransfersServer{transfers: transferService}
}

func (s *TransfersServer) InternalDownload(c fiber.Ctx, _ string, _ internalapi.InternalDownloadParams) error {
	return handleInternalDownloadFiber(c, nil, s.transfers, nil)
}

func (s *TransfersServer) InternalDownloadPart(c fiber.Ctx, _ string, _ internalapi.InternalDownloadPartParams) error {
	return handleInternalDownloadPartFiber(c, nil, s.transfers)
}

func (s *TransfersServer) InternalMultipartComplete(c fiber.Ctx) error {
	return handleInternalMultipartCompleteFiber(s.transfers)(c)
}

func (s *TransfersServer) InternalMultipartInit(c fiber.Ctx) error {
	return handleInternalMultipartInitFiber(s.transfers)(c)
}

func (s *TransfersServer) InternalMultipartUpload(c fiber.Ctx) error {
	return handleInternalMultipartUploadFiber(s.transfers)(c)
}

func (s *TransfersServer) InternalUploadBlank(c fiber.Ctx) error {
	return handleInternalUploadBlankFiber(s.transfers)(c)
}

func (s *TransfersServer) InternalUploadBulk(c fiber.Ctx) error {
	return handleInternalUploadBulkFiber(nil, s.transfers)(c)
}

func (s *TransfersServer) InternalUploadURL(c fiber.Ctx, _ string, _ internalapi.InternalUploadURLParams) error {
	return handleInternalUploadURLFiber(nil, s.transfers)(c)
}
