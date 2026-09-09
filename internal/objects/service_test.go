package objects_test

import (
	"context"
	"fmt"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/credentialcipher"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
	"strings"
	"testing"
	"time"
)

func newTestService(backend objects.ObjectStore) *objects.Service {
	return objects.NewService(backend)
}

func buildGen3Context(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("gen3")
	session.AuthHeaderPresent = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}

func buildLocalAuthzContext(privileges map[string]map[string]bool) context.Context {
	session := access.NewSession("local")
	session.AuthzEnforced = true
	session.SetAuthorizations(nil, privileges, true)
	return access.WithSession(context.Background(), session)
}

func ptr[T any](value T) *T { return &value }

func registerCandidates(ctx context.Context, service *objects.Service, candidates []objects.Candidate) (int, error) {
	records := make([]objects.Record, 0, len(candidates))
	for _, candidate := range candidates {
		record, err := objects.CandidateToRecord(candidate, time.Now().UTC())
		if err != nil {
			return 0, err
		}
		records = append(records, record)
	}
	if err := service.RegisterObjects(ctx, records); err != nil {
		return 0, err
	}
	return len(records), nil
}

func newSQLiteDatabase(t *testing.T) *store.Store {
	t.Helper()
	cipher, err := credentialcipher.NewFromEnv()
	if err != nil {
		t.Fatalf("create credential cipher: %v", err)
	}
	database, err := sqlite.NewSqliteDB(":memory:", cipher)
	if err != nil {
		t.Fatalf("create in-memory SQLite database: %v", err)
	}
	return database
}

type bulkOverwriteStore struct {
	objects.ObjectStore
	Objects map[string]*objects.Record
	Aliases map[string]string
}

func (f *bulkOverwriteStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := f.Objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyObj := *obj
	return &copyObj, nil
}

func (f *bulkOverwriteStore) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if obj, ok := f.Objects[id]; ok {
			result = append(result, *obj)
		}
	}
	return result, nil
}

func (f *bulkOverwriteStore) DeleteObject(_ context.Context, id string) error {
	delete(f.Objects, id)
	return nil
}

func (f *bulkOverwriteStore) CreateObject(_ context.Context, obj *objects.Record) error {
	if f.Objects == nil {
		f.Objects = make(map[string]*objects.Record)
	}
	copyObj := *obj
	f.Objects[string(obj.Id)] = &copyObj
	return nil
}

func (f *bulkOverwriteStore) BulkDeleteObjects(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := f.DeleteObject(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (f *bulkOverwriteStore) RegisterObjects(ctx context.Context, records []objects.Record) error {
	for i := range records {
		if err := f.CreateObject(ctx, &records[i]); err != nil {
			return err
		}
	}
	return nil
}

func (f *bulkOverwriteStore) ReplaceObjects(ctx context.Context, records []objects.Record) error {
	return f.RegisterObjects(ctx, records)
}

func (f *bulkOverwriteStore) CreateObjectAlias(_ context.Context, aliasID, canonicalID string) error {
	if _, ok := f.Objects[canonicalID]; !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	if f.Aliases == nil {
		f.Aliases = make(map[string]string)
	}
	f.Aliases[aliasID] = canonicalID
	return nil
}

func (f *bulkOverwriteStore) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	canonicalID, ok := f.Aliases[aliasID]
	if !ok {
		return "", fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return canonicalID, nil
}

func (f *bulkOverwriteStore) ListScopedObjectIDsByChecksums(_ context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	result := make(map[string][]string, len(checksums))
	for _, checksum := range checksums {
		for id, obj := range f.Objects {
			if !recordHasChecksum(obj, checksum) || !recordInScope(obj, organization, project) {
				continue
			}
			result[checksum] = append(result[checksum], id)
		}
	}
	return result, nil
}

type readObjectStore struct {
	objects.ObjectStore
	Objects   map[string]*objects.Record
	BulkCalls [][]string
}

func (f *readObjectStore) CreateObject(_ context.Context, obj *objects.Record) error {
	if f.Objects == nil {
		f.Objects = make(map[string]*objects.Record)
	}
	copyObj := *obj
	f.Objects[string(obj.Id)] = &copyObj
	return nil
}

func (f *readObjectStore) GetObject(_ context.Context, id string) (*objects.Record, error) {
	obj, ok := f.Objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyObj := *obj
	return &copyObj, nil
}

func (f *readObjectStore) GetBulkObjects(_ context.Context, ids []string) ([]objects.Record, error) {
	f.BulkCalls = append(f.BulkCalls, append([]string(nil), ids...))
	result := make([]objects.Record, 0, len(ids))
	for _, id := range ids {
		if obj, ok := f.Objects[id]; ok {
			result = append(result, *obj)
		}
	}
	return result, nil
}

func (f *readObjectStore) GetObjectsByChecksum(_ context.Context, checksum string) ([]objects.Record, error) {
	result := make([]objects.Record, 0)
	for _, obj := range f.Objects {
		if recordHasChecksum(obj, checksum) {
			result = append(result, *obj)
		}
	}
	return result, nil
}

func (f *readObjectStore) GetObjectsByChecksums(ctx context.Context, checksums []string) (map[string][]objects.Record, error) {
	result := make(map[string][]objects.Record, len(checksums))
	for _, checksum := range checksums {
		matches, err := f.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		result[checksum] = matches
	}
	return result, nil
}

func (f *readObjectStore) ListScopedObjectIDsByChecksums(_ context.Context, organization, project string, checksums []string) (map[string][]string, error) {
	result := make(map[string][]string, len(checksums))
	for _, checksum := range checksums {
		for id, obj := range f.Objects {
			if recordHasChecksum(obj, checksum) && recordInScope(obj, organization, project) {
				result[checksum] = append(result[checksum], id)
			}
		}
	}
	return result, nil
}

func recordHasChecksum(obj *objects.Record, checksum string) bool {
	for _, candidate := range obj.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), strings.TrimSpace(checksum)) {
			return true
		}
	}
	return false
}

func recordInScope(obj *objects.Record, organization, project string) bool {
	projects := clientaccess.ControlledAccessToAuthzMap(objects.AccessResources(obj))[strings.TrimSpace(organization)]
	if strings.TrimSpace(project) == "" || len(projects) == 0 {
		return len(projects) > 0
	}
	for _, candidate := range projects {
		if candidate == project {
			return true
		}
	}
	return false
}
