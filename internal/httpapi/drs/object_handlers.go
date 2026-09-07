package drs

import (
	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	"github.com/gofiber/fiber/v3"
)

func handleGetObjectFiber(service *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("object_id")
		obj, err := service.GetObject(c.Context(), id, "")
		if err != nil {
			return response.HandleError(c, err)
		}
		return c.JSON(ObjectPayload(*obj))
	}
}

func handleGetBulkObjectsFiber(service *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var body struct {
			BulkObjectIds []string `json:"bulk_object_ids"`
		}
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
		}

		objects, err := service.GetBulkObjects(c.Context(), body.BulkObjectIds, "")
		if err != nil {
			return response.HandleError(c, err)
		}

		resolved := make([]any, 0, len(objects))
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
}

func handleGetObjectsByChecksumFiber(service *objects.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		checksum := c.Params("checksum")
		fetched, err := service.GetObjectsByChecksum(c.Context(), checksum, "")
		if err != nil {
			return response.HandleError(c, err)
		}

		resolved := make([]any, 0)
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
}
