package buckets

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/server/bucketapi"
	domainbuckets "github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/faults"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/httpapi/response"
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

func handleInternalBucketsFiber(c fiber.Ctx, bucketService *domainbuckets.Service) error {
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return response.HandleError(c, faults.ErrUnauthorized)
	}
	visible, err := bucketService.ListVisibleBuckets(c.Context())
	if err != nil {
		return response.HandleError(c, err)
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

func handleInternalPutBucketFiber(c fiber.Ctx, bucketService *domainbuckets.Service) error {
	var req bucketapi.PutBucketRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return response.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}

	rawProvider := strings.TrimSpace(bucketStringValue(req.Provider))
	bucketProvider, err := address.ParseBucketProvider(rawProvider)
	if err != nil {
		return response.Reject(c, fiber.StatusBadRequest, "provider must be one of: s3, gcs, azure")
	}
	req.Provider = bucketPointer(bucketProvider)

	req.Bucket = strings.TrimSpace(req.Bucket)
	req.Organization = strings.TrimSpace(req.Organization)
	req.ProjectId = strings.TrimSpace(req.ProjectId)
	region := strings.TrimSpace(bucketStringValue(req.Region))
	accessKey := strings.TrimSpace(bucketStringValue(req.AccessKey))
	secretKey := strings.TrimSpace(bucketStringValue(req.SecretKey))
	endpoint := strings.TrimSpace(bucketStringValue(req.Endpoint))
	if req.Bucket == "" {
		return response.Reject(c, fiber.StatusBadRequest, "bucket is required")
	}
	if req.Organization == "" && req.ProjectId != "" {
		return response.Reject(c, fiber.StatusBadRequest, "organization is required when project_id is set")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return response.HandleError(c, faults.ErrUnauthorized)
	}
	if err := domainbuckets.AuthorizeScopeWrite(c.Context(), req.Organization, req.ProjectId, "create", "update"); err != nil {
		return response.HandleError(c, err)
	}

	prefix, err := address.NormalizeStoragePath(readOptionalPath(req.Path), req.Bucket)
	if err != nil {
		return response.Reject(c, fiber.StatusBadRequest, err.Error())
	}

	credentialID := ""
	var existingCred *domainbuckets.Credential
	var credErr error
	existingCred, credErr = bucketService.GetS3Credential(c.Context(), req.Bucket)
	if credErr != nil && !isCredentialNotFoundError(credErr) {
		return response.HandleError(c, credErr)
	}
	if credErr == nil && existingCred != nil {
		credentialID = existingCred.CredentialID
	}
	if credentialID == "" {
		credentialID = domainbuckets.DeriveCredentialID(req.Bucket, bucketProvider, region, endpoint, accessKey)
	}
	if existingCred == nil {
		existingCred, credErr = bucketService.GetS3Credential(c.Context(), credentialID)
	}
	hasExistingCred := credErr == nil && existingCred != nil
	if hasExistingCred && rawProvider == "" {
		bucketProvider = existingCred.Provider
		req.Provider = bucketPointer(bucketProvider)
	}
	scopeOnly := hasExistingCred && accessKey == "" && secretKey == "" && endpoint == "" && region == "" && rawProvider == "" && req.Organization != ""

	if !hasExistingCred && bucketProvider == address.S3Provider && (accessKey == "" || secretKey == "") {
		return response.Reject(c, fiber.StatusBadRequest, "access_key and secret_key are required for new s3 credentials")
	}

	if req.Organization != "" {
		if err := bucketService.CreateBucketScope(c.Context(), &domainbuckets.Scope{
			Organization: req.Organization,
			ProjectID:    req.ProjectId,
			CredentialID: credentialID,
			Bucket:       req.Bucket,
			PathPrefix:   prefix,
		}); err != nil {
			return response.HandleError(c, err)
		}
	}
	if scopeOnly {
		return c.SendStatus(fiber.StatusCreated)
	}

	if hasExistingCred {
		if region == "" {
			region = existingCred.Region
		}
		if accessKey == "" {
			accessKey = existingCred.AccessKey
		}
		if secretKey == "" {
			secretKey = existingCred.SecretKey
		}
		if endpoint == "" {
			endpoint = existingCred.Endpoint
		}
	}
	if err := address.ValidateBucketNameWithEndpoint(bucketProvider, req.Bucket, endpoint); err != nil {
		return response.Reject(c, fiber.StatusBadRequest, err.Error())
	}

	cred := &domainbuckets.Credential{
		CredentialID: credentialID,
		Bucket:       req.Bucket,
		Provider:     bucketProvider,
		Region:       region,
		AccessKey:    accessKey,
		SecretKey:    secretKey,
		Endpoint:     endpoint,
	}
	if bucketProvider == address.S3Provider && (strings.TrimSpace(cred.AccessKey) == "" || strings.TrimSpace(cred.SecretKey) == "") {
		return response.Reject(c, fiber.StatusBadRequest, "access_key and secret_key are required for s3 credentials")
	}
	if err := bucketService.SaveS3Credential(c.Context(), cred); err != nil {
		return response.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusCreated)
}

func isCredentialNotFoundError(err error) bool {
	return err != nil && strings.EqualFold(strings.TrimSpace(err.Error()), "credential not found")
}

func authorizeBucketDelete(ctx context.Context, bucketService *domainbuckets.Service, bucket string) error {
	if apimiddleware.MissingGen3AuthHeader(ctx) {
		return faults.ErrUnauthorized
	}
	scopes, err := bucketService.ListBucketScopes(ctx)
	if err != nil {
		return err
	}
	if !domainbuckets.BucketsAllowedByNames(ctx, scopes, bucket, "delete", "update") {
		return faults.ErrUnauthorized
	}
	return nil
}

func handleInternalDeleteBucketFiber(c fiber.Ctx, bucketService *domainbuckets.Service) error {
	credentialID := strings.TrimSpace(c.Params("bucket"))
	if credentialID == "" {
		return response.Reject(c, fiber.StatusBadRequest, "bucket name is required")
	}
	if err := authorizeBucketDelete(c.Context(), bucketService, credentialID); err != nil {
		return response.HandleError(c, err)
	}
	if err := bucketService.DeleteS3Credential(c.Context(), credentialID); err != nil {
		return response.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func handleInternalCreateBucketScopeFiber(c fiber.Ctx, bucketService *domainbuckets.Service) error {
	routeCredentialID := strings.TrimSpace(c.Params("bucket"))
	if routeCredentialID == "" {
		return response.Reject(c, fiber.StatusBadRequest, "credential id is required")
	}
	cred, err := bucketService.GetS3Credential(c.Context(), routeCredentialID)
	if err != nil {
		return response.HandleError(c, err)
	}

	var req bucketapi.AddBucketScopeRequest
	if err := decodeStrictJSON(c.Body(), &req); err != nil {
		return response.Reject(c, fiber.StatusBadRequest, "Invalid request body: "+err.Error())
	}
	req.Organization = strings.TrimSpace(req.Organization)
	req.ProjectId = strings.TrimSpace(req.ProjectId)
	if req.Organization == "" {
		return response.Reject(c, fiber.StatusBadRequest, "organization is required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return response.HandleError(c, faults.ErrUnauthorized)
	}
	if err := domainbuckets.AuthorizeScopeWrite(c.Context(), req.Organization, req.ProjectId, "create", "update"); err != nil {
		return response.HandleError(c, err)
	}

	prefix, err := address.NormalizeStoragePath(readOptionalPath(req.Path), cred.Bucket)
	if err != nil {
		return response.Reject(c, fiber.StatusBadRequest, err.Error())
	}
	if err := bucketService.CreateBucketScope(c.Context(), &domainbuckets.Scope{
		Organization: req.Organization,
		ProjectID:    req.ProjectId,
		CredentialID: cred.CredentialID,
		Bucket:       cred.Bucket,
		PathPrefix:   prefix,
	}); err != nil {
		return response.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusCreated)
}

func handleInternalDeleteBucketScopeFiber(c fiber.Ctx, bucketService *domainbuckets.Service) error {
	routeCredentialID := strings.TrimSpace(c.Params("bucket"))
	if routeCredentialID == "" {
		return response.Reject(c, fiber.StatusBadRequest, "credential id is required")
	}
	hasPathQuery := c.Request().URI().QueryArgs().Has("path")
	organization := strings.TrimSpace(c.Query("organization"))
	scopePath := strings.TrimSpace(c.Query("path"))
	projectID := strings.TrimSpace(c.Query("project_id"))
	if organization == "" || !hasPathQuery {
		return response.Reject(c, fiber.StatusBadRequest, "organization and path are required")
	}
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return response.HandleError(c, faults.ErrUnauthorized)
	}
	if err := domainbuckets.AuthorizeScopeWrite(c.Context(), organization, projectID, "delete", "update"); err != nil {
		return response.HandleError(c, err)
	}
	pathPrefix := ""
	if scopePath != "" {
		cred, err := bucketService.GetS3Credential(c.Context(), routeCredentialID)
		if err != nil {
			return response.HandleError(c, err)
		}
		pathPrefix, err = address.NormalizeStoragePath(scopePath, cred.Bucket)
		if err != nil {
			return response.Reject(c, fiber.StatusBadRequest, err.Error())
		}
	}
	scopes, err := bucketService.ListBucketScopes(c.Context())
	if err != nil {
		return response.HandleError(c, err)
	}
	matchCount := 0
	for _, scope := range scopes {
		if !(strings.EqualFold(scope.Bucket, routeCredentialID) || strings.EqualFold(scope.CredentialID, routeCredentialID)) {
			continue
		}
		if strings.TrimSpace(scope.Organization) != organization {
			continue
		}
		if strings.TrimSpace(scope.ProjectID) != projectID {
			continue
		}
		if strings.Trim(strings.TrimSpace(scope.PathPrefix), "/") != pathPrefix {
			continue
		}
		matchCount++
	}
	if matchCount == 0 {
		return response.Reject(c, fiber.StatusNotFound, "bucket scope not found")
	}
	if matchCount > 1 {
		return response.Reject(c, fiber.StatusConflict, "bucket scope delete matched multiple rows")
	}
	if err := bucketService.DeleteBucketScope(c.Context(), organization, projectID, routeCredentialID, pathPrefix); err != nil {
		return response.HandleError(c, err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func handleInternalListBucketScopesFiber(c fiber.Ctx, bucketService *domainbuckets.Service) error {
	if apimiddleware.MissingGen3AuthHeader(c.Context()) {
		return response.HandleError(c, faults.ErrUnauthorized)
	}
	routeCredentialID := strings.TrimSpace(c.Params("bucket"))
	if routeCredentialID == "" {
		return response.Reject(c, fiber.StatusBadRequest, "credential id is required")
	}

	scopes, err := bucketService.ListBucketScopes(c.Context())
	if err != nil {
		return response.HandleError(c, err)
	}

	result := []bucketapi.BucketScopeResponse{}
	for _, scope := range scopes {
		if strings.EqualFold(scope.Bucket, routeCredentialID) || strings.EqualFold(scope.CredentialID, routeCredentialID) {
			if !domainbuckets.ScopeAllowed(c.Context(), scope, "read") {
				continue
			}
			scheme := "s3"
			if cred, err := bucketService.GetS3Credential(c.Context(), scope.CredentialID); err == nil && cred != nil {
				scheme = address.ProviderToScheme(cred.Provider)
			}
			path := fmt.Sprintf("%s://%s", scheme, scope.Bucket)
			if strings.TrimSpace(scope.PathPrefix) != "" {
				path = fmt.Sprintf("%s/%s", path, strings.Trim(strings.TrimSpace(scope.PathPrefix), "/"))
			}
			result = append(result, bucketapi.BucketScopeResponse{
				Organization: scope.Organization,
				ProjectId:    scope.ProjectID,
				Path:         &path,
			})
		}
	}
	return c.JSON(result)
}
