package lfs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	clienthash "github.com/calypr/syfon/client/hash"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/requestid"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
)

type LFSServer struct {
	opts    Options
	service *transferlfs.Service
}

func NewLFSServer(service *transferlfs.Service, opts Options) *LFSServer {
	return &LFSServer{opts: opts, service: service}
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

	responseObjects := make([]lfsapi.BatchResponseObject, len(req.Objects))
	valid := make([]transferlfs.BatchObject, 0, len(req.Objects))
	validIndexes := make([]int, 0, len(req.Objects))
	for index, input := range req.Objects {
		responseObjects[index] = lfsapi.BatchResponseObject{Oid: input.Oid, Size: input.Size}
		if input.Size < 0 {
			responseObjects[index].Size = 0
			responseObjects[index].Error = &lfsapi.ObjectError{Code: http.StatusBadRequest, Message: "size must be non-negative"}
			continue
		}
		oid := clienthash.NormalizeOid(input.Oid)
		if oid == "" {
			responseObjects[index].Error = &lfsapi.ObjectError{Code: http.StatusBadRequest, Message: "invalid oid"}
			continue
		}
		responseObjects[index].Oid = oid
		valid = append(valid, transferlfs.BatchObject{OID: oid, Size: input.Size})
		validIndexes = append(validIndexes, index)
	}
	batch, err := s.service.Batch(ctx, transferlfs.BatchRequest{Operation: string(req.Operation), Objects: valid})
	if err != nil {
		return lfsapi.LfsBatch500ApplicationVndGitLfsPlusJSONResponse{Message: lfsInternalError(ctx, "batch", http.StatusInternalServerError, err)}, nil
	}
	for index, item := range batch.Objects {
		responseIndex := validIndexes[index]
		responseObjects[responseIndex].Size = item.Size
		if item.Err != nil {
			responseObjects[responseIndex].Error = batchErrToObjectError(ctx, item.Err, req.Operation == "download")
			continue
		}
		if req.Operation == "download" {
			responseObjects[responseIndex].Actions = &lfsapi.BatchActions{Download: &lfsapi.Action{Href: item.DownloadURL}}
		} else if !item.Existing {
			oid := responseObjects[responseIndex].Oid
			responseObjects[responseIndex].Actions = &lfsapi.BatchActions{Upload: &lfsapi.Action{Href: GetBaseURL(ctx) + "/info/lfs/objects/" + oid}, Verify: &lfsapi.Action{Href: GetBaseURL(ctx) + "/info/lfs/verify"}}
		}
	}
	transfer := "basic"
	hashAlgorithm := "sha256"
	return lfsapi.LfsBatch200ApplicationVndGitLfsPlusJSONResponse{Transfer: &transfer, Objects: responseObjects, HashAlgo: &hashAlgorithm}, nil
}

func (s *LFSServer) LfsVerify(ctx context.Context, request lfsapi.LfsVerifyRequestObject) (lfsapi.LfsVerifyResponseObject, error) {
	if request.Body == nil {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "missing request body"}, nil
	}
	oid := clienthash.NormalizeOid(request.Body.Oid)
	if oid == "" {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "invalid oid"}, nil
	}
	if request.Body.Size < 0 {
		return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: "size must be non-negative"}, nil
	}
	if err := s.service.Verify(ctx, oid); err != nil {
		var candidateErr *transferlfs.MetadataCandidateError
		if errors.As(err, &candidateErr) {
			return lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse{Message: err.Error()}, nil
		}
		if errorapi.IsNotFoundError(err) {
			return lfsapi.LfsVerify404ApplicationVndGitLfsPlusJSONResponse{Message: "Object not found"}, nil
		}
		return lfsapi.LfsVerify500ApplicationVndGitLfsPlusJSONResponse{Message: lfsInternalError(ctx, "verify", http.StatusInternalServerError, err)}, nil
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
	for index, candidate := range input.Candidates {
		if candidate.Size != nil && *candidate.Size < 0 {
			return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] size must be non-negative", index)}, nil
		}
	}
	candidates := make([]objects.Candidate, 0, len(input.Candidates))
	for _, candidate := range input.Candidates {
		candidates = append(candidates, FromGeneratedCandidate(candidate))
	}
	if err := s.service.Stage(ctx, candidates); err != nil {
		var stageErr *transferlfs.MetadataStageError
		if errors.As(err, &stageErr) {
			if stageErr.MissingSHA {
				return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] missing canonical sha256", stageErr.Index)}, nil
			}
			return lfsapi.LfsStageMetadata400JSONResponse{Message: fmt.Sprintf("candidate[%d] invalid: %v", stageErr.Index, stageErr)}, nil
		}
		return lfsapi.LfsStageMetadata500JSONResponse{Message: lfsInternalError(ctx, "stage metadata", http.StatusInternalServerError, err)}, nil
	}
	return lfsapi.LfsStageMetadata200JSONResponse{Staged: int32(len(candidates))}, nil
}

func (s *LFSServer) LfsUploadProxy(ctx context.Context, request lfsapi.LfsUploadProxyRequestObject) (lfsapi.LfsUploadProxyResponseObject, error) {
	oid := clienthash.NormalizeOid(request.Oid)
	if oid == "" {
		return lfsapi.LfsUploadProxy400TextResponse("invalid oid"), nil
	}
	if err := s.service.UploadProxy(ctx, oid, request.Body); err != nil {
		if errors.Is(err, errorapi.ErrBucketNotConfigured) {
			return lfsapi.LfsUploadProxy507TextResponse(lfsInternalError(ctx, "upload", http.StatusInsufficientStorage, err)), nil
		}
		return lfsapi.LfsUploadProxy500TextResponse(lfsInternalError(ctx, "upload", http.StatusInternalServerError, err)), nil
	}
	return lfsapi.LfsUploadProxy200Response{}, nil
}

func batchErrToObjectError(ctx context.Context, err error, download bool) *lfsapi.ObjectError {
	if download {
		var lookupErr *transferlfs.DownloadLookupError
		if errors.As(err, &lookupErr) {
			err = lookupErr.Err
		}
	}
	if errors.Is(err, errorapi.ErrObjectLocationUnavailable) {
		return &lfsapi.ObjectError{Code: http.StatusNotFound, Message: "no object location available"}
	}
	if errors.Is(err, errorapi.ErrBucketNotConfigured) {
		return &lfsapi.ObjectError{Code: http.StatusInsufficientStorage, Message: lfsInternalError(ctx, "batch", http.StatusInsufficientStorage, err)}
	}
	if errorapi.IsNotFoundError(err) {
		return &lfsapi.ObjectError{Code: http.StatusNotFound, Message: "object not found"}
	}
	if errors.Is(err, errorapi.ErrAccessDenied) {
		return &lfsapi.ObjectError{Code: http.StatusForbidden, Message: "forbidden"}
	}
	return &lfsapi.ObjectError{Code: http.StatusInternalServerError, Message: lfsInternalError(ctx, "batch", http.StatusInternalServerError, err)}
}

func dbErrToBatchError(ctx context.Context, err error) *lfsapi.ObjectError {
	return batchErrToObjectError(ctx, err, false)
}
func downloadErrToBatchError(ctx context.Context, err error) *lfsapi.ObjectError {
	return batchErrToObjectError(ctx, err, true)
}

func lfsInternalError(ctx context.Context, operation string, status int, err error) string {
	slog.Error("lfs request failed", "request_id", requestid.GetRequestID(ctx), "operation", operation, "status", status, "err", err)
	return http.StatusText(status)
}
