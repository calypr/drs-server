package drs

import (
	generated "github.com/calypr/syfon/apigen/drs"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func RegisterDRSRoutes(router fiber.Router, objectService *objectrecords.Service, accessService *transfers.Service, serviceInfo generated.Service) {
	handlers := &server{
		objectService: objectService,
		accessService: accessService,
		serviceInfo:   serviceInfo,
	}

	generated.RegisterHandlers(router, handlers)
}

type server struct {
	objectService *objectrecords.Service
	accessService *transfers.Service
	serviceInfo   generated.Service
}

var _ generated.ServerInterface = (*server)(nil)

func (s *server) OptionsBulkObject(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }

func (s *server) OptionsObject(c fiber.Ctx, _ generated.ObjectId) error {
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *server) GetServiceInfo(c fiber.Ctx) error { return c.JSON(s.serviceInfo) }
