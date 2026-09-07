package lfs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/server/lfsapi"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/faults"
	apimiddleware "github.com/calypr/syfon/internal/httpapi/middleware"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
)

type LFSServer struct {
	opts                Options
	uploadWorkflow      *transfers.LFSUploadWorkflow
	metadataWorkflow    *transfers.LFSMetadataWorkflow
	preparationWorkflow *transfers.LFSPreparationWorkflow
}

func NewLFSServer(deps Dependencies, opts Options) *LFSServer {
	partUploader := deps.PartUploader
	if partUploader == nil {
		partUploader = storage.UploadSignedMultipartPart
	}
	return &LFSServer{
		opts:                opts,
		uploadWorkflow:      transfers.NewLFSUploadWorkflow(deps.TransferService, transfers.LFSUploadPartUploader(partUploader), deps.FileCounters),
		metadataWorkflow:    transfers.NewLFSMetadataWorkflow(deps.TransferService, deps.ObjectService, deps.FileCounters),
		preparationWorkflow: transfers.NewLFSPreparationWorkflow(deps.TransferService, deps.ObjectService, deps.Credentials, deps.FileCounters),
	}
}

func (s *LFSServer) LfsBatch(ctx context.Context, request lfsapi.LfsBatchRequestObject) (lfsapi.LfsBatchResponseObject, error) {
	req := request.Body
	if req == nil {
		return lfsapi.LfsBatch500ApplicationVndGitLfsPlusJSONResponse{Message: "missing request body"}, nil
	}
	req.Operation = lfsapi.BatchRequestOperation(strings.ToLower(strings.TrimSpace(string(req.Operation))))
	if req.Operation != "download" && req.Operation != "upload" {
		return lfsapi.LfsBatch422ApplicationVndGitLfsPlusJSONResponse{Message: "operation must be 'download' or 'upload'"}, nil
	}
	if len(req.Objects) == 0 {
		return lfsapi.LfsBatch422ApplicationVndGitLfsPlusJSONResponse{Message: "objects cannot be empty"}, nil
	}
	if s.opts.MaxBatchObjects > 0 && len(req.Objects) > s.opts.MaxBatchObjects {
		return lfsapi.LfsBatch413ApplicationVndGitLfsPlusJSONResponse{Message: "batch contains too many objects"}, nil
	}

	transfer := "basic"
	responseObjects := make([]lfsapi.BatchResponseObject, 0, len(req.Objects))
	hashAlgorithm := "sha256"
	for _, input := range req.Objects {
		objectResponse := lfsapi.BatchResponseObject{Oid: input.Oid, Size: input.Size}
		oid := syfoncommon.NormalizeOid(input.Oid)
		if oid == "" {
			objectResponse.Error = &lfsapi.ObjectError{Code: int32(http.StatusBadRequest), Message: "invalid oid"}
			responseObjects = append(responseObjects, objectResponse)
			continue
		}
		objectResponse.Oid = oid
		if req.Operation == "download" {
			preparation, err := s.preparationWorkflow.PrepareDownload(ctx, oid)
			if err != nil {
				objectResponse.Error = downloadErrToBatchError(ctx, err)
			} else {
				objectResponse.Actions = &lfsapi.BatchActions{Download: &lfsapi.Action{Href: preparation.SignedURL}}
			}
		} else {
			preparation, err := s.preparationWorkflow.PrepareUpload(ctx, oid, input.Size)
			objectResponse.Size = preparation.Size
			if err != nil {
				objectResponse.Error = dbErrToBatchError(ctx, err)
			} else if !preparation.Existing {
				objectResponse.Actions = &lfsapi.BatchActions{
					Upload: &lfsapi.Action{Href: GetBaseURL(ctx) + "/info/lfs/objects/" + oid},
					Verify: &lfsapi.Action{Href: GetBaseURL(ctx) + "/info/lfs/verify"},
				}
			}
		}
		responseObjects = append(responseObjects, objectResponse)
	}

	return lfsapi.LfsBatch200ApplicationVndGitLfsPlusJSONResponse{
		Transfer: &transfer,
		Objects:  responseObjects,
		HashAlgo: &hashAlgorithm,
	}, nil
}

func (s *LFSServer) LfsVerify(ctx context.Context, request lfsapi.LfsVerifyRequestObject) (lfsapi.LfsVerifyResponseObject, error) {
	if request.Body == nil {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "missing request body"}, nil
	}
	oid := syfoncommon.NormalizeOid(request.Body.Oid)
	if oid == "" {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "invalid oid"}, nil
	}

	if err := s.metadataWorkflow.Verify(ctx, oid); err != nil {
		var candidateErr *transfers.LFSMetadataCandidateError
		if errors.As(err, &candidateErr) {
			return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: err.Error()}, nil
		}
		if faults.IsNotFoundError(err) {
			return lfsapi.LfsVerify404ApplicationVndGitLfsPlusJSONResponse{Message: "Object not found"}, nil
		}
		return lfsapi.LfsVerify500ApplicationVndGitLfsPlusJSONResponse{Message: err.Error()}, nil
	}
	return lfsapi.LfsVerify200Response{}, nil
}

func (s *LFSServer) LfsStageMetadata(ctx context.Context, request lfsapi.LfsStageMetadataRequestObject) (lfsapi.LfsStageMetadataResponseObject, error) {
	var input *lfsapi.MetadataSubmitRequest
	if request.JSONBody != nil {
		input = request.JSONBody
	} else if request.ApplicationVndGitLfsPlusJSONBody != nil {
		input = request.ApplicationVndGitLfsPlusJSONBody
	}
	if input == nil || len(input.Candidates) == 0 {
		return lfsapi.LfsStageMetadata400JSONResponse{Message: "candidates cannot be empty"}, nil
	}

	candidates := make([]objects.Candidate, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidates = append(candidates, FromGeneratedCandidate(candidate))
	}
	if err := s.metadataWorkflow.Stage(ctx, candidates); err != nil {
		var stageErr *transfers.LFSMetadataStageError
		if errors.As(err, &stageErr) {
			if stageErr.MissingSHA {
				return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] missing canonical sha256", stageErr.Index)}, nil
			}
			return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] invalid: %v", stageErr.Index, stageErr)}, nil
		}
		return lfsapi.LfsStageMetadata500JSONResponse{Message: err.Error()}, nil
	}
	return lfsapi.LfsStageMetadata200JSONResponse{Staged: int32(len(candidates))}, nil
}

func (s *LFSServer) LfsUploadProxy(ctx context.Context, request lfsapi.LfsUploadProxyRequestObject) (lfsapi.LfsUploadProxyResponseObject, error) {
	oid := syfoncommon.NormalizeOid(request.Oid)
	if oid == "" {
		return lfsapi.LfsUploadProxy400TextResponse("invalid oid"), nil
	}
	target, err := s.preparationWorkflow.ResolveUploadTarget(ctx, oid)
	if err != nil {
		if errors.Is(err, transfers.ErrLFSNoBucketConfigured) {
			return lfsapi.LfsUploadProxy507TextResponse(err.Error()), nil
		}
		return lfsapi.LfsUploadProxy500TextResponse(err.Error()), nil
	}
	if err := s.uploadWorkflow.Upload(ctx, request.Body, target.Bucket, target.Key, target.ObjectID); err != nil {
		return lfsapi.LfsUploadProxy500TextResponse(err.Error()), nil
	}
	return lfsapi.LfsUploadProxy200Response{}, nil
}

func dbErrToBatchError(ctx context.Context, err error) *lfsapi.ObjectError {
	if errors.Is(err, transfers.ErrLFSNoObjectLocation) {
		return &lfsapi.ObjectError{Code: 404, Message: "no object location available"}
	}
	if errors.Is(err, transfers.ErrLFSNoBucketConfigured) {
		return &lfsapi.ObjectError{Code: 507, Message: "no bucket configured"}
	}
	if faults.IsNotFoundError(err) {
		return &lfsapi.ObjectError{Code: 404, Message: "object not found"}
	}
	if err == faults.ErrUnauthorized {
		return &lfsapi.ObjectError{Code: int32(apimiddleware.AuthFailureStatus(ctx)), Message: "unauthorized"}
	}
	return &lfsapi.ObjectError{Code: 500, Message: err.Error()}
}

func downloadErrToBatchError(ctx context.Context, err error) *lfsapi.ObjectError {
	var lookupErr *transfers.LFSDownloadLookupError
	if errors.As(err, &lookupErr) {
		return dbErrToBatchError(ctx, lookupErr.Err)
	}
	if errors.Is(err, transfers.ErrLFSNoObjectLocation) {
		return dbErrToBatchError(ctx, err)
	}
	return &lfsapi.ObjectError{Code: 500, Message: err.Error()}
}
