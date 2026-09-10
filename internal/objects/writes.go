package objects

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
)

func materializeRecordTime(record drs.DrsObject, now time.Time) drs.DrsObject {
	if record.CreatedTime.IsZero() {
		record.CreatedTime = now
	}
	if record.UpdatedTime == nil || record.UpdatedTime.IsZero() {
		updated := record.CreatedTime
		record.UpdatedTime = &updated
	}
	return record
}

// CreateRecords returns the submitted records after scope and time preparation.
func (s *Service) CreateRecords(ctx context.Context, inputs []RecordInput) ([]drs.DrsObject, error) {
	now := time.Now().UTC()
	prepared := make([]drs.DrsObject, len(inputs))
	for i, input := range inputs {
		record, err := enforceCanonicalProjectScope(input.Record, input.Scope.Organization, input.Scope.Project)
		if err != nil {
			return nil, err
		}
		prepared[i] = materializeRecordTime(record, now)
	}
	if err := s.RegisterObjects(ctx, prepared); err != nil {
		return nil, err
	}
	return prepared, nil
}

// RegisterCandidates materializes DRS candidates, persists them through the
// existing registration policy, and rereads each durable record with read
// authorization in request order.
func (s *Service) RegisterCandidates(ctx context.Context, candidates []drs.DrsObjectCandidate) ([]drs.DrsObject, error) {
	prepared := make([]drs.DrsObject, 0, len(candidates))
	for _, candidate := range candidates {
		record, err := MaterializeCandidate(candidate, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		prepared = append(prepared, record)
	}
	if err := s.RegisterObjects(ctx, prepared); err != nil {
		return nil, err
	}

	registered := make([]drs.DrsObject, 0, len(prepared))
	for _, record := range prepared {
		read, err := s.GetObject(ctx, record.Id, objectMethodRead)
		if err != nil {
			return nil, err
		}
		registered = append(registered, *read)
	}
	return registered, nil
}

// UpdateAccessMethodsAndRead updates one record and returns its durable,
// read-authorized representation.
func (s *Service) UpdateAccessMethodsAndRead(ctx context.Context, objectID string, methods []drs.AccessMethod) (*drs.DrsObject, error) {
	obj, err := s.store.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if err := requireAllObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return nil, err
	}
	if err := s.store.UpdateObjectAccessMethods(ctx, objectID, methods); err != nil {
		return nil, err
	}
	return s.GetObject(ctx, objectID, objectMethodRead)
}

// BulkUpdateAccessMethodsAndRead retains first-seen response order; the last
// update for a duplicate object ID wins.
func (s *Service) BulkUpdateAccessMethodsAndRead(ctx context.Context, updates []drs.AccessMethodUpdate) ([]drs.DrsObject, error) {
	if len(updates) == 0 {
		return nil, nil
	}

	orderedIDs := make([]string, 0, len(updates))
	latest := make(map[string][]drs.AccessMethod, len(updates))
	for _, update := range updates {
		if _, seen := latest[update.ObjectId]; !seen {
			orderedIDs = append(orderedIDs, update.ObjectId)
		}
		latest[update.ObjectId] = update.AccessMethods
	}

	objects, err := s.store.GetBulkObjects(ctx, orderedIDs)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*drs.DrsObject, len(objects))
	for i := range objects {
		byID[objects[i].Id] = &objects[i]
	}
	for _, objectID := range orderedIDs {
		obj, ok := byID[objectID]
		if !ok {
			return nil, errorapi.ErrObjectNotFound
		}
		if err := requireAllObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
			return nil, err
		}
	}
	if err := s.store.BulkUpdateAccessMethods(ctx, latest); err != nil {
		return nil, err
	}

	read := make([]drs.DrsObject, 0, len(orderedIDs))
	for _, objectID := range orderedIDs {
		obj, err := s.GetObject(ctx, objectID, objectMethodRead)
		if err != nil {
			return nil, err
		}
		read = append(read, *obj)
	}
	return read, nil
}

func (s *Service) RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) (*drs.DrsObject, error) {
	obj, err := s.store.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if !hasObjectMethod(ctx, obj, objectMethodUpdate, nil) {
		return nil, errorapi.ErrAccessDenied
	}

	normalized := clientaccess.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return nil, fmt.Errorf("resource is required")
	}
	resource = normalized[0]

	resources := AccessResources(obj)
	found := false
	for _, existing := range resources {
		if strings.TrimSpace(existing) == resource {
			found = true
		}
	}
	if !found {
		return nil, errorapi.ErrObjectNotFound
	}

	if err := s.store.RemoveObjectControlledAccess(ctx, objectID, resource); err != nil {
		return nil, err
	}

	updated, err := s.store.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (s *Service) RegisterObjects(ctx context.Context, objs []drs.DrsObject) error {
	if err := s.validateExistingContentRead(ctx, objs); err != nil {
		return err
	}
	if err := bulkObjectMethodError(ctx, objs, objectMethodCreate, nil); err != nil {
		return err
	}
	return s.store.RegisterObjects(ctx, objs)
}

func (s *Service) validateExistingContentRead(ctx context.Context, objs []drs.DrsObject) error {
	seen := make(map[string]struct{})
	hashes := make([]string, 0, len(objs))
	for i := range objs {
		sha, ok := CanonicalSHA256(objs[i].Checksums)
		if !ok || sha == "" {
			continue
		}
		if _, done := seen[sha]; done {
			continue
		}
		seen[sha] = struct{}{}
		hashes = append(hashes, sha)
	}
	existingByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return err
	}
	allExisting := make([]drs.DrsObject, 0)
	for _, existing := range existingByChecksum {
		allExisting = append(allExisting, existing...)
	}
	policy, err := s.publicReadPolicy(ctx, allExisting)
	if err != nil {
		return err
	}
	for _, sha := range hashes {
		existing := existingByChecksum[sha]
		for j := range existing {
			if hasObjectMethod(ctx, &existing[j], objectMethodRead, policy) {
				continue
			}
			return errorapi.ErrAccessDenied
		}
	}
	return nil
}

func (s *Service) UpdateRecord(ctx context.Context, id string, input RecordInput) (drs.DrsObject, error) {
	update, err := enforceCanonicalProjectScope(input.Record, input.Scope.Organization, input.Scope.Project)
	if err != nil {
		return drs.DrsObject{}, err
	}
	existing, err := s.GetObject(ctx, id, objectMethodUpdate)
	if err != nil {
		return drs.DrsObject{}, err
	}
	if input.ExplicitSize != nil && *input.ExplicitSize != existing.Size {
		return drs.DrsObject{}, errorapi.ErrObjectSizeImmutable
	}
	if incomingSHA, ok := CanonicalSHA256(update.Checksums); ok {
		storedSHA, stored := CanonicalSHA256(existing.Checksums)
		if stored && incomingSHA != storedSHA {
			return drs.DrsObject{}, errorapi.ErrObjectChecksumImmutable
		}
	}
	merged := *existing
	merged.Id = id
	updatedAt := time.Now().UTC()
	merged.UpdatedTime = &updatedAt
	if update.Name != nil {
		name := CleanToBasename(*update.Name)
		if name == "" {
			merged.Name = nil
		} else {
			merged.Name = objectStringPtr(name)
		}
	}
	if update.Description != nil {
		merged.Description = update.Description
	}
	if update.Version != nil {
		merged.Version = update.Version
	}
	if update.Aliases != nil {
		merged.Aliases = update.Aliases
	}
	if update.ControlledAccess != nil {
		merged.ControlledAccess = update.ControlledAccess
	}
	if update.AccessMethods != nil {
		merged.AccessMethods = update.AccessMethods
	}
	if update.Checksums != nil {
		merged.Checksums = mergeAdditionalChecksums(existing.Checksums, update.Checksums)
	}
	if err := s.store.ReplaceObjects(ctx, []drs.DrsObject{merged}); err != nil {
		return drs.DrsObject{}, err
	}
	return merged, nil
}

// BulkOverwriteResult summarizes a project-scoped, source-wins metadata copy.
type BulkOverwriteResult struct {
	Created         int
	Replaced        int
	DIDMatched      int
	ChecksumMatched int
}

// BulkOverwriteObjects replaces records from one project snapshot without
// canonicalizing checksum siblings. A checksum can therefore exist in more
// than one project, while still identifying an existing record in this scope.
func (s *Service) BulkOverwriteObjects(ctx context.Context, organization, project string, candidates []drs.DrsObject) (BulkOverwriteResult, error) {
	var result BulkOverwriteResult
	if len(candidates) == 0 {
		return result, nil
	}
	scope, err := NewScope(organization, project)
	if err != nil {
		return result, err
	}
	resource, err := clientaccess.ResourcePath(scope.Organization, scope.Project)
	if err != nil {
		return result, err
	}

	now := time.Now().UTC()
	prepared := make([]drs.DrsObject, len(candidates))
	for i, candidate := range candidates {
		normalized, err := enforceCanonicalProjectScope(candidate, scope.Organization, scope.Project)
		if err != nil {
			return result, err
		}
		prepared[i] = materializeRecordTime(normalized, now)
	}
	candidates = prepared

	byDID := make(map[string]int, len(candidates))
	hashes := make([]string, 0, len(candidates))
	for i := range candidates {
		did := strings.TrimSpace(candidates[i].Id)
		if did == "" {
			return result, fmt.Errorf("record[%d]: did is required", i)
		}
		if _, ok := byDID[did]; ok {
			return result, fmt.Errorf("%w: duplicate source did %q", errorapi.ErrBulkOverwriteConflict, did)
		}
		byDID[did] = i
		if sha, ok := CanonicalSHA256(candidates[i].Checksums); ok {
			hashes = append(hashes, sha)
		}
	}

	checksumMatches, err := s.store.ListScopedObjectIDsByChecksums(ctx, organization, project, uniqueOverwriteStrings(hashes))
	if err != nil {
		return result, err
	}
	ids := make([]string, 0, len(candidates))
	for did := range byDID {
		ids = append(ids, did)
	}
	for _, matches := range checksumMatches {
		ids = append(ids, matches...)
	}
	existingList, err := s.store.GetBulkObjects(ctx, uniqueOverwriteStrings(ids))
	if err != nil {
		return result, err
	}
	existing := make(map[string]drs.DrsObject, len(existingList))
	for _, obj := range existingList {
		existing[obj.Id] = obj
	}

	resolved := make([]drs.DrsObject, len(candidates))
	usedTargets := make(map[string]string, len(candidates))
	for i, candidate := range candidates {
		sourceDID := candidate.Id
		canonicalID, aliasErr := s.store.ResolveObjectAlias(ctx, sourceDID)
		if aliasErr == nil && canonicalID != sourceDID {
			return result, fmt.Errorf("%w: target DID %q is an alias for %q", errorapi.ErrBulkOverwriteConflict, sourceDID, canonicalID)
		}
		if aliasErr != nil && !errorapi.IsNotFoundError(aliasErr) {
			return result, aliasErr
		}
		targetDID := sourceDID
		matched := false
		if current, ok := existing[sourceDID]; ok {
			if !containsResource(AccessResources(&current), resource) {
				return result, fmt.Errorf("%w: target DID %q is outside project %s", errorapi.ErrBulkOverwriteConflict, sourceDID, resource)
			}
			matched = true
			result.DIDMatched++
		} else if sha, ok := CanonicalSHA256(candidate.Checksums); ok {
			matches := uniqueOverwriteStrings(checksumMatches[sha])
			switch len(matches) {
			case 0:
			case 1:
				targetDID = matches[0]
				matched = true
				result.ChecksumMatched++
			default:
				return result, fmt.Errorf("%w: target project already has multiple records for sha256 %q: %s", errorapi.ErrBulkOverwriteConflict, sha, strings.Join(matches, ", "))
			}
		}
		if prior, ok := usedTargets[targetDID]; ok {
			return result, fmt.Errorf("%w: source records %q and %q resolve to target DID %q", errorapi.ErrBulkOverwriteConflict, prior, sourceDID, targetDID)
		}
		usedTargets[targetDID] = sourceDID
		candidate.Id = targetDID
		candidate.SelfUri = "drs://" + targetDID
		resolved[i] = candidate
		if matched {
			if !access.HasObjectMethodAccess(ctx, objectMethodUpdate, []string{resource}) {
				return result, errorapi.ErrAccessDenied
			}
			current := existing[targetDID]
			if err := requireAllObjectMethod(ctx, &current, objectMethodUpdate); err != nil {
				return result, err
			}
			if !hasObjectMethod(ctx, &candidate, objectMethodUpdate, nil) {
				return result, errorapi.ErrAccessDenied
			}
			result.Replaced++
		} else {
			if !access.HasObjectMethodAccess(ctx, objectMethodCreate, []string{resource}) {
				return result, errorapi.ErrAccessDenied
			}
			if !hasObjectMethod(ctx, &candidate, objectMethodCreate, nil) {
				return result, errorapi.ErrAccessDenied
			}
			result.Created++
		}
	}

	if err := s.store.RegisterObjects(ctx, resolved); err != nil {
		return BulkOverwriteResult{}, err
	}
	return result, nil
}

func containsResource(resources []string, target string) bool {
	for _, resource := range resources {
		if strings.TrimSpace(resource) == target {
			return true
		}
	}
	return false
}

func uniqueOverwriteStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
