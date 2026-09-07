package transfers

import (
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
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

func RegisterObjectRoutes(router fiber.Router, objectService *objectrecords.Service, transferService *domaintransfers.Service, fileCounters usage.FileCounterRecorder) {
	router.Get(RouteDownload, func(c fiber.Ctx) error {
		return handleInternalDownloadFiber(c, objectService, transferService, fileCounters)
	})
	router.Get(RouteDownloadPart, func(c fiber.Ctx) error {
		return handleInternalDownloadPartFiber(c, objectService, transferService)
	})
	router.Post(RouteUpload, handleInternalUploadBlankFiber(transferService))
	router.Get(RouteUploadURL, handleInternalUploadURLFiber(objectService, transferService))
}

func RegisterBulkAndMultipartRoutes(router fiber.Router, objectService *objectrecords.Service, transferService *domaintransfers.Service) {
	multipartLifecycle := domaintransfers.NewMultipartLifecycle(transferService)
	router.Post(RouteUploadBulk, handleInternalUploadBulkFiber(objectService, transferService))
	router.Post(RouteMultipartInit, handleInternalMultipartInitFiber(objectService, transferService, multipartLifecycle))
	router.Post(RouteMultipartUpload, handleInternalMultipartUploadFiber(multipartLifecycle))
	router.Post(RouteMultipartComplete, handleInternalMultipartCompleteFiber(multipartLifecycle))
}
