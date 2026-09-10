package httpapi

import (
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

func (s *internalServer) InternalDeleteProject(c fiber.Ctx, _, _ string) error {
	if s.projectStorage == nil {
		return HandleError(c, errorapi.Define(errorapi.ErrorCodeStorageUnavailable, errorapi.ErrorCategoryUnavailable, "project storage service is not configured"))
	}
	organization := strings.TrimSpace(c.Params("organization"))
	projectID := strings.TrimSpace(c.Params("project_id"))
	if organization == "" || projectID == "" {
		return Reject(c, fiber.StatusBadRequest, "organization and project_id are required")
	}
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	result, err := s.projectStorage.DeleteProjectDataAuthorized(c.Context(), organization, projectID)
	if err != nil {
		return HandleError(c, err)
	}

	return c.JSON(result)
}

func (s *internalServer) InternalScopeRepairAudit(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.ScopeRepairOptions
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.Project = strings.TrimSpace(req.Project)
	req.CheckStorage = true
	if req.Organization == "" || req.Project == "" {
		return Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	report, err := s.projectStorage.AuditAuthorized(c.Context(), req)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(report)
}

func (s *internalServer) InternalScopeRepairApply(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.ScopeRepairOptions
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.Project = strings.TrimSpace(req.Project)
	req.CheckStorage = true
	if req.Organization == "" || req.Project == "" {
		return Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	result, err := s.projectStorage.ApplyAuthorized(c.Context(), req)
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(result)
}

func (s *internalServer) InternalInspectObject(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectObjectRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	resp, err := s.projectStorage.ProbeObject(c.Context(), projectstorage.InspectRequest{
		ID:                strings.TrimSpace(req.Id),
		Organization:      strings.TrimSpace(req.Organization),
		Project:           strings.TrimSpace(req.Project),
		Key:               strings.TrimSpace(req.Key),
		Scheme:            strings.TrimSpace(req.Scheme),
		ObjectURL:         strings.TrimSpace(req.ObjectUrl),
		ExpectedSizeBytes: req.ExpectedSizeBytes,
		ExpectedSHA256:    strings.TrimSpace(req.ExpectedSha256),
	})
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(resp)
}

func (s *internalServer) InternalInspectObjectBulk(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectObjectsBulkRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if len(req.Items) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: items are required")
	}
	items := make([]projectstorage.InspectRequest, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, projectstorage.InspectRequest{
			ID:                strings.TrimSpace(item.Id),
			Organization:      strings.TrimSpace(item.Organization),
			Project:           strings.TrimSpace(item.Project),
			Key:               strings.TrimSpace(item.Key),
			Scheme:            strings.TrimSpace(item.Scheme),
			ObjectURL:         strings.TrimSpace(item.ObjectUrl),
			ExpectedSizeBytes: item.ExpectedSizeBytes,
			ExpectedSHA256:    strings.TrimSpace(item.ExpectedSha256),
		})
	}
	results := s.projectStorage.ProbeObjects(c.Context(), items)
	out := internalapi.InternalInspectObjectBulkResponse{Items: results}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectObjectBulkList(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectObjectsBulkRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if len(req.Items) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: items are required")
	}
	items := make([]projectstorage.InspectRequest, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, projectstorage.InspectRequest{
			ID:                strings.TrimSpace(item.Id),
			ObjectURL:         strings.TrimSpace(item.ObjectUrl),
			ExpectedSizeBytes: item.ExpectedSizeBytes,
			ExpectedName:      strings.TrimSpace(item.ExpectedName),
		})
	}
	results := s.projectStorage.ValidateInventoryObjects(c.Context(), items)
	out := internalapi.InternalInspectObjectBulkResponse{Items: results}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectBucket(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectProjectBucketRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	result, err := s.projectStorage.InspectProjectStorage(c.Context(), strings.TrimSpace(req.Organization), strings.TrimSpace(req.Project), projectstorage.InspectionOptions{
		Mode:        projectstorage.InspectionMode(strings.TrimSpace(req.Mode)),
		IncludeHead: req.IncludeHead,
		PathPrefix:  strings.TrimSpace(req.PathPrefix),
	})
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(result)
}

func (s *internalServer) InternalInspectProjectBucketInventory(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectProjectBucketRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	result, err := s.projectStorage.InspectProjectStorage(c.Context(), strings.TrimSpace(req.Organization), strings.TrimSpace(req.Project), projectstorage.InspectionOptions{
		Mode:       projectstorage.ModeItems,
		PathPrefix: strings.TrimSpace(req.PathPrefix),
	})
	if err != nil {
		return HandleError(c, err)
	}
	return c.JSON(result)
}

func (s *internalServer) InternalInspectProjectRecords(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectProjectRecordsRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	organization := strings.TrimSpace(req.Organization)
	project := strings.TrimSpace(req.Project)
	if organization == "" || project == "" {
		return Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	if s.objects == nil {
		return HandleError(c, errorapi.Define(errorapi.ErrorCodeStorageUnsupported, errorapi.ErrorCategoryInvalidInput, "object service is not configured"))
	}
	records, err := s.objects.AuditProjectRecords(c.Context(), objects.ProjectRecordAuditQuery{
		Organization: organization,
		Project:      project,
		PathPrefix:   req.PathPrefix,
	})
	if err != nil {
		return HandleError(c, err)
	}
	out := internalapi.InternalInspectProjectRecordsResponse{Items: make([]internalapi.InternalInspectProjectRecordItem, 0, len(records))}
	for _, record := range records {
		out.Items = append(out.Items, projectRecordAuditItemFromObjects(record, organization, project))
	}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectScopes(c fiber.Ctx, _ internalapi.InternalInspectProjectScopesParams) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalInspectProjectScopesRequest
	switch c.Method() {
	case fiber.MethodGet:
		req.Organization = c.Query("organization")
		req.Project = c.Query("project")
	default:
		if err := decodeStrictJSON(c.Body(), &req); err != nil {
			return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
	}
	organization := strings.TrimSpace(req.Organization)
	project := strings.TrimSpace(req.Project)
	if organization == "" || project == "" {
		return Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	scopes, err := s.buckets.ListVisibleProjectScopes(c.Context(), organization, project)
	if err != nil {
		return HandleError(c, err)
	}
	out := internalapi.InternalInspectProjectScopesResponse{Items: make([]internalapi.InternalInspectProjectScopeItem, 0)}
	for _, scope := range scopes {
		row := internalapi.InternalInspectProjectScopeItem{
			Bucket:       scope.Bucket,
			Organization: scope.Organization,
			ProjectId:    scope.ProjectID,
			Path:         scope.Path,
		}
		out.Items = append(out.Items, row)
	}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectScopesPost(c fiber.Ctx) error {
	return s.InternalInspectProjectScopes(c, internalapi.InternalInspectProjectScopesParams{})
}

func (s *internalServer) InternalDeleteProjectBucketObjects(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalapi.InternalDeleteProjectBucketObjectsRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if len(req.ObjectUrls) == 0 {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: object_urls are required")
	}
	results := s.projectStorage.DeleteProjectObjects(c.Context(), strings.TrimSpace(req.Organization), strings.TrimSpace(req.Project), req.ObjectUrls)
	out := internalapi.InternalDeleteProjectBucketObjectsResponse{Items: results}
	return c.JSON(out)
}

func projectRecordAuditItemFromObjects(record objects.Record, organization, project string) internalapi.InternalInspectProjectRecordItem {
	accessURLs := []string{}
	accessMethods := []internalapi.InternalProjectAccessMethod{}
	if record.AccessMethods != nil {
		accessMethods = make([]internalapi.InternalProjectAccessMethod, 0, len(*record.AccessMethods))
		for _, method := range *record.AccessMethods {
			item := internalapi.InternalProjectAccessMethod{Type: strings.TrimSpace(method.Type)}
			if method.AccessId != nil {
				item.AccessId = strings.TrimSpace(*method.AccessId)
			}
			if method.AccessUrl != nil {
				item.Url = strings.TrimSpace(method.AccessUrl.Url)
				if item.Url != "" {
					accessURLs = append(accessURLs, item.Url)
				}
				if method.AccessUrl.Headers != nil {
					item.Headers = append([]string(nil), (*method.AccessUrl.Headers)...)
				}
			}
			accessMethods = append(accessMethods, item)
		}
	}
	checksum, _ := objects.CanonicalSHA256(record.Checksums)
	item := internalapi.InternalInspectProjectRecordItem{
		ObjectId:      string(record.Id),
		Checksum:      checksum,
		Organization:  organization,
		Project:       project,
		Size:          record.Size,
		AccessUrls:    accessURLs,
		AccessMethods: accessMethods,
	}
	if record.Name != nil {
		item.Name = strings.TrimSpace(*record.Name)
	}
	if !record.CreatedTime.IsZero() {
		item.CreatedTime = record.CreatedTime.Format(time.RFC3339Nano)
	}
	if record.UpdatedTime != nil && !record.UpdatedTime.IsZero() {
		item.UpdatedTime = record.UpdatedTime.Format(time.RFC3339Nano)
	}
	return item
}
