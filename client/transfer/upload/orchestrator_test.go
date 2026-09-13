package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	drsapi "github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/client/common"
	"github.com/calypr/syfon/client/transfer"
)

type completedLocationBackend struct{ uploaderStub }

func (b *completedLocationBackend) MultipartCompleteWithLocation(context.Context, string, string, []transfer.MultipartPart) (string, error) {
	return "s3://physical-bucket/original-project-prefix/payload.bin", nil
}

func TestRegisterLargeFileUsesCompletedMultipartLocation(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "payload.bin")
	file, err := os.Create(filePath)
	if err != nil {
		t.Fatal(err)
	}
	size := int64(4.5 * float64(common.GB))
	if err := file.Truncate(size); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATA_CLIENT_CACHE_DIR", filepath.Join(t.TempDir(), "cache"))
	backend := &completedLocationBackend{uploaderStub: uploaderStub{resolveFunc: func(context.Context, string, string, common.FileMetadata, string) (string, error) {
		return "", fmt.Errorf("completed multipart location must not be re-resolved")
	}}}
	metadata := &metadataClientStub{registeredID: "stored-id"}
	obj := &drsapi.DrsObject{Id: "requested-id", Size: size}
	got, err := RegisterFile(context.Background(), backend, metadata, obj, filePath, "physical-bucket")
	if err != nil {
		t.Fatal(err)
	}
	const want = "s3://physical-bucket/original-project-prefix/payload.bin"
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
		t.Fatalf("access methods = %+v", got.AccessMethods)
	}
	if actual := (*got.AccessMethods)[0].AccessUrl.Url; actual != want {
		t.Fatalf("registered URL = %q, want completed location %q", actual, want)
	}
	if len(metadata.requests) != 1 || (*metadata.requests[0].Candidates[0].AccessMethods)[0].AccessUrl.Url != want {
		t.Fatalf("registration request = %+v", metadata.requests)
	}
}

type metadataClientStub struct {
	registeredID string
	registers    int
	updates      int
	requests     []drsapi.RegisterObjectsJSONRequestBody
	object       drsapi.DrsObject
	getErr       error
}

func (m *metadataClientStub) GetObject(context.Context, string) (drsapi.DrsObject, error) {
	if m.getErr != nil {
		return drsapi.DrsObject{}, m.getErr
	}
	if m.object.Id != "" {
		return m.object, nil
	}
	return drsapi.DrsObject{}, errorapi.ErrNotFound
}

func (m *metadataClientStub) RegisterObjects(_ context.Context, req drsapi.RegisterObjectsJSONRequestBody) (drsapi.N201ObjectsCreated, error) {
	m.registers++
	m.requests = append(m.requests, cloneRegisterRequest(req))
	return drsapi.N201ObjectsCreated{
		Objects: []drsapi.DrsObject{{
			Id: m.registeredID,
		}},
	}, nil
}

func (m *metadataClientStub) UpdateObjectAccessMethods(_ context.Context, objectID string, accessMethods []drsapi.AccessMethod) (drsapi.DrsObject, error) {
	m.updates++
	updated := m.object
	if strings.TrimSpace(updated.Id) == "" {
		updated.Id = objectID
	}
	methods := append([]drsapi.AccessMethod(nil), accessMethods...)
	updated.AccessMethods = &methods
	m.object = updated
	return updated, nil
}

func cloneRegisterRequest(req drsapi.RegisterObjectsJSONRequestBody) drsapi.RegisterObjectsJSONRequestBody {
	out := req
	out.Candidates = append([]drsapi.DrsObjectCandidate(nil), req.Candidates...)
	for i := range out.Candidates {
		if out.Candidates[i].ControlledAccess != nil {
			values := append([]string(nil), (*out.Candidates[i].ControlledAccess)...)
			out.Candidates[i].ControlledAccess = &values
		}
		if out.Candidates[i].AccessMethods != nil {
			values := append([]drsapi.AccessMethod(nil), (*out.Candidates[i].AccessMethods)...)
			out.Candidates[i].AccessMethods = &values
		}
	}
	return out
}

func TestUploadUsesTransferEngine(t *testing.T) {
	t.Parallel()

	file := createTempFileWithData(t, "payload")
	defer file.Close()
	backend := &uploaderStub{}
	metadata := common.FileMetadata{Authorizations: map[string][]string{"org": {"project"}}}
	if err := Upload(context.Background(), backend, file.Name(), "object-key", "did", "bucket", metadata, false, false); err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if backend.lastResolve.guid != "did" || backend.lastResolve.fileName != "object-key" || backend.lastResolve.bucket != "bucket" {
		t.Fatalf("unexpected upload resolution: %+v", backend.lastResolve)
	}
	if backend.lastUpload.body != "payload" {
		t.Fatalf("uploaded body = %q", backend.lastUpload.body)
	}
}

func TestRegisterFileUploadsUsingRegisteredObjectID(t *testing.T) {
	t.Parallel()

	file := createTempFileWithData(t, "payload")
	defer file.Close()

	uploader := &uploaderStub{}
	metadata := &metadataClientStub{registeredID: "server-object-id"}
	name := "payload.bin"
	obj := &drsapi.DrsObject{
		Id:   "requested-object-id",
		Name: &name,
		Size: 7,
		Checksums: []drsapi.Checksum{{
			Type:     "sha256",
			Checksum: "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7",
		}},
	}

	if _, err := RegisterFile(context.Background(), uploader, metadata, obj, file.Name(), "bucket-a"); err != nil {
		t.Fatalf("RegisterFile returned error: %v", err)
	}
	if uploader.lastResolve.guid != "requested-object-id" {
		t.Fatalf("expected upload URL to use requested object id, got %q", uploader.lastResolve.guid)
	}
	if uploader.lastResolve.fileName != "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7" {
		t.Fatalf("expected checksum upload key, got %q", uploader.lastResolve.fileName)
	}
}

func TestRegisterFileUsesSHA256AliasAsCASKey(t *testing.T) {
	t.Parallel()

	file := createTempFileWithData(t, "payload")
	defer file.Close()
	const checksum = "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"
	obj := &drsapi.DrsObject{
		Id:   "requested-object-id",
		Size: 7,
		Checksums: []drsapi.Checksum{{
			Type:     "SHA-256",
			Checksum: checksum,
		}},
	}
	backend := &uploaderStub{}
	metadata := &metadataClientStub{registeredID: "server-object-id"}
	if _, err := RegisterFile(context.Background(), backend, metadata, obj, file.Name(), "bucket-a"); err != nil {
		t.Fatalf("RegisterFile returned error: %v", err)
	}
	if backend.lastResolve.fileName != checksum {
		t.Fatalf("upload key = %q, want checksum %q", backend.lastResolve.fileName, checksum)
	}
}

func TestRegisterFileOnlyFallsBackForTypedNotFound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		lookupErr  error
		wantErr    bool
		wantLookup error
	}{
		{name: "not found", lookupErr: errorapi.ErrNotFound},
		{name: "wrapped not found", lookupErr: fmt.Errorf("lookup: %w", errorapi.ErrObjectNotFound)},
		{name: "unauthorized", lookupErr: fmt.Errorf("lookup: %w", errorapi.ErrUnauthorized), wantErr: true, wantLookup: errorapi.ErrUnauthorized},
		{name: "transport", lookupErr: errors.New("transport unavailable"), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := createTempFileWithData(t, "payload")
			defer file.Close()
			backend := &uploaderStub{}
			metadata := &metadataClientStub{registeredID: "server-object-id", getErr: test.lookupErr}
			obj := &drsapi.DrsObject{Id: "requested-object-id", Size: 7}

			_, err := RegisterFile(context.Background(), backend, metadata, obj, file.Name(), "bucket-a")
			if test.wantErr {
				if err == nil || !strings.Contains(err.Error(), "failed to look up existing object") {
					t.Fatalf("lookup error = %v, want wrapped lookup failure", err)
				}
				if test.wantLookup != nil && !errors.Is(err, test.wantLookup) {
					t.Fatalf("lookup error = %v, want cause %v", err, test.wantLookup)
				}
				if metadata.registers != 0 {
					t.Fatalf("RegisterObjects calls = %d, want 0", metadata.registers)
				}
				return
			}
			if err != nil {
				t.Fatalf("typed not-found RegisterFile error = %v", err)
			}
			if metadata.registers != 1 {
				t.Fatalf("RegisterObjects calls = %d, want 1", metadata.registers)
			}
		})
	}
}

func TestRegisterFilePreservesScopedRoutingMetadata(t *testing.T) {
	t.Parallel()

	file := createTempFileWithData(t, "payload")
	defer file.Close()

	name := "payload.bin"
	controlledAccess := []string{"/organization/syfon/project/e2e"}
	accessMethods := []drsapi.AccessMethod{{
		Type:      "s3",
		AccessUrl: &drsapi.AccessURL{Url: "s3://syfon-e2e-bucket/project-subpath/3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"},
	}}
	obj := &drsapi.DrsObject{
		Id:               "requested-object-id",
		Name:             &name,
		Size:             7,
		ControlledAccess: &controlledAccess,
		AccessMethods:    &accessMethods,
		Checksums: []drsapi.Checksum{{
			Type:     "sha256",
			Checksum: "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7",
		}},
	}

	uploader := &uploaderStub{}
	metadata := &metadataClientStub{
		registeredID: "server-object-id",
		object: drsapi.DrsObject{
			Id:               "server-object-id",
			Name:             &name,
			Size:             7,
			ControlledAccess: &controlledAccess,
			AccessMethods:    &accessMethods,
			Checksums:        obj.Checksums,
		},
	}

	if _, err := RegisterFile(context.Background(), uploader, metadata, obj, file.Name(), "syfon-e2e-bucket"); err != nil {
		t.Fatalf("RegisterFile returned error: %v", err)
	}
	if len(metadata.requests) != 1 {
		t.Fatalf("expected single final register call, got %d", len(metadata.requests))
	}
	if metadata.updates != 1 {
		t.Fatalf("expected finalized access-method update, got %d", metadata.updates)
	}
	for i, req := range metadata.requests {
		if len(req.Candidates) != 1 {
			t.Fatalf("register call %d expected one candidate, got %d", i, len(req.Candidates))
		}
		candidate := req.Candidates[0]
		if candidate.ControlledAccess == nil || len(*candidate.ControlledAccess) != 1 || (*candidate.ControlledAccess)[0] != controlledAccess[0] {
			t.Fatalf("register call %d did not preserve controlled_access: %#v", i, candidate.ControlledAccess)
		}
		if candidate.AccessMethods == nil || len(*candidate.AccessMethods) == 0 {
			t.Fatalf("register call %d did not preserve access methods: %#v", i, candidate.AccessMethods)
		}
	}
	if obj.ControlledAccess == nil || len(*obj.ControlledAccess) != 1 || (*obj.ControlledAccess)[0] != controlledAccess[0] {
		t.Fatalf("returned object did not preserve controlled_access: %#v", obj.ControlledAccess)
	}
}

func TestRegisterFilePrefersExplicitControlledAccessOverExistingObject(t *testing.T) {
	t.Parallel()

	file := createTempFileWithData(t, "payload")
	defer file.Close()

	name := "payload.bin"
	targetControlledAccess := []string{"/organization/dst/project/copied"}
	sourceControlledAccess := []string{"/organization/src/project/original"}
	accessMethods := []drsapi.AccessMethod{{
		Type:      "s3",
		AccessUrl: &drsapi.AccessURL{Url: "s3://syfon-bucket/original/3d71f043937a09db4f2b47c46f19923ef823f6a777a15fde0b2c9c7"},
	}}
	obj := &drsapi.DrsObject{
		Id:               "requested-object-id",
		Name:             &name,
		Size:             7,
		ControlledAccess: &targetControlledAccess,
		Checksums: []drsapi.Checksum{{
			Type:     "sha256",
			Checksum: "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7",
		}},
	}

	uploader := &uploaderStub{}
	metadata := &metadataClientStub{
		registeredID: "server-object-id",
		object: drsapi.DrsObject{
			Id:               "server-object-id",
			Name:             &name,
			Size:             7,
			ControlledAccess: &sourceControlledAccess,
			AccessMethods:    &accessMethods,
			Checksums:        obj.Checksums,
		},
	}

	if _, err := RegisterFile(context.Background(), uploader, metadata, obj, file.Name(), "syfon-bucket"); err != nil {
		t.Fatalf("RegisterFile returned error: %v", err)
	}
	if len(metadata.requests) != 1 {
		t.Fatalf("expected single final register call, got %d", len(metadata.requests))
	}
	candidate := metadata.requests[0].Candidates[0]
	if candidate.ControlledAccess == nil || len(*candidate.ControlledAccess) != 1 || (*candidate.ControlledAccess)[0] != targetControlledAccess[0] {
		t.Fatalf("expected explicit controlled_access to win, got %#v", candidate.ControlledAccess)
	}
	if candidate.AccessMethods == nil || len(*candidate.AccessMethods) == 0 {
		t.Fatalf("expected existing access methods to still be preserved, got %#v", candidate.AccessMethods)
	}
}

func TestRegisterFileSinglePartStreamsProgress(t *testing.T) {
	t.Parallel()

	payload := make([]byte, common.OnProgressThreshold+257)
	for i := range payload {
		payload[i] = 'a'
	}

	file := createTempFileWithData(t, string(payload))
	defer file.Close()

	uploader := &uploaderStub{
		uploadFunc: func(_ context.Context, _ string, body io.Reader, _ int64) error {
			buf := make([]byte, 64*1024)
			for {
				_, err := body.Read(buf)
				if err == io.EOF {
					return nil
				}
				if err != nil {
					return err
				}
			}
		},
	}
	metadata := &metadataClientStub{registeredID: "server-object-id"}
	name := "payload.bin"
	obj := &drsapi.DrsObject{
		Id:   "requested-object-id",
		Name: &name,
		Size: int64(len(payload)),
		Checksums: []drsapi.Checksum{{
			Type:     "sha256",
			Checksum: "3d71f043937a09b77826109db4f2b47c46f19923ef823f6a777a15fde0b2c9c7",
		}},
	}

	var events []common.ProgressEvent
	ctx := common.WithOid(context.Background(), obj.Checksums[0].Checksum)
	ctx = common.WithProgress(ctx, func(ev common.ProgressEvent) error {
		events = append(events, ev)
		return nil
	})

	if _, err := RegisterFile(ctx, uploader, metadata, obj, file.Name(), "bucket-a"); err != nil {
		t.Fatalf("RegisterFile returned error: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected streamed progress events, got %+v", events)
	}

	progressEventsSeen := 0
	for _, ev := range events {
		if ev.Event == "progress" {
			progressEventsSeen++
		}
	}
	if progressEventsSeen < 2 {
		t.Fatalf("expected threshold and finalize progress events, got %+v", events)
	}

	last := events[len(events)-1]
	if last.BytesSoFar != int64(len(payload)) {
		t.Fatalf("final progress bytes = %d, want %d", last.BytesSoFar, len(payload))
	}
}

func TestRegisterFileUsesOneUploadTarget(t *testing.T) {
	t.Parallel()
	file := createTempFileWithData(t, "payload")
	defer file.Close()
	calls := 0
	uploader := &uploaderStub{resolveFunc: func(context.Context, string, string, common.FileMetadata, string) (string, error) {
		calls++
		return fmt.Sprintf("https://upload.example/target-%d", calls), nil
	}}
	metadata := &metadataClientStub{registeredID: "id"}
	obj := &drsapi.DrsObject{Id: "id", Size: 7}
	got, err := RegisterFile(context.Background(), uploader, metadata, obj, file.Name(), "bucket")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("resolved upload target %d times", calls)
	}
	if got.AccessMethods == nil || len(*got.AccessMethods) != 1 {
		t.Fatalf("missing access method: %+v", got)
	}
	if target := (*got.AccessMethods)[0].AccessUrl.Url; target != uploader.lastUpload.url || target != "https://upload.example/target-1" {
		t.Fatalf("registered %q but uploaded to %q", target, uploader.lastUpload.url)
	}
}
