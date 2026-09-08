package drs

import (
	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/gofiber/fiber/v3"
)

func (s *server) GetObject(c fiber.Ctx, objectID generated.ObjectId, _ generated.GetObjectParams) error {
	obj, err := s.objectService.GetObject(c.Context(), string(objectID), "")
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(ObjectPayload(*obj))
}

func (s *server) PostObject(c fiber.Ctx, objectID generated.ObjectId) error {
	return s.GetObject(c, objectID, generated.GetObjectParams{})
}

func (s *server) GetBulkObjects(c fiber.Ctx, _ generated.GetBulkObjectsParams) error {
	var body struct {
		BulkObjectIds []string `json:"bulk_object_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	objects, err := s.objectService.GetBulkObjects(c.Context(), body.BulkObjectIds, "")
	if err != nil {
		return middleware.HandleError(c, err)
	}

	resolved := make([]ObjectResponse, 0, len(objects))
	for _, obj := range objects {
		resolved = append(resolved, ObjectPayload(obj))
	}

	return c.JSON(fiber.Map{
		"resolved_drs_object": resolved,
		"summary": generated.Summary{
			Requested: drsPtr(len(body.BulkObjectIds)),
			Resolved:  drsPtr(len(resolved)),
		},
	})
}

func (s *server) GetObjectsByChecksum(c fiber.Ctx, checksum generated.ChecksumParameter) error {
	fetched, err := s.objectService.GetObjectsByChecksum(c.Context(), string(checksum), "")
	if err != nil {
		return middleware.HandleError(c, err)
	}

	resolved := make([]ObjectResponse, 0)
	for _, obj := range fetched {
		resolved = append(resolved, ObjectPayload(obj))
	}

	return c.JSON(fiber.Map{
		"resolved_drs_object": resolved,
		"summary": generated.Summary{
			Requested: drsPtr(1),
			Resolved:  drsPtr(len(resolved)),
		},
	})
}
