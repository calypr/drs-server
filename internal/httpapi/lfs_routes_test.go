package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/apigen/lfsapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

func TestWriteLFSErrorPreservesLFSContentType(t *testing.T) {
	app := fiber.New()
	app.Get("/error", func(c fiber.Ctx) error {
		return writeLFSError(c, http.StatusTooManyRequests, "rate limit exceeded", false)
	})

	response, err := app.Test(httptest.NewRequest(http.MethodGet, "/error", nil))
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusTooManyRequests)
	}
	if got := response.Header.Get("Content-Type"); got != "application/vnd.git-lfs+json" {
		t.Fatalf("Content-Type = %q, want application/vnd.git-lfs+json", got)
	}
}

func TestLFSBatchDownloadUsesTransferAndUsagePorts(t *testing.T) {
	oid := strings.Repeat("a", 64)
	ports := newLFSTestPorts(
		map[string]*objects.Record{
			oid: {
				Id:        objects.RecordID(oid),
				Size:      10,
				Checksums: []objects.Checksum{{Type: "sha256", Checksum: oid}},
				AccessMethods: &[]objects.AccessMethod{{
					Type:      "s3",
					AccessUrl: &objects.AccessURL{Url: "s3://bucket/" + oid},
				}},
			},
		},
		map[string]buckets.Credential{"bucket": {Bucket: "bucket"}},
	)
	storageFake := &lfsTestStorage{}
	router := newLFSTestRouter(ports, storageFake, defaultLFSOptions())
	body, _ := json.Marshal(map[string]any{
		"operation": "download",
		"objects":   []map[string]any{{"oid": oid, "size": 10}},
	})
	request := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/batch", bytes.NewReader(body))
	request.Header.Set("Accept", "application/vnd.git-lfs+json")
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("batch status = %d body=%s", response.Code, response.Body.String())
	}
	var payload lfsapi.BatchResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if len(payload.Objects) != 1 || payload.Objects[0].Actions == nil || payload.Objects[0].Actions.Download == nil {
		t.Fatalf("download actions = %+v", payload.Objects)
	}
	if len(ports.downloads) != 1 || ports.downloads[0] != oid {
		t.Fatalf("download counters = %v", ports.downloads)
	}
	if len(ports.transferEvents) != 1 || ports.transferEvents[0].EventType != usage.TransferEventAccessIssued {
		t.Fatalf("transfer events = %+v", ports.transferEvents)
	}
}

func TestLFSMetadataVerifyPreservesPendingPopBeforeRegister(t *testing.T) {
	oid := strings.Repeat("b", 64)
	ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{"bucket": {Bucket: "bucket"}})
	router := newLFSTestRouter(ports, &lfsTestStorage{}, defaultLFSOptions())
	metadata, _ := json.Marshal(map[string]any{"candidates": []map[string]any{{
		"name": "object.bin", "size": 12,
		"checksums":      []map[string]any{{"type": "sha256", "checksum": oid}},
		"access_methods": []map[string]any{{"type": "s3", "access_url": map[string]any{"url": "s3://bucket/" + oid}}},
	}}})
	request := httptest.NewRequest(http.MethodPost, "/info/lfs/objects/metadata", bytes.NewReader(metadata))
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metadata status = %d body=%s", response.Code, response.Body.String())
	}
	entry, ok := ports.pending[oid]
	if !ok || entry.CreatedAt.IsZero() || entry.ExpiresAt.Sub(entry.CreatedAt) != transferlfs.PendingMetadataTTL {
		t.Fatalf("pending metadata timestamps = %+v", entry)
	}
	verify, _ := json.Marshal(map[string]any{"oid": oid, "size": 12})
	request = httptest.NewRequest(http.MethodPost, "/info/lfs/verify", bytes.NewReader(verify))
	request.Header.Set("Accept", "application/vnd.git-lfs+json")
	request.Header.Set("Content-Type", "application/vnd.git-lfs+json")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("verify status = %d body=%s", response.Code, response.Body.String())
	}
	if _, ok := ports.pending[oid]; ok {
		t.Fatal("pending metadata was not consumed")
	}
}

func TestLFSUploadProxyPreservesOpaqueMultipartAndPartOrder(t *testing.T) {
	oid := strings.Repeat("c", 64)
	ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{"bucket": {Bucket: "bucket"}})
	storageFake := &lfsTestStorage{}
	deps := newLFSTestDependencies(ports, storageFake)
	storageFake.uploadPart = func(content []byte) (string, error) {
		if string(content) != "payload" {
			t.Fatalf("multipart content = %q", content)
		}
		return "etag", nil
	}
	server := newLFSServer(deps, defaultLFSOptions())
	response, err := server.LfsUploadProxy(context.Background(), lfsapi.LfsUploadProxyRequestObject{
		Oid:  oid,
		Body: bytes.NewReader([]byte("payload")),
	})
	if err != nil {
		t.Fatalf("upload proxy error: %v", err)
	}
	if _, ok := response.(lfsapi.LfsUploadProxy200Response); !ok {
		t.Fatalf("upload proxy response = %T, want 200", response)
	}
	want := storage.Target{Provider: "s3", LookupKey: "bucket", PhysicalBucket: "bucket", Key: oid, CanonicalURL: "s3://bucket/" + oid, LookupCandidates: []string{"bucket"}}
	if !reflect.DeepEqual(storageFake.initTarget, want) {
		t.Fatalf("multipart init target = %+v", storageFake.initTarget)
	}
	if storageFake.partRequest.UploadID != "opaque-upload-id" || storageFake.partRequest.PartNumber != 1 {
		t.Fatalf("multipart part request = %+v", storageFake.partRequest)
	}
	if storageFake.complete.UploadID != "opaque-upload-id" || len(storageFake.complete.Parts) != 1 || storageFake.complete.Parts[0].ETag != "etag" {
		t.Fatalf("multipart completion = %+v", storageFake.complete)
	}
}

func TestLFSTopLevelInternalErrorsDoNotExposeDetails(t *testing.T) {
	const detail = "postgres://user:secret@database"
	oid := strings.Repeat("e", 64)
	ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{})
	ports.getErr = errors.New(detail)
	server := newLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), defaultLFSOptions())

	response, err := server.LfsVerify(context.Background(), lfsapi.LfsVerifyRequestObject{
		Body: &lfsapi.LfsVerifyApplicationVndGitLfsPlusJSONRequestBody{Oid: oid, Size: 1},
	})
	if err != nil {
		t.Fatalf("verify returned error: %v", err)
	}
	internal, ok := response.(lfsapi.LfsVerify500ApplicationVndGitLfsPlusJSONResponse)
	if !ok || internal.Message != http.StatusText(http.StatusInternalServerError) || strings.Contains(internal.Message, detail) {
		t.Fatalf("unexpected verify response: %#v", response)
	}

	ports.getErr = nil
	response507, err := server.LfsUploadProxy(context.Background(), lfsapi.LfsUploadProxyRequestObject{
		Oid:  oid,
		Body: strings.NewReader("payload"),
	})
	if err != nil {
		t.Fatalf("upload proxy returned error: %v", err)
	}
	insufficient, ok := response507.(lfsapi.LfsUploadProxy507TextResponse)
	if !ok || string(insufficient) != http.StatusText(http.StatusInsufficientStorage) {
		t.Fatalf("unexpected upload response: %#v", response507)
	}
}

func TestLFSBatchInternalErrorsDoNotExposeDetails(t *testing.T) {
	const detail = "credential secret leaked"
	internal := batchErrToObjectError(context.Background(), errors.New(detail), false)
	if internal.Code != http.StatusInternalServerError || internal.Message != http.StatusText(http.StatusInternalServerError) || strings.Contains(internal.Message, detail) {
		t.Fatalf("unexpected batch error: %+v", internal)
	}
	insufficient := batchErrToObjectError(context.Background(), errorapi.ErrBucketNotConfigured, false)
	if insufficient.Code != http.StatusInsufficientStorage || insufficient.Message != http.StatusText(http.StatusInsufficientStorage) {
		t.Fatalf("unexpected no-bucket error: %+v", insufficient)
	}
}

func TestLFSUploadProxyUsesCanonicalOIDForScopedTargets(t *testing.T) {
	oid := strings.Repeat("d", 64)
	newTransferService := func(ports *lfsTestServicePorts, storageFake *lfsTestStorage) *transfers.Service {
		return transfers.NewService(transfers.Dependencies{
			Objects:     objects.NewService(ports, nil),
			Storage:     storageFake,
			Scopes:      lfsTestScopeReader{scopes: map[string]buckets.Scope{"org|project": {Organization: "org", ProjectID: "project", Bucket: "physical", PathPrefix: "project-prefix"}}},
			Credentials: ports,
			Events:      ports,
		})
	}

	tests := []struct {
		name     string
		populate func(*lfsTestServicePorts)
	}{
		{
			name: "existing object",
			populate: func(ports *lfsTestServicePorts) {
				resources := []string{"/programs/org/projects/project"}
				methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://legacy/stale-key"}}}
				ports.records["record-existing"] = &objects.Record{
					Id:               "record-existing",
					Checksums:        []objects.Checksum{{Type: "sha256", Checksum: oid}},
					AccessMethods:    &methods,
					ControlledAccess: &resources,
				}
			},
		},
		{
			name: "pending metadata",
			populate: func(ports *lfsTestServicePorts) {
				resources := []string{"/programs/org/projects/project"}
				methods := []objects.AccessMethod{{Type: "s3", AccessUrl: &objects.AccessURL{Url: "s3://legacy/stale-key"}}}
				ports.pending[oid] = transferlfs.PendingMetadata{
					OID: oid,
					Candidate: objects.Candidate{
						Checksums:        &[]objects.Checksum{{Type: "sha256", Checksum: oid}},
						AccessMethods:    &methods,
						ControlledAccess: &resources,
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ports := newLFSTestPorts(map[string]*objects.Record{}, map[string]buckets.Credential{"physical": {Bucket: "physical"}})
			tt.populate(ports)
			storageFake := &lfsTestStorage{}
			deps := newLFSTestDependenciesWithTransfer(ports, storageFake, newTransferService(ports, storageFake))
			storageFake.uploadPart = func([]byte) (string, error) { return "etag", nil }
			server := newLFSServer(deps, defaultLFSOptions())

			response, err := server.LfsUploadProxy(context.Background(), lfsapi.LfsUploadProxyRequestObject{
				Oid:  oid,
				Body: strings.NewReader("payload"),
			})
			if err != nil {
				t.Fatalf("upload proxy error: %v", err)
			}
			if _, ok := response.(lfsapi.LfsUploadProxy200Response); !ok {
				t.Fatalf("upload proxy response = %T (%+v)", response, response)
			}
			want := storage.Target{Provider: "s3", LookupKey: "physical", PhysicalBucket: "physical", Key: "project-prefix/" + oid, Path: "/project-prefix/" + oid, CanonicalURL: "s3://physical/project-prefix/" + oid, LookupCandidates: []string{"physical"}}
			if !reflect.DeepEqual(storageFake.initTarget, want) {
				t.Fatalf("multipart init target = %+v, want %+v", storageFake.initTarget, want)
			}
		})
	}
}

func TestFromGeneratedCandidatePreservesLegacyFields(t *testing.T) {
	size := int64(42)
	id := "lfs-explicit-id"
	typ := "s3"
	region := "legacy-cloud"
	url := "s3://bucket/object.bin"
	candidate := lfsapi.DrsObjectCandidate{
		Id:   &id,
		Name: lfsStringPtr("object.bin"),
		Size: &size,
		Checksums: &[]lfsapi.Checksum{{
			Type: "sha256", Checksum: strings.Repeat("a", 64),
		}},
		AccessMethods: &[]lfsapi.AccessMethod{{
			AccessId:  lfsStringPtr("s3"),
			Type:      &typ,
			Region:    &region,
			AccessUrl: &lfsapi.AccessMethodAccessUrl{Url: &url},
			Authorizations: &lfsapi.AccessMethodAuthorizations{
				BearerAuthIssuers: lfsStringSlicePtr([]string{"issuer"}),
			},
		}},
	}

	got := fromLFSGeneratedCandidate(candidate)
	if got.Aliases == nil || len(*got.Aliases) != 1 || (*got.Aliases)[0] != "id:"+id {
		t.Fatalf("explicit id alias = %#v", got.Aliases)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
		t.Fatalf("access methods = %#v", got.AccessMethods)
	}
	method := (*got.AccessMethods)[0]
	if method.Cloud == nil || *method.Cloud != region || method.Region != nil {
		t.Fatalf("region/cloud mapping = %#v", method)
	}
	if method.Authorizations != nil {
		t.Fatalf("dropped legacy fields were retained: %#v", method)
	}
	if method.AccessUrl == nil || method.AccessUrl.Url != url {
		t.Fatalf("access URL mapping = %#v", method.AccessUrl)
	}
}

func TestFromGeneratedCandidatePreservesExplicitZeroSize(t *testing.T) {
	size := int64(0)
	got := fromLFSGeneratedCandidate(lfsapi.DrsObjectCandidate{Size: &size})
	if got.Size == nil || *got.Size != 0 {
		t.Fatalf("explicit zero size = %#v, want nonnil pointer to zero", got.Size)
	}
}

func TestFromGeneratedCandidateDerivesAliasFromSHA256(t *testing.T) {
	oid := strings.Repeat("b", 64)
	got := fromLFSGeneratedCandidate(lfsapi.DrsObjectCandidate{
		Checksums: &[]lfsapi.Checksum{{Type: "sha256", Checksum: oid}},
	})
	if got.Aliases == nil || len(*got.Aliases) != 1 || (*got.Aliases)[0] != "id:"+oid {
		t.Fatalf("sha256 alias = %#v", got.Aliases)
	}
}

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
	router := newLFSTestRouter(ports, &lfsTestStorage{}, defaultLFSOptions())
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
		server := newLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), defaultLFSOptions())
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
		server := newLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), defaultLFSOptions())
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
		server := newLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}), defaultLFSOptions())
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
