package objects_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
)

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

func newSQLiteDatabase(t *testing.T) *store.Store {
	t.Helper()
	database, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatalf("create in-memory SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

type objectTestStore struct {
	objects.ObjectStore
	Objects map[string]*drs.DrsObject
	Aliases map[string]string
}

func (f *objectTestStore) GetObject(_ context.Context, id string) (*drs.DrsObject, error) {
	if canonicalID := f.Aliases[id]; canonicalID != "" {
		id = canonicalID
	}
	obj, ok := f.Objects[id]
	if !ok {
		return nil, fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return cloneObjectTestRecord(obj), nil
}

func (f *objectTestStore) GetBulkObjects(_ context.Context, ids []string) ([]drs.DrsObject, error) {
	result := make([]drs.DrsObject, 0, len(ids))
	for _, id := range ids {
		if obj, ok := f.Objects[id]; ok {
			result = append(result, *cloneObjectTestRecord(obj))
		}
	}
	return result, nil
}

func (f *objectTestStore) RegisterObjects(_ context.Context, records []drs.DrsObject) error {
	if f.Objects == nil {
		f.Objects = make(map[string]*drs.DrsObject)
	}
	for i := range records {
		copyObj := *cloneObjectTestRecord(&records[i])
		f.Objects[copyObj.Id] = &copyObj
	}
	return nil
}

func (f *objectTestStore) BulkDeleteObjects(_ context.Context, ids []string) error {
	for _, id := range ids {
		delete(f.Objects, id)
	}
	return nil
}

func (f *objectTestStore) CreateObjectAlias(_ context.Context, aliasID, canonicalID string) error {
	if _, ok := f.Objects[canonicalID]; !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	if f.Aliases == nil {
		f.Aliases = make(map[string]string)
	}
	f.Aliases[aliasID] = canonicalID
	return nil
}

func (f *objectTestStore) ResolveObjectAlias(_ context.Context, aliasID string) (string, error) {
	canonicalID, ok := f.Aliases[aliasID]
	if !ok {
		return "", fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	return canonicalID, nil
}

func (f *objectTestStore) GetObjectsByChecksums(_ context.Context, checksums []string) (map[string][]drs.DrsObject, error) {
	result := make(map[string][]drs.DrsObject, len(checksums))
	for _, checksum := range checksums {
		for _, obj := range f.Objects {
			if recordHasChecksum(obj, checksum) {
				result[checksum] = append(result[checksum], *cloneObjectTestRecord(obj))
			}
		}
	}
	return result, nil
}

func (f *objectTestStore) GetPublicReadByIDs(_ context.Context, _ []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (f *objectTestStore) UpdateObjectAccessMethods(_ context.Context, id string, methods []drs.AccessMethod) error {
	record, ok := f.Objects[id]
	if !ok {
		return fmt.Errorf("%w: object not found", errorapi.ErrNotFound)
	}
	copyMethods := append([]drs.AccessMethod(nil), methods...)
	record.AccessMethods = &copyMethods
	return nil
}

func (f *objectTestStore) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]drs.AccessMethod) error {
	for id, methods := range updates {
		if err := f.UpdateObjectAccessMethods(ctx, id, methods); err != nil {
			return err
		}
	}
	return nil
}

func cloneObjectTestRecord(obj *drs.DrsObject) *drs.DrsObject {
	copyObj := *obj
	if obj.AccessMethods != nil {
		methods := append([]drs.AccessMethod(nil), (*obj.AccessMethods)...)
		copyObj.AccessMethods = &methods
	}
	copyObj.Checksums = append([]drs.Checksum(nil), obj.Checksums...)
	return &copyObj
}

func (f *objectTestStore) ListScopedObjectIDsByChecksums(_ context.Context, organization, project string, checksums []string) (map[string][]string, error) {
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

func (f *objectTestStore) ListObjectIDsByScope(_ context.Context, organization, project string) ([]string, error) {
	ids := make([]string, 0, len(f.Objects))
	for id, obj := range f.Objects {
		if recordInScope(obj, organization, project) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func recordHasChecksum(obj *drs.DrsObject, checksum string) bool {
	for _, candidate := range obj.Checksums {
		if strings.EqualFold(strings.TrimSpace(candidate.Checksum), strings.TrimSpace(checksum)) {
			return true
		}
	}
	return false
}

func recordInScope(obj *drs.DrsObject, organization, project string) bool {
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

func TestCollapseProjectChecksumDuplicatesReturnsOneCanonicalRecord(t *testing.T) {
	resource := "/programs/org/projects/project"
	controlled := []string{resource}
	sha := strings.Repeat("a", 64)
	oldName := "old.txt"
	newName := "new.txt"
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	store := &objectTestStore{Objects: map[string]*drs.DrsObject{
		"old-id": {
			Id:               "old-id",
			Name:             &oldName,
			CreatedTime:      created,
			UpdatedTime:      &created,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &controlled,
		},
		"new-id": {
			Id:               "new-id",
			Name:             &newName,
			CreatedTime:      updated,
			UpdatedTime:      &updated,
			Checksums:        []drs.Checksum{{Type: "sha256", Checksum: sha}},
			ControlledAccess: &controlled,
		},
	}}
	service := objects.NewService(store)
	ctx := buildGen3Context(map[string]map[string]bool{resource: {
		"create": true,
		"read":   true,
		"update": true,
		"delete": true,
	}})

	collapsed, err := service.CollapseProjectChecksumDuplicates(ctx, "org", "project")
	if err != nil {
		t.Fatalf("CollapseProjectChecksumDuplicates: %v", err)
	}
	if collapsed != 1 {
		t.Fatalf("collapsed=%d, want 1", collapsed)
	}
	if len(store.Objects) != 1 || store.Objects["old-id"] == nil {
		t.Fatalf("objects=%v, want only old-id", store.Objects)
	}
	if got := store.Aliases["new-id"]; got != "old-id" {
		t.Fatalf("new-id alias=%q, want old-id", got)
	}
	if got := store.Objects["old-id"].Name; got == nil || *got != newName {
		t.Fatalf("canonical name=%v, want %q", got, newName)
	}
}
