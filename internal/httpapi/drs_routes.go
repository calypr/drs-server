package httpapi

import (
	"encoding/json"
	"strings"

	generated "github.com/calypr/syfon/apigen/drs"
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

	requests := make([]transfers.BulkAccessLookupRequest, 0, len(*body.BulkObjectAccessIds))
	for _, item := range *body.BulkObjectAccessIds {
		accessIDs := []string(nil)
		if item.BulkAccessIds != nil {
			accessIDs = append(accessIDs, (*item.BulkAccessIds)...)
		}
		objectID := ""
		if item.BulkObjectId != nil {
			objectID = strings.TrimSpace(*item.BulkObjectId)
		}
		requests = append(requests, transfers.BulkAccessLookupRequest{
			ObjectID:  objectID,
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
	opts := objects.DeleteOptions{
		DeleteStorageData: body.DeleteStorageData != nil && *body.DeleteStorageData,
	}
	if err := s.objectService.DeleteObject(c.Context(), string(objectID), opts); err != nil {
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
	obj, err := s.objectService.UpdateAccessMethodsAndRead(c.Context(), objectID, drsFromGeneratedAccessMethods(body.AccessMethods))
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(drsObjectPayload(*obj))
}

func (s *drsServer) BulkUpdateAccessMethods(c fiber.Ctx) error {
	var body generated.BulkAccessMethodUpdateRequest
	if err := c.Bind().JSON(&body); err != nil || len(body.Updates) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	updates := make([]objects.AccessMethodUpdate, 0, len(body.Updates))
	for _, update := range body.Updates {
		id := strings.TrimSpace(update.ObjectId)
		if id == "" || len(update.AccessMethods) == 0 {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
		updates = append(updates, objects.AccessMethodUpdate{
			ObjectID: id,
			Methods:  drsFromGeneratedAccessMethods(update.AccessMethods),
		})
	}

	updated, err := s.objectService.BulkUpdateAccessMethodsAndRead(c.Context(), updates)
	if err != nil {
		return HandleError(c, err)
	}

	response := make([]drsObjectResponse, 0, len(updated))
	for _, obj := range updated {
		response = append(response, drsObjectPayload(obj))
	}
	return c.JSON(fiber.Map{"objects": response})
}

func (s *drsServer) BulkDeleteObjects(c fiber.Ctx) error {
	var body generated.BulkDeleteRequest
	if err := c.Bind().JSON(&body); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}
	if len(body.BulkObjectIds) == 0 {
		return Reject(c, fiber.StatusBadRequest, "bulk_object_ids cannot be empty")
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

	opts := objects.DeleteOptions{
		DeleteStorageData: body.DeleteStorageData != nil && *body.DeleteStorageData,
	}
	if err := s.objectService.BulkDeleteObjects(c.Context(), ids, opts); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *drsServer) BulkAddChecksums(c fiber.Ctx) error { return unsupportedChecksumAddition(c) }

func (s *drsServer) AddChecksums(c fiber.Ctx, _ string) error { return unsupportedChecksumAddition(c) }

func unsupportedChecksumAddition(c fiber.Ctx) error {
	return Reject(c, fiber.StatusNotFound, "Checksum addition is not supported")
}

func (s *drsServer) GetObject(c fiber.Ctx, objectID generated.ObjectId, _ generated.GetObjectParams) error {
	obj, err := s.objectService.GetObject(c.Context(), string(objectID), "")
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(drsObjectPayload(*obj))
}

func (s *drsServer) PostObject(c fiber.Ctx, objectID generated.ObjectId) error {
	return s.GetObject(c, objectID, generated.GetObjectParams{})
}

func (s *drsServer) GetBulkObjects(c fiber.Ctx, _ generated.GetBulkObjectsParams) error {
	var body struct {
		BulkObjectIds []string `json:"bulk_object_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body")
	}

	objects, err := s.objectService.GetBulkObjects(c.Context(), body.BulkObjectIds, "")
	if err != nil {
		return HandleError(c, err)
	}

	resolved := make([]drsObjectResponse, 0, len(objects))
	for _, obj := range objects {
		resolved = append(resolved, drsObjectPayload(obj))
	}

	return c.JSON(fiber.Map{
		"resolved_drs_object": resolved,
		"summary": generated.Summary{
			Requested: drsPtr(len(body.BulkObjectIds)),
			Resolved:  drsPtr(len(resolved)),
		},
	})
}

func (s *drsServer) GetObjectsByChecksum(c fiber.Ctx, checksum generated.ChecksumParameter) error {
	fetched, err := s.objectService.GetObjectsByChecksum(c.Context(), string(checksum), "")
	if err != nil {
		return HandleError(c, err)
	}

	resolved := make([]drsObjectResponse, 0)
	for _, obj := range fetched {
		resolved = append(resolved, drsObjectPayload(obj))
	}

	return c.JSON(fiber.Map{
		"resolved_drs_object": resolved,
		"summary": generated.Summary{
			Requested: drsPtr(1),
			Resolved:  drsPtr(len(resolved)),
		},
	})
}

func (s *drsServer) RegisterObjects(c fiber.Ctx) error {
	var body generated.RegisterObjectsJSONBody
	var candidates []objects.Candidate
	if err := json.Unmarshal(c.Body(), &body); err == nil && len(body.Candidates) > 0 {
		candidates = make([]objects.Candidate, 0, len(body.Candidates))
		for _, candidate := range body.Candidates {
			candidates = append(candidates, drsFromGeneratedCandidate(candidate))
		}
	} else {
		var single generated.DrsObjectCandidate
		if err2 := json.Unmarshal(c.Body(), &single); err2 == nil && len(single.Checksums) > 0 {
			candidates = []objects.Candidate{drsFromGeneratedCandidate(single)}
		} else {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body")
		}
	}

	registered, err := s.objectService.RegisterCandidates(c.Context(), candidates)
	if err != nil {
		return HandleError(c, err)
	}

	response := make([]drsObjectResponse, len(registered))
	for i, record := range registered {
		response[i] = drsObjectPayload(record)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"objects": response})
}

func registerDRSRoutes(router fiber.Router, objectService *objects.Service, accessService *transfers.Service, serviceInfo generated.Service) {
	handlers := &drsServer{
		objectService: objectService,
		accessService: accessService,
		serviceInfo:   serviceInfo,
	}

	generated.RegisterHandlers(router, handlers)
}

type drsServer struct {
	objectService *objects.Service
	accessService *transfers.Service
	serviceInfo   generated.Service
}

var _ generated.ServerInterface = (*drsServer)(nil)

func (s *drsServer) OptionsBulkObject(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }

func (s *drsServer) OptionsObject(c fiber.Ctx, _ generated.ObjectId) error {
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *drsServer) GetServiceInfo(c fiber.Ctx) error { return c.JSON(s.serviceInfo) }

// drsFromGeneratedCandidate translates the DRS registration request into an object candidate.
func drsFromGeneratedCandidate(value generated.DrsObjectCandidate) objects.Candidate {
	out := objects.Candidate{
		Aliases:          value.Aliases,
		Description:      value.Description,
		MimeType:         value.MimeType,
		Name:             value.Name,
		ControlledAccess: value.ControlledAccess,
		Size:             &value.Size,
	}
	if value.Checksums != nil {
		out.Checksums = &value.Checksums
	}
	if value.AccessMethods != nil {
		methods := make([]objects.AccessMethod, 0, len(*value.AccessMethods))
		for _, method := range *value.AccessMethods {
			methods = append(methods, drsFromGeneratedAccessMethod(method))
		}
		out.AccessMethods = &methods
	}
	if value.Contents != nil {
		out.Contents = cloneGeneratedContents(value.Contents)
	}
	return out
}

func drsToGenerated(record objects.Record) generated.DrsObject {
	out := generated.DrsObject{
		Id:               string(record.Id),
		ControlledAccess: record.ControlledAccess,
		CreatedTime:      record.CreatedTime,
		Description:      record.Description,
		MimeType:         record.MimeType,
		Name:             record.Name,
		SelfUri:          record.SelfUri,
		Size:             record.Size,
		UpdatedTime:      record.UpdatedTime,
		Version:          record.Version,
	}
	out.Checksums = record.Checksums
	if record.AccessMethods != nil {
		methods := make([]generated.AccessMethod, 0, len(*record.AccessMethods))
		for _, method := range *record.AccessMethods {
			methods = append(methods, drsToGeneratedAccessMethod(method))
		}
		out.AccessMethods = &methods
	}
	if record.Aliases != nil {
		out.Aliases = record.Aliases
	}
	if record.Contents != nil {
		out.Contents = cloneGeneratedContents(record.Contents)
	}
	return out
}

// drsObjectResponse is the typed DRS response with the legacy identity and alias
// fields retained by this server's wire contract.
type drsObjectResponse struct {
	generated.DrsObject
	Did         string    `json:"did,omitempty"`
	NameAliases *[]string `json:"name_aliases,omitempty"`
}

// MarshalJSON preserves the minimal identity response when a generated DRS
// field cannot be encoded, such as an invalid timestamp.
func (value drsObjectResponse) MarshalJSON() ([]byte, error) {
	type response struct {
		generated.DrsObject
		Did         string    `json:"did,omitempty"`
		NameAliases *[]string `json:"name_aliases,omitempty"`
	}
	encoded, err := json.Marshal(response(value))
	if err == nil {
		return encoded, nil
	}
	type fallback struct {
		ID      string `json:"id,omitempty"`
		DID     string `json:"did,omitempty"`
		SelfURI string `json:"self_uri"`
	}
	return json.Marshal(fallback{ID: value.Id, DID: value.Did, SelfURI: value.SelfUri})
}

// drsObjectPayload projects a domain record into the typed DRS response.
func drsObjectPayload(record objects.Record) drsObjectResponse {
	var aliases *[]string
	if record.NameAliases != nil {
		copyAliases := append([]string(nil), record.NameAliases...)
		aliases = &copyAliases
	}
	return drsObjectResponse{
		DrsObject:   drsToGenerated(record),
		Did:         string(record.Id),
		NameAliases: aliases,
	}
}

func drsFromGeneratedAccessMethods(methods []generated.AccessMethod) []objects.AccessMethod {
	out := make([]objects.AccessMethod, 0, len(methods))
	for _, method := range methods {
		out = append(out, drsFromGeneratedAccessMethod(method))
	}
	return out
}

// drsToGeneratedAccessMethods translates domain access methods for generated
// request/response models that embed the DRS access contract.
func drsToGeneratedAccessMethods(methods *[]objects.AccessMethod) *[]generated.AccessMethod {
	if methods == nil {
		return nil
	}
	out := make([]generated.AccessMethod, 0, len(*methods))
	for _, method := range *methods {
		out = append(out, drsToGeneratedAccessMethod(method))
	}
	return &out
}

func drsToGeneratedAccessMethod(method objects.AccessMethod) generated.AccessMethod {
	out := generated.AccessMethod{AccessId: method.AccessId, Available: method.Available, Cloud: method.Cloud, Region: method.Region, Type: generated.AccessMethodType(method.Type)}
	if method.AccessUrl != nil {
		out.AccessUrl = &struct {
			Headers *[]string `json:"headers,omitempty"`
			Url     string    `json:"url"`
		}{Headers: method.AccessUrl.Headers, Url: method.AccessUrl.Url}
	}
	if method.Authorizations != nil {
		supported := (*[]generated.AccessMethodAuthorizationsSupportedTypes)(nil)
		if method.Authorizations.SupportedTypes != nil {
			converted := make([]generated.AccessMethodAuthorizationsSupportedTypes, len(*method.Authorizations.SupportedTypes))
			for i, value := range *method.Authorizations.SupportedTypes {
				converted[i] = generated.AccessMethodAuthorizationsSupportedTypes(value)
			}
			supported = &converted
		}
		out.Authorizations = &struct {
			BearerAuthIssuers   *[]string                                             `json:"bearer_auth_issuers,omitempty"`
			DrsObjectId         *string                                               `json:"drs_object_id,omitempty"`
			PassportAuthIssuers *[]string                                             `json:"passport_auth_issuers,omitempty"`
			SupportedTypes      *[]generated.AccessMethodAuthorizationsSupportedTypes `json:"supported_types,omitempty"`
		}{BearerAuthIssuers: method.Authorizations.BearerAuthIssuers, DrsObjectId: method.Authorizations.DrsObjectId, PassportAuthIssuers: method.Authorizations.PassportAuthIssuers, SupportedTypes: supported}
	}
	return out
}

func drsFromGeneratedAccessMethod(method generated.AccessMethod) objects.AccessMethod {
	out := objects.AccessMethod{AccessId: method.AccessId, Available: method.Available, Cloud: method.Cloud, Region: method.Region, Type: string(method.Type)}
	if method.AccessUrl != nil {
		out.AccessUrl = &objects.AccessURL{Headers: method.AccessUrl.Headers, Url: method.AccessUrl.Url}
	}
	if method.Authorizations != nil {
		var supported *[]string
		if method.Authorizations.SupportedTypes != nil {
			converted := make([]string, len(*method.Authorizations.SupportedTypes))
			for i, value := range *method.Authorizations.SupportedTypes {
				converted[i] = string(value)
			}
			supported = &converted
		}
		out.Authorizations = &objects.AccessAuthorizations{BearerAuthIssuers: method.Authorizations.BearerAuthIssuers, DrsObjectId: method.Authorizations.DrsObjectId, PassportAuthIssuers: method.Authorizations.PassportAuthIssuers, SupportedTypes: supported}
	}
	return out
}

func cloneGeneratedContents(contents *[]generated.ContentsObject) *[]generated.ContentsObject {
	if contents == nil {
		return nil
	}
	cloned := make([]generated.ContentsObject, len(*contents))
	for i, content := range *contents {
		cloned[i] = content
		cloned[i].Contents = cloneGeneratedContents(content.Contents)
	}
	return &cloned
}

func drsPtr[T any](value T) *T {
	return &value
}
