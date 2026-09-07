package records

import (
	"context"
	"errors"
	"fmt"
	"strings"

	objectmodel "github.com/calypr/syfon/internal/objects"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/faults"
)

func (m *mutationService) DeleteBulkByScope(ctx context.Context, organization, project string) (int, error) {
	if err := requireScopeMethod(ctx, organization, project, objectMethodDelete); err != nil {
		return 0, err
	}

	ids, err := m.scope.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	if lister := m.authorizedQuery; lister != nil {
		resources, _, restrictToResources := objectMethodResourceFilter(ctx, objectMethodDelete)
		if optimized, err := lister.ListObjectIDsByScopeAndResources(ctx, organization, project, resources, restrictToResources); err == nil {
			ids = optimized
		}
	}

	toDelete, err := m.deletableObjectIDsForMethod(ctx, ids, false)
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
	return m.accessPolicy.RemoveObjectControlledAccessBulk(ctx, toDelete, resource)
}

func (m *mutationService) DeleteObject(ctx context.Context, id string) error {
	return m.DeleteObjectWithOptions(ctx, id, DeleteOptions{})
}

type DeleteOptions struct {
	DeleteStorageData bool
}

func (m *mutationService) DeleteObjectWithOptions(ctx context.Context, id string, opts DeleteOptions) error {
	if opts.DeleteStorageData {
		return fmt.Errorf("%w: physical storage deletion is not atomic with catalog mutation", faults.ErrConflict)
	}
	obj, err := m.recordReader.GetObject(ctx, id)
	if err != nil {
		return err
	}
	if err := requireAllObjectMethod(ctx, obj, objectMethodDelete); err != nil {
		return err
	}
	if opts.DeleteStorageData && (obj.PublicRead || len(objectmodel.AccessResources(obj)) > 0) {
		return fmt.Errorf("%w: cannot delete shared content storage without exclusive ownership", faults.ErrConflict)
	}
	return m.recordWriter.DeleteObject(ctx, id)
}

func (m *mutationService) BulkDeleteObjects(ctx context.Context, ids []string) error {
	return m.BulkDeleteObjectsWithOptions(ctx, ids, DeleteOptions{})
}

func (m *mutationService) BulkDeleteObjectsWithOptions(ctx context.Context, ids []string, opts DeleteOptions) error {
	if opts.DeleteStorageData {
		return fmt.Errorf("%w: physical storage deletion is not atomic with catalog mutation", faults.ErrConflict)
	}
	toDelete, err := m.deletablePhysicalObjectIDsForBulk(ctx, ids)
	if err != nil {
		return err
	}
	if len(toDelete) == 0 {
		return nil
	}
	return m.recordWriter.BulkDeleteObjects(ctx, toDelete)
}

func (m *mutationService) deletablePhysicalObjectIDsForBulk(ctx context.Context, ids []string) ([]string, error) {
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*objectmodel.Record, len(objects))
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
			canonicalID, resolveErr := m.aliases.ResolveObjectAlias(ctx, objectID)
			if resolveErr == nil && strings.TrimSpace(canonicalID) != "" {
				return nil, fmt.Errorf("%w: bulk delete requires a physical object UUID; %q is an alias for %q", faults.ErrConflict, objectID, strings.TrimSpace(canonicalID))
			}
			if resolveErr != nil && !errors.Is(resolveErr, faults.ErrNotFound) {
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
func (m *mutationService) DeleteObjectsByChecksums(ctx context.Context, hashes []string) (int, error) {
	if lister := m.authorizedQuery; lister != nil {
		resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, objectMethodDelete)
		if byChecksum, err := lister.ListObjectIDsByChecksumsAndResources(ctx, hashes, resources, includeUnscoped, restrictToResources); err == nil {
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
			objects, err := m.recordReader.GetBulkObjects(ctx, toDelete)
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
			if err := m.recordWriter.BulkDeleteObjects(ctx, authorized); err != nil {
				return 0, err
			}
			return len(authorized), nil
		}
	}

	objectsByChecksum, err := m.content.GetObjectsByChecksums(ctx, hashes)
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
	if err := m.recordWriter.BulkDeleteObjects(ctx, toDelete); err != nil {
		return 0, err
	}
	return len(toDelete), nil
}
func (m *mutationService) deletableObjectIDs(ctx context.Context, ids []string) ([]string, error) {
	return m.deletableObjectIDsForMethod(ctx, ids, true)
}

func (m *mutationService) deletableObjectIDsForMethod(ctx context.Context, ids []string, requireAll bool) ([]string, error) {
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
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
