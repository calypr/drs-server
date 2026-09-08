package records

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	objectmodel "github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
)

type updateOperationStore struct {
	*sqlite.SqliteDB
	object   objectmodel.Record
	replaced []objectmodel.Record
}

func (r *updateOperationStore) GetObject(context.Context, string) (*objectmodel.Record, error) {
	object := r.object
	return &object, nil
}

func (r *updateOperationStore) GetBulkObjects(context.Context, []string) ([]objectmodel.Record, error) {
	return []objectmodel.Record{r.object}, nil
}

func (w *updateOperationStore) ReplaceObjects(_ context.Context, records []objectmodel.Record) error {
	w.replaced = append([]objectmodel.Record(nil), records...)
	return nil
}

func newUpdateOperationService(store ObjectStore) *Service {
	return NewService(store)
}

func TestUpdateRecordPreservesSizePresenceAndReplacement(t *testing.T) {
	name := "updated.txt"
	db, err := sqlite.NewSqliteDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	store := &updateOperationStore{SqliteDB: db, object: objectmodel.Record{Id: "object", Size: 7}}
	service := newUpdateOperationService(store)
	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)

	merged, err := service.UpdateRecord(context.Background(), "object", objectmodel.Record{Name: &name}, nil, now)
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if merged.Id != "object" || merged.Size != 7 || merged.Name == nil || *merged.Name != name || !merged.UpdatedTime.Equal(now) {
		t.Fatalf("merged record = %+v", merged)
	}
	if len(store.replaced) != 1 || store.replaced[0].Id != "object" || store.replaced[0].Size != 7 {
		t.Fatalf("replacement = %+v", store.replaced)
	}

	explicitZero := int64(0)
	_, err = service.UpdateRecord(context.Background(), "object", objectmodel.Record{}, &explicitZero, now)
	if !errors.Is(err, errorapi.ErrConflict) || !strings.Contains(err.Error(), "object size is immutable") {
		t.Fatalf("explicit zero size error = %v", err)
	}
	if len(store.replaced) != 1 {
		t.Fatalf("conflicting update replaced object: %+v", store.replaced)
	}
}
