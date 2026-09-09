package records

import (
	"context"
	"strings"

	objectmodel "github.com/calypr/syfon/internal/objects"

	"github.com/calypr/syfon/apigen/errorapi"
)

// GetObject retrieves the prepared canonical record identified by ID, alias,
// or checksum and validates access.
func (s *Service) GetObject(ctx context.Context, ident string, requiredMethod string) (*objectmodel.Record, error) {
	if strings.TrimSpace(ident) == "" {
		return nil, errorapi.ErrObjectNotFound
	}

	checksum, checksumIdent := objectmodel.NormalizeSHA256Query(ident)
	if checksumIdent {
		obj, found, err := s.canonicalRecordForChecksum(ctx, checksum, requiredMethod)
		if err != nil {
			return nil, err
		}
		if found {
			return obj, nil
		}
	}

	if obj, found, err := s.lookupObjectByID(ctx, ident); err != nil {
		return nil, err
	} else if found {
		return s.canonicalRecordAndCheckAccess(ctx, obj, requiredMethod)
	}

	if obj, found, err := s.lookupObjectByAlias(ctx, ident); err != nil {
		return nil, err
	} else if found {
		return s.canonicalRecordAndCheckAccess(ctx, obj, requiredMethod)
	}

	if !checksumIdent {
		if obj, found, err := s.lookupObjectByChecksum(ctx, ident, requiredMethod); err != nil {
			return nil, err
		} else if found {
			return s.canonicalRecordAndCheckAccess(ctx, obj, requiredMethod)
		}
	}

	return nil, errorapi.ErrObjectNotFound
}

func (s *Service) canonicalRecordForChecksum(ctx context.Context, checksum, method string) (*objectmodel.Record, bool, error) {
	physical, err := s.store.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		return nil, false, err
	}
	physical = objectsWithSHA256(physical, checksum)
	if len(physical) == 0 {
		return nil, false, nil
	}
	family := canonicalizeContentObjects(physical)
	if len(family) == 0 {
		return nil, false, nil
	}
	obj := &family[0]
	if err := requireObjectMethod(ctx, obj, method); err != nil {
		return nil, true, err
	}
	return obj, true, nil
}

func (s *Service) lookupObjectByChecksum(ctx context.Context, ident string, requiredMethod string) (*objectmodel.Record, bool, error) {
	byChecksum, err := s.GetObjectsByChecksum(ctx, ident, requiredMethod)
	if err != nil {
		return nil, false, err
	}
	if len(byChecksum) == 0 {
		if strings.TrimSpace(requiredMethod) != "" {
			allMatches, err := s.GetObjectsByChecksum(ctx, ident, "")
			if err != nil {
				return nil, false, err
			}
			if len(allMatches) > 0 {
				return nil, true, errorapi.ErrAccessDenied
			}
		}
		return nil, false, nil
	}
	return &byChecksum[0], true, nil
}

func (s *Service) lookupObjectByID(ctx context.Context, ident string) (*objectmodel.Record, bool, error) {
	obj, err := s.store.GetObject(ctx, ident)
	if err == nil {
		return obj, true, nil
	}
	if errorapi.IsNotFoundError(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func (s *Service) lookupObjectByAlias(ctx context.Context, ident string) (*objectmodel.Record, bool, error) {
	canonicalID, aliasErr := s.store.ResolveObjectAlias(ctx, ident)
	if aliasErr != nil {
		if errorapi.IsNotFoundError(aliasErr) {
			return nil, false, nil
		}
		return nil, false, aliasErr
	}
	if strings.TrimSpace(canonicalID) == "" {
		return nil, false, nil
	}

	obj, err := s.store.GetObject(ctx, canonicalID)
	if err != nil {
		if errorapi.IsNotFoundError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return obj, true, nil
}

func (s *Service) canonicalRecordAndCheckAccess(ctx context.Context, obj *objectmodel.Record, method string) (*objectmodel.Record, error) {
	record, err := s.canonicalRecordForObject(ctx, obj)
	if err != nil {
		return nil, err
	}
	if err := requireObjectMethod(ctx, record, method); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Service) canonicalRecordForObject(ctx context.Context, obj *objectmodel.Record) (*objectmodel.Record, error) {
	sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
	if !ok {
		cloned := cloneObject(*obj)
		return &cloned, nil
	}
	siblings, err := s.store.GetObjectsByChecksum(ctx, sha)
	if err != nil {
		return nil, err
	}
	physical := objectsWithSHA256(siblings, sha)
	canonical := canonicalizeContentObjects(physical)
	if len(canonical) == 0 {
		return nil, errorapi.ErrObjectNotFound
	}
	return &canonical[0], nil
}

func (s *Service) GetObjectsByChecksums(ctx context.Context, hashes []string, requiredMethod string) (map[string][]objectmodel.Record, error) {
	objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	filtered := make(map[string][]objectmodel.Record, len(objectsByChecksum))
	for checksum, objects := range objectsByChecksum {
		matching := objectsWithSHA256(objects, checksum)
		filtered[checksum] = filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod)
	}
	return filtered, nil
}

func (s *Service) GetObjectsByChecksum(ctx context.Context, checksum string, requiredMethod string) ([]objectmodel.Record, error) {
	objects, err := s.store.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		return nil, err
	}
	matching := objectsWithSHA256(objects, checksum)
	return filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod), nil
}

func (s *Service) GetBulkObjects(ctx context.Context, ids []string, requiredMethod string) ([]objectmodel.Record, error) {
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(objects))
	for _, obj := range objects {
		if sha, ok := objectmodel.CanonicalSHA256(obj.Checksums); ok {
			hashes = append(hashes, sha)
		}
	}
	siblingsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	canonical := make([]objectmodel.Record, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		resolved := cloneObject(obj)
		if sha, ok := objectmodel.CanonicalSHA256(obj.Checksums); ok {
			matching := objectsWithSHA256(siblingsByChecksum[sha], sha)
			family := canonicalizeContentObjects(matching)
			if len(family) == 0 {
				return nil, errorapi.ErrObjectNotFound
			}
			resolved = family[0]
		}
		if _, ok := seen[string(string(resolved.Id))]; ok {
			continue
		}
		seen[string(string(resolved.Id))] = struct{}{}
		canonical = append(canonical, resolved)
	}
	return filterObjectsByMethod(ctx, canonical, requiredMethod), nil
}
