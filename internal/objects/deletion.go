package objects

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
)

func (s *Service) DeleteBulkByScope(ctx context.Context, organization, project string) (int, error) {
	if err := requireScopeMethod(ctx, organization, project, objectMethodDelete); err != nil {
		return 0, err
	}

	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	resources, _, restrictToResources := objectMethodResourceFilter(ctx, objectMethodDelete)
	if optimized, err := s.store.ListObjectIDsByScopeAndResources(ctx, organization, project, resources, restrictToResources); err == nil {
		ids = optimized
	}

	toDelete, err := s.deletableObjectIDsForMethod(ctx, ids, false)
	if err != nil {
		return 0, err
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return 0, err
	}
	return s.store.RemoveObjectControlledAccessBulk(ctx, toDelete, resource)
}

type DeleteOptions struct {
	DeleteStorageData bool
}

func (s *Service) DeleteObject(ctx context.Context, id string, opts DeleteOptions) error {
	if opts.DeleteStorageData {
		return fmt.Errorf("%w: physical storage deletion is not atomic with catalog mutation", errorapi.ErrConflict)
	}
	obj, err := s.store.GetObject(ctx, id)
	if err != nil {
		return err
	}
	if err := requireAllObjectMethod(ctx, obj, objectMethodDelete); err != nil {
		return err
	}
	return s.store.DeleteObject(ctx, id)
}

func (s *Service) BulkDeleteObjects(ctx context.Context, ids []string, opts DeleteOptions) error {
	if opts.DeleteStorageData {
		return fmt.Errorf("%w: physical storage deletion is not atomic with catalog mutation", errorapi.ErrConflict)
	}
	toDelete, err := s.deletablePhysicalObjectIDsForBulk(ctx, ids)
	if err != nil {
		return err
	}
	if len(toDelete) == 0 {
		return nil
	}
	return s.store.BulkDeleteObjects(ctx, toDelete)
}

func (s *Service) deletablePhysicalObjectIDsForBulk(ctx context.Context, ids []string) ([]string, error) {
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*Record, len(objects))
	for i := range objects {
		byID[string(objects[i].Id)] = &objects[i]
	}

	toDelete := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		objectID := strings.TrimSpace(rawID)
		if objectID == "" {
			continue
		}
		obj, ok := byID[objectID]
		if !ok {
			canonicalID, resolveErr := s.store.ResolveObjectAlias(ctx, objectID)
			if resolveErr == nil && strings.TrimSpace(canonicalID) != "" {
				return nil, fmt.Errorf("%w: bulk delete requires a physical object UUID; %q is an alias for %q", errorapi.ErrConflict, objectID, strings.TrimSpace(canonicalID))
			}
			if resolveErr != nil && !errors.Is(resolveErr, errorapi.ErrNotFound) {
				return nil, resolveErr
			}
			continue
		}
		if !hasObjectMethod(ctx, obj, objectMethodDelete) {
			continue
		}
		if err := requireAllObjectMethod(ctx, obj, objectMethodDelete); err != nil {
			continue
		}
		if _, alreadySeen := seen[objectID]; alreadySeen {
			continue
		}
		seen[objectID] = struct{}{}
		toDelete = append(toDelete, objectID)
	}
	return toDelete, nil
}
func (s *Service) DeleteObjectsByChecksums(ctx context.Context, hashes []string) (int, error) {
	resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, objectMethodDelete)
	if byChecksum, err := s.store.ListObjectIDsByChecksumsAndResources(ctx, hashes, resources, includeUnscoped, restrictToResources); err == nil {
		seen := make(map[string]struct{})
		toDelete := make([]string, 0)
		for _, hash := range hashes {
			for _, objectID := range byChecksum[hash] {
				if _, ok := seen[objectID]; ok {
					continue
				}
				seen[objectID] = struct{}{}
				toDelete = append(toDelete, objectID)
			}
		}
		if len(toDelete) == 0 {
			return 0, nil
		}
		objects, err := s.store.GetBulkObjects(ctx, toDelete)
		if err != nil {
			return 0, err
		}
		authorized := make([]string, 0, len(objects))
		for i := range objects {
			if err := requireAllObjectMethod(ctx, &objects[i], objectMethodDelete); err != nil {
				continue
			}
			authorized = append(authorized, string(objects[i].Id))
		}
		if len(authorized) == 0 {
			return 0, nil
		}
		if err := s.store.BulkDeleteObjects(ctx, authorized); err != nil {
			return 0, err
		}
		return len(authorized), nil
	}

	objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	toDelete := make([]string, 0)
	for _, hash := range hashes {
		for _, obj := range objectsByChecksum[hash] {
			if _, ok := seen[string(obj.Id)]; ok {
				continue
			}
			if !hasObjectMethod(ctx, &obj, objectMethodDelete) {
				continue
			}
			if err := requireAllObjectMethod(ctx, &obj, objectMethodDelete); err != nil {
				continue
			}
			seen[string(obj.Id)] = struct{}{}
			toDelete = append(toDelete, string(obj.Id))
		}
	}
	if len(toDelete) == 0 {
		return 0, nil
	}
	if err := s.store.BulkDeleteObjects(ctx, toDelete); err != nil {
		return 0, err
	}
	return len(toDelete), nil
}
func (s *Service) deletableObjectIDsForMethod(ctx context.Context, ids []string, requireAll bool) ([]string, error) {
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered := filterObjectsByMethod(ctx, objects, objectMethodDelete)
	toDelete := make([]string, 0, len(filtered))
	for _, obj := range filtered {
		if requireAll {
			if err := requireAllObjectMethod(ctx, &obj, objectMethodDelete); err != nil {
				continue
			}
		}
		toDelete = append(toDelete, string(obj.Id))
	}
	return toDelete, nil
}
