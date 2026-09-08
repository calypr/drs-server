package records

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/apigen/errorapi"
	objectmodel "github.com/calypr/syfon/internal/objects"
	"github.com/calypr/syfon/internal/persistence/sqlite"
	"github.com/calypr/syfon/internal/persistence/store"
)

type updateOperationStore struct {
	*store.Store
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
	db, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &updateOperationStore{Store: db, object: objectmodel.Record{Id: "object", Size: 7}}
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

func TestUpdateRecordInScopeUsesServiceClockAndNormalizesScope(t *testing.T) {
	db, err := sqlite.NewSqliteDB(":memory:", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := &updateOperationStore{Store: db, object: objectmodel.Record{Id: "object", Size: 7}}
	service := newUpdateOperationService(store)
	fixed := time.Date(2026, time.September, 8, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	merged, err := service.UpdateRecordInScope(context.Background(), "object", objectmodel.Scope{
		Organization: "org",
		Project:      "project",
	}, objectmodel.Record{}, nil)
	if err != nil {
		t.Fatalf("UpdateRecordInScope() error = %v", err)
	}
	if merged.ControlledAccess == nil || !slices.Contains(*merged.ControlledAccess, "/organization/org/project/project") {
		t.Fatalf("scope was not normalized: %+v", merged.ControlledAccess)
	}
	if merged.UpdatedTime == nil || !merged.UpdatedTime.Equal(fixed) {
		t.Fatalf("updated time = %v, want %v", merged.UpdatedTime, fixed)
	}
}
