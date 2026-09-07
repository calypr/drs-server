package objects

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/syfon/internal/faults"
)

func (m *mutationService) UpdateRecord(ctx context.Context, id string, update Record, explicitSize *int64, now time.Time) (Record, error) {
	existing, err := m.GetObject(ctx, id, objectMethodUpdate)
	if err != nil {
		return Record{}, err
	}
	if explicitSize != nil && *explicitSize != existing.Size {
		return Record{}, fmt.Errorf("%w: object size is immutable", faults.ErrConflict)
	}
	if incomingSHA, ok := CanonicalSHA256(update.Checksums); ok {
		storedSHA, stored := CanonicalSHA256(existing.Checksums)
		if stored && incomingSHA != storedSHA {
			return Record{}, fmt.Errorf("%w: object checksum identity is immutable", faults.ErrConflict)
		}
	}
	merged, err := MergeRecordUpdate(*existing, update, id, now.UTC())
	if err != nil {
		return Record{}, err
	}
	if err := m.recordWriter.ReplaceObjects(ctx, []Record{merged}); err != nil {
		return Record{}, err
	}
	return merged, nil
}
