package records

import (
	"context"
	"strings"

	objectmodel "github.com/calypr/syfon/internal/objects"

	"github.com/calypr/syfon/apigen/errorapi"
)

// GetObject retrieves the prepared canonical record identified by ID, alias,
// or checksum and validates access.  Callers that need the identity and
// physical-record distinction should use GetCanonicalContent.
func (s *Service) GetObject(ctx context.Context, ident string, requiredMethod string) (*objectmodel.Record, error) {
	view, err := s.GetCanonicalContent(ctx, ident, requiredMethod)
	if err != nil {
		return nil, err
	}
	return &view.Record, nil
}

// GetCanonicalContent resolves a physical lookup to the prepared same-content
// view.  The returned ContentID is checksum-derived and Records retains the
// physical rows used to build the merged objectmodel.Record.
func (s *Service) GetCanonicalContent(ctx context.Context, ident string, requiredMethod string) (*objectmodel.CanonicalContent, error) {
	if strings.TrimSpace(ident) == "" {
		return nil, errorapi.ErrObjectNotFound
	}

	checksum, checksumIdent := objectmodel.NormalizeSHA256Query(ident)
	if checksumIdent {
		view, found, err := s.canonicalContentForChecksum(ctx, checksum, requiredMethod)
		if err != nil {
			return nil, err
		}
		if found {
			return view, nil
		}
	}

	if obj, found, err := s.lookupObjectByID(ctx, ident); err != nil {
		return nil, err
	} else if found {
		return s.canonicalContentAndCheckAccess(ctx, obj, requiredMethod)
	}

	if obj, found, err := s.lookupObjectByAlias(ctx, ident); err != nil {
		return nil, err
	} else if found {
		return s.canonicalContentAndCheckAccess(ctx, obj, requiredMethod)
	}

	if !checksumIdent {
		if obj, found, err := s.lookupObjectByChecksum(ctx, ident, requiredMethod); err != nil {
			return nil, err
		} else if found {
			return s.canonicalContentAndCheckAccess(ctx, obj, requiredMethod)
		}
	}

	return nil, errorapi.ErrObjectNotFound
}

func (s *Service) canonicalContentForChecksum(ctx context.Context, checksum, method string) (*objectmodel.CanonicalContent, bool, error) {
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
	view := &objectmodel.CanonicalContent{
		ContentID: objectmodel.ContentID(checksum),
		Record:    family[0],
		Records:   physical,
	}
	if err := requireObjectMethod(ctx, &view.Record, method); err != nil {
		return nil, true, err
	}
	return view, true, nil
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

func (s *Service) canonicalContentAndCheckAccess(ctx context.Context, obj *objectmodel.Record, method string) (*objectmodel.CanonicalContent, error) {
	view, err := s.canonicalContentForObject(ctx, obj)
	if err != nil {
		return nil, err
	}
	if err := requireObjectMethod(ctx, &view.Record, method); err != nil {
		return nil, err
	}
	return view, nil
}

func (s *Service) canonicalContentForObject(ctx context.Context, obj *objectmodel.Record) (*objectmodel.CanonicalContent, error) {
	sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
	if !ok {
		cloned := cloneObject(*obj)
		return &objectmodel.CanonicalContent{Record: cloned, Records: []objectmodel.Record{cloned}}, nil
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
	return &objectmodel.CanonicalContent{ContentID: objectmodel.ContentID(sha), Record: canonical[0], Records: physical}, nil
}
