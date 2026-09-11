package copyproject

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/internalapi"
	"github.com/calypr/syfon/client/logs"
	"github.com/calypr/syfon/client/services"
	"github.com/spf13/cobra"
)

type copyInternalAPI struct {
	internalapi.ClientWithResponsesInterface
	downloadURLs map[string]string
	uploadURL    string
	getStatus    int
	created      *internalapi.InternalRecord
}

func (f *copyInternalAPI) InternalDownloadWithResponse(_ context.Context, fileID string, _ *internalapi.InternalDownloadParams, _ ...internalapi.RequestEditorFn) (*internalapi.InternalDownloadResponse, error) {
	value := f.downloadURLs[fileID]
	return &internalapi.InternalDownloadResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK},
		JSON200:      &internalapi.InternalSignedURL{Url: &value},
	}, nil
}

func (f *copyInternalAPI) InternalUploadURLWithResponse(_ context.Context, _ string, _ *internalapi.InternalUploadURLParams, _ ...internalapi.RequestEditorFn) (*internalapi.InternalUploadURLResponse, error) {
	value := f.uploadURL
	return &internalapi.InternalUploadURLResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK},
		JSON200:      &internalapi.InternalSignedURL{Url: &value},
	}, nil
}

func (f *copyInternalAPI) InternalGetWithResponse(_ context.Context, _ string, _ ...internalapi.RequestEditorFn) (*internalapi.InternalGetResponse, error) {
	status := f.getStatus
	if status == 0 {
		status = http.StatusNotFound
	}
	return &internalapi.InternalGetResponse{HTTPResponse: &http.Response{StatusCode: status}}, nil
}

func (f *copyInternalAPI) InternalCreateWithResponse(_ context.Context, body internalapi.InternalCreateJSONRequestBody, _ ...internalapi.RequestEditorFn) (*internalapi.InternalCreateResponse, error) {
	created := internalapi.InternalRecord(body)
	f.created = &created
	response := internalapi.InternalRecordResponse{Did: created.Did}
	return &internalapi.InternalCreateResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusCreated},
		JSON201:      &response,
	}, nil
}

type copyDRSAPI struct {
	drsapi.ClientWithResponsesInterface
	objects    map[string]drsapi.DrsObject
	getStatus  map[string]int
	registered *drsapi.RegisterObjectsJSONRequestBody
	updated    *drsapi.UpdateObjectAccessMethodsJSONRequestBody
}

func (f *copyDRSAPI) GetObjectWithResponse(_ context.Context, objectID drsapi.ObjectId, _ *drsapi.GetObjectParams, _ ...drsapi.RequestEditorFn) (*drsapi.GetObjectResponse, error) {
	id := string(objectID)
	status := f.getStatus[id]
	if status == 0 {
		status = http.StatusNotFound
	}
	response := &drsapi.GetObjectResponse{HTTPResponse: &http.Response{StatusCode: status}}
	if object, ok := f.objects[id]; ok {
		response.JSON200 = &object
		response.HTTPResponse.StatusCode = http.StatusOK
	}
	return response, nil
}

func (f *copyDRSAPI) RegisterObjectsWithResponse(_ context.Context, body drsapi.RegisterObjectsJSONRequestBody, _ ...drsapi.RequestEditorFn) (*drsapi.RegisterObjectsResponse, error) {
	request := drsapi.RegisterObjectsJSONRequestBody(body)
	f.registered = &request
	return &drsapi.RegisterObjectsResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusCreated},
		JSON201:      &drsapi.N201ObjectsCreated{},
	}, nil
}

func (f *copyDRSAPI) UpdateObjectAccessMethodsWithResponse(_ context.Context, objectID string, body drsapi.UpdateObjectAccessMethodsJSONRequestBody, _ ...drsapi.RequestEditorFn) (*drsapi.UpdateObjectAccessMethodsResponse, error) {
	request := drsapi.UpdateObjectAccessMethodsJSONRequestBody(body)
	f.updated = &request
	object := drsapi.DrsObject{Id: objectID, AccessMethods: &request.AccessMethods}
	return &drsapi.UpdateObjectAccessMethodsResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK},
		JSON200:      &object,
	}, nil
}

type copyRecordClient struct {
	data  *services.DataService
	index *services.IndexService
	drs   *services.DRSService
}

func (f *copyRecordClient) Health() *services.HealthService   { return nil }
func (f *copyRecordClient) Data() *services.DataService       { return f.data }
func (f *copyRecordClient) Index() *services.IndexService     { return f.index }
func (f *copyRecordClient) DRS() *services.DRSService         { return f.drs }
func (f *copyRecordClient) Buckets() *services.BucketsService { return nil }
func (f *copyRecordClient) Metrics() *services.MetricsService { return nil }
func (f *copyRecordClient) LFS() *services.LFSService         { return nil }

func TestCopyRecordCopiesBytesAndPublishesMetadata(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.txt")
	targetPath := filepath.Join(t.TempDir(), "nested", "target.bin")
	payload := []byte("record payload")
	if err := os.WriteFile(sourcePath, payload, 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	sourceURL := fileURL(t, sourcePath)
	targetURL := fileURL(t, targetPath)
	sourceInternal := &copyInternalAPI{downloadURLs: map[string]string{"did-copy": sourceURL}}
	targetInternal := &copyInternalAPI{uploadURL: targetURL}
	accessMethods := []drsapi.AccessMethod{{Type: drsapi.AccessMethodType("s3"), AccessUrl: &drsapi.AccessURL{Url: "s3://source/object"}}}
	size := int64(len(payload))
	checksum := "sha256-value"
	sourceDRS := &copyDRSAPI{objects: map[string]drsapi.DrsObject{"did-copy": {Id: "did-copy", Size: size, AccessMethods: &accessMethods}}}
	targetDRS := &copyDRSAPI{}
	logger := logs.NewGen3Logger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	sourceData := services.NewDataService(sourceInternal, nil, logger, services.NewDRSService(sourceDRS))
	targetData := services.NewDataService(targetInternal, nil, logger, services.NewDRSService(targetDRS))
	sourceClient := &copyRecordClient{data: sourceData, drs: services.NewDRSService(sourceDRS)}
	targetClient := &copyRecordClient{data: targetData, index: services.NewIndexService(targetInternal), drs: services.NewDRSService(targetDRS)}

	rec := internalapi.InternalRecord{
		Did:           "did-copy",
		Name:          stringPtr("payload.txt"),
		Size:          &size,
		Hashes:        &internalapi.HashInfo{"sha256": checksum},
		AccessMethods: &accessMethods,
	}
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	if err := copyRecord(context.Background(), cmd, sourceClient, targetClient, rec, "target-bucket", "s3://target-bucket/organizations/target-org/projects/target-project", "/organization/target-org/project/target-project", 1, 1, t.TempDir()); err != nil {
		t.Fatalf("copyRecord returned error: %v", err)
	}
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("target bytes = %q; want %q", got, payload)
	}
	for _, want := range []string{"[1/1] Copying did-copy", "Downloading did-copy", "Uploading did-copy", "s3://target-bucket/organizations/target-org/projects/target-project/sha256-value"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("copy output %q does not contain %q", output.String(), want)
		}
	}
	if targetDRS.registered == nil || len(targetDRS.registered.Candidates) != 1 || targetDRS.registered.Candidates[0].Name == nil || *targetDRS.registered.Candidates[0].Name != "payload.txt" {
		t.Fatalf("registered DRS request = %+v", targetDRS.registered)
	}
	if targetDRS.updated == nil || len(targetDRS.updated.AccessMethods) != 1 || targetDRS.updated.AccessMethods[0].AccessUrl == nil || targetDRS.updated.AccessMethods[0].AccessUrl.Url != "s3://target-bucket/organizations/target-org/projects/target-project/sha256-value" {
		t.Fatalf("updated DRS request = %+v", targetDRS.updated)
	}
	if targetInternal.created == nil || targetInternal.created.Did != "did-copy" || targetInternal.created.ControlledAccess == nil || len(*targetInternal.created.ControlledAccess) != 1 || (*targetInternal.created.ControlledAccess)[0] != "/organization/target-org/project/target-project" {
		t.Fatalf("created index record = %+v", targetInternal.created)
	}
}

func TestCopyRecordReturnsDownloadErrorForCallerToSkip(t *testing.T) {
	missingTempDir := filepath.Join(t.TempDir(), "does-not-exist")
	cmd := &cobra.Command{}
	var output bytes.Buffer
	cmd.SetOut(&output)
	err := copyRecord(context.Background(), cmd, nil, nil, internalapi.InternalRecord{Did: "did-skip"}, "bucket", "", "/organization/org/project/project", 2, 3, missingTempDir)
	if err == nil || !strings.Contains(err.Error(), "failed to create temp file") {
		t.Fatalf("copyRecord error = %v; want temp-file error", err)
	}
	if !strings.Contains(output.String(), "[2/3] Copying did-skip (size: 0, name: )") {
		t.Fatalf("copy output = %q", output.String())
	}
}

func fileURL(t *testing.T, filePath string) string {
	t.Helper()
	parsed := &url.URL{Scheme: "file", Path: filePath}
	return parsed.String()
}
