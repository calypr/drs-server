package records

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	objectmodel "github.com/calypr/syfon/internal/objects"

	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/faults"
)

func (m *queryService) GetObjectsByChecksums(ctx context.Context, hashes []string, requiredMethod string) (map[string][]objectmodel.Record, error) {
	objectsByChecksum, err := m.content.GetObjectsByChecksums(ctx, hashes)
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

func (m *queryService) GetObjectsByChecksum(ctx context.Context, checksum string, requiredMethod string) ([]objectmodel.Record, error) {
	objects, err := m.content.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		return nil, err
	}
	matching := objectsWithSHA256(objects, checksum)
	return filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod), nil
}

func (m *queryService) GetBulkObjects(ctx context.Context, ids []string, requiredMethod string) ([]objectmodel.Record, error) {
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(objects))
	for _, obj := range objects {
		if sha, ok := objectmodel.CanonicalSHA256(obj.Checksums); ok {
			hashes = append(hashes, sha)
		}
	}
	siblingsByChecksum, err := m.content.GetObjectsByChecksums(ctx, hashes)
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
				return nil, faults.ErrNotFound
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

func (m *queryService) GetPreparedScopedObjects(ctx context.Context, ids []string, organization, project, requiredMethod string) ([]objectmodel.Record, error) {
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return m.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
}

func (m *queryService) PrepareScopedObjects(ctx context.Context, objects []objectmodel.Record, organization, project, requiredMethod string) ([]objectmodel.Record, error) {
	started := time.Now()
	expanded, err := m.expandProjectChecksumSiblingObjects(ctx, objects, organization, project)
	if err != nil {
		return nil, err
	}
	filtered := filterObjectsByMethod(ctx, expanded, requiredMethod)
	canonicalStart := time.Now()
	canonical := canonicalizeProjectScopedObjects(filtered, organization, project)
	log.Printf("INFO: syfon_prepare_scoped_objects organization=%s project=%s input_count=%d expanded_count=%d filtered_count=%d output_count=%d canonicalize_scoped_objects_ms=%d duration_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), len(objects), len(expanded), len(filtered), len(canonical), time.Since(canonicalStart).Milliseconds(), time.Since(started).Milliseconds())
	return canonical, nil
}

func (m *queryService) ListPreparedObjectsPageByScope(ctx context.Context, organization, project, requiredMethod, startAfter string, limit, offset int) ([]objectmodel.Record, error) {
	if limit <= 0 {
		return []objectmodel.Record{}, nil
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
	collected := make([]objectmodel.Record, 0, target)
	seen := make(map[string]struct{}, target)
	started := time.Now()
	rawPages := 0
	rawIDs := 0

	for len(collected) < target {
		ids, err := m.ListObjectIDsPageByScope(ctx, organization, project, requiredMethod, rawStart, batchSize, 0)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			break
		}
		rawPages++
		rawIDs += len(ids)
		rawStart = ids[len(ids)-1]

		prepared, err := m.GetPreparedScopedObjects(ctx, ids, organization, project, requiredMethod)
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
		log.Printf("INFO: syfon_list_prepared_objects_page_by_scope organization=%s project=%s start_after=%t limit=%d offset=%d raw_pages=%d raw_ids=%d records=0 duration_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), startAfter != "", limit, offset, rawPages, rawIDs, time.Since(started).Milliseconds())
		return []objectmodel.Record{}, nil
	}
	end := skip + limit
	if end > len(collected) {
		end = len(collected)
	}
	out := collected[skip:end]
	log.Printf("INFO: syfon_list_prepared_objects_page_by_scope organization=%s project=%s start_after=%t limit=%d offset=%d raw_pages=%d raw_ids=%d records=%d duration_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), startAfter != "", limit, offset, rawPages, rawIDs, len(out), time.Since(started).Milliseconds())
	return out, nil
}

func (m *queryService) ListObjectIDsPageByChecksum(ctx context.Context, checksum, checksumType, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	var objects []objectmodel.Record
	if strings.TrimSpace(organization) != "" || strings.TrimSpace(project) != "" {
		raw, err := m.content.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		scoped := make([]objectmodel.Record, 0, len(raw))
		for _, obj := range raw {
			if objectMatchesScope(&obj, organization, project) {
				scoped = append(scoped, obj)
			}
		}
		filtered := filterObjectsByMethod(ctx, scoped, requiredMethod)
		objects = canonicalizeProjectScopedObjects(filtered, organization, project)
	} else {
		var err error
		objects, err = m.GetObjectsByChecksum(ctx, checksum, requiredMethod)
		if err != nil {
			return nil, err
		}
	}
	ids := make([]string, 0, len(objects))
	for _, obj := range objects {
		if checksumType != "" && !objectmodel.RecordHasChecksumTypeAndValue(obj, checksumType, checksum) {
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

func (m *queryService) ListObjectIDsPageByScope(ctx context.Context, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	if m.pages != nil && canUseUnrestrictedScopePage(ctx, requiredMethod) {
		pageStart := time.Now()
		ids, err := m.pages.ListObjectIDsPageByScope(ctx, organization, project, startAfter, limit, offset)
		log.Printf("INFO: syfon_list_object_ids_page_by_scope organization=%s project=%s start_after=%t limit=%d offset=%d ids=%d db_page_ms=%d optimized=%t", strings.TrimSpace(organization), strings.TrimSpace(project), strings.TrimSpace(startAfter) != "", limit, offset, len(ids), time.Since(pageStart).Milliseconds(), true)
		return ids, err
	}

	ids, err := m.ListObjectIDsByScope(ctx, organization, project, requiredMethod)
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

func (m *queryService) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	objectURL = strings.TrimSpace(objectURL)
	if objectURL == "" {
		return []string{}, nil
	}
	if m.urlPages != nil {
		resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, requiredMethod)
		if access.IsGen3Mode(ctx) && access.IsAuthzEnforced(ctx) && !access.HasAuthHeader(ctx) {
			return []string{}, nil
		}
		return m.urlPages.ListObjectIDsPageByURL(ctx, objectURL, organization, project, startAfter, limit, offset, resources, includeUnscoped, restrictToResources)
	}

	ids, err := m.ListObjectIDsByScope(ctx, organization, project, requiredMethod)
	if err != nil {
		return nil, err
	}
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(objects))
	for _, obj := range objects {
		if objectHasAccessURL(&obj, objectURL) {
			out = append(out, string(obj.Id))
		}
	}
	sort.Strings(out)
	if startAfter != "" {
		offset = searchAfterID(out, startAfter)
	}
	if offset >= len(out) {
		return []string{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func (m *queryService) ListObjectIDsByScope(ctx context.Context, organization, project string, requiredMethod string) ([]string, error) {
	if strings.TrimSpace(organization) == "" && strings.EqualFold(strings.TrimSpace(requiredMethod), objectMethodRead) {
		if ids, ok, err := m.listReadableObjectIDs(ctx); ok || err != nil {
			return ids, err
		}
	}
	listStart := time.Now()
	ids, err := m.scope.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	log.Printf("INFO: syfon_list_object_ids_by_scope organization=%s project=%s ids=%d list_scope_ids_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), len(ids), time.Since(listStart).Milliseconds())
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered, err := m.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(filtered))
	for _, obj := range filtered {
		out = append(out, string(obj.Id))
	}
	return out, nil
}

func (m *queryService) ListObjectsByScope(ctx context.Context, organization, project, requiredMethod string) ([]objectmodel.Record, error) {
	if strings.TrimSpace(organization) == "" && strings.EqualFold(strings.TrimSpace(requiredMethod), objectMethodRead) {
		if ids, ok, err := m.listReadableObjectIDs(ctx); ok {
			if err != nil {
				return nil, err
			}
			objects, err := m.recordReader.GetBulkObjects(ctx, ids)
			if err != nil {
				return nil, err
			}
			return filterObjectsByMethod(ctx, objects, requiredMethod), nil
		}
	}
	ids, err := m.scope.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return m.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
}

// ListPhysicalObjectsByScope returns each stored object row in a project scope.
// Callers that repair physical access methods need the row identity and methods
// without the same-checksum canonical merge used by normal reads.
func (m *queryService) ListPhysicalObjectsByScope(ctx context.Context, organization, project, requiredMethod string) ([]objectmodel.Record, error) {
	ids, err := m.scope.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return filterObjectsByMethod(ctx, objects, requiredMethod), nil
}

// ListMissingScopedSHA256 returns the requested SHA-256 checksums that are not
// registered for the given project. It deliberately uses the indexed checksum
// lookup and does not hydrate complete DRS records or access methods.
func (m *queryService) ListMissingScopedSHA256(ctx context.Context, organization, project string, checksums []string) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" || project == "" || len(checksums) == 0 {
		return nil, faults.ErrUnauthorized
	}
	if err := requireScopeMethod(ctx, organization, project, objectMethodRead); err != nil {
		return nil, err
	}

	existingByChecksum, err := m.checksumScope.ListScopedObjectIDsByChecksums(ctx, organization, project, checksums)
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

func (m *queryService) expandProjectChecksumSiblingObjects(ctx context.Context, objects []objectmodel.Record, organization, project string) ([]objectmodel.Record, error) {
	if len(objects) == 0 {
		return []objectmodel.Record{}, nil
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
		sha, _ := objectmodel.CanonicalSHA256(obj.Checksums)
		if _, seen := seenChecksums[sha]; seen {
			continue
		}
		seenChecksums[sha] = struct{}{}
		checksums = append(checksums, sha)
	}
	if len(wantedKeys) == 0 || len(checksums) == 0 || strings.TrimSpace(organization) == "" || strings.TrimSpace(project) == "" {
		return objects, nil
	}

	listStart := time.Now()
	idsByChecksum, err := m.checksumScope.ListScopedObjectIDsByChecksums(ctx, organization, project, checksums)
	if err != nil {
		return nil, err
	}
	listDuration := time.Since(listStart)

	expanded := make([]objectmodel.Record, 0, len(objects))
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

	hydrateStart := time.Now()
	if len(missingIDs) > 0 {
		siblings, err := m.recordReader.GetBulkObjects(ctx, missingIDs)
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
	hydrateDuration := time.Since(hydrateStart)
	log.Printf("INFO: syfon_expand_scoped_checksum_siblings organization=%s project=%s checksums=%d input_count=%d missing_ids=%d output_count=%d list_scoped_sibling_ids_ms=%d hydrate_missing_siblings_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), len(checksums), len(objects), len(missingIDs), len(expanded), listDuration.Milliseconds(), hydrateDuration.Milliseconds())
	return expanded, nil
}

func objectHasAccessURL(obj *objectmodel.Record, objectURL string) bool {
	if obj == nil || obj.AccessMethods == nil {
		return false
	}
	for _, method := range *obj.AccessMethods {
		if method.AccessUrl != nil && strings.TrimSpace(method.AccessUrl.Url) == objectURL {
			return true
		}
	}
	return false
}

func (m *queryService) listReadableObjectIDs(ctx context.Context) ([]string, bool, error) {
	lister := m.resources
	if lister == nil || !access.IsAuthzEnforced(ctx) {
		return nil, false, nil
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return []string{}, true, nil
	}

	resources := readableResources(ctx)
	ids, err := lister.ListObjectIDsByResources(ctx, resources, true)
	return ids, true, err
}

func (m *queryService) listReadableObjectIDsPage(ctx context.Context, startAfter string, limit, offset int) ([]string, bool, error) {
	pager := m.pages
	if pager == nil || !access.IsAuthzEnforced(ctx) {
		return nil, false, nil
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return []string{}, true, nil
	}

	resources := readableResources(ctx)
	ids, err := pager.ListObjectIDsPageByResources(ctx, resources, true, startAfter, limit, offset)
	return ids, true, err
}

func (m *queryService) canPageScopeRead(ctx context.Context, organization, project string) bool {
	if !access.IsAuthzEnforced(ctx) {
		return true
	}
	resource, err := clientaccess.ResourcePath(organization, project)
	if err != nil {
		return false
	}
	return access.HasMethodAccess(ctx, objectMethodRead, []string{resource})
}

func readableResources(ctx context.Context) []string {
	return authorizedResources(ctx, objectMethodRead)
}

func (m *queryService) readableChecksumFilter(ctx context.Context, organization, project string) ([]string, bool, bool, bool) {
	if !access.IsAuthzEnforced(ctx) {
		return nil, false, false, true
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return nil, false, true, true
	}
	if access.HasMethodAccess(ctx, objectMethodRead, []string{"/programs"}) || access.HasMethodAccess(ctx, objectMethodRead, []string{"/data_file"}) {
		return nil, false, false, true
	}
	if strings.TrimSpace(organization) != "" && m.canPageScopeRead(ctx, organization, project) {
		return nil, false, false, true
	}
	return readableResources(ctx), true, true, true
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

func (m *queryService) authorizedChecksumIDs(ctx context.Context, checksum, requiredMethod string) ([]string, bool, error) {
	lister := m.authorizedQuery
	if lister == nil {
		return nil, false, nil
	}
	resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, requiredMethod)
	byChecksum, err := lister.ListObjectIDsByChecksumsAndResources(ctx, []string{checksum}, resources, includeUnscoped, restrictToResources)
	if err != nil {
		return nil, false, err
	}
	return byChecksum[checksum], true, nil
}

func searchAfterID(ids []string, startAfter string) int {
	idx := sort.SearchStrings(ids, startAfter)
	for idx < len(ids) && ids[idx] <= startAfter {
		idx++
	}
	return idx
}

func objectMatchesScope(obj *objectmodel.Record, organization, project string) bool {
	if obj == nil || strings.TrimSpace(organization) == "" {
		return obj != nil
	}
	projects, ok := obj.Authorizations[organization]
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

func filterObjectsByMethod(ctx context.Context, objects []objectmodel.Record, method string) []objectmodel.Record {
	if strings.TrimSpace(method) == "" {
		return objects
	}
	filtered := make([]objectmodel.Record, 0, len(objects))
	for _, obj := range objects {
		if hasObjectMethod(ctx, &obj, method) {
			filtered = append(filtered, obj)
		}
	}
	return filtered
}
