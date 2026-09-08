package drs

import (
	"encoding/json"
	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
	"strings"
)

func (s *server) GetAccessURL(c fiber.Ctx, objectID generated.ObjectId, accessID generated.AccessId) error {
	s.accessService.BindLegacyDependencies(s.objectService, nil)
	result, err := s.accessService.IssueAccess(c.Context(), transfers.AccessLookupRequest{ObjectID: string(objectID), AccessID: string(accessID)})
	if err != nil {
		return middleware.HandleError(c, err)
	}
	if !result.Found {
		return middleware.Reject(c, fiber.StatusNotFound, "Access ID not found or has no URL")
	}
	return c.JSON(generated.AccessURL{Url: result.URL})
}

func (s *server) PostAccessURL(c fiber.Ctx, objectID generated.ObjectId, accessID generated.AccessId) error {
	return s.GetAccessURL(c, objectID, accessID)
}

func (s *server) GetBulkAccessURL(c fiber.Ctx) error {
	s.accessService.BindLegacyDependencies(s.objectService, nil)
	var body generated.BulkObjectAccessId
	if err := c.Bind().JSON(&body); err != nil || body.BulkObjectAccessIds == nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	requests := make([]transfers.BulkAccessLookupRequest, 0, len(*body.BulkObjectAccessIds))
	for _, item := range *body.BulkObjectAccessIds {
		accessIDs := []string(nil)
		if item.BulkAccessIds != nil {
			accessIDs = append(accessIDs, (*item.BulkAccessIds)...)
		}
		requests = append(requests, transfers.BulkAccessLookupRequest{
			ObjectID:  strings.TrimSpace(drsStringValue(item.BulkObjectId)),
			AccessIDs: accessIDs,
		})
	}
	lookup := make([]transfers.AccessLookupRequest, 0, len(requests))
	for _, request := range requests {
		if len(request.AccessIDs) == 0 {
			lookup = append(lookup, transfers.AccessLookupRequest{ObjectID: request.ObjectID})
			continue
		}
		for _, accessID := range request.AccessIDs {
			lookup = append(lookup, transfers.AccessLookupRequest{ObjectID: request.ObjectID, AccessID: accessID})
		}
	}
	result := s.accessService.IssueAccessBulk(c.Context(), lookup)
	resolved := make([]generated.BulkAccessURL, 0, len(result.Resolved))
	for _, item := range result.Resolved {
		resolved = append(resolved, generated.BulkAccessURL{
			DrsObjectId: drsPtr(item.ObjectID),
			DrsAccessId: drsPtr(item.AccessID),
			Url:         item.URL,
		})
	}

	resp := fiber.Map{
		"resolved_drs_object_access_urls": resolved,
		"summary": generated.Summary{
			Requested:  drsPtr(result.Requested),
			Resolved:   drsPtr(len(resolved)),
			Unresolved: drsPtr(result.Requested - len(resolved)),
		},
	}
	if len(result.UnresolvedObjectIDs) > 0 {
		resp["unresolved_drs_objects"] = []fiber.Map{{
			"error_code": fiber.StatusNotFound,
			"object_ids": result.UnresolvedObjectIDs,
		}}
	}
	return c.JSON(resp)
}

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

func (s *server) RegisterObjects(c fiber.Ctx) error {
	var body generated.RegisterObjectsJSONBody
	var candidates []objects.Candidate
	if err := json.Unmarshal(c.Body(), &body); err == nil && len(body.Candidates) > 0 {
		candidates = make([]objects.Candidate, 0, len(body.Candidates))
		for _, candidate := range body.Candidates {
			candidates = append(candidates, FromGeneratedCandidate(candidate))
		}
	} else {
		var single generated.DrsObjectCandidate
		if err2 := json.Unmarshal(c.Body(), &single); err2 == nil && len(single.Checksums) > 0 {
			candidates = []objects.Candidate{FromGeneratedCandidate(single)}
		} else {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}

	registered, err := s.objectService.RegisterCandidates(c.Context(), candidates)
	if err != nil {
		return middleware.HandleError(c, err)
	}

	response := make([]ObjectResponse, len(registered))
	for i, record := range registered {
		response[i] = ObjectPayload(record)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"objects": response})
}

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
