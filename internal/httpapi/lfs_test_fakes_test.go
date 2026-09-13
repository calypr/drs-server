package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/buckets"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/storage"
	"github.com/calypr/syfon/internal/transfers"
	transferlfs "github.com/calypr/syfon/internal/transfers/lfs"
	"github.com/calypr/syfon/internal/usage"
	"github.com/gofiber/fiber/v3"
)

// lfsTestServicePorts is one in-memory fixture for the service capabilities
// used by the LFS routes. Keeping the state and implementations together
// avoids a graph of fakes that only forward calls to one another.
type lfsTestServicePorts struct {
	*drsObjectStore
	credentials    map[string]buckets.Credential
	pending        map[string]transferlfs.PendingMetadata
	transferEvents []usage.Event
	uploads        []string
	downloads      []string
	getErr         error
}

func newLFSTestPorts(t testing.TB, records map[string]*drs.DrsObject, credentials map[string]buckets.Credential) *lfsTestServicePorts {
	t.Helper()
	if records == nil {
		records = map[string]*drs.DrsObject{}
	}
	if credentials == nil {
		credentials = map[string]buckets.Credential{}
	}
	return &lfsTestServicePorts{
		drsObjectStore: newDRSObjectStore(t, records), credentials: credentials,
		pending: map[string]transferlfs.PendingMetadata{},
	}
}

func (p *lfsTestServicePorts) GetObject(ctx context.Context, id string) (*drs.DrsObject, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	return p.drsObjectStore.GetObject(ctx, id)
}

func (p *lfsTestServicePorts) GetS3Credential(_ context.Context, bucket string) (*buckets.Credential, error) {
	credential, ok := p.credentials[bucket]
	if !ok {
		return nil, fmt.Errorf("%w: credential not found", errorapi.ErrNotFound)
	}
	copyCredential := credential
	return &copyCredential, nil
}

func (p *lfsTestServicePorts) ListS3Credentials(_ context.Context) ([]buckets.Credential, error) {
	keys := make([]string, 0, len(p.credentials))
	for key := range p.credentials {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]buckets.Credential, 0, len(keys))
	for _, key := range keys {
		result = append(result, p.credentials[key])
	}
	return result, nil
}

func (p *lfsTestServicePorts) SavePendingMetadata(_ context.Context, entries []transferlfs.PendingMetadata) error {
	for _, entry := range entries {
		p.pending[entry.OID] = entry
	}
	return nil
}

func (p *lfsTestServicePorts) GetPendingMetadata(_ context.Context, oid string) (*transferlfs.PendingMetadata, error) {
	entry, ok := p.pending[oid]
	if !ok {
		return nil, fmt.Errorf("%w: pending metadata not found", errorapi.ErrNotFound)
	}
	return &entry, nil
}

func (p *lfsTestServicePorts) ConsumePendingMetadata(_ context.Context, expected transferlfs.PendingMetadata) (bool, error) {
	oid := expected.OID
	entry, ok := p.pending[oid]
	if !ok {
		return false, nil
	}
	if !entry.CreatedAt.Equal(expected.CreatedAt) || !entry.ExpiresAt.Equal(expected.ExpiresAt) || !reflect.DeepEqual(entry.Candidate, expected.Candidate) {
		return false, nil
	}
	delete(p.pending, oid)
	return true, nil
}

func (p *lfsTestServicePorts) RecordTransferAttributionEvents(_ context.Context, events []usage.Event) error {
	p.transferEvents = append(p.transferEvents, events...)
	return nil
}

func (p *lfsTestServicePorts) RecordFileUpload(_ context.Context, objectID string) error {
	p.uploads = append(p.uploads, objectID)
	return nil
}

func (p *lfsTestServicePorts) RecordFileDownload(_ context.Context, objectID string) error {
	p.downloads = append(p.downloads, objectID)
	return nil
}

var _ objects.ObjectStore = (*lfsTestServicePorts)(nil)
var _ buckets.CredentialReader = (*lfsTestServicePorts)(nil)
var _ transferlfs.PendingStore = (*lfsTestServicePorts)(nil)
var _ usage.FileCounterRecorder = (*lfsTestServicePorts)(nil)

func newLFSTransferService(storageFake *lfsTestStorage, ports *lfsTestServicePorts) *transfers.Service {
	return transfers.NewService(transfers.Dependencies{
		Objects:      objects.NewService(ports),
		Storage:      storageFake,
		Credentials:  ports,
		Events:       ports,
		FileCounters: ports,
	})
}

type lfsTestRouter struct{ app *fiber.App }

func (r *lfsTestRouter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	response, err := r.app.Test(request)
	if err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(err.Error()))
		return
	}
	defer response.Body.Close()
	for key, values := range response.Header {
		for _, value := range values {
			writer.Header().Add(key, value)
		}
	}
	writer.WriteHeader(response.StatusCode)
	_, _ = io.Copy(writer, response.Body)
}

type lfsTestStorage struct {
	accessLocation string
	partLocation   string
	uploadPart     func([]byte) (string, error)
	initTarget     storage.Target
	partRequest    storage.MultipartPartRequest
	complete       storage.CompleteMultipartRequest
}

func (f *lfsTestStorage) Sign(_ context.Context, request storage.SignRequest) (storage.SignedAccess, error) {
	location := request.Target.OriginalURL
	if f.accessLocation != "" {
		location = f.accessLocation
	}
	return storage.SignedAccess{Location: location + "?signed=true"}, nil
}

func (f *lfsTestStorage) BeginMultipart(_ context.Context, request storage.BeginMultipartRequest) (storage.UploadID, error) {
	f.initTarget = request.Target
	return storage.UploadID("opaque-upload-id"), nil
}

func (f *lfsTestStorage) SignMultipartPart(_ context.Context, request storage.MultipartPartRequest) (storage.SignedAccess, error) {
	f.partRequest = request
	if f.partLocation != "" {
		return storage.SignedAccess{Location: f.partLocation}, nil
	}
	return storage.SignedAccess{Location: fmt.Sprintf("s3://%s/%s", request.Target.PhysicalBucket, request.Target.Key)}, nil
}

func (f *lfsTestStorage) CompleteMultipart(_ context.Context, request storage.CompleteMultipartRequest) error {
	f.complete = request
	return nil
}

func newLFSTestDependencies(ports *lfsTestServicePorts, storageFake *lfsTestStorage) *transferlfs.Service {
	transferService := newLFSTransferService(storageFake, ports)
	return newLFSTestDependenciesWithTransfer(ports, storageFake, transferService)
}

func newLFSTestDependenciesWithTransfer(ports *lfsTestServicePorts, storageFake *lfsTestStorage, transferService *transfers.Service) *transferlfs.Service {
	objectService := objects.NewService(ports)
	lfsService := transferlfs.NewService(transferService, objectService, ports, ports, ports, storageFakeUploader(storageFake))
	return lfsService
}

func storageFakeUploader(fake *lfsTestStorage) func(context.Context, string, []byte) (string, error) {
	return func(_ context.Context, _ string, content []byte) (string, error) {
		if fake.uploadPart != nil {
			return fake.uploadPart(content)
		}
		return "etag", nil
	}
}

func newLFSTestRouter(ports *lfsTestServicePorts, storageFake *lfsTestStorage, opts LFSOptions) *lfsTestRouter {
	app := fiber.New()
	registerLFSRoutes(app, newLFSTestDependencies(ports, storageFake), opts)
	return &lfsTestRouter{app: app}
}

type lfsTestScopeReader struct {
	scopes map[string]buckets.Scope
}

func defaultLFSOptions() LFSOptions {
	return LFSOptions{
		MaxBatchObjects:              1000,
		MaxBatchBodyBytes:            10 * 1024 * 1024,
		RequestLimitPerMinute:        1200,
		BandwidthLimitBytesPerMinute: 0,
	}
}

func (r lfsTestScopeReader) LookupBucketScope(_ context.Context, organization, project string) (buckets.Scope, bool, error) {
	scope, ok := r.scopes[organization+"|"+project]
	return scope, ok, nil
}

func newLFSTestServerForNumericValidation(t testing.TB) *lfsServer {
	t.Helper()
	ports := newLFSTestPorts(t, nil, nil)
	return &lfsServer{service: newLFSTestDependencies(ports, &lfsTestStorage{}), opts: defaultLFSOptions()}
}
