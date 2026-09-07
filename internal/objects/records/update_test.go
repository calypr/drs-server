package records

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/syfon/internal/faults"
	objectmodel "github.com/calypr/syfon/internal/objects"
)

type updateOperationReader struct {
	object objectmodel.Record
}

func (r *updateOperationReader) GetObject(context.Context, string) (*objectmodel.Record, error) {
	object := r.object
	return &object, nil
}

func (r *updateOperationReader) GetBulkObjects(context.Context, []string) ([]objectmodel.Record, error) {
	return []objectmodel.Record{r.object}, nil
}

type updateOperationWriter struct {
	replaced []objectmodel.Record
}

func (w *updateOperationWriter) DeleteObject(context.Context, string) error              { return nil }
func (w *updateOperationWriter) CreateObject(context.Context, *objectmodel.Record) error { return nil }
func (w *updateOperationWriter) BulkDeleteObjects(context.Context, []string) error {
	return nil
}
func (w *updateOperationWriter) RegisterObjects(context.Context, []objectmodel.Record) error {
	return nil
}
func (w *updateOperationWriter) ReplaceObjects(_ context.Context, records []objectmodel.Record) error {
	w.replaced = append([]objectmodel.Record(nil), records...)
	return nil
}

func newUpdateOperationService(reader RecordReader, writer RecordWriter) *mutationService {
	return &mutationService{
		queryService: &queryService{recordReader: reader},
		recordWriter: writer,
	}
}

func TestUpdateRecordPreservesSizePresenceAndReplacement(t *testing.T) {
	name := "updated.txt"
	reader := &updateOperationReader{object: objectmodel.Record{Id: "object", Size: 7}}
	writer := &updateOperationWriter{}
	service := newUpdateOperationService(reader, writer)
	now := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)

	merged, err := service.UpdateRecord(context.Background(), "object", objectmodel.Record{Name: &name}, nil, now)
	if err != nil {
		t.Fatalf("UpdateRecord() error = %v", err)
	}
	if merged.Id != "object" || merged.Size != 7 || merged.Name == nil || *merged.Name != name || !merged.UpdatedTime.Equal(now) {
		t.Fatalf("merged record = %+v", merged)
	}
	if len(writer.replaced) != 1 || writer.replaced[0].Id != "object" || writer.replaced[0].Size != 7 {
		t.Fatalf("replacement = %+v", writer.replaced)
	}

	explicitZero := int64(0)
	_, err = service.UpdateRecord(context.Background(), "object", objectmodel.Record{}, &explicitZero, now)
	if !errors.Is(err, faults.ErrConflict) || !strings.Contains(err.Error(), "object size is immutable") {
		t.Fatalf("explicit zero size error = %v", err)
	}
	if len(writer.replaced) != 1 {
		t.Fatalf("conflicting update replaced object: %+v", writer.replaced)
	}
}
