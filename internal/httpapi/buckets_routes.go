package httpapi

import (
	"strings"

	"github.com/calypr/syfon/apigen/bucketapi"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/access"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	projectstorage "github.com/calypr/syfon/internal/projects/storage"
	"github.com/gofiber/fiber/v3"
)

type bucketServer struct {
	bucketService  *domainbuckets.Service
	projectStorage *projectstorage.Service
}

func (s *bucketServer) DeleteBucketScope(c fiber.Ctx, bucket string, params bucketapi.DeleteBucketScopeParams) error {
	routeCredentialID := strings.TrimSpace(bucket)
	if routeCredentialID == "" {
		return Reject(c, fiber.StatusBadRequest, "credential id is required")
	}
	organization := strings.TrimSpace(params.Organization)
	scopePath := strings.TrimSpace(params.Path)
	projectID := ""
	if params.ProjectId != nil {
		projectID = strings.TrimSpace(*params.ProjectId)
	}
	if organization == "" {
		return Reject(c, fiber.StatusBadRequest, "organization and path are required")
	}
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	if err := s.bucketService.DeleteScope(c.Context(), routeCredentialID, organization, projectID, scopePath); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *bucketServer) DeleteProjectData(c fiber.Ctx, organization, projectID string) error {
	if s.projectStorage == nil {
		return HandleError(c, errorapi.Define(errorapi.ErrorCodeStorageUnavailable, errorapi.ErrorCategoryUnavailable, "project storage service is not configured"))
	}
	organization = strings.TrimSpace(organization)
	projectID = strings.TrimSpace(projectID)
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
	return c.JSON(bucketapi.DeleteProjectDataResponse{
		Organization:        result.Organization,
		ProjectId:           result.ProjectID,
		DeletedObjects:      result.DeletedObjects,
		DeletedBucketScopes: result.DeletedBucketScopes,
	})
}

func (s *bucketServer) ListBuckets(c fiber.Ctx) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	visible, err := s.bucketService.ListVisibleBuckets(c.Context())
	if err != nil {
		return HandleError(c, err)
	}

	resp := bucketapi.BucketsResponse{S3BUCKETS: map[string]bucketapi.BucketMetadata{}}
	for _, entry := range visible {
		cred := entry.Credential
		meta := bucketapi.BucketMetadata{
			Bucket:      valuePointer(cred.Bucket),
			EndpointUrl: valuePointer(cred.Endpoint),
			Provider:    valuePointer(cred.Provider),
			Region:      valuePointer(cred.Region),
		}
		if len(entry.Programs) > 0 {
			programs := append([]string(nil), entry.Programs...)
			meta.Programs = &programs
		}
		resp.S3BUCKETS[cred.Bucket] = meta
	}
	return c.JSON(resp)
}

func (s *bucketServer) PutBucket(c fiber.Ctx) error {
	var req bucketapi.PutBucketRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Organization = strings.TrimSpace(req.Organization)
	req.ProjectId = strings.TrimSpace(req.ProjectId)
	if req.Bucket == "" {
		return Reject(c, fiber.StatusBadRequest, "bucket is required")
	}
	if req.Organization == "" && req.ProjectId != "" {
		return Reject(c, fiber.StatusBadRequest, "organization is required when project_id is set")
	}
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	if err := s.bucketService.Put(c.Context(), domainbuckets.PutRequest{
		Bucket:       req.Bucket,
		Organization: req.Organization,
		ProjectID:    req.ProjectId,
		Provider:     req.Provider,
		Region:       req.Region,
		AccessKey:    req.AccessKey,
		SecretKey:    req.SecretKey,
		Endpoint:     req.Endpoint,
		Path:         req.Path,
	}); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusCreated)
}

func (s *bucketServer) DeleteBucket(c fiber.Ctx, bucket string) error {
	credentialID := strings.TrimSpace(bucket)
	if credentialID == "" {
		return Reject(c, fiber.StatusBadRequest, "bucket name is required")
	}
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	if err := s.bucketService.DeleteBucket(c.Context(), credentialID); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *bucketServer) AddBucketScope(c fiber.Ctx, bucket string) error {
	routeCredentialID := strings.TrimSpace(bucket)
	if routeCredentialID == "" {
		return Reject(c, fiber.StatusBadRequest, "credential id is required")
	}
	var req bucketapi.AddBucketScopeRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.ProjectId = strings.TrimSpace(req.ProjectId)
	if req.Organization == "" {
		return Reject(c, fiber.StatusBadRequest, "organization is required")
	}
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}

	path := ""
	if req.Path != nil {
		path = strings.TrimSpace(*req.Path)
	}
	if err := s.bucketService.CreateScopeForBucket(c.Context(), routeCredentialID, req.Organization, req.ProjectId, path); err != nil {
		return HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusCreated)
}

func (s *bucketServer) ListBucketScopes(c fiber.Ctx, bucket string) error {
	if access.MissingGen3AuthHeader(c.Context()) {
		return HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	routeCredentialID := strings.TrimSpace(bucket)
	if routeCredentialID == "" {
		return Reject(c, fiber.StatusBadRequest, "credential id is required")
	}

	scopes, err := s.bucketService.ListVisibleScopes(c.Context(), routeCredentialID)
	if err != nil {
		return HandleError(c, err)
	}

	result := make([]bucketapi.BucketScopeResponse, 0, len(scopes))
	for _, scope := range scopes {
		path := scope.Path
		result = append(result, bucketapi.BucketScopeResponse{Organization: scope.Organization, ProjectId: scope.ProjectID, Path: &path})
	}
	return c.JSON(result)
}

func registerBucketRoutes(router fiber.Router, bucketService *domainbuckets.Service, projectStorage *projectstorage.Service) {
	bucketapi.RegisterHandlers(router, &bucketServer{
		bucketService:  bucketService,
		projectStorage: projectStorage,
	})
}
