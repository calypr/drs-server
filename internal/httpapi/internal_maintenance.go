package httpapi

import (
	"log"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	internalapi "github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects/scoperepair"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

type projectCleanupResponse struct {
	Organization        string `json:"organization"`
	ProjectID           string `json:"project_id"`
	DeletedObjects      int    `json:"deleted_objects"`
	DeletedBucketScopes int    `json:"deleted_bucket_scopes"`
}

func (s *internalServer) InternalDeleteProject(c fiber.Ctx, _, _ string) error {
	if s.cleanup == nil {
		return middleware.HandleError(c, errorapi.Define(errorapi.ErrorCodeStorageUnavailable, errorapi.ErrorCategoryUnavailable, "project storage service is not configured"))
	}
	organization := strings.TrimSpace(c.Params("organization"))
	projectID := strings.TrimSpace(c.Params("project_id"))
	if organization == "" || projectID == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "organization and project_id are required")
	}
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	result, err := s.cleanup.DeleteProjectDataAuthorized(c.Context(), organization, projectID)
	if err != nil {
		return middleware.HandleError(c, err)
	}

	return c.JSON(projectCleanupResponse{
		Organization:        result.Organization,
		ProjectID:           result.ProjectID,
		DeletedObjects:      result.DeletedObjects,
		DeletedBucketScopes: result.DeletedBucketScopes,
	})
}

func (s *internalServer) InternalScopeRepairAudit(c fiber.Ctx) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req scoperepair.Options
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.Project = strings.TrimSpace(req.Project)
	req.CheckStorage = true
	if req.Organization == "" || req.Project == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	report, err := s.repair.AuditAuthorized(c.Context(), req)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(report)
}

func (s *internalServer) InternalScopeRepairApply(c fiber.Ctx) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req scoperepair.Options
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.Project = strings.TrimSpace(req.Project)
	req.CheckStorage = true
	if req.Organization == "" || req.Project == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	result, err := s.repair.ApplyAuthorized(c.Context(), req)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	return c.JSON(result)
}

type internalInspectObjectRequest struct {
	ID                string `json:"id,omitempty"`
	Organization      string `json:"organization,omitempty"`
	Project           string `json:"project,omitempty"`
	Key               string `json:"key,omitempty"`
	Scheme            string `json:"scheme,omitempty"`
	ObjectURL         string `json:"object_url,omitempty"`
	ExpectedSizeBytes *int64 `json:"expected_size_bytes,omitempty"`
	ExpectedSHA256    string `json:"expected_sha256,omitempty"`
	ExpectedName      string `json:"expected_name,omitempty"`
}

type internalInspectObjectsBulkRequest struct {
	Items []internalInspectObjectRequest `json:"items"`
}

type internalInspectProjectBucketRequest struct {
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
	IncludeHead  bool   `json:"include_head,omitempty"`
	Mode         string `json:"mode,omitempty"`
	PathPrefix   string `json:"path_prefix,omitempty"`
}

type internalInspectProjectRecordsRequest struct {
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
	PathPrefix   string `json:"path_prefix,omitempty"`
}

type internalInspectProjectScopesRequest struct {
	Organization string `json:"organization,omitempty"`
	Project      string `json:"project,omitempty"`
}

type internalDeleteProjectBucketObjectsRequest struct {
	Organization string   `json:"organization,omitempty"`
	Project      string   `json:"project,omitempty"`
	ObjectURLs   []string `json:"object_urls"`
}

type internalInspectObjectResponse struct {
	ObjectURL   string `json:"object_url"`
	Provider    string `json:"provider"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	MetaSHA256  string `json:"meta_sha256,omitempty"`
	ETag        string `json:"etag,omitempty"`
	LastModTime string `json:"last_modified,omitempty"`
}

type internalInspectObjectBulkResponse struct {
	Items []internalInspectObjectBulkItem `json:"items"`
}

type internalInspectProjectBucketResponse struct {
	Summary *internalInspectProjectBucketSummary `json:"summary,omitempty"`
	Items   []internalInspectProjectBucketItem   `json:"items"`
}

type internalInspectProjectRecordsResponse struct {
	Items []internalInspectProjectRecordItem `json:"items"`
}

type internalInspectProjectScopesResponse struct {
	Items []internalInspectProjectScopeItem `json:"items"`
}

type internalDeleteProjectBucketObjectsResponse struct {
	Items []internalDeleteProjectBucketObjectsItem `json:"items"`
}

type internalInspectObjectBulkItem struct {
	ID                   string   `json:"id,omitempty"`
	ObjectURL            string   `json:"object_url,omitempty"`
	Provider             string   `json:"provider,omitempty"`
	Bucket               string   `json:"bucket,omitempty"`
	Key                  string   `json:"key,omitempty"`
	Path                 string   `json:"path,omitempty"`
	Exists               bool     `json:"exists"`
	Status               string   `json:"status"`
	Error                string   `json:"error,omitempty"`
	ErrorKind            string   `json:"error_kind,omitempty"`
	SizeBytes            *int64   `json:"size_bytes,omitempty"`
	MetaSHA256           string   `json:"meta_sha256,omitempty"`
	ETag                 string   `json:"etag,omitempty"`
	LastModTime          string   `json:"last_modified,omitempty"`
	ValidationStatus     string   `json:"validation_status"`
	SizeMatch            *bool    `json:"size_match,omitempty"`
	NameMatch            *bool    `json:"name_match,omitempty"`
	SHA256Match          *bool    `json:"sha256_match,omitempty"`
	ValidationMismatches []string `json:"validation_mismatches,omitempty"`
}

type internalInspectProjectBucketSummary struct {
	Provider          string `json:"provider"`
	Bucket            string `json:"bucket"`
	Prefix            string `json:"prefix,omitempty"`
	ObjectURL         string `json:"object_url,omitempty"`
	Exists            bool   `json:"exists"`
	ObjectCount       int    `json:"object_count"`
	TotalBytes        int64  `json:"total_bytes"`
	ComputedAt        string `json:"computed_at"`
	Mode              string `json:"mode"`
	InventoryComplete bool   `json:"inventory_complete"`
	InventoryWarning  string `json:"inventory_warning,omitempty"`
}

type internalInspectProjectBucketItem struct {
	ObjectURL         string `json:"object_url"`
	Provider          string `json:"provider"`
	Bucket            string `json:"bucket"`
	Key               string `json:"key"`
	Path              string `json:"path"`
	SizeBytes         int64  `json:"size_bytes"`
	MetaSHA256        string `json:"meta_sha256,omitempty"`
	ETag              string `json:"etag,omitempty"`
	LastModTime       string `json:"last_modified,omitempty"`
	InventoryComplete bool   `json:"inventory_complete,omitempty"`
}

type internalInspectProjectRecordItem struct {
	ObjectID      string                        `json:"object_id"`
	Name          string                        `json:"name,omitempty"`
	Checksum      string                        `json:"checksum"`
	Organization  string                        `json:"organization"`
	Project       string                        `json:"project"`
	Size          int64                         `json:"size"`
	CreatedTime   string                        `json:"created_time,omitempty"`
	UpdatedTime   string                        `json:"updated_time,omitempty"`
	AccessURLs    []string                      `json:"access_urls"`
	AccessMethods []internalProjectAccessMethod `json:"access_methods"`
}

type internalProjectAccessMethod struct {
	AccessID string   `json:"access_id,omitempty"`
	Type     string   `json:"type,omitempty"`
	URL      string   `json:"url,omitempty"`
	Headers  []string `json:"headers,omitempty"`
}

type internalInspectProjectScopeItem struct {
	Bucket       string `json:"bucket"`
	Organization string `json:"organization"`
	ProjectID    string `json:"project_id,omitempty"`
	Path         string `json:"path,omitempty"`
}

type internalDeleteProjectBucketObjectsItem struct {
	ObjectURL string `json:"object_url"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

func (s *internalServer) InternalInspectObject(c fiber.Ctx) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectObjectRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	resp, err := s.inspector.ProbeObject(c.Context(), projectstorage.InspectRequest{
		ID:                strings.TrimSpace(req.ID),
		Organization:      strings.TrimSpace(req.Organization),
		Project:           strings.TrimSpace(req.Project),
		Key:               strings.TrimSpace(req.Key),
		Scheme:            strings.TrimSpace(req.Scheme),
		ObjectURL:         strings.TrimSpace(req.ObjectURL),
		ExpectedSizeBytes: req.ExpectedSizeBytes,
		ExpectedSHA256:    strings.TrimSpace(req.ExpectedSHA256),
	})
	if err != nil {
		return handleInspectStorageError(c, err)
	}
	out := internalInspectObjectResponse{
		ObjectURL:  resp.ObjectURL,
		Provider:   resp.Provider,
		Bucket:     resp.Bucket,
		Key:        resp.Key,
		Path:       resp.Path,
		SizeBytes:  resp.SizeBytes,
		MetaSHA256: resp.MetaSHA256,
		ETag:       resp.ETag,
	}
	if !resp.LastModTime.IsZero() {
		out.LastModTime = resp.LastModTime.Format("2006-01-02T15:04:05Z07:00")
	}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectObjectBulk(c fiber.Ctx) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectObjectsBulkRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if len(req.Items) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: items are required")
	}
	items := make([]projectstorage.InspectRequest, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, projectstorage.InspectRequest{
			ID:                strings.TrimSpace(item.ID),
			Organization:      strings.TrimSpace(item.Organization),
			Project:           strings.TrimSpace(item.Project),
			Key:               strings.TrimSpace(item.Key),
			Scheme:            strings.TrimSpace(item.Scheme),
			ObjectURL:         strings.TrimSpace(item.ObjectURL),
			ExpectedSizeBytes: item.ExpectedSizeBytes,
			ExpectedSHA256:    strings.TrimSpace(item.ExpectedSHA256),
		})
	}
	results := s.inspector.ProbeObjects(c.Context(), items)
	out := internalInspectObjectBulkResponse{Items: make([]internalInspectObjectBulkItem, 0, len(results))}
	for _, result := range results {
		out.Items = append(out.Items, bulkInspectItemFromProjectStorage(result))
	}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectObjectBulkList(c fiber.Ctx) error {
	started := time.Now()
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectObjectsBulkRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if len(req.Items) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: items are required")
	}
	items := make([]projectstorage.ListValidationRequest, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, projectstorage.ListValidationRequest{
			ID:                strings.TrimSpace(item.ID),
			ObjectURL:         strings.TrimSpace(item.ObjectURL),
			ExpectedSizeBytes: item.ExpectedSizeBytes,
			ExpectedName:      strings.TrimSpace(item.ExpectedName),
		})
	}
	results := s.inspector.ValidateInventoryObjects(c.Context(), items)
	out := internalInspectObjectBulkResponse{Items: make([]internalInspectObjectBulkItem, 0, len(results))}
	for _, result := range results {
		out.Items = append(out.Items, bulkListInspectItemFromProjectStorage(result))
	}
	log.Printf("INFO: syfon_inspect_bulk_list_handler items=%d results=%d duration_ms=%d", len(items), len(out.Items), time.Since(started).Milliseconds())
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectBucket(c fiber.Ctx) error {
	started := time.Now()
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectProjectBucketRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	result, err := s.inspector.InspectProjectStorage(c.Context(), strings.TrimSpace(req.Organization), strings.TrimSpace(req.Project), projectstorage.InspectionOptions{
		Mode:        projectstorage.InspectionMode(strings.TrimSpace(req.Mode)),
		IncludeHead: req.IncludeHead,
		PathPrefix:  strings.TrimSpace(req.PathPrefix),
	})
	if err != nil {
		log.Printf("INFO: syfon_project_bucket_handler organization=%s project=%s mode=%s path_prefix=%q include_head=%t duration_ms=%d error=%q", req.Organization, req.Project, req.Mode, req.PathPrefix, req.IncludeHead, time.Since(started).Milliseconds(), err.Error())
		return handleInspectStorageError(c, err)
	}
	out := projectBucketInventoryResponseFromProjectStorage(result)
	exists := false
	objectCount := 0
	totalBytes := int64(0)
	mode := strings.TrimSpace(req.Mode)
	if out.Summary != nil {
		exists = out.Summary.Exists
		objectCount = out.Summary.ObjectCount
		totalBytes = out.Summary.TotalBytes
		mode = out.Summary.Mode
	}
	log.Printf("INFO: syfon_project_bucket_handler organization=%s project=%s mode=%s path_prefix=%q include_head=%t exists=%t object_count=%d returned_items=%d total_bytes=%d duration_ms=%d", req.Organization, req.Project, mode, req.PathPrefix, req.IncludeHead, exists, objectCount, len(out.Items), totalBytes, time.Since(started).Milliseconds())
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectBucketInventory(c fiber.Ctx) error {
	started := time.Now()
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectProjectBucketRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	result, err := s.inspector.InspectProjectStorage(c.Context(), strings.TrimSpace(req.Organization), strings.TrimSpace(req.Project), projectstorage.InspectionOptions{
		Mode:       projectstorage.ModeItems,
		PathPrefix: strings.TrimSpace(req.PathPrefix),
	})
	if err != nil {
		log.Printf("INFO: syfon_project_bucket_inventory_handler organization=%s project=%s path_prefix=%q duration_ms=%d error=%q", req.Organization, req.Project, req.PathPrefix, time.Since(started).Milliseconds(), err.Error())
		return handleInspectStorageError(c, err)
	}
	out := projectBucketInventoryResponseFromProjectStorage(result)
	objectCount := 0
	totalBytes := int64(0)
	bucket := ""
	prefix := ""
	if out.Summary != nil {
		objectCount = out.Summary.ObjectCount
		totalBytes = out.Summary.TotalBytes
		bucket = out.Summary.Bucket
		prefix = out.Summary.Prefix
	}
	log.Printf("INFO: syfon_project_bucket_inventory_handler organization=%s project=%s path_prefix=%q bucket=%s prefix=%q object_count=%d returned_items=%d total_bytes=%d duration_ms=%d", req.Organization, req.Project, req.PathPrefix, bucket, prefix, objectCount, len(out.Items), totalBytes, time.Since(started).Milliseconds())
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectRecords(c fiber.Ctx) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectProjectRecordsRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	organization := strings.TrimSpace(req.Organization)
	project := strings.TrimSpace(req.Project)
	if organization == "" || project == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	objects, err := s.inspector.AuditProjectRecords(c.Context(), organization, project, strings.Trim(strings.TrimSpace(req.PathPrefix), "/"))
	if err != nil {
		return middleware.HandleError(c, err)
	}
	out := internalInspectProjectRecordsResponse{Items: make([]internalInspectProjectRecordItem, 0, len(objects))}
	for _, obj := range objects {
		out.Items = append(out.Items, projectRecordAuditItemFromProjectStorage(obj))
	}
	return c.JSON(out)
}

func (s *internalServer) InternalInspectProjectScopes(c fiber.Ctx, _ internalapi.InternalInspectProjectScopesParams) error {
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalInspectProjectScopesRequest
	switch c.Method() {
	case fiber.MethodGet:
		req.Organization = c.Query("organization")
		req.Project = c.Query("project")
	default:
		if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
			return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
		}
	}
	organization := strings.TrimSpace(req.Organization)
	project := strings.TrimSpace(req.Project)
	if organization == "" || project == "" {
		return middleware.Reject(c, fiber.StatusBadRequest, "organization and project are required")
	}
	scopes, err := s.buckets.ListVisibleProjectScopes(c.Context(), organization, project)
	if err != nil {
		return middleware.HandleError(c, err)
	}
	out := internalInspectProjectScopesResponse{Items: make([]internalInspectProjectScopeItem, 0)}
	for _, scope := range scopes {
		row := internalInspectProjectScopeItem{
			Bucket:       scope.Bucket,
			Organization: scope.Organization,
			ProjectID:    scope.ProjectID,
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
	if middleware.MissingGen3AuthHeader(c.Context()) {
		return middleware.Reject(c, fiber.StatusUnauthorized, "Unauthorized")
	}
	var req internalDeleteProjectBucketObjectsRequest
	if err := maintenanceDecodeStrictJSON(c.Body(), &req); err != nil {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	if len(req.ObjectURLs) == 0 {
		return middleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: object_urls are required")
	}
	results := s.cleanup.DeleteProjectObjects(c.Context(), strings.TrimSpace(req.Organization), strings.TrimSpace(req.Project), req.ObjectURLs)
	out := internalDeleteProjectBucketObjectsResponse{
		Items: make([]internalDeleteProjectBucketObjectsItem, 0, len(results)),
	}
	for _, result := range results {
		out.Items = append(out.Items, internalDeleteProjectBucketObjectsItem{
			ObjectURL: result.ObjectURL,
			Status:    result.Status,
			Error:     result.Error,
		})
	}
	return c.JSON(out)
}

func handleInspectStorageError(c fiber.Ctx, err error) error {
	return middleware.HandleError(c, err)
}

func bulkInspectItemFromProjectStorage(result projectstorage.ProbeResult) internalInspectObjectBulkItem {
	out := internalInspectObjectBulkItem{
		ID:                   result.ID,
		ObjectURL:            result.ObjectURL,
		Provider:             result.Provider,
		Bucket:               result.Bucket,
		Key:                  result.Key,
		Path:                 result.Path,
		Exists:               result.Exists,
		Status:               string(result.Status),
		Error:                result.Error,
		ErrorKind:            result.ErrorKind,
		SizeBytes:            result.SizeBytes,
		MetaSHA256:           result.MetaSHA256,
		ETag:                 result.ETag,
		ValidationStatus:     string(result.ValidationStatus),
		SizeMatch:            result.SizeMatch,
		SHA256Match:          result.SHA256Match,
		ValidationMismatches: append([]string(nil), result.ValidationMismatches...),
	}
	if !result.LastModTime.IsZero() {
		out.LastModTime = result.LastModTime.Format(time.RFC3339)
	}
	return out
}

func bulkListInspectItemFromProjectStorage(result projectstorage.ListValidationResult) internalInspectObjectBulkItem {
	out := internalInspectObjectBulkItem{
		ID:                   result.ID,
		ObjectURL:            result.ObjectURL,
		Provider:             result.Provider,
		Bucket:               result.Bucket,
		Key:                  result.Key,
		Path:                 result.Path,
		Exists:               result.Exists,
		Status:               string(result.Status),
		Error:                result.Error,
		ErrorKind:            result.ErrorKind,
		SizeBytes:            result.SizeBytes,
		ETag:                 result.ETag,
		ValidationStatus:     string(result.ValidationStatus),
		SizeMatch:            result.SizeMatch,
		NameMatch:            result.NameMatch,
		ValidationMismatches: append([]string(nil), result.ValidationMismatches...),
	}
	if !result.LastModTime.IsZero() {
		out.LastModTime = result.LastModTime.Format(time.RFC3339)
	}
	return out
}

func projectBucketSummaryFromProjectStorage(summary projectstorage.Summary) *internalInspectProjectBucketSummary {
	out := &internalInspectProjectBucketSummary{
		Provider:          summary.Provider,
		Bucket:            summary.Bucket,
		Prefix:            summary.Prefix,
		ObjectURL:         summary.ObjectURL,
		Exists:            summary.Exists,
		ObjectCount:       summary.ObjectCount,
		TotalBytes:        summary.TotalBytes,
		Mode:              string(summary.Mode),
		InventoryComplete: summary.InventoryComplete,
		InventoryWarning:  summary.InventoryWarning,
	}
	if !summary.ComputedAt.IsZero() {
		out.ComputedAt = summary.ComputedAt.Format(time.RFC3339)
	}
	return out
}

func projectBucketInventoryResponseFromProjectStorage(result *projectstorage.InspectionResult) internalInspectProjectBucketResponse {
	if result == nil {
		return internalInspectProjectBucketResponse{Items: []internalInspectProjectBucketItem{}}
	}
	out := internalInspectProjectBucketResponse{
		Summary: projectBucketSummaryFromProjectStorage(result.Summary),
		Items:   make([]internalInspectProjectBucketItem, 0, len(result.Items)),
	}
	for _, item := range result.Items {
		row := internalInspectProjectBucketItem{
			ObjectURL:         item.ObjectURL,
			Provider:          item.Provider,
			Bucket:            item.Bucket,
			Key:               item.Key,
			Path:              item.Path,
			SizeBytes:         item.SizeBytes,
			MetaSHA256:        item.MetaSHA256,
			ETag:              item.ETag,
			InventoryComplete: result.Summary.InventoryComplete,
		}
		if !item.LastModTime.IsZero() {
			row.LastModTime = item.LastModTime.Format(time.RFC3339)
		}
		out.Items = append(out.Items, row)
	}
	return out
}

func projectRecordAuditItemFromProjectStorage(record projectstorage.ProjectRecordAudit) internalInspectProjectRecordItem {
	accessMethods := make([]internalProjectAccessMethod, 0, len(record.AccessMethods))
	for _, method := range record.AccessMethods {
		accessMethods = append(accessMethods, internalProjectAccessMethod{
			AccessID: method.AccessID,
			Type:     method.Type,
			URL:      method.URL,
			Headers:  append([]string(nil), method.Headers...),
		})
	}
	item := internalInspectProjectRecordItem{
		ObjectID:      record.ObjectID,
		Name:          record.Name,
		Checksum:      record.Checksum,
		Organization:  record.Organization,
		Project:       record.Project,
		Size:          record.Size,
		AccessURLs:    append([]string{}, record.AccessURLs...),
		AccessMethods: accessMethods,
	}
	if !record.CreatedTime.IsZero() {
		item.CreatedTime = record.CreatedTime.Format(time.RFC3339Nano)
	}
	if record.UpdatedTime != nil && !record.UpdatedTime.IsZero() {
		item.UpdatedTime = record.UpdatedTime.Format(time.RFC3339Nano)
	}
	return item
}
