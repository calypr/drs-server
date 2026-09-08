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

// RegisterCandidates materializes DRS candidates, persists them through the
// existing registration policy, and rereads each durable record with read
// authorization in request order.
func (s *Service) RegisterCandidates(ctx context.Context, candidates []objectmodel.Candidate) ([]objectmodel.Record, error) {
	prepared := make([]objectmodel.Record, 0, len(candidates))
	for _, candidate := range candidates {
		record, err := objectmodel.CandidateToRecord(candidate, s.writeNow())
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, record)
	}
	if err := s.RegisterObjects(ctx, prepared); err != nil {
		return nil, err
	}

	registered := make([]objectmodel.Record, 0, len(prepared))
	for _, record := range prepared {
		read, err := s.GetObject(ctx, string(record.Id), objectMethodRead)
		if err != nil {
			return nil, err
		}
		registered = append(registered, *read)
	}
	return registered, nil
}

// AccessMethodUpdate keeps the caller's ordered update sequence separate from
// the persistence port's map-shaped bulk operation.
type AccessMethodUpdate struct {
	ObjectID string
	Methods  []objectmodel.AccessMethod
}

// UpdateAccessMethodsAndRead updates one record and returns its durable,
// read-authorized representation.
func (s *Service) UpdateAccessMethodsAndRead(ctx context.Context, objectID string, methods []objectmodel.AccessMethod) (*objectmodel.Record, error) {
	if err := s.UpdateObjectAccessMethods(ctx, objectID, methods); err != nil {
		return nil, err
	}
	return s.GetObject(ctx, objectID, objectMethodRead)
}

// BulkUpdateAccessMethodsAndRead retains first-seen response order and last
// duplicate update wins while using the existing map-shaped writer once.
func (s *Service) BulkUpdateAccessMethodsAndRead(ctx context.Context, updates []AccessMethodUpdate) ([]objectmodel.Record, error) {
	if len(updates) == 0 {
		return nil, nil
	}

	orderedIDs := make([]string, 0, len(updates))
	latest := make(map[string][]objectmodel.AccessMethod, len(updates))
	for _, update := range updates {
		if _, seen := latest[update.ObjectID]; !seen {
			orderedIDs = append(orderedIDs, update.ObjectID)
		}
		latest[update.ObjectID] = update.Methods
	}

	if err := s.BulkUpdateAccessMethods(ctx, latest); err != nil {
		return nil, err
	}

	read := make([]objectmodel.Record, 0, len(orderedIDs))
	for _, objectID := range orderedIDs {
		obj, err := s.GetObject(ctx, objectID, objectMethodRead)
		if err != nil {
			return nil, err
		}
		read = append(read, *obj)
	}
	return read, nil
}
