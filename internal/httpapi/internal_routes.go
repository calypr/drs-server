package httpapi

import (
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	domaintransfers "github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func projectCleanupHandler(server *internalServer) fiber.Handler {
	return func(c fiber.Ctx) error {
		return server.InternalDeleteProject(c, c.Params("organization"), c.Params("project_id"))
	}
}

type internalServer struct {
	objects   *objects.Service
	transfers *domaintransfers.Service
	inspector *projectstorage.Inspector
	cleanup   *projectstorage.ProjectCleanup
	buckets   *buckets.Service
	repair    *scoperepair.Service
}

var _ internalapi.ServerInterface = (*internalServer)(nil)
