package objects

import (
	"context"
	"fmt"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"sort"
	"strings"
)

const maxRecordListLimit = 10000

func normalizeListQuery(query RecordListQuery) (RecordListQuery, error) {
	scope, err := NewScope(query.Scope.Organization, query.Scope.Project)
	if err != nil {
		return RecordListQuery{}, err
	}
	query.Scope = scope
	query.ObjectURL = strings.TrimSpace(query.ObjectURL)
	query.StartAfter = strings.TrimSpace(query.StartAfter)
	if query.Limit < 0 {
		return RecordListQuery{}, fmt.Errorf("limit must be >= 0")
	}
	if query.Limit > maxRecordListLimit {
		query.Limit = maxRecordListLimit
	}
	if query.StartAfter != "" {
		query.Page = 0
		return query, nil
	}
	if query.Page < 0 {
		return RecordListQuery{}, fmt.Errorf("page must be >= 0")
	}
	if _, err := recordListPageOffset(query.Page, query.Limit); err != nil {
		return RecordListQuery{}, err
	}
	return query, nil
}

func recordListPageOffset(page, limit int) (int, error) {
	if page == 0 || limit == 0 {
		return 0, nil
	}
	maxInt := int(^uint(0) >> 1)
	if page > maxInt/limit {
		return 0, fmt.Errorf("page offset is too large")
	}
	return page * limit, nil
}

// ListPreparedPage owns branch selection, ID pagination, scoped hydration,
// canonicalization, URL filtering, and authorization-dependent scans for one
// record listing.
func (s *Service) ListPreparedPage(ctx context.Context, query RecordListQuery) ([]Record, error) {
	query, err := normalizeListQuery(query)
	if err != nil {
		return nil, err
	}
	offset, err := recordListPageOffset(query.Page, query.Limit)
	if err != nil {
		return nil, err
	}
	scope := query.Scope
	if query.Checksum != nil {
		checksumType, checksum := ParseHashQuery(query.Checksum.Value, query.Checksum.Type)
		ids, err := s.ListObjectIDsPageByChecksum(ctx, checksum, checksumType, scope.Organization, scope.Project, query.RequiredMethod, query.StartAfter, query.Limit, offset)
		if err != nil {
			return nil, err
		}
		return s.GetPreparedScopedObjects(ctx, ids, scope.Organization, scope.Project, query.RequiredMethod)
	}
	if query.ObjectURL != "" {
		ids, err := s.ListObjectIDsPageByURL(ctx, query.ObjectURL, scope.Organization, scope.Project, query.RequiredMethod, query.StartAfter, query.Limit, offset)
		if err != nil {
			return nil, err
		}
		return s.GetPreparedScopedObjects(ctx, ids, scope.Organization, scope.Project, query.RequiredMethod)
	}
	return s.ListPreparedObjectsPageByScope(ctx, scope.Organization, scope.Project, query.RequiredMethod, query.StartAfter, query.Limit, offset)
}

// LookupChecksumQueries resolves a checksum batch in input order. Each input
// query receives its own result, including duplicate queries.
func (s *Service) LookupChecksumQueries(ctx context.Context, queries []ChecksumQuery, requiredMethod string) ([]ChecksumMatches, error) {
	values := make([]string, 0, len(queries))
	for _, query := range queries {
		_, value := ParseHashQuery(query.Value, query.Type)
		values = append(values, value)
	}
	objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, values)
	if err != nil {
		return nil, err
	}

	matches := make([]ChecksumMatches, 0, len(queries))
	for _, query := range queries {
		checksumType, checksum := ParseHashQuery(query.Value, query.Type)
		objects := objectsWithSHA256(objectsByChecksum[checksum], checksum)
		objects = filterObjectsByMethod(ctx, canonicalizeContentObjects(objects), requiredMethod)
		if checksumType != "" {
			filtered := make([]Record, 0, len(objects))
			for _, obj := range objects {
				if RecordHasChecksumTypeAndValue(obj, checksumType, checksum) {
					filtered = append(filtered, obj)
				}
			}
			objects = filtered
		}
		matches = append(matches, ChecksumMatches{Query: query, Records: objects})
	}
	return matches, nil
}

func (s *Service) GetPreparedScopedObjects(ctx context.Context, ids []string, organization, project, requiredMethod string) ([]Record, error) {
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return s.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
}

func (s *Service) PrepareScopedObjects(ctx context.Context, objects []Record, organization, project, requiredMethod string) ([]Record, error) {
	expanded, err := s.expandProjectChecksumSiblingObjects(ctx, objects, organization, project)
	if err != nil {
		return nil, err
	}
	filtered := filterObjectsByMethod(ctx, expanded, requiredMethod)
	return canonicalizeProjectScopedObjects(filtered, organization, project), nil
}

func (s *Service) ListPreparedObjectsPageByScope(ctx context.Context, organization, project, requiredMethod, startAfter string, limit, offset int) ([]Record, error) {
	if limit <= 0 {
		return []Record{}, nil
	}
	if offset < 0 {
		offset = 0
	}

	startAfter = strings.TrimSpace(startAfter)
	skip := offset
	if startAfter != "" {
		skip = 0
	}
	target := limit + skip
	batchSize := target
	if batchSize < 100 {
		batchSize = 100
	}

	rawStart := startAfter
	collected := make([]Record, 0, target)
	seen := make(map[string]struct{}, target)
	for len(collected) < target {
		ids, err := s.ListObjectIDsPageByScope(ctx, organization, project, requiredMethod, rawStart, batchSize, 0)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			break
		}
		rawStart = ids[len(ids)-1]

		prepared, err := s.GetPreparedScopedObjects(ctx, ids, organization, project, requiredMethod)
		if err != nil {
			return nil, err
		}
		for _, obj := range prepared {
			if startAfter != "" && string(obj.Id) <= startAfter {
				continue
			}
			if _, ok := seen[string(obj.Id)]; ok {
				continue
			}
			seen[string(obj.Id)] = struct{}{}
			collected = append(collected, obj)
			if len(collected) >= target {
				break
			}
		}
		if len(ids) < batchSize {
			break
		}
	}

	if skip >= len(collected) {
		return []Record{}, nil
	}
	end := skip + limit
	if end > len(collected) {
		end = len(collected)
	}
	return collected[skip:end], nil
}

func (s *Service) ListObjectIDsPageByChecksum(ctx context.Context, checksum, checksumType, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	var objects []Record
	if strings.TrimSpace(organization) != "" || strings.TrimSpace(project) != "" {
		raw, err := s.store.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		scoped := make([]Record, 0, len(raw))
		for _, obj := range raw {
			if objectMatchesScope(&obj, organization, project) {
				scoped = append(scoped, obj)
			}
		}
		filtered := filterObjectsByMethod(ctx, scoped, requiredMethod)
		objects = canonicalizeProjectScopedObjects(filtered, organization, project)
	} else {
		var err error
		objects, err = s.GetObjectsByChecksum(ctx, checksum, requiredMethod)
		if err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(objects))
	for _, obj := range objects {
		if checksumType != "" && !RecordHasChecksumTypeAndValue(obj, checksumType, checksum) {
			continue
		}
		if strings.TrimSpace(organization) != "" && !objectMatchesScope(&obj, organization, project) {
			continue
		}
		ids = append(ids, string(obj.Id))
	}
	sort.Strings(ids)
	if startAfter != "" {
		offset = searchAfterID(ids, startAfter)
	}
	if offset >= len(ids) {
		return []string{}, nil
	}
	end := offset + limit
	if end > len(ids) {
		end = len(ids)
	}
	return ids[offset:end], nil
}

func (s *Service) ListObjectIDsPageByScope(ctx context.Context, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	if canUseUnrestrictedScopePage(ctx, requiredMethod) {
		return s.store.ListObjectIDsPageByScope(ctx, organization, project, startAfter, limit, offset)
	}

	ids, err := s.ListObjectIDsByScope(ctx, organization, project, requiredMethod)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []string{}, nil
	}
	sort.Strings(ids)
	if startAfter != "" {
		offset = searchAfterID(ids, startAfter)
	}
	if offset >= len(ids) {
		return []string{}, nil
	}
	end := offset + limit
	if end > len(ids) {
		end = len(ids)
	}
	return ids[offset:end], nil
}

func canUseUnrestrictedScopePage(ctx context.Context, requiredMethod string) bool {
	resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, requiredMethod)
	return !restrictToResources && len(resources) == 0 && includeUnscoped
}

func (s *Service) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	objectURL = strings.TrimSpace(objectURL)
	if objectURL == "" {
		return []string{}, nil
	}
	resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, requiredMethod)
	if access.IsGen3Mode(ctx) && access.IsAuthzEnforced(ctx) && !access.HasAuthHeader(ctx) {
		return []string{}, nil
	}
	return s.store.ListObjectIDsPageByURL(ctx, objectURL, organization, project, startAfter, limit, offset, resources, includeUnscoped, restrictToResources)
}

func (s *Service) ListObjectIDsByScope(ctx context.Context, organization, project string, requiredMethod string) ([]string, error) {
	if strings.TrimSpace(organization) == "" && strings.EqualFold(strings.TrimSpace(requiredMethod), objectMethodRead) {
		if ids, ok, err := s.listReadableObjectIDs(ctx); ok || err != nil {
			return ids, err
		}
	}
	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered, err := s.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(filtered))
	for _, obj := range filtered {
		out = append(out, string(obj.Id))
	}
	return out, nil
}

// ListPhysicalObjectsByScope returns each stored object row in a project scope.
// Callers that repair physical access methods need the row identity and methods
// without the same-checksum canonical merge used by normal reads.
func (s *Service) ListPhysicalObjectsByScope(ctx context.Context, organization, project, requiredMethod string) ([]Record, error) {
	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return filterObjectsByMethod(ctx, objects, requiredMethod), nil
}

// ListMissingScopedSHA256 returns the requested SHA-256 checksums that are not
// registered for the given project.
func (s *Service) ListMissingScopedSHA256(ctx context.Context, organization, project string, checksums []string) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" || project == "" || len(checksums) == 0 {
		return nil, errorapi.ErrAccessDenied
	}
	if err := requireScopeMethod(ctx, organization, project, objectMethodRead); err != nil {
		return nil, err
	}

	existingByChecksum, err := s.store.ListScopedObjectIDsByChecksums(ctx, organization, project, checksums)
	if err != nil {
		return nil, err
	}

	missing := make([]string, 0, len(checksums))
	for _, checksum := range checksums {
		if objectIDs := existingByChecksum[checksum]; len(objectIDs) == 0 {
			missing = append(missing, checksum)
		}
	}
	return missing, nil
}

func (s *Service) expandProjectChecksumSiblingObjects(ctx context.Context, objects []Record, organization, project string) ([]Record, error) {
	if len(objects) == 0 {
		return []Record{}, nil
	}

	wantedKeys := make(map[string]struct{}, len(objects))
	checksums := make([]string, 0, len(objects))
	seenChecksums := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, "")
		if !ok {
			continue
		}
		wantedKeys[key] = struct{}{}
		sha, _ := CanonicalSHA256(obj.Checksums)
		if _, seen := seenChecksums[sha]; seen {
			continue
		}
		seenChecksums[sha] = struct{}{}
		checksums = append(checksums, sha)
	}
	if len(wantedKeys) == 0 || len(checksums) == 0 || strings.TrimSpace(organization) == "" || strings.TrimSpace(project) == "" {
		return objects, nil
	}

	idsByChecksum, err := s.store.ListScopedObjectIDsByChecksums(ctx, organization, project, checksums)
	if err != nil {
		return nil, err
	}
	expanded := make([]Record, 0, len(objects))
	seenIDs := make(map[string]struct{}, len(objects))
	missingIDs := make([]string, 0)
	missingSeen := make(map[string]struct{})
	for _, obj := range objects {
		if _, seen := seenIDs[string(obj.Id)]; seen {
			continue
		}
		seenIDs[string(obj.Id)] = struct{}{}
		expanded = append(expanded, obj)
	}
	for _, sha := range checksums {
		for _, id := range idsByChecksum[sha] {
			if _, seen := seenIDs[id]; seen {
				continue
			}
			if _, queued := missingSeen[id]; queued {
				continue
			}
			missingSeen[id] = struct{}{}
			missingIDs = append(missingIDs, id)
		}
	}

	if len(missingIDs) > 0 {
		siblings, err := s.store.GetBulkObjects(ctx, missingIDs)
		if err != nil {
			return nil, err
		}
		for _, obj := range siblings {
			key, ok := canonicalProjectChecksumKey(&obj, "")
			if !ok {
				continue
			}
			if _, wanted := wantedKeys[key]; !wanted {
				continue
			}
			if _, seen := seenIDs[string(obj.Id)]; seen {
				continue
			}
			seenIDs[string(obj.Id)] = struct{}{}
			expanded = append(expanded, obj)
		}
	}
	return expanded, nil
}

func (s *Service) listReadableObjectIDs(ctx context.Context) ([]string, bool, error) {
	if !access.IsAuthzEnforced(ctx) {
		return nil, false, nil
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return []string{}, true, nil
	}

	resources := readableResources(ctx)
	ids, err := s.store.ListObjectIDsByResources(ctx, resources, true)
	return ids, true, err
}

func readableResources(ctx context.Context) []string {
	return authorizedResources(ctx, objectMethodRead)
}

func objectMethodResourceFilter(ctx context.Context, method string) ([]string, bool, bool) {
	method = strings.TrimSpace(method)
	if method == "" || !access.IsAuthzEnforced(ctx) {
		return nil, true, false
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return nil, false, true
	}
	if access.HasMethodAccess(ctx, method, []string{"/programs"}) || access.HasMethodAccess(ctx, method, []string{"/data_file"}) {
		return nil, strings.EqualFold(method, objectMethodRead), false
	}
	return authorizedResources(ctx, method), strings.EqualFold(method, objectMethodRead), true
}

func authorizedResources(ctx context.Context, method string) []string {
	privileges := access.GetUserPrivileges(ctx)
	if len(privileges) == 0 {
		return clientaccess.NormalizeAccessResources(access.GetUserAuthz(ctx))
	}
	resources := make([]string, 0, len(privileges))
	for resource, methods := range privileges {
		if methods[method] || methods["*"] {
			resources = append(resources, resource)
		}
	}
	return clientaccess.NormalizeAccessResources(resources)
}

func searchAfterID(ids []string, startAfter string) int {
	idx := sort.SearchStrings(ids, startAfter)
	for idx < len(ids) && ids[idx] <= startAfter {
		idx++
	}
	return idx
}

func objectMatchesScope(obj *Record, organization, project string) bool {
	if obj == nil || strings.TrimSpace(organization) == "" {
		return obj != nil
	}
	authz := clientaccess.ControlledAccessToAuthzMap(AccessResources(obj))
	projects, ok := authz[organization]
	if !ok {
		return false
	}
	if strings.TrimSpace(project) == "" || len(projects) == 0 {
		return true
	}
	for _, p := range projects {
		if p == project {
			return true
		}
	}
	return false
}

func filterObjectsByMethod(ctx context.Context, objects []Record, method string) []Record {
	if strings.TrimSpace(method) == "" {
		return objects
	}
	filtered := make([]Record, 0, len(objects))
	for _, obj := range objects {
		if hasObjectMethod(ctx, &obj, method) {
			filtered = append(filtered, obj)
		}
	}
	return filtered
}

// GetObject retrieves the prepared canonical record identified by ID, alias,
// or checksum and validates access.
func (s *Service) GetObject(ctx context.Context, ident string, requiredMethod string) (*Record, error) {
	if strings.TrimSpace(ident) == "" {
		return nil, errorapi.ErrObjectNotFound
	}

	checksum, checksumIdent := NormalizeSHA256Query(ident)
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

func (s *Service) canonicalRecordForChecksum(ctx context.Context, checksum, method string) (*Record, bool, error) {
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

func (s *Service) lookupObjectByChecksum(ctx context.Context, ident string, requiredMethod string) (*Record, bool, error) {
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

func (s *Service) lookupObjectByID(ctx context.Context, ident string) (*Record, bool, error) {
	obj, err := s.store.GetObject(ctx, ident)
	if err == nil {
		return obj, true, nil
	}
	if errorapi.IsNotFoundError(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func (s *Service) lookupObjectByAlias(ctx context.Context, ident string) (*Record, bool, error) {
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

func (s *Service) canonicalRecordAndCheckAccess(ctx context.Context, obj *Record, method string) (*Record, error) {
	record, err := s.canonicalRecordForObject(ctx, obj)
	if err != nil {
		return nil, err
	}
	if err := requireObjectMethod(ctx, record, method); err != nil {
		return nil, err
	}
	return record, nil
}

func (s *Service) canonicalRecordForObject(ctx context.Context, obj *Record) (*Record, error) {
	sha, ok := CanonicalSHA256(obj.Checksums)
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

func (s *Service) GetObjectsByChecksums(ctx context.Context, hashes []string, requiredMethod string) (map[string][]Record, error) {
	objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	filtered := make(map[string][]Record, len(objectsByChecksum))
	for checksum, objects := range objectsByChecksum {
		matching := objectsWithSHA256(objects, checksum)
		filtered[checksum] = filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod)
	}
	return filtered, nil
}

func (s *Service) GetObjectsByChecksum(ctx context.Context, checksum string, requiredMethod string) ([]Record, error) {
	objects, err := s.store.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		return nil, err
	}
	matching := objectsWithSHA256(objects, checksum)
	return filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod), nil
}

func (s *Service) GetBulkObjects(ctx context.Context, ids []string, requiredMethod string) ([]Record, error) {
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(objects))
	for _, obj := range objects {
		if sha, ok := CanonicalSHA256(obj.Checksums); ok {
			hashes = append(hashes, sha)
		}
	}
	siblingsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	canonical := make([]Record, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		resolved := cloneObject(obj)
		if sha, ok := CanonicalSHA256(obj.Checksums); ok {
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
