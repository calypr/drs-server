package records

import (
	"context"
	objectmodel "github.com/calypr/syfon/internal/objects"
	"sort"
)

func (s *Service) CollapseProjectChecksumDuplicates(ctx context.Context, organization, project string) (int, error) {
	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return 0, err
	}
	if err := bulkObjectMethodError(ctx, objects, objectMethodUpdate); err != nil {
		return 0, err
	}

	grouped := make(map[string][]objectmodel.Record)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, "")
		if !ok {
			continue
		}
		grouped[key] = append(grouped[key], cloneObject(obj))
	}

	merged := make([]objectmodel.Record, 0, len(grouped))
	aliasMap := make(map[string]string)
	toDelete := make([]string, 0)
	keys := make([]string, 0, len(grouped))
	for key, group := range grouped {
		if len(group) < 2 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := grouped[key]
		canonical := collapseCanonicalGroup(group)
		merged = append(merged, canonical)
		for _, obj := range group {
			if obj.Id == canonical.Id {
				continue
			}
			aliasMap[string(obj.Id)] = string(canonical.Id)
			toDelete = append(toDelete, string(obj.Id))
		}
	}

	if len(merged) == 0 {
		return 0, nil
	}
	if err := s.store.RegisterObjects(ctx, merged); err != nil {
		return 0, err
	}
	for aliasID, canonicalID := range aliasMap {
		if err := s.store.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
			return 0, err
		}
	}
	if err := s.store.BulkDeleteObjects(ctx, uniqueStrings(toDelete)); err != nil {
		return 0, err
	}
	return len(aliasMap), nil
}
