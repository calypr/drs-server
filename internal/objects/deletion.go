package objects

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
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

	stored, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return 0, err
	}
	policy, err := s.publicReadPolicy(ctx, stored)
	if err != nil {
		return 0, err
	}
	readable := filterObjectsByMethod(ctx, stored, objectMethodDelete, policy)
	toDelete := make([]string, 0, len(readable))
	for _, obj := range readable {
		toDelete = append(toDelete, obj.Id)
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

func (s *Service) DeleteObject(ctx context.Context, id string) error {
	obj, err := s.store.GetObject(ctx, id)
	if err != nil {
		return err
	}
	if err := requireAllObjectMethod(ctx, obj, objectMethodDelete); err != nil {
		return err
	}
	return s.store.DeleteObject(ctx, id)
}

func (s *Service) BulkDeleteObjects(ctx context.Context, ids []string) error {
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
	byID := make(map[string]*drs.DrsObject, len(objects))
	for i := range objects {
		byID[objects[i].Id] = &objects[i]
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
	objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return 0, err
	}
	seen := make(map[string]struct{})
	toDelete := make([]string, 0)
	for _, hash := range hashes {
		for _, obj := range objectsByChecksum[hash] {
			if _, ok := seen[obj.Id]; ok {
				continue
			}
			if err := requireAllObjectMethod(ctx, &obj, objectMethodDelete); err != nil {
				continue
			}
			seen[obj.Id] = struct{}{}
			toDelete = append(toDelete, obj.Id)
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
