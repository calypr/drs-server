package records

import (
	"context"
	"fmt"
	"time"

	objectmodel "github.com/calypr/syfon/internal/objects"

	"github.com/calypr/syfon/internal/faults"
)

func (m *mutationService) UpdateRecord(ctx context.Context, id string, update objectmodel.Record, explicitSize *int64, now time.Time) (objectmodel.Record, error) {
	existing, err := m.GetObject(ctx, id, objectMethodUpdate)
	if err != nil {
		return objectmodel.Record{}, err
	}
	if explicitSize != nil && *explicitSize != existing.Size {
		return objectmodel.Record{}, fmt.Errorf("%w: object size is immutable", faults.ErrConflict)
	}
	if incomingSHA, ok := objectmodel.CanonicalSHA256(update.Checksums); ok {
		storedSHA, stored := objectmodel.CanonicalSHA256(existing.Checksums)
		if stored && incomingSHA != storedSHA {
			return objectmodel.Record{}, fmt.Errorf("%w: object checksum identity is immutable", faults.ErrConflict)
		}
	}
	merged, err := objectmodel.MergeRecordUpdate(*existing, update, id, now.UTC())
	if err != nil {
		return objectmodel.Record{}, err
	}
	if err := m.recordWriter.ReplaceObjects(ctx, []objectmodel.Record{merged}); err != nil {
		return objectmodel.Record{}, err
	}
	return merged, nil
}

func (m *mutationService) ReplaceObjects(ctx context.Context, objs []objectmodel.Record) error {
	return m.recordWriter.ReplaceObjects(ctx, objs)
}
