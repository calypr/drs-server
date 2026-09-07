package services

import (
	"context"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/bucketapi"
)

type BucketsService struct {
	gen bucketapi.ClientWithResponsesInterface
}

func NewBucketsService(gen bucketapi.ClientWithResponsesInterface) *BucketsService {
	return &BucketsService{gen: gen}
}

func (s *BucketsService) List(ctx context.Context) (bucketapi.BucketsResponse, error) {
	resp, err := s.gen.ListBucketsWithResponse(ctx)
	if err != nil {
		return bucketapi.BucketsResponse{}, err
	}
	if resp.JSON200 == nil {
		return bucketapi.BucketsResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *BucketsService) Put(ctx context.Context, req bucketapi.PutBucketRequest) error {
	resp, err := s.gen.PutBucketWithResponse(ctx, bucketapi.PutBucketJSONRequestBody(req))
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}

func (s *BucketsService) Delete(ctx context.Context, bucket string) error {
	resp, err := s.gen.DeleteBucketWithResponse(ctx, bucket)
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}

func (s *BucketsService) AddScope(ctx context.Context, bucket string, req bucketapi.AddBucketScopeRequest) error {
	resp, err := s.gen.AddBucketScopeWithResponse(ctx, bucket, bucketapi.AddBucketScopeJSONRequestBody(req))
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusCreated {
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}

func (s *BucketsService) DeleteScope(ctx context.Context, bucket, organization, path, projectID string) error {
	var projectIDParam *string
	if trimmed := strings.TrimSpace(projectID); trimmed != "" {
		projectIDParam = &trimmed
	}
	resp, err := s.gen.DeleteBucketScopeWithResponse(ctx, bucket, &bucketapi.DeleteBucketScopeParams{
		Organization: organization,
		Path:         path,
		ProjectId:    projectIDParam,
	})
	if err != nil {
		return err
	}
	if resp.StatusCode() != http.StatusOK && resp.StatusCode() != http.StatusNoContent {
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}

func (s *BucketsService) ListScopes(ctx context.Context, bucket string) ([]bucketapi.BucketScopeResponse, error) {
	resp, err := s.gen.ListBucketScopesWithResponse(ctx, bucket)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil {
		return nil, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}

func (s *BucketsService) DeleteProjectData(ctx context.Context, organization, projectID string) (bucketapi.DeleteProjectDataResponse, error) {
	resp, err := s.gen.DeleteProjectDataWithResponse(ctx, organization, projectID)
	if err != nil {
		return bucketapi.DeleteProjectDataResponse{}, err
	}
	if resp.JSON200 == nil {
		return bucketapi.DeleteProjectDataResponse{}, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return *resp.JSON200, nil
}
