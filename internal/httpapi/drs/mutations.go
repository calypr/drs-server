package drs

import (
	"strings"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/gofiber/fiber/v3"
)

func (s *server) PostUploadRequest(c fiber.Ctx) error {
	const uploadRequestRoutingError = "upload-request requires explicit upload routing; default bucket selection is disabled"
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}

	var req generated.UploadRequest
	if err := c.Bind().JSON(&req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(req.Requests) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
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
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}

	return middleware.Reject(c, fiber.StatusBadRequest, uploadRequestRoutingError)
}

func (s *server) DeleteObject(c fiber.Ctx, objectID generated.ObjectId) error {
	var body generated.DeleteRequest
	if len(c.Body()) > 0 {
		if err := c.Bind().JSON(&body); err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}
	opts := objectrecords.DeleteOptions{
		DeleteStorageData: body.DeleteStorageData != nil && *body.DeleteStorageData,
	}
	if err := s.objectService.DeleteObjectWithOptions(c.Context(), string(objectID), opts); err != nil {
		return middleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *server) UpdateObjectAccessMethods(c fiber.Ctx, objectID string) error {
	objectID = strings.TrimSpace(objectID)
	var body generated.AccessMethodUpdateRequest
	if err := c.Bind().JSON(&body); err != nil || len(body.AccessMethods) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	obj, err := s.objectService.UpdateAccessMethodsAndRead(c.Context(), objectID, FromGeneratedAccessMethods(body.AccessMethods))
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(ObjectPayload(*obj))
}

func (s *server) BulkUpdateAccessMethods(c fiber.Ctx) error {
	var body generated.BulkAccessMethodUpdateRequest
	if err := c.Bind().JSON(&body); err != nil || len(body.Updates) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	updates := make([]objectrecords.AccessMethodUpdate, 0, len(body.Updates))
	for _, update := range body.Updates {
		id := strings.TrimSpace(update.ObjectId)
		if id == "" || len(update.AccessMethods) == 0 {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		updates = append(updates, objectrecords.AccessMethodUpdate{
			ObjectID: id,
			Methods:  FromGeneratedAccessMethods(update.AccessMethods),
		})
	}

	updated, err := s.objectService.BulkUpdateAccessMethodsAndRead(c.Context(), updates)
	if err != nil {
		return middleware.HandleError(c, err)
	}

	response := make([]ObjectResponse, 0, len(updated))
	for _, obj := range updated {
		response = append(response, ObjectPayload(obj))
	}
	return c.JSON(fiber.Map{"objects": response})
}

func (s *server) BulkDeleteObjects(c fiber.Ctx) error {
	var body generated.BulkDeleteRequest
	if err := c.Bind().JSON(&body); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(body.BulkObjectIds) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "bulk_object_ids cannot be empty")
	}

	ids := make([]string, 0, len(body.BulkObjectIds))
	seen := make(map[string]struct{}, len(body.BulkObjectIds))
	for _, rawID := range body.BulkObjectIds {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return middleware.Reject(c, fiber.StatusBadRequest, "bulk_object_ids cannot contain empty values")
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
	if err := s.objectService.BulkDeleteObjectsWithOptions(c.Context(), ids, opts); err != nil {
		return middleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *server) BulkAddChecksums(c fiber.Ctx) error { return unsupportedChecksumAddition(c) }

func (s *server) AddChecksums(c fiber.Ctx, _ string) error { return unsupportedChecksumAddition(c) }

func unsupportedChecksumAddition(c fiber.Ctx) error {
	return middleware.Reject(c, fiber.StatusNotFound, "Checksum addition is not supported")
}
