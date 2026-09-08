package drs

import (
	"context"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	"github.com/calypr/syfon/internal/objects"
	objectrecords "github.com/calypr/syfon/internal/objects/records"
	"github.com/calypr/syfon/internal/transfers"
	"github.com/calypr/syfon/internal/usage"
)

type testDRSServicesFixture struct {
	objectService   *objectrecords.Service
	transferService *transfers.Service
}

func testDRSServices(store *drsObjectFixture, storageAccess transfers.AccessPort) *testDRSServicesFixture {
	return &testDRSServicesFixture{
		objectService: objectrecords.NewService(store),
		transferService: transfers.NewService(transfers.Dependencies{
			Access: storageAccess,
			Events: testTransferEvents{},
		}),
	}
}

type drsObjectFixture struct {
	objectrecords.ObjectStore
	reader        *drsObjectReader
	writer        *drsObjectWriter
	accessMethods *drsObjectAccessMethods
	aliases       *drsObjectAliases
	content       *drsObjectContent
}

func (s *drsObjectFixture) GetObject(ctx context.Context, id string) (*objects.Record, error) {
	return s.reader.GetObject(ctx, id)
}
func (s *drsObjectFixture) GetBulkObjects(ctx context.Context, ids []string) ([]objects.Record, error) {
	return s.reader.GetBulkObjects(ctx, ids)
}
func (s *drsObjectFixture) DeleteObject(ctx context.Context, id string) error {
	return s.writer.DeleteObject(ctx, id)
}
func (s *drsObjectFixture) BulkDeleteObjects(ctx context.Context, ids []string) error {
	return s.writer.BulkDeleteObjects(ctx, ids)
}
func (s *drsObjectFixture) RegisterObjects(ctx context.Context, records []objects.Record) error {
	return s.writer.RegisterObjects(ctx, records)
}
func (s *drsObjectFixture) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	return s.writer.ReplaceObjects(ctx, records)
}
func (s *drsObjectFixture) UpdateObjectAccessMethods(ctx context.Context, id string, methods []objects.AccessMethod) error {
	return s.accessMethods.UpdateObjectAccessMethods(ctx, id, methods)
}
func (s *drsObjectFixture) BulkUpdateAccessMethods(ctx context.Context, methods map[string][]objects.AccessMethod) error {
	return s.accessMethods.BulkUpdateAccessMethods(ctx, methods)
}
func (s *drsObjectFixture) DeleteObjectAlias(ctx context.Context, id string) error {
	return s.aliases.DeleteObjectAlias(ctx, id)
}
func (s *drsObjectFixture) CreateObjectAlias(ctx context.Context, id, canonical string) error {
	return s.aliases.CreateObjectAlias(ctx, id, canonical)
}
func (s *drsObjectFixture) ResolveObjectAlias(ctx context.Context, id string) (string, error) {
	return s.aliases.ResolveObjectAlias(ctx, id)
}
func (s *drsObjectFixture) GetObjectsByChecksum(ctx context.Context, checksum string) ([]objects.Record, error) {
	return s.content.GetObjectsByChecksum(ctx, checksum)
}
func (s *drsObjectFixture) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	return s.content.GetObjectsByChecksums(ctx, checksums)
}

type drsObjectData struct {
	objects map[string]*objects.Record
	aliases map[string]string
}

func newDRSObjectStore(records map[string]*objects.Record) *drsObjectFixture {
	data := &drsObjectData{objects: records, aliases: make(map[string]string)}
	return &drsObjectFixture{
		reader:        &drsObjectReader{data: data},
		writer:        &drsObjectWriter{data: data},
		accessMethods: &drsObjectAccessMethods{data: data},
		aliases:       &drsObjectAliases{data: data},
		content:       &drsObjectContent{data: data},
	}
}

type drsObjectReader struct{ data *drsObjectData }

func (r *drsObjectReader) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := r.data.objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return cloneDRSRecord(obj), nil
}

func (r *drsObjectReader) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		obj, ok := r.data.objects[id]
		if !ok {
			continue
		}
		result = append(result, *cloneDRSRecord(obj))
	}
	return result, nil
}

type drsObjectWriter struct{ data *drsObjectData }

func (w *drsObjectWriter) DeleteObject(_ context.Context, id string) error {
	delete(w.data.objects, id)
	return nil
}

func (w *drsObjectWriter) CreateObject(_ context.Context, obj *objects.Record) error {
	w.data.objects[string(obj.Id)] = cloneDRSRecord(obj)
	return nil
}

func (w *drsObjectWriter) RegisterObjects(_ context.Context, records []objects.Record) error {
	for i := range records {
		w.data.objects[string(records[i].Id)] = cloneDRSRecord(&records[i])
	}
	return nil
}

func (w *drsObjectWriter) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	w.data.objects = make(map[string]*objects.Record, len(records))
	return w.RegisterObjects(ctx, records)
}

func (w *drsObjectWriter) BulkDeleteObjects(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(w.data.objects, id)
	}
	return nil
}

type drsObjectAccessMethods struct{ data *drsObjectData }

func (w *drsObjectAccessMethods) UpdateObjectAccessMethods(_ context.Context, objectID string, methods []objects.AccessMethod) error {
	obj, ok := w.data.objects[objectID]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copy := append([]objects.AccessMethod(nil), methods...)
	obj.AccessMethods = &copy
	return nil
}

func (w *drsObjectAccessMethods) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]objects.AccessMethod) error {
	for id, methods := range updates {
		if err := w.UpdateObjectAccessMethods(ctx, id, methods); err != nil {
			return err
		}
	}
	return nil
}

type drsObjectAliases struct{ data *drsObjectData }

func (a *drsObjectAliases) DeleteObjectAlias(_ context.Context, aliasID string) error {
	delete(a.data.aliases, aliasID)
	return nil
}

func (a *drsObjectAliases) CreateObjectAlias(_ context.Context, aliasID, canonicalObjectID string) error {
	a.data.aliases[aliasID] = canonicalObjectID
	return nil
}

func (a *drsObjectAliases) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	canonicalID, ok := a.data.aliases[aliasID]
	if !ok {
		return "", fmt.Errorf("%w: alias not found", errorapi.ErrNotFound)
	}
	return canonicalID, nil
}

type drsObjectContent struct{ data *drsObjectData }

func (c *drsObjectContent) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	checksum = strings.TrimSpace(checksum)
	result := make([]objects.Record, 0)
	for id, obj := range c.data.objects {
		if id == checksum || string(obj.Id) == checksum || recordHasChecksum(obj, checksum) {
			result = append(result, *cloneDRSRecord(obj))
		}
	}
	return result, nil
}

func (c *drsObjectContent) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	result := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := c.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		result[checksum] = matches
	}
	return result, nil
}

func recordHasChecksum(obj *objects.Record, wanted string) bool {
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

type testTransferEvents struct{}

func (testTransferEvents) RecordTransferAttributionEvents(context.Context, []usage.Event) error {
	return nil
}

var (
	_ objectrecords.ObjectStore = (*drsObjectFixture)(nil)
	_ transfers.EventRecorder   = testTransferEvents{}
)
