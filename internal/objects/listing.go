package objects

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/internal/access"
)

// ListRecords returns one authorized page after project checksum-family merging.
func (s *Service) ListRecords(ctx context.Context, query RecordListQuery) ([]drs.DrsObject, error) {
	scope, err := NewScope(query.Scope.Organization, query.Scope.Project)
	if err != nil {
		return nil, err
	}
	query.Scope = scope
	query.ObjectURL = strings.TrimSpace(query.ObjectURL)
	query.StartAfter = strings.TrimSpace(query.StartAfter)
	if query.Limit < 0 {
		return nil, fmt.Errorf("limit must be >= 0")
	}
	offset := 0
	if query.StartAfter == "" {
		if query.Page < 0 {
			return nil, fmt.Errorf("page must be >= 0")
		}
		if query.Limit > 0 && query.Page > int(^uint(0)>>1)/query.Limit {
			return nil, fmt.Errorf("page offset is too large")
		}
		offset = query.Page * query.Limit
	}
	if query.Limit == 0 {
		return []drs.DrsObject{}, nil
	}

	if query.Checksum != nil {
		checksumType, checksum := ParseHashQuery(query.Checksum.Value, query.Checksum.Type)
		objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, []string{checksum})
		if err != nil {
			return nil, err
		}
		records := objectsByChecksum[checksum]
		if scope.Organization == "" {
			policy, err := s.publicReadPolicy(ctx, records)
			if err != nil {
				return nil, err
			}
			records = filterObjectsByMethod(ctx, canonicalizeContentObjects(objectsWithSHA256(records, checksum), policy), query.RequiredMethod, policy)
		} else {
			scoped := make([]drs.DrsObject, 0, len(records))
			for _, record := range records {
				if objectMatchesScope(&record, scope.Organization, scope.Project) {
					scoped = append(scoped, record)
				}
			}
			policy, err := s.publicReadPolicy(ctx, scoped)
			if err != nil {
				return nil, err
			}
			records = canonicalizeProjectScopedObjects(filterObjectsByMethod(ctx, scoped, query.RequiredMethod, policy), scope.Organization, scope.Project, policy)
		}
		ids := make([]string, 0, len(records))
		for _, record := range records {
			if checksumType != "" && !RecordHasChecksumTypeAndValue(record, checksumType, checksum) {
				continue
			}
			if scope.Organization != "" && !objectMatchesScope(&record, scope.Organization, scope.Project) {
				continue
			}
			ids = append(ids, record.Id)
		}
		return s.loadScopedRecords(ctx, pageIDs(ids, query.StartAfter, query.Limit, offset), query)
	}
	if query.ObjectURL != "" {
		resources, unscoped, restricted := objectMethodResourceFilter(ctx, query.RequiredMethod)
		if access.IsGen3Mode(ctx) && access.IsAuthzEnforced(ctx) && !access.HasAuthHeader(ctx) {
			return []drs.DrsObject{}, nil
		}
		ids, err := s.store.ListObjectIDsPageByURL(ctx, query.ObjectURL, scope.Organization, scope.Project, query.StartAfter, query.Limit, offset, resources, unscoped, restricted)
		if err != nil {
			return nil, err
		}
		return s.loadScopedRecords(ctx, ids, query)
	}
	return s.listScopeRecords(ctx, query, offset)
}

func (s *Service) loadScopedRecords(ctx context.Context, ids []string, query RecordListQuery) ([]drs.DrsObject, error) {
	records, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return s.prepareScopedRecords(ctx, records, query.Scope, query.RequiredMethod)
}

func (s *Service) prepareScopedRecords(ctx context.Context, records []drs.DrsObject, scope Scope, method string) ([]drs.DrsObject, error) {
	expanded, err := s.expandProjectChecksumSiblingObjects(ctx, records, scope.Organization, scope.Project)
	if err != nil {
		return nil, err
	}
	policy, err := s.publicReadPolicy(ctx, expanded)
	if err != nil {
		return nil, err
	}
	return canonicalizeProjectScopedObjects(filterObjectsByMethod(ctx, expanded, method, policy), scope.Organization, scope.Project, policy), nil
}

func (s *Service) listScopeRecords(ctx context.Context, query RecordListQuery, offset int) ([]drs.DrsObject, error) {
	resources, unscoped, restricted := objectMethodResourceFilter(ctx, query.RequiredMethod)
	databasePage := !restricted && len(resources) == 0 && unscoped
	var allIDs []string
	if !databasePage {
		var err error
		allIDs, err = s.ListObjectIDsByScope(ctx, query.Scope.Organization, query.Scope.Project, query.RequiredMethod)
		if err != nil {
			return nil, err
		}
	}
	target := query.Limit + offset
	batchSize := max(target, 100)
	rawStart := query.StartAfter
	collected := make([]drs.DrsObject, 0, target)
	seen := make(map[string]struct{}, target)
	for len(collected) < target {
		var ids []string
		var err error
		if databasePage {
			ids, err = s.store.ListObjectIDsPageByScope(ctx, query.Scope.Organization, query.Scope.Project, rawStart, batchSize, 0)
		} else {
			ids = pageIDs(allIDs, rawStart, batchSize, 0)
		}
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			break
		}
		rawStart = ids[len(ids)-1]
		records, err := s.loadScopedRecords(ctx, ids, query)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if query.StartAfter != "" && record.Id <= query.StartAfter {
				continue
			}
			if _, ok := seen[record.Id]; ok {
				continue
			}
			seen[record.Id] = struct{}{}
			collected = append(collected, record)
			if len(collected) == target {
				break
			}
		}
		if len(ids) < batchSize {
			break
		}
	}
	if offset >= len(collected) {
		return []drs.DrsObject{}, nil
	}
	return collected[offset:], nil
}

func pageIDs(ids []string, start string, limit, offset int) []string {
	sort.Strings(ids)
	if start != "" {
		offset = searchAfterID(ids, start)
	}
	if offset >= len(ids) {
		return []string{}
	}
	return ids[offset : offset+min(limit, len(ids)-offset)]
}
