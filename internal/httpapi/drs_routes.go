package httpapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	generated "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/gofiber/fiber/v3"
)

func (s *drsServer) GetAccessURL(c fiber.Ctx, objectID generated.ObjectId, accessID generated.AccessId) error {
	result, err := s.accessService.IssueAccess(c.Context(), transfers.AccessLookupRequest{ObjectID: string(objectID), AccessID: string(accessID)})
	if err != nil {
		return HandleError(c, err)
	}
	if !result.Found {
		return Reject(c, fiber.StatusNotFound, "Access ID not found or has no URL")
	}
	return c.JSON(generated.AccessURL{Url: result.URL})
}

func (s *drsServer) PostAccessURL(c fiber.Ctx, objectID generated.ObjectId, accessID generated.AccessId) error {
	return s.GetAccessURL(c, objectID, accessID)
}

func (s *drsServer) GetBulkAccessURL(c fiber.Ctx) error {
	var body generated.BulkObjectAccessId
	if err := c.Bind().JSON(&body); err != nil || body.BulkObjectAccessIds == nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if err := s.rejectBulkTooLarge(c, len(*body.BulkObjectAccessIds)); err != nil {
		return err
	}

	requests := make([]transfers.AccessLookupRequest, 0, len(*body.BulkObjectAccessIds))
	for _, item := range *body.BulkObjectAccessIds {
		objectID := ""
		if item.BulkObjectId != nil {
			objectID = strings.TrimSpace(*item.BulkObjectId)
		}
		if item.BulkAccessIds == nil || len(*item.BulkAccessIds) == 0 {
			requests = append(requests, transfers.AccessLookupRequest{ObjectID: objectID})
			continue
		}
		for _, accessID := range *item.BulkAccessIds {
			requests = append(requests, transfers.AccessLookupRequest{ObjectID: objectID, AccessID: accessID})
		}
	}
	result := s.accessService.IssueAccessBulk(c.Context(), requests)
	resolved := make([]generated.BulkAccessURL, 0, len(result.Resolved))
	for _, item := range result.Resolved {
		resolved = append(resolved, generated.BulkAccessURL{
			DrsObjectId: valuePointer(item.ObjectID),
			DrsAccessId: valuePointer(item.AccessID),
			Url:         item.URL,
		})
	}

	summary := generated.Summary{
		Requested:  valuePointer(result.Requested),
		Resolved:   valuePointer(len(resolved)),
		Unresolved: valuePointer(result.Requested - len(resolved)),
	}
	resp := generated.N200OkAccesses{
		ResolvedDrsObjectAccessUrls: &resolved,
		Summary:                     &summary,
	}
	if len(result.Failures) > 0 {
		type failureGroup struct {
			status    int
			objectIDs []string
			seen      map[string]struct{}
		}
		groups := make([]failureGroup, 0)
		groupByStatus := make(map[int]int)
		for _, failure := range result.Failures {
			objectID := strings.TrimSpace(failure.ObjectID)
			if objectID == "" {
				continue
			}
			status := ClassifyError(c.Context(), failure.Err).Status
			groupIndex, ok := groupByStatus[status]
			if !ok {
				groupIndex = len(groups)
				groupByStatus[status] = groupIndex
				groups = append(groups, failureGroup{status: status, seen: make(map[string]struct{})})
			}
			if _, seen := groups[groupIndex].seen[objectID]; seen {
				continue
			}
			groups[groupIndex].seen[objectID] = struct{}{}
			groups[groupIndex].objectIDs = append(groups[groupIndex].objectIDs, objectID)
		}
		unresolved := make(generated.Unresolved, len(groups))
		for i := range groups {
			unresolved[i].ErrorCode = valuePointer(groups[i].status)
			unresolved[i].ObjectIds = &groups[i].objectIDs
		}
		resp.UnresolvedDrsObjects = &unresolved
	}
	return c.JSON(resp)
}

func (s *drsServer) PostUploadRequest(c fiber.Ctx) error {
	const uploadRequestRoutingError = "upload-request requires explicit upload routing; default bucket selection is disabled"
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}

	var req generated.UploadRequest
	if err := c.Bind().JSON(&req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(req.Requests) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if err := s.rejectBulkTooLarge(c, len(req.Requests)); err != nil {
		return err
	}
	for _, item := range req.Requests {
		key := strings.TrimSpace(item.Name)
		if oid, ok := objects.CanonicalSHA256(item.Checksums); ok && oid != "" {
			key = oid
		}
		if key == "" {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}

	return Reject(c, fiber.StatusBadRequest, uploadRequestRoutingError)
}

func (s *drsServer) DeleteObject(c fiber.Ctx, objectID generated.ObjectId) error {
	var body generated.DeleteRequest
	if len(c.Body()) > 0 {
		if err := c.Bind().JSON(&body); err != nil {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}
	if body.DeleteStorageData != nil && *body.DeleteStorageData {
		return HandleError(c, unsupportedStorageDeletion())
	}
	if err := s.objectService.DeleteObject(c.Context(), string(objectID)); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *drsServer) UpdateObjectAccessMethods(c fiber.Ctx, objectID string) error {
	objectID = strings.TrimSpace(objectID)
	var body generated.AccessMethodUpdateRequest
	if err := c.Bind().JSON(&body); err != nil || len(body.AccessMethods) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	obj, err := s.objectService.UpdateAccessMethodsAndRead(c.Context(), objectID, body.AccessMethods)
	if err != nil {
		return HandleError(c, err)
	}
	setDRSIdentity(obj)
	return c.JSON(*obj)
}

func (s *drsServer) BulkUpdateAccessMethods(c fiber.Ctx) error {
	var body generated.BulkAccessMethodUpdateRequest
	if err := c.Bind().JSON(&body); err != nil || len(body.Updates) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if err := s.rejectBulkTooLarge(c, len(body.Updates)); err != nil {
		return err
	}

	for _, update := range body.Updates {
		id := strings.TrimSpace(update.ObjectId)
		if id == "" || len(update.AccessMethods) == 0 {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}

	updated, err := s.objectService.BulkUpdateAccessMethodsAndRead(c.Context(), body.Updates)
	if err != nil {
		return HandleError(c, err)
	}

	for i := range updated {
		setDRSIdentity(&updated[i])
	}
	return c.JSON(generated.N200BulkAccessMethodUpdate{Objects: updated})
}

func (s *drsServer) BulkDeleteObjects(c fiber.Ctx) error {
	var body generated.BulkDeleteRequest
	if err := c.Bind().JSON(&body); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(body.BulkObjectIds) == 0 {
		return Reject(c, fiber.StatusBadRequest, "bulk_object_ids cannot be empty")
	}
	if err := s.rejectBulkTooLarge(c, len(body.BulkObjectIds)); err != nil {
		return err
	}

	ids := make([]string, 0, len(body.BulkObjectIds))
	seen := make(map[string]struct{}, len(body.BulkObjectIds))
	for _, rawID := range body.BulkObjectIds {
		id := strings.TrimSpace(rawID)
		if id == "" {
			return Reject(c, fiber.StatusBadRequest, "bulk_object_ids cannot contain empty values")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}

	if body.DeleteStorageData != nil && *body.DeleteStorageData {
		return HandleError(c, unsupportedStorageDeletion())
	}
	if err := s.objectService.BulkDeleteObjects(c.Context(), ids); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *drsServer) BulkAddChecksums(c fiber.Ctx) error { return unsupportedChecksumAddition(c) }

func (s *drsServer) AddChecksums(c fiber.Ctx, _ string) error { return unsupportedChecksumAddition(c) }

func unsupportedChecksumAddition(c fiber.Ctx) error {
	return Reject(c, fiber.StatusNotFound, "Checksum addition is not supported")
}

func unsupportedStorageDeletion() error {
	return fmt.Errorf("%w: physical storage deletion is not atomic with catalog mutation", errorapi.ErrConflict)
}

func (s *drsServer) GetObject(c fiber.Ctx, objectID generated.ObjectId, _ generated.GetObjectParams) error {
	obj, err := s.objectService.GetObject(c.Context(), string(objectID), "")
	if err != nil {
		return HandleError(c, err)
	}
	setDRSIdentity(obj)
	return c.JSON(*obj)
}

func (s *drsServer) PostObject(c fiber.Ctx, objectID generated.ObjectId) error {
	return s.GetObject(c, objectID, generated.GetObjectParams{})
}

func (s *drsServer) GetBulkObjects(c fiber.Ctx, _ generated.GetBulkObjectsParams) error {
	var body generated.GetBulkObjectsJSONBody
	if err := c.Bind().JSON(&body); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if err := s.rejectBulkTooLarge(c, len(body.BulkObjectIds)); err != nil {
		return err
	}

	objects, err := s.objectService.GetBulkObjects(c.Context(), body.BulkObjectIds, "")
	if err != nil {
		return HandleError(c, err)
	}

	for i := range objects {
		setDRSIdentity(&objects[i])
	}
	summary := generated.Summary{
		Requested: valuePointer(len(body.BulkObjectIds)),
		Resolved:  valuePointer(len(objects)),
	}
	return c.JSON(generated.N200OkDrsObjects{
		ResolvedDrsObject: &objects,
		Summary:           &summary,
	})
}

func (s *drsServer) GetObjectsByChecksum(c fiber.Ctx, checksum generated.ChecksumParameter) error {
	key := string(checksum)
	if decoded, err := url.PathUnescape(key); err == nil {
		key = decoded
	}
	key = strings.TrimSpace(key)
	byChecksum, err := s.objectService.GetObjectsByChecksums(c.Context(), []string{key}, "")
	if err != nil {
		return HandleError(c, err)
	}
	fetched := byChecksum[key]
	if fetched == nil {
		fetched = []generated.DrsObject{}
	}

	for i := range fetched {
		setDRSIdentity(&fetched[i])
	}
	summary := generated.Summary{
		Requested: valuePointer(1),
		Resolved:  valuePointer(len(fetched)),
	}
	return c.JSON(generated.N200OkDrsObjects{
		ResolvedDrsObject: &fetched,
		Summary:           &summary,
	})
}

func (s *drsServer) RegisterObjects(c fiber.Ctx) error {
	var body generated.RegisterObjectsJSONBody
	var candidates []generated.DrsObjectCandidate
	if err := json.Unmarshal(c.Body(), &body); err == nil && len(body.Candidates) > 0 {
		candidates = body.Candidates
	} else {
		var single generated.DrsObjectCandidate
		if err2 := json.Unmarshal(c.Body(), &single); err2 == nil && len(single.Checksums) > 0 {
			candidates = []generated.DrsObjectCandidate{single}
		} else {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}
	if err := s.rejectBulkTooLarge(c, len(candidates)); err != nil {
		return err
	}

	registered, err := s.objectService.RegisterCandidates(c.Context(), candidates)
	if err != nil {
		return HandleError(c, err)
	}

	for i := range registered {
		setDRSIdentity(&registered[i])
	}
	return c.Status(fiber.StatusCreated).JSON(generated.N201ObjectsCreated{Objects: registered})
}

func registerDRSRoutes(router fiber.Router, objectService *objects.Service, accessService *transfers.Service, serviceInfo generated.N200ServiceInfo, maxBulkRequestLength ...int) {
	maxBulk := 0
	if len(maxBulkRequestLength) > 0 {
		maxBulk = maxBulkRequestLength[0]
	}
	handlers := &drsServer{
		objectService:        objectService,
		accessService:        accessService,
		serviceInfo:          serviceInfo,
		maxBulkRequestLength: maxBulk,
	}

	generated.RegisterHandlers(router, handlers)
}

type drsServer struct {
	objectService        *objects.Service
	accessService        *transfers.Service
	serviceInfo          generated.N200ServiceInfo
	maxBulkRequestLength int
}

var _ generated.ServerInterface = (*drsServer)(nil)

func (s *drsServer) OptionsBulkObject(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }

func (s *drsServer) OptionsObject(c fiber.Ctx, _ generated.ObjectId) error {
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *drsServer) GetServiceInfo(c fiber.Ctx) error { return c.JSON(s.serviceInfo) }

func (s *drsServer) rejectBulkTooLarge(c fiber.Ctx, length int) error {
	if s.maxBulkRequestLength > 0 && length > s.maxBulkRequestLength {
		return Reject(c, fiber.StatusRequestEntityTooLarge, "bulk request exceeds maxBulkRequestLength")
	}
	return nil
}

func setDRSIdentity(object *generated.DrsObject) {
	if object == nil || object.Id == "" {
		return
	}
	if object.Did == nil || *object.Did == "" {
		did := object.Id
		object.Did = &did
	}
}
