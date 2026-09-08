package records

import (
	"context"
	"time"

	objectmodel "github.com/calypr/syfon/internal/objects"
)

// writeNow returns the service clock in UTC. The fallback keeps a manually
// constructed Service safe in same-package tests while NewService remains the
// production constructor.
func (s *Service) writeNow() time.Time {
	clock := s.now
	if clock == nil {
		clock = time.Now
	}
	return clock().UTC()
}

func materializeRecordTime(record objectmodel.Record, now time.Time) objectmodel.Record {
	if record.CreatedTime.IsZero() {
		record.CreatedTime = now
	}
	if record.UpdatedTime == nil || record.UpdatedTime.IsZero() {
		updated := record.CreatedTime
		record.UpdatedTime = &updated
	}
	return record
}

// RegisterScopedObjects applies each record's own project scope before the
// existing-content read, create authorization, and single writer call. The
// input slice is updated with materialized values so internal callers can
// return their submitted records without a durable reread.
func (s *Service) RegisterScopedObjects(ctx context.Context, scoped []objectmodel.ScopedRecord) error {
	now := s.writeNow()
	prepared := make([]objectmodel.Record, len(scoped))
	for i := range scoped {
		record, err := objectmodel.EnforceCanonicalProjectScope(
			scoped[i].Record,
			scoped[i].Scope.Organization,
			scoped[i].Scope.Project,
		)
		if err != nil {
			return err
		}
		record = materializeRecordTime(record, now)
		scoped[i].Record = record
		prepared[i] = record
	}
	return s.RegisterObjects(ctx, prepared)
}

// UpdateRecordInScope applies the caller's project scope before the existing
// update authorization, immutable-field checks, merge, and replacement.
func (s *Service) UpdateRecordInScope(ctx context.Context, id string, scope objectmodel.Scope, update objectmodel.Record, explicitSize *int64) (objectmodel.Record, error) {
	normalized, err := objectmodel.EnforceCanonicalProjectScope(update, scope.Organization, scope.Project)
	if err != nil {
		return objectmodel.Record{}, err
	}
	return s.UpdateRecord(ctx, id, normalized, explicitSize, s.writeNow())
}
