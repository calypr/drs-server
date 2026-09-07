package drsapi

import (
	"github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/api/apiutil"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/core"
	"github.com/gofiber/fiber/v3"
)

func RegisterDRSRoutes(router fiber.Router, om *core.ObjectManager) {
	// Static routes first
	router.Post("/objects/register", handleRegisterObjectsFiber(om))
	router.Post("/objects/access", handleGetBulkAccessURLFiber(om))
	router.Post("/objects/delete", handleBulkDeleteObjectsFiber(om))
	router.Put("/objects/delete", handleBulkDeleteObjectsFiber(om))
	router.Put("/objects/checksums", handleUnsupportedChecksumAdditionFiber())
	router.Post("/objects/access-methods", handleUpdateAccessMethodsFiber(om))
	router.Put("/objects/access-methods", handleUpdateAccessMethodsFiber(om))
	router.Get("/objects/checksum/:checksum", handleGetObjectsByChecksumFiber(om))
	router.Post("/objects", handleGetBulkObjectsFiber(om))
	router.Get("/service-info", handleGetServiceInfoFiber(om))
	router.Post("/upload-request", handleUploadRequestFiber(om))

	// Dynamic routes with parameters last
	router.Get("/objects/:object_id", handleGetObjectFiber(om))
	router.Post("/objects/:object_id", handleGetObjectFiber(om))
	router.Delete("/objects/:object_id", handleDeleteObjectFiber(om))
	router.Post("/objects/:object_id/delete", handleDeleteObjectFiber(om))
	router.Put("/objects/:object_id/delete", handleDeleteObjectFiber(om))
	router.Put("/objects/:object_id/checksums", handleUnsupportedChecksumAdditionFiber())
	router.Get("/objects/:object_id/access/:access_id", handleGetAccessURLFiber(om))
	router.Post("/objects/:object_id/access/:access_id", handleGetAccessURLFiber(om))
	router.Post("/objects/:object_id/access-methods", handleUpdateAccessMethodsFiber(om))
	router.Put("/objects/:object_id/access-methods", handleUpdateAccessMethodsFiber(om))

	// Options
	router.Options("/objects", handleOptionsBulkObjectFiber(om))
	router.Options("/objects/:object_id", handleOptionsBulkObjectFiber(om))
}

func handleUnsupportedChecksumAdditionFiber() fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(drs.Error{Msg: common.Ptr("Checksum addition is not supported")})
	}
}

func handleGetServiceInfoFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		info, err := om.GetServiceInfo(c.Context())
		if err != nil {
			return apiutil.HandleError(c, err)
		}
		return c.JSON(info)
	}
}

func handleOptionsBulkObjectFiber(om *core.ObjectManager) fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	}
}
