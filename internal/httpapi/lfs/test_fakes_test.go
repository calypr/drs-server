package lfs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

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
	objects.ObjectStore
	records        map[string]*objects.Record
	aliases        map[string]string
	credentials    map[string]buckets.Credential
	pending        map[string]transferlfs.PendingMetadata
	transferEvents []usage.Event
	uploads        []string
	downloads      []string
	getErr         error
}

func newLFSTestPorts(records map[string]*objects.Record, credentials map[string]buckets.Credential) *lfsTestServicePorts {
	if records == nil {
		records = map[string]*objects.Record{}
	}
	if credentials == nil {
		credentials = map[string]buckets.Credential{}
	}
	return &lfsTestServicePorts{
		records: records, aliases: map[string]string{}, credentials: credentials,
		pending: map[string]transferlfs.PendingMetadata{},
	}
}

func (p *lfsTestServicePorts) GetObject(_ context.Context, id string) (*objects.Record, error) {
	if p.getErr != nil {
		return nil, p.getErr
	}
	record, ok := p.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyRecord := *record
	return &copyRecord, nil
}

func (p *lfsTestServicePorts) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if record, ok := p.records[id]; ok {
			result = append(result, *record)
		}
	}
	return result, nil
}

func (p *lfsTestServicePorts) DeleteObject(_ context.Context, id string) error {
	delete(p.records, id)
	return nil
}

func (p *lfsTestServicePorts) BulkDeleteObjects(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(p.records, id)
	}
	return nil
}

func (p *lfsTestServicePorts) RegisterObjects(_ context.Context, records []objects.Record) error {
	for i := range records {
		copyRecord := records[i]
		p.records[string(copyRecord.Id)] = &copyRecord
	}
	return nil
}

func (p *lfsTestServicePorts) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	p.records = make(map[string]*objects.Record, len(records))
	return p.RegisterObjects(ctx, records)
}

func (p *lfsTestServicePorts) UpdateObjectAccessMethods(_ context.Context, id string, methods []objects.AccessMethod) error {
	record, ok := p.records[id]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyMethods := append([]objects.AccessMethod(nil), methods...)
	record.AccessMethods = &copyMethods
	return nil
}

func (p *lfsTestServicePorts) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objects.AccessMethod) error {
	for id, methods := range updates {
		if err := p.UpdateObjectAccessMethods(ctx, id, methods); err != nil {
			return err
		}
	}
	return nil
}

func (p *lfsTestServicePorts) CreateObjectAlias(_ context.Context, id, canonical string) error {
	p.aliases[id] = canonical
	return nil
}

func (p *lfsTestServicePorts) ResolveObjectAlias(_ context.Context, id string) (string, error) {
	canonical, ok := p.aliases[id]
	if !ok {
		return "", fmt.Errorf("%w: object alias not found", errorapi.ErrNotFound)
	}
	return canonical, nil
}

func (p *lfsTestServicePorts) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	result := make([]objects.Record, 0)
	for _, record := range p.records {
		if recordMatchesChecksum(record, checksum) {
			result = append(result, *record)
		}
	}
	return result, nil
}

func (p *lfsTestServicePorts) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	result := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := p.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		result[checksum] = matches
	}
	return result, nil
}

func recordMatchesChecksum(record *objects.Record, checksum string) bool {
	if record == nil {
		return false
	}
	if string(record.Id) == checksum {
		return true
	}
	for _, candidate := range record.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), strings.TrimSpace(checksum)) {
			return true
		}
	}
	return false
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

func (p *lfsTestServicePorts) PopPendingMetadata(_ context.Context, oid string) (*transferlfs.PendingMetadata, error) {
	entry, ok := p.pending[oid]
	if !ok {
		return nil, fmt.Errorf("%w: pending metadata not found", errorapi.ErrNotFound)
	}
	delete(p.pending, oid)
	return &entry, nil
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

func (f *lfsTestStorage) BeginMultipart(_ context.Context, target storage.Target) (storage.UploadID, error) {
	f.initTarget = target
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

func newLFSTestDependencies(ports *lfsTestServicePorts, storageFake *lfsTestStorage) Dependencies {
	transferService := newLFSTransferService(storageFake, ports)
	return newLFSTestDependenciesWithTransfer(ports, storageFake, transferService)
}

func newLFSTestDependenciesWithTransfer(ports *lfsTestServicePorts, storageFake *lfsTestStorage, transferService *transfers.Service) Dependencies {
	objectService := objects.NewService(ports)
	lfsService := transferlfs.NewService(transferService, objectService, ports, ports, ports, storageFakeUploader(storageFake))
	return Dependencies{
		Service: lfsService,
	}
}

func storageFakeUploader(fake *lfsTestStorage) storage.SignedPartUploader {
	return func(_ context.Context, _ string, content []byte) (string, error) {
		if fake.uploadPart != nil {
			return fake.uploadPart(content)
		}
		return "etag", nil
	}
}

func newLFSTestRouter(ports *lfsTestServicePorts, storageFake *lfsTestStorage, opts Options) *lfsTestRouter {
	app := fiber.New()
	RegisterLFSRoutes(app, newLFSTestDependencies(ports, storageFake), opts)
	return &lfsTestRouter{app: app}
}

type lfsTestScopeReader struct {
	scopes map[string]buckets.Scope
}

func (r lfsTestScopeReader) LookupBucketScope(_ context.Context, organization, project string) (buckets.Scope, bool, error) {
	scope, ok := r.scopes[organization+"|"+project]
	return scope, ok, nil
}

func newLFSTestServerForNumericValidation() *LFSServer {
	ports := newLFSTestPorts(nil, nil)
	return NewLFSServer(newLFSTestDependencies(ports, &lfsTestStorage{}).Service, DefaultOptions())
}

func stringPtr(value string) *string { return &value }

func stringSlicePtr(value []string) *[]string { return &value }
