package records

import (
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

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

func RegisterRoutes(router fiber.Router, objectService *objects.Service) {
	router.Get("/", handleInternalListFiber(objectService))
	router.Get(RouteIndex, handleInternalListFiber(objectService))
	router.Get(RouteIndexDetail, handleInternalGetFiber(objectService))
	router.Post(RouteIndex, handleInternalCreateFiber(objectService))
	router.Put(RouteIndexDetail, handleInternalUpdateFiber(objectService))
	router.Delete(RouteIndexDetail, handleInternalDeleteFiber(objectService))
	router.Post(RouteIndexControlledAccessRemove, handleInternalRemoveControlledAccessFiber(objectService))
	router.Delete("/", handleInternalDeleteByQueryFiber(objectService))
	router.Delete(RouteIndex, handleInternalDeleteByQueryFiber(objectService))
	router.Post(RouteBulkHashes, handleInternalBulkHashesFiber(objectService))
	router.Post(RouteBulkSHA256, handleInternalBulkSHA256ValidityFiber(objectService))
	router.Post(RouteBulkSHA256Missing, handleInternalBulkMissingSHA256Fiber(objectService))
	router.Post(RouteBulkCreate, handleInternalBulkCreateFiber(objectService))
	router.Put(RouteBulkOverwrite, handleInternalBulkOverwriteFiber(objectService))
	router.Post(RouteBulkDocs, handleInternalBulkDocumentsFiber(objectService))
	router.Post(RouteBulkDeleteHashes, handleInternalBulkDeleteFiber(objectService))
}
