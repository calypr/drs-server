package services

import (
	"context"

	"github.com/calypr/syfon/apigen/lfsapi"
)

type LFSService struct {
	gen lfsapi.ClientWithResponsesInterface
}

func NewLFSService(gen lfsapi.ClientWithResponsesInterface) *LFSService {
	return &LFSService{gen: gen}
}

// Batch executes a Git LFS Batch API request.
func (s *LFSService) Batch(ctx context.Context, op lfsapi.BatchRequestOperation, objects []lfsapi.BatchRequestObject) (*lfsapi.BatchResponse, error) {
	req := lfsapi.LfsBatchApplicationVndGitLfsPlusJSONRequestBody{
		Operation: op,
		Objects:   objects,
	}
	resp, err := s.gen.LfsBatchWithApplicationVndGitLfsPlusJSONBodyWithResponse(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.ApplicationvndGitLfsJSON200 == nil {
		return nil, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return resp.ApplicationvndGitLfsJSON200, nil
}

// StageMetadata stages object metadata for deferred registration.
func (s *LFSService) StageMetadata(ctx context.Context, candidates []lfsapi.DrsObjectCandidate, ttl *int64) (int32, error) {
	req := lfsapi.LfsStageMetadataApplicationVndGitLfsPlusJSONRequestBody{
		Candidates: candidates,
		TtlSeconds: ttl,
	}
	resp, err := s.gen.LfsStageMetadataWithApplicationVndGitLfsPlusJSONBodyWithResponse(ctx, req)
	if err != nil {
		return 0, err
	}
	if resp.JSON200 == nil {
		return 0, apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return resp.JSON200.Staged, nil
}

// Verify verifies an uploaded object via the LFS Verify API.
func (s *LFSService) Verify(ctx context.Context, oid string, size int64) error {
	req := lfsapi.LfsVerifyApplicationVndGitLfsPlusJSONRequestBody{
		Oid:  oid,
		Size: size,
	}
	resp, err := s.gen.LfsVerifyWithApplicationVndGitLfsPlusJSONBodyWithResponse(ctx, req)
	if err != nil {
		return err
	}
	if resp.StatusCode() != 200 {
		return apiResponseError(resp.HTTPResponse, resp.Body)
	}
	return nil
}
