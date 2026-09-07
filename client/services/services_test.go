package services

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	bucketapi "github.com/calypr/syfon/apigen/client/bucketapi"
	lfsapi "github.com/calypr/syfon/apigen/client/lfsapi"
	metricsapi "github.com/calypr/syfon/apigen/client/metricsapi"
	"github.com/calypr/syfon/client/request"
)

type fakeRequester struct {
	method       string
	path         string
	body         any
	err          error
	responseJSON []byte
	builder      request.RequestBuilder
}

func (f *fakeRequester) Do(ctx context.Context, method, path string, body, out any, opts ...request.RequestOption) error {
	f.method = method
	f.path = path
	f.body = body
	f.builder = request.RequestBuilder{Method: method, Url: path, Headers: map[string]string{}}
	for _, opt := range opts {
		opt(&f.builder)
	}
	if outResp, ok := out.(**http.Response); ok {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}
		*outResp = resp
		return f.err
	}
	if err := f.decodeInto(out); err != nil {
		return err
	}
	return f.err
}

func (f *fakeRequester) decodeInto(out any) error {
	if out == nil || len(f.responseJSON) == 0 {
		return nil
	}
	return json.Unmarshal(f.responseJSON, out)
}

type fakeBucketClient struct {
	listResp          *bucketapi.ListBucketsResp
	listErr           error
	putResp           *bucketapi.PutBucketResp
	putErr            error
	putReq            *bucketapi.PutBucketRequest
	deleteResp        *bucketapi.DeleteBucketResp
	deleteErr         error
	deleteBucket      string
	deleteScopeResp   *bucketapi.DeleteBucketScopeResp
	deleteScopeErr    error
	deleteScopeBucket string
	deleteScopeParams *bucketapi.DeleteBucketScopeParams
	deleteProjectResp *bucketapi.DeleteProjectDataResp
	deleteProjectErr  error
	deleteProjectOrg  string
	deleteProjectID   string
	addScopeResp      *bucketapi.AddBucketScopeResp
	addScopeErr       error
	addScopeReq       *bucketapi.AddBucketScopeRequest
	addScopePath      string
	listScopesResp    *bucketapi.ListBucketScopesResp
	listScopesErr     error
}

func (f *fakeBucketClient) ListBucketsWithResponse(ctx context.Context, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.ListBucketsResp, error) {
	return f.listResp, f.listErr
}

func (f *fakeBucketClient) PutBucketWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.PutBucketResp, error) {
	return nil, errors.New("unused")
}

func (f *fakeBucketClient) PutBucketWithResponse(ctx context.Context, body bucketapi.PutBucketJSONRequestBody, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.PutBucketResp, error) {
	copy := bucketapi.PutBucketRequest(body)
	f.putReq = &copy
	return f.putResp, f.putErr
}

func (f *fakeBucketClient) DeleteBucketWithResponse(ctx context.Context, bucket string, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.DeleteBucketResp, error) {
	f.deleteBucket = bucket
	return f.deleteResp, f.deleteErr
}

func (f *fakeBucketClient) DeleteBucketScopeWithResponse(ctx context.Context, bucket string, params *bucketapi.DeleteBucketScopeParams, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.DeleteBucketScopeResp, error) {
	f.deleteScopeBucket = bucket
	if params != nil {
		copy := *params
		f.deleteScopeParams = &copy
	}
	return f.deleteScopeResp, f.deleteScopeErr
}

func (f *fakeBucketClient) AddBucketScopeWithBodyWithResponse(ctx context.Context, bucket string, contentType string, body io.Reader, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.AddBucketScopeResp, error) {
	return nil, errors.New("unused")
}

func (f *fakeBucketClient) AddBucketScopeWithResponse(ctx context.Context, bucket string, body bucketapi.AddBucketScopeJSONRequestBody, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.AddBucketScopeResp, error) {
	copy := bucketapi.AddBucketScopeRequest(body)
	f.addScopeReq = &copy
	f.addScopePath = bucket
	return f.addScopeResp, f.addScopeErr
}

func (f *fakeBucketClient) ListBucketScopesWithResponse(ctx context.Context, bucket string, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.ListBucketScopesResp, error) {
	return f.listScopesResp, f.listScopesErr
}

func (f *fakeBucketClient) DeleteProjectDataWithResponse(ctx context.Context, organization string, projectId string, reqEditors ...bucketapi.RequestEditorFn) (*bucketapi.DeleteProjectDataResp, error) {
	f.deleteProjectOrg = organization
	f.deleteProjectID = projectId
	return f.deleteProjectResp, f.deleteProjectErr
}

type fakeMetricsClient struct {
	summaryResp   *metricsapi.GetMetricsSummaryResponse
	summaryErr    error
	summaryParams *metricsapi.GetMetricsSummaryParams
	filesResp     *metricsapi.ListMetricsFilesResponse
	filesErr      error
	filesParams   *metricsapi.ListMetricsFilesParams
	fileResp      *metricsapi.GetMetricsFileResponse
	fileErr       error
	fileObjectID  string
}

func (f *fakeMetricsClient) ListMetricsFilesWithResponse(ctx context.Context, params *metricsapi.ListMetricsFilesParams, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.ListMetricsFilesResponse, error) {
	f.filesParams = params
	return f.filesResp, f.filesErr
}

func (f *fakeMetricsClient) BulkMetricsFilesWithBodyWithResponse(ctx context.Context, params *metricsapi.BulkMetricsFilesParams, contentType string, body io.Reader, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.BulkMetricsFilesResponse, error) {
	return &metricsapi.BulkMetricsFilesResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotImplemented}}, nil
}

func (f *fakeMetricsClient) BulkMetricsFilesWithResponse(ctx context.Context, params *metricsapi.BulkMetricsFilesParams, body metricsapi.BulkMetricsFilesJSONRequestBody, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.BulkMetricsFilesResponse, error) {
	return &metricsapi.BulkMetricsFilesResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotImplemented}}, nil
}

func (f *fakeMetricsClient) GetMetricsFileWithResponse(ctx context.Context, objectId string, params *metricsapi.GetMetricsFileParams, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.GetMetricsFileResponse, error) {
	f.fileObjectID = objectId
	return f.fileResp, f.fileErr
}

func (f *fakeMetricsClient) GetMetricsSummaryWithResponse(ctx context.Context, params *metricsapi.GetMetricsSummaryParams, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.GetMetricsSummaryResponse, error) {
	f.summaryParams = params
	return f.summaryResp, f.summaryErr
}

func (f *fakeMetricsClient) RecordProviderTransferEventsWithBodyWithResponse(ctx context.Context, params *metricsapi.RecordProviderTransferEventsParams, contentType string, body io.Reader, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.RecordProviderTransferEventsResponse, error) {
	return &metricsapi.RecordProviderTransferEventsResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotImplemented}}, nil
}

func (f *fakeMetricsClient) RecordProviderTransferEventsWithResponse(ctx context.Context, params *metricsapi.RecordProviderTransferEventsParams, body metricsapi.RecordProviderTransferEventsJSONRequestBody, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.RecordProviderTransferEventsResponse, error) {
	return &metricsapi.RecordProviderTransferEventsResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotImplemented}}, nil
}

func (f *fakeMetricsClient) GetTransferBreakdownWithResponse(ctx context.Context, params *metricsapi.GetTransferBreakdownParams, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.GetTransferBreakdownResponse, error) {
	return &metricsapi.GetTransferBreakdownResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotImplemented}}, nil
}

func (f *fakeMetricsClient) GetTransferSummaryWithResponse(ctx context.Context, params *metricsapi.GetTransferSummaryParams, reqEditors ...metricsapi.RequestEditorFn) (*metricsapi.GetTransferSummaryResponse, error) {
	return &metricsapi.GetTransferSummaryResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotImplemented}}, nil
}

type fakeLFSClient struct {
	batchResp  *lfsapi.LfsBatchResponse
	batchErr   error
	batchReq   *lfsapi.LfsBatchApplicationVndGitLfsPlusJSONRequestBody
	stageResp  *lfsapi.LfsStageMetadataResponse
	stageErr   error
	stageReq   *lfsapi.LfsStageMetadataApplicationVndGitLfsPlusJSONRequestBody
	verifyResp *lfsapi.LfsVerifyResponse
	verifyErr  error
	verifyReq  *lfsapi.LfsVerifyApplicationVndGitLfsPlusJSONRequestBody
}

func (f *fakeLFSClient) LfsBatchWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsBatchResponse, error) {
	return nil, errors.New("unused")
}

func (f *fakeLFSClient) LfsBatchWithApplicationVndGitLfsPlusJSONBodyWithResponse(ctx context.Context, body lfsapi.LfsBatchApplicationVndGitLfsPlusJSONRequestBody, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsBatchResponse, error) {
	copy := body
	f.batchReq = &copy
	return f.batchResp, f.batchErr
}

func (f *fakeLFSClient) LfsStageMetadataWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsStageMetadataResponse, error) {
	return nil, errors.New("unused")
}

func (f *fakeLFSClient) LfsStageMetadataWithResponse(ctx context.Context, body lfsapi.LfsStageMetadataJSONRequestBody, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsStageMetadataResponse, error) {
	return nil, errors.New("unused")
}

func (f *fakeLFSClient) LfsStageMetadataWithApplicationVndGitLfsPlusJSONBodyWithResponse(ctx context.Context, body lfsapi.LfsStageMetadataApplicationVndGitLfsPlusJSONRequestBody, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsStageMetadataResponse, error) {
	copy := body
	f.stageReq = &copy
	return f.stageResp, f.stageErr
}

func (f *fakeLFSClient) LfsUploadProxyWithBodyWithResponse(ctx context.Context, oid string, contentType string, body io.Reader, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsUploadProxyResponse, error) {
	return nil, errors.New("unused")
}

func (f *fakeLFSClient) LfsVerifyWithBodyWithResponse(ctx context.Context, contentType string, body io.Reader, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsVerifyResponse, error) {
	return nil, errors.New("unused")
}

func (f *fakeLFSClient) LfsVerifyWithApplicationVndGitLfsPlusJSONBodyWithResponse(ctx context.Context, body lfsapi.LfsVerifyApplicationVndGitLfsPlusJSONRequestBody, reqEditors ...lfsapi.RequestEditorFn) (*lfsapi.LfsVerifyResponse, error) {
	copy := body
	f.verifyReq = &copy
	return f.verifyResp, f.verifyErr
}

func TestHealthServicePing(t *testing.T) {
	t.Parallel()

	fake := &fakeRequester{}
	service := NewHealthService(fake)
	if err := service.Ping(context.Background()); err != nil {
		t.Fatalf("Ping returned error: %v", err)
	}
	if fake.method != http.MethodGet || fake.path != "/healthz" {
		t.Fatalf("unexpected request: %s %s", fake.method, fake.path)
	}
}

func TestBucketsService(t *testing.T) {
	t.Parallel()

	t.Run("list success", func(t *testing.T) {
		fake := &fakeBucketClient{
			listResp: &bucketapi.ListBucketsResp{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200:      &bucketapi.BucketsResponse{S3BUCKETS: map[string]bucketapi.BucketMetadata{"bucket-a": {}}},
			},
		}
		got, err := NewBucketsService(fake).List(context.Background())
		if err != nil {
			t.Fatalf("List returned error: %v", err)
		}
		if len(got.S3BUCKETS) != 1 {
			t.Fatalf("unexpected bucket count: %+v", got)
		}
	})

	t.Run("list unexpected status", func(t *testing.T) {
		fake := &fakeBucketClient{listResp: &bucketapi.ListBucketsResp{HTTPResponse: &http.Response{StatusCode: http.StatusTeapot}}}
		_, err := NewBucketsService(fake).List(context.Background())
		if err == nil {
			t.Fatal("expected error when JSON200 is nil")
		}
	})

	t.Run("put delete and scope success", func(t *testing.T) {
		provider := "s3"
		region := "us-east-1"
		fake := &fakeBucketClient{
			putResp:         &bucketapi.PutBucketResp{HTTPResponse: &http.Response{StatusCode: http.StatusCreated}},
			deleteResp:      &bucketapi.DeleteBucketResp{HTTPResponse: &http.Response{StatusCode: http.StatusNoContent}},
			deleteScopeResp: &bucketapi.DeleteBucketScopeResp{HTTPResponse: &http.Response{StatusCode: http.StatusNoContent}},
			addScopeResp:    &bucketapi.AddBucketScopeResp{HTTPResponse: &http.Response{StatusCode: http.StatusCreated}},
		}
		service := NewBucketsService(fake)
		putReq := bucketapi.PutBucketRequest{Bucket: "bucket-a", Organization: "org", ProjectId: "proj", Provider: &provider, Region: &region}
		if err := service.Put(context.Background(), putReq); err != nil {
			t.Fatalf("Put returned error: %v", err)
		}
		if fake.putReq == nil || fake.putReq.Bucket != "bucket-a" || fake.putReq.Organization != "org" {
			t.Fatalf("unexpected put request: %+v", fake.putReq)
		}
		if err := service.Delete(context.Background(), "bucket-a"); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}
		if fake.deleteBucket != "bucket-a" {
			t.Fatalf("unexpected delete bucket: %q", fake.deleteBucket)
		}
		scopeReq := bucketapi.AddBucketScopeRequest{Organization: "org", ProjectId: "proj"}
		if err := service.AddScope(context.Background(), "bucket-a", scopeReq); err != nil {
			t.Fatalf("AddScope returned error: %v", err)
		}
		if fake.addScopeReq == nil || fake.addScopeReq.ProjectId != "proj" || fake.addScopePath != "bucket-a" {
			t.Fatalf("unexpected add scope request: req=%+v bucket=%q", fake.addScopeReq, fake.addScopePath)
		}
		if err := service.DeleteScope(context.Background(), "bucket-a", "org", "s3://bucket-a/path", "proj"); err != nil {
			t.Fatalf("DeleteScope returned error: %v", err)
		}
		if fake.deleteScopeBucket != "bucket-a" || fake.deleteScopeParams == nil || fake.deleteScopeParams.Organization != "org" || fake.deleteScopeParams.Path != "s3://bucket-a/path" || fake.deleteScopeParams.ProjectId == nil || *fake.deleteScopeParams.ProjectId != "proj" {
			t.Fatalf("unexpected delete scope request: bucket=%q params=%+v", fake.deleteScopeBucket, fake.deleteScopeParams)
		}
		fake.deleteProjectResp = &bucketapi.DeleteProjectDataResp{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK},
			JSON200: &bucketapi.DeleteProjectDataResponse{
				Organization:        "org",
				ProjectId:           "proj",
				DeletedObjects:      5,
				DeletedBucketScopes: 1,
			},
		}
		resp, err := service.DeleteProjectData(context.Background(), "org", "proj")
		if err != nil {
			t.Fatalf("DeleteProjectData returned error: %v", err)
		}
		if fake.deleteProjectOrg != "org" || fake.deleteProjectID != "proj" {
			t.Fatalf("unexpected delete project request: org=%q project=%q", fake.deleteProjectOrg, fake.deleteProjectID)
		}
		if resp.DeletedObjects != 5 || resp.DeletedBucketScopes != 1 {
			t.Fatalf("unexpected delete project response: %+v", resp)
		}
	})

	t.Run("put and delete failures", func(t *testing.T) {
		service := NewBucketsService(&fakeBucketClient{
			putResp:           &bucketapi.PutBucketResp{HTTPResponse: &http.Response{StatusCode: http.StatusBadRequest}},
			deleteResp:        &bucketapi.DeleteBucketResp{HTTPResponse: &http.Response{StatusCode: http.StatusBadGateway}},
			deleteScopeResp:   &bucketapi.DeleteBucketScopeResp{HTTPResponse: &http.Response{StatusCode: http.StatusBadGateway}},
			deleteProjectResp: &bucketapi.DeleteProjectDataResp{HTTPResponse: &http.Response{StatusCode: http.StatusBadGateway}},
		})
		if err := service.Put(context.Background(), bucketapi.PutBucketRequest{}); err == nil {
			t.Fatal("expected put failure")
		}
		if err := service.Delete(context.Background(), "bucket-a"); err == nil {
			t.Fatal("expected delete failure")
		}
		if err := service.DeleteScope(context.Background(), "bucket-a", "org", "s3://bucket-a/path", "proj"); err == nil {
			t.Fatal("expected delete scope failure")
		}
		if _, err := service.DeleteProjectData(context.Background(), "org", "proj"); err == nil {
			t.Fatal("expected delete project failure")
		}
	})
}

func TestMetricsService(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	objectID := "obj-1"
	inactive := int64(7)

	t.Run("summary success", func(t *testing.T) {
		fake := &fakeMetricsClient{
			summaryResp: &metricsapi.GetMetricsSummaryResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200:      &metricsapi.FileUsageSummary{InactiveFileCount: &inactive},
			},
		}
		got, err := NewMetricsService(fake).Summary(context.Background(), MetricsSummaryOptions{InactiveDays: 30})
		if err != nil {
			t.Fatalf("Summary returned error: %v", err)
		}
		if got.InactiveFileCount == nil || *got.InactiveFileCount != 7 {
			t.Fatalf("unexpected summary: %+v", got)
		}
		if fake.summaryParams == nil || fake.summaryParams.InactiveDays == nil || *fake.summaryParams.InactiveDays != 30 {
			t.Fatalf("unexpected summary params: %+v", fake.summaryParams)
		}
	})

	t.Run("files success and empty data", func(t *testing.T) {
		fake := &fakeMetricsClient{
			filesResp: &metricsapi.ListMetricsFilesResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &metricsapi.MetricsListResponse{
					Data: &[]metricsapi.FileUsage{{ObjectId: &objectID, LastAccessTime: &now}},
				},
			},
		}
		service := NewMetricsService(fake)
		files, err := service.Files(context.Background(), MetricsFilesOptions{Limit: 5, Offset: 2, InactiveDays: 10})
		if err != nil {
			t.Fatalf("Files returned error: %v", err)
		}
		if len(files) != 1 || files[0].ObjectId == nil || *files[0].ObjectId != objectID {
			t.Fatalf("unexpected files response: %+v", files)
		}
		if fake.filesParams == nil || fake.filesParams.Limit == nil || *fake.filesParams.Limit != 5 || fake.filesParams.Offset == nil || *fake.filesParams.Offset != 2 || fake.filesParams.InactiveDays == nil || *fake.filesParams.InactiveDays != 10 {
			t.Fatalf("unexpected files params: %+v", fake.filesParams)
		}

		fake.filesResp = &metricsapi.ListMetricsFilesResponse{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK},
			JSON200:      &metricsapi.MetricsListResponse{},
		}
		files, err = service.Files(context.Background(), MetricsFilesOptions{})
		if err != nil {
			t.Fatalf("Files with empty data returned error: %v", err)
		}
		if len(files) != 0 {
			t.Fatalf("expected empty file list, got %+v", files)
		}
	})

	t.Run("summary and file failures", func(t *testing.T) {
		service := NewMetricsService(&fakeMetricsClient{
			summaryResp: &metricsapi.GetMetricsSummaryResponse{HTTPResponse: &http.Response{StatusCode: http.StatusBadGateway}},
			fileResp:    &metricsapi.GetMetricsFileResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotFound}},
		})
		if _, err := service.Summary(context.Background(), MetricsSummaryOptions{}); err == nil {
			t.Fatal("expected summary failure")
		}
		if _, err := service.File(context.Background(), "obj-404"); err == nil {
			t.Fatal("expected file failure")
		}
	})

	t.Run("file success", func(t *testing.T) {
		fake := &fakeMetricsClient{
			fileResp: &metricsapi.GetMetricsFileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200:      &metricsapi.FileUsage{ObjectId: &objectID, LastAccessTime: &now},
			},
		}
		got, err := NewMetricsService(fake).File(context.Background(), objectID)
		if err != nil {
			t.Fatalf("File returned error: %v", err)
		}
		if got.ObjectId == nil || *got.ObjectId != objectID || fake.fileObjectID != objectID {
			t.Fatalf("unexpected file response: %+v / requested %q", got, fake.fileObjectID)
		}
	})

}

func TestLFSService(t *testing.T) {
	t.Parallel()

	t.Run("batch success", func(t *testing.T) {
		fake := &fakeLFSClient{
			batchResp: &lfsapi.LfsBatchResponse{
				HTTPResponse:                &http.Response{StatusCode: http.StatusOK},
				ApplicationvndGitLfsJSON200: &lfsapi.BatchResponse{Objects: []lfsapi.BatchResponseObject{{Oid: "oid-1", Size: 123}}},
			},
		}
		service := NewLFSService(fake)
		objects := []lfsapi.BatchRequestObject{{Oid: "oid-1", Size: 123}}
		resp, err := service.Batch(context.Background(), lfsapi.Upload, objects)
		if err != nil {
			t.Fatalf("Batch returned error: %v", err)
		}
		if resp == nil || len(resp.Objects) != 1 || fake.batchReq == nil || fake.batchReq.Operation != lfsapi.Upload {
			t.Fatalf("unexpected batch response/request: resp=%+v req=%+v", resp, fake.batchReq)
		}
	})

	t.Run("batch failure", func(t *testing.T) {
		service := NewLFSService(&fakeLFSClient{batchResp: &lfsapi.LfsBatchResponse{HTTPResponse: &http.Response{StatusCode: http.StatusBadRequest}}})
		if _, err := service.Batch(context.Background(), lfsapi.Download, nil); err == nil {
			t.Fatal("expected batch failure")
		}
	})

	t.Run("stage metadata success", func(t *testing.T) {
		staged := int32(2)
		ttl := int64(600)
		candidateID := "candidate-1"
		fake := &fakeLFSClient{
			stageResp: &lfsapi.LfsStageMetadataResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200:      &lfsapi.MetadataSubmitResponse{Staged: staged},
			},
		}
		service := NewLFSService(fake)
		count, err := service.StageMetadata(context.Background(), []lfsapi.DrsObjectCandidate{{Id: &candidateID}}, &ttl)
		if err != nil {
			t.Fatalf("StageMetadata returned error: %v", err)
		}
		if count != staged || fake.stageReq == nil || fake.stageReq.TtlSeconds == nil || *fake.stageReq.TtlSeconds != ttl {
			t.Fatalf("unexpected stage metadata result: count=%d req=%+v", count, fake.stageReq)
		}
	})

	t.Run("verify success and failure", func(t *testing.T) {
		service := NewLFSService(&fakeLFSClient{verifyResp: &lfsapi.LfsVerifyResponse{HTTPResponse: &http.Response{StatusCode: http.StatusOK}}})
		if err := service.Verify(context.Background(), "oid-1", 123); err != nil {
			t.Fatalf("Verify returned error: %v", err)
		}

		service = NewLFSService(&fakeLFSClient{verifyResp: &lfsapi.LfsVerifyResponse{HTTPResponse: &http.Response{StatusCode: http.StatusForbidden}}})
		if err := service.Verify(context.Background(), "oid-1", 123); err == nil {
			t.Fatal("expected verify failure")
		}
	})
}
