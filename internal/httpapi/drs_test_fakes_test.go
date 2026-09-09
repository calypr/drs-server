package httpapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type testDRSServicesFixture struct {
	objectService   *objects.Service
	transferService *transfers.Service
}

func testDRSServices(store *drsObjectStore, storageAccess transfers.StoragePort) *testDRSServicesFixture {
	objectService := objects.NewService(store, nil)
	return &testDRSServicesFixture{
		objectService: objectService,
		transferService: transfers.NewService(transfers.Dependencies{
			Objects: objectService,
			Storage: storageAccess,
			Events:  drsTestTransferEvents{},
		}),
	}
}

// drsObjectStore is the single in-memory object fixture used by the DRS
// endpoint tests. Its methods are deliberately direct; the old fixture graph
// only forwarded each method to one component over the same maps.
type drsObjectStore struct {
	objects.ObjectStore
	objects map[string]*objects.Record
	aliases map[string]string
}

func newDRSObjectStore(records map[string]*objects.Record) *drsObjectStore {
	return &drsObjectStore{objects: records, aliases: make(map[string]string)}
}

func (s *drsObjectStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := s.objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return cloneDRSRecord(obj), nil
}

func (s *drsObjectStore) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if obj, ok := s.objects[id]; ok {
			result = append(result, *cloneDRSRecord(obj))
		}
	}
	return result, nil
}

func (s *drsObjectStore) DeleteObject(_ context.Context, id string) error {
	delete(s.objects, id)
	return nil
}

func (s *drsObjectStore) BulkDeleteObjects(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(s.objects, id)
	}
	return nil
}

func (s *drsObjectStore) RegisterObjects(_ context.Context, records []objects.Record) error {
	for i := range records {
		s.objects[string(records[i].Id)] = cloneDRSRecord(&records[i])
	}
	return nil
}

func (s *drsObjectStore) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	s.objects = make(map[string]*objects.Record, len(records))
	return s.RegisterObjects(ctx, records)
}

func (s *drsObjectStore) UpdateObjectAccessMethods(_ context.Context, objectID string, methods []objects.AccessMethod) error {
	obj, ok := s.objects[objectID]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyMethods := append([]objects.AccessMethod(nil), methods...)
	obj.AccessMethods = &copyMethods
	return nil
}

func (s *drsObjectStore) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objects.AccessMethod) error {
	for id, methods := range updates {
		if err := s.UpdateObjectAccessMethods(ctx, id, methods); err != nil {
			return err
		}
	}
	return nil
}

func (s *drsObjectStore) CreateObjectAlias(_ context.Context, aliasID, canonicalObjectID string) error {
	s.aliases[aliasID] = canonicalObjectID
	return nil
}

func (s *drsObjectStore) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	canonicalID, ok := s.aliases[aliasID]
	if !ok {
		return "", fmt.Errorf("%w: alias not found", errorapi.ErrNotFound)
	}
	return canonicalID, nil
}

func (s *drsObjectStore) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	checksum = strings.TrimSpace(checksum)
	result := make([]objects.Record, 0)
	for id, obj := range s.objects {
		if id == checksum || string(obj.Id) == checksum || drsRecordHasChecksum(obj, checksum) {
			result = append(result, *cloneDRSRecord(obj))
		}
	}
	return result, nil
}

func (s *drsObjectStore) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	result := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := s.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		result[checksum] = matches
	}
	return result, nil
}

func drsRecordHasChecksum(obj *objects.Record, wanted string) bool {
	for _, checksum := range obj.Checksums {
		if strings.EqualFold(strings.TrimSpace(checksum.Checksum), wanted) {
			return true
		}
	}
	return false
}

func cloneDRSRecord(obj *objects.Record) *objects.Record {
	copy := *obj
	if obj.AccessMethods != nil {
		methods := append([]objects.AccessMethod(nil), (*obj.AccessMethods)...)
		copy.AccessMethods = &methods
	}
	if obj.Checksums != nil {
		copy.Checksums = append([]objects.Checksum(nil), obj.Checksums...)
	}
	return &copy
}

type drsTestTransferEvents struct{}

func (drsTestTransferEvents) RecordTransferAttributionEvents(context.Context, []usage.Event) error {
	return nil
}

var (
	_ objects.ObjectStore     = (*drsObjectStore)(nil)
	_ transfers.EventRecorder = drsTestTransferEvents{}
)
