package drs

import (
	"strings"

	generated "github.com/calypr/syfon/apigen/server/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func handleUploadRequestFiber() fiber.Handler {
	const uploadRequestRoutingError = "upload-request requires explicit upload routing; default bucket selection is disabled"
	return func(c fiber.Ctx) error {
		if middleware.MissingGen3AuthHeader(c.Context()) {
			return c.SendStatus(fiber.StatusUnauthorized)
		}

		var req generated.UploadRequest
		if err := c.Bind().JSON(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
		}
		if len(req.Requests) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
		}
		for _, item := range req.Requests {
			key := strings.TrimSpace(item.Name)
			checksums := make([]objects.Checksum, len(item.Checksums))
			for i, checksum := range item.Checksums {
				checksums[i] = objects.Checksum{Type: checksum.Type, Checksum: checksum.Checksum}
			}
			if oid, ok := objects.CanonicalSHA256(checksums); ok && oid != "" {
				key = oid
			}
			if key == "" {
				return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
			}
		}

		return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr(uploadRequestRoutingError)})
	}
}

func handleDeleteObjectFiber(service *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		id := c.Params("object_id")
		var body generated.DeleteRequest
		if len(c.Body()) > 0 {
			if err := c.Bind().JSON(&body); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
			}
		}
		opts := objectrecords.DeleteOptions{
			DeleteStorageData: body.DeleteStorageData != nil && *body.DeleteStorageData,
		}
		if err := service.DeleteObjectWithOptions(c.Context(), id, opts); err != nil {
			return response.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}

func handleUpdateAccessMethodsFiber(service *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		objectID := strings.TrimSpace(c.Params("object_id"))
		if objectID != "" {
			var body generated.AccessMethodUpdateRequest
			if err := c.Bind().JSON(&body); err != nil || len(body.AccessMethods) == 0 {
				return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
			}
			if err := service.UpdateObjectAccessMethods(c.Context(), objectID, FromGeneratedAccessMethods(body.AccessMethods)); err != nil {
				return response.HandleError(c, err)
			}
			obj, err := service.GetObject(c.Context(), objectID, "read")
			if err != nil {
				return response.HandleError(c, err)
			}
			return c.JSON(ObjectPayload(*obj))
		}

		var body generated.BulkAccessMethodUpdateRequest
		if err := c.Bind().JSON(&body); err != nil || len(body.Updates) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
		}

		updates := make(map[string][]generated.AccessMethod, len(body.Updates))
		orderedIDs := make([]string, 0, len(body.Updates))
		for _, update := range body.Updates {
			id := strings.TrimSpace(update.ObjectId)
			if id == "" || len(update.AccessMethods) == 0 {
				return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
			}
			if _, exists := updates[id]; !exists {
				orderedIDs = append(orderedIDs, id)
			}
			updates[id] = update.AccessMethods
		}

		if err := service.BulkUpdateAccessMethods(c.Context(), FromGeneratedAccessMethodMap(updates)); err != nil {
			return response.HandleError(c, err)
		}

		objects := make([]any, 0, len(orderedIDs))
		for _, id := range orderedIDs {
			obj, err := service.GetObject(c.Context(), id, "read")
			if err != nil {
				return response.HandleError(c, err)
			}
			objects = append(objects, ObjectPayload(*obj))
		}
		return c.JSON(fiber.Map{"objects": objects})
	}
}

func handleBulkDeleteObjectsFiber(service *objectrecords.Service) fiber.Handler {
	return func(c fiber.Ctx) error {
		var body generated.BulkDeleteRequest
		if err := c.Bind().JSON(&body); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("Invalid request body")})
		}
		if len(body.BulkObjectIds) == 0 {
			return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("bulk_object_ids cannot be empty")})
		}

		ids := make([]string, 0, len(body.BulkObjectIds))
		seen := make(map[string]struct{}, len(body.BulkObjectIds))
		for _, rawID := range body.BulkObjectIds {
			id := strings.TrimSpace(rawID)
			if id == "" {
				return c.Status(fiber.StatusBadRequest).JSON(generated.Error{Msg: drsPtr("bulk_object_ids cannot contain empty values")})
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}

		opts := objectrecords.DeleteOptions{
			DeleteStorageData: body.DeleteStorageData != nil && *body.DeleteStorageData,
		}
		if err := service.BulkDeleteObjectsWithOptions(c.Context(), ids, opts); err != nil {
			return response.HandleError(c, err)
		}
		return c.SendStatus(fiber.StatusNoContent)
	}
}
