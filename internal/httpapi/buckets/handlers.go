package buckets

import (
	"strings"

	"github.com/calypr/syfon/apigen/bucketapi"
	"github.com/calypr/syfon/apigen/errorapi"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/storage/address"
	"github.com/gofiber/fiber/v3"
)

func bucketPointer[T any](value T) *T {
	return &value
}

func bucketStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *bucketServer) ListBuckets(c fiber.Ctx) error {
	bucketService := s.bucketService
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	visible, err := bucketService.ListVisibleBuckets(c.Context())
	if err != nil {
		return apimiddleware.HandleError(c, err)
	}

	resp := bucketapi.BucketsResponse{S3BUCKETS: map[string]bucketapi.BucketMetadata{}}
	for _, entry := range visible {
		cred := entry.Credential
		meta := bucketapi.BucketMetadata{
			Bucket:      bucketPointer(cred.Bucket),
			EndpointUrl: bucketPointer(cred.Endpoint),
			Provider:    bucketPointer(cred.Provider),
			Region:      bucketPointer(cred.Region),
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
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	rawProvider := strings.TrimSpace(bucketStringValue(req.Provider))
	if _, err := address.ParseBucketProvider(rawProvider); err != nil {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "provider must be one of: s3, gcs, azure")
	}

	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Organization = strings.TrimSpace(req.Organization)
	req.ProjectId = strings.TrimSpace(req.ProjectId)
	if req.Bucket == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "bucket is required")
	}
	if req.Organization == "" && req.ProjectId != "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "organization is required when project_id is set")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
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
		return apimiddleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusCreated)
}

func (s *bucketServer) DeleteBucket(c fiber.Ctx, bucket string) error {
	credentialID := strings.TrimSpace(bucket)
	if credentialID == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "bucket name is required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	if err := s.bucketService.DeleteBucket(c.Context(), credentialID); err != nil {
		return apimiddleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *bucketServer) AddBucketScope(c fiber.Ctx, bucket string) error {
	routeCredentialID := strings.TrimSpace(bucket)
	if routeCredentialID == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "credential id is required")
	}
	var req bucketapi.AddBucketScopeRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.ProjectId = strings.TrimSpace(req.ProjectId)
	if req.Organization == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "organization is required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}

	if err := s.bucketService.CreateScopeForBucket(c.Context(), routeCredentialID, req.Organization, req.ProjectId, readOptionalPath(req.Path)); err != nil {
		return apimiddleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusCreated)
}

func (s *bucketServer) deleteBucketScopeRequest(c fiber.Ctx, bucket string, params bucketapi.DeleteBucketScopeParams) error {
	routeCredentialID := strings.TrimSpace(bucket)
	if routeCredentialID == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "credential id is required")
	}
	organization := strings.TrimSpace(params.Organization)
	scopePath := strings.TrimSpace(params.Path)
	projectID := ""
	if params.ProjectId != nil {
		projectID = strings.TrimSpace(*params.ProjectId)
	}
	if organization == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "organization and path are required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	if err := s.bucketService.DeleteScope(c.Context(), routeCredentialID, organization, projectID, scopePath); err != nil {
		return apimiddleware.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *bucketServer) ListBucketScopes(c fiber.Ctx, bucket string) error {
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return apimiddleware.HandleError(c, errorapi.ErrAuthenticationRequired)
	}
	routeCredentialID := strings.TrimSpace(bucket)
	if routeCredentialID == "" {
		return apimiddleware.Reject(c, fiber.StatusBadRequest, "credential id is required")
	}

	scopes, err := s.bucketService.ListVisibleScopes(c.Context(), routeCredentialID)
	if err != nil {
		return apimiddleware.HandleError(c, err)
	}

	result := make([]bucketapi.BucketScopeResponse, 0, len(scopes))
	for _, scope := range scopes {
		path := scope.Path
		result = append(result, bucketapi.BucketScopeResponse{Organization: scope.Organization, ProjectId: scope.ProjectID, Path: &path})
	}
	return c.JSON(result)
}
