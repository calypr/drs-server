package records

import (
	"context"
	"time"

	objectmodel "github.com/calypr/syfon/internal/objects"

	"github.com/calypr/syfon/apigen/errorapi"
)

func (s *Service) UpdateRecord(ctx context.Context, id string, update objectmodel.Record, explicitSize *int64, now time.Time) (objectmodel.Record, error) {
	existing, err := s.GetObject(ctx, id, objectMethodUpdate)
	if err != nil {
		return objectmodel.Record{}, err
	}
	if explicitSize != nil && *explicitSize != existing.Size {
		return objectmodel.Record{}, errorapi.ErrObjectSizeImmutable
	}
	if incomingSHA, ok := objectmodel.CanonicalSHA256(update.Checksums); ok {
		storedSHA, stored := objectmodel.CanonicalSHA256(existing.Checksums)
		if stored && incomingSHA != storedSHA {
			return objectmodel.Record{}, errorapi.ErrObjectChecksumImmutable
		}
	}
	merged, err := objectmodel.MergeRecordUpdate(*existing, update, id, now.UTC())
	if err != nil {
		return objectmodel.Record{}, err
	}
	if err := s.store.ReplaceObjects(ctx, []objectmodel.Record{merged}); err != nil {
		return objectmodel.Record{}, err
	}
	return merged, nil
}

func (s *Service) ReplaceObjects(ctx context.Context, objs []objectmodel.Record) error {
	return s.store.ReplaceObjects(ctx, objs)
}
