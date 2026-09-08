package lfs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
)

func TestLFSBatchRejectsNegativeObjectSize(t *testing.T) {
	server := newLFSTestServerForNumericValidation()
	response, err := server.LfsBatch(context.Background(), lfsapi.LfsBatchRequestObject{
		Body: &lfsapi.LfsBatchApplicationVndGitLfsPlusJSONRequestBody{
			Operation: "upload",
			Objects:   []lfsapi.BatchRequestObject{{Oid: strings.Repeat("a", 64), Size: -1}},
		},
	})
	if err != nil {
		t.Fatalf("LfsBatch() error = %v", err)
	}
	batch, ok := response.(lfsapi.LfsBatch200ApplicationVndGitLfsPlusJSONResponse)
	if !ok || len(batch.Objects) != 1 {
		t.Fatalf("LfsBatch() response = %#v", response)
	}
	if batch.Objects[0].Size != 0 || batch.Objects[0].Error == nil || batch.Objects[0].Error.Code != 400 {
		t.Fatalf("negative size response = %+v", batch.Objects[0])
	}
}

func TestLFSVerifyRejectsNegativeSize(t *testing.T) {
	server := newLFSTestServerForNumericValidation()
	response, err := server.LfsVerify(context.Background(), lfsapi.LfsVerifyRequestObject{
		Body: &lfsapi.LfsVerifyApplicationVndGitLfsPlusJSONRequestBody{Oid: strings.Repeat("b", 64), Size: -1},
	})
	if err != nil {
		t.Fatalf("LfsVerify() error = %v", err)
	}
	if invalid, ok := response.(lfsapi.LfsVerify400ApplicationVndGitLfsPlusJSONResponse); !ok || invalid.Message != "size must be non-negative" {
		t.Fatalf("negative size response = %#v", response)
	}
}

func TestLFSStageMetadataRejectsNegativeCandidateSize(t *testing.T) {
	server := newLFSTestServerForNumericValidation()
	size := int64(-1)
	response, err := server.LfsStageMetadata(context.Background(), lfsapi.LfsStageMetadataRequestObject{
		JSONBody: &lfsapi.LfsStageMetadataJSONRequestBody{
			Candidates: []lfsapi.DrsObjectCandidate{{Size: &size}},
		},
	})
	if err != nil {
		t.Fatalf("LfsStageMetadata() error = %v", err)
	}
	if invalid, ok := response.(lfsapi.LfsStageMetadata400JSONResponse); !ok || invalid.Message != "candidate[0] size must be non-negative" {
		t.Fatalf("negative candidate size response = %#v", response)
	}
}

func TestLFSNegativeVerifySizeUsesHTTPErrorContract(t *testing.T) {
	ports := newLFSTestPorts(nil, nil)
	router := newLFSTestRouter(ports, &lfsTestStorage{}, DefaultOptions())
	body, _ := json.Marshal(map[string]any{"oid": strings.Repeat("c", 64), "size": -1})
	request := httptest.NewRequest(http.MethodPost, "/info/lfs/verify", strings.NewReader(string(body)))
	request.Header.Set("Accept", "application/vnd.git-lfs+json")
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response, err := router.app.Test(request)
	if err != nil {
		t.Fatalf("negative verify request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative verify status = %d, want 400", response.StatusCode)
	}
	if got := response.Header.Get("Content-Type"); got != "application/vnd.git-lfs+json" {
		t.Fatalf("negative verify media type = %q", got)
	}
}

func TestLFSZeroSizesRemainAccepted(t *testing.T) {
	oid := strings.Repeat("d", 64)
	t.Run("batch", func(t *testing.T) {
		ports := newLFSTestPorts(map[string]*objects.Record{
			oid: {
				Id:        objects.RecordID(oid),
				Checksums: []objects.Checksum{{Type: "sha256", Checksum: oid}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + oid},
				}},
			},
		}, map[string]buckets.Credential{"bucket": {Bucket: "bucket"}})
		server := NewLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), DefaultOptions())
		response, err := server.LfsBatch(context.Background(), lfsapi.LfsBatchRequestObject{
			Body: &lfsapi.LfsBatchApplicationVndGitLfsPlusJSONRequestBody{
				Operation: "download",
				Objects:   []lfsapi.BatchRequestObject{{Oid: oid, Size: 0}},
			},
		})
		if err != nil {
			t.Fatalf("LfsBatch() error = %v", err)
		}
		batch := response.(lfsapi.LfsBatch200ApplicationVndGitLfsPlusJSONResponse)
		if batch.Objects[0].Error != nil || batch.Objects[0].Size != 0 {
			t.Fatalf("zero batch size response = %+v", batch.Objects[0])
		}
	})

	t.Run("verify", func(t *testing.T) {
		ports := newLFSTestPorts(map[string]*objects.Record{oid: {Id: objects.RecordID(oid)}}, nil)
		server := NewLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), DefaultOptions())
		response, err := server.LfsVerify(context.Background(), lfsapi.LfsVerifyRequestObject{
			Body: &lfsapi.LfsVerifyApplicationVndGitLfsPlusJSONRequestBody{Oid: oid, Size: 0},
		})
		if err != nil {
			t.Fatalf("LfsVerify() error = %v", err)
		}
		if _, ok := response.(lfsapi.LfsVerify200Response); !ok {
			t.Fatalf("zero verify response = %#v", response)
		}
	})

	t.Run("stage metadata", func(t *testing.T) {
		size := int64(0)
		typ := "s3"
		url := "s3://bucket/" + oid
		ports := newLFSTestPorts(nil, map[string]buckets.Credential{"bucket": {Bucket: "bucket"}})
		server := NewLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), DefaultOptions())
		response, err := server.LfsStageMetadata(context.Background(), lfsapi.LfsStageMetadataRequestObject{
			JSONBody: &lfsapi.LfsStageMetadataJSONRequestBody{Candidates: []lfsapi.DrsObjectCandidate{{
				Size:      &size,
				Checksums: &[]lfsapi.Checksum{{Type: "sha256", Checksum: oid}},
				AccessMethods: &[]lfsapi.AccessMethod{{
					Type:      &typ,
					AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url},
				}},
			}}},
		})
		if err != nil {
			t.Fatalf("LfsStageMetadata() error = %v", err)
		}
		if staged, ok := response.(lfsapi.LfsStageMetadata200JSONResponse); !ok || staged.Staged != 1 {
			t.Fatalf("zero metadata size response = %#v", response)
		}
	})
}

func newLFSTestServerForNumericValidation() *LFSServer {
	ports := newLFSTestPorts(nil, nil)
	return NewLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), DefaultOptions())
}
