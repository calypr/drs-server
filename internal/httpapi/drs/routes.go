package drs

import (
	generated "github.com/calypr/syfon/apigen/server/drs"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func RegisterDRSRoutes(router fiber.Router, objectService *objectrecords.Service, accessService *transfers.Service, serviceInfo generated.Service) {
	router.Post("/objects/register", handleRegisterObjectsFiber(objectService))
	router.Post("/objects/access", handleGetBulkAccessURLFiber(objectService, accessService))
	router.Post("/objects/delete", handleBulkDeleteObjectsFiber(objectService))
	router.Put("/objects/delete", handleBulkDeleteObjectsFiber(objectService))
	router.Put("/objects/checksums", handleUnsupportedChecksumAdditionFiber())
	router.Post("/objects/access-methods", handleUpdateAccessMethodsFiber(objectService))
	router.Put("/objects/access-methods", handleUpdateAccessMethodsFiber(objectService))
	router.Get("/objects/checksum/:checksum", handleGetObjectsByChecksumFiber(objectService))
	router.Post("/objects", handleGetBulkObjectsFiber(objectService))
	router.Get("/service-info", handleGetServiceInfoFiber(serviceInfo))
	router.Post("/upload-request", handleUploadRequestFiber())

	router.Get("/objects/:object_id", handleGetObjectFiber(objectService))
	router.Post("/objects/:object_id", handleGetObjectFiber(objectService))
	router.Delete("/objects/:object_id", handleDeleteObjectFiber(objectService))
	router.Post("/objects/:object_id/delete", handleDeleteObjectFiber(objectService))
	router.Put("/objects/:object_id/delete", handleDeleteObjectFiber(objectService))
	router.Put("/objects/:object_id/checksums", handleUnsupportedChecksumAdditionFiber())
	router.Get("/objects/:object_id/access/:access_id", handleGetAccessURLFiber(objectService, accessService))
	router.Post("/objects/:object_id/access/:access_id", handleGetAccessURLFiber(objectService, accessService))
	router.Post("/objects/:object_id/access-methods", handleUpdateAccessMethodsFiber(objectService))
	router.Put("/objects/:object_id/access-methods", handleUpdateAccessMethodsFiber(objectService))

	router.Options("/objects", handleOptionsBulkObjectFiber())
	router.Options("/objects/:object_id", handleOptionsBulkObjectFiber())
}

func handleUnsupportedChecksumAdditionFiber() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(generated.Error{Msg: drsPtr("Checksum addition is not supported")})
	}
}

func handleGetServiceInfoFiber(serviceInfo generated.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.JSON(serviceInfo)
	}
}

func handleOptionsBulkObjectFiber() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	}
}
