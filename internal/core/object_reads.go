package core

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
)

// GetObject retrieves an internal object by ID, Alias, or Checksum and validates access.
func (m *ObjectManager) GetObject(ctx context.Context, ident string, requiredMethod string) (*models.InternalObject, error) {
	if strings.TrimSpace(ident) == "" {
		return nil, faults.ErrNotFound
	}

	checksumIdent := common.LooksLikeSHA256(ident)
	if checksumIdent {
		if obj, found, err := m.lookupObjectByChecksum(ctx, ident, requiredMethod); err != nil {
			return nil, err
		} else if found {
			return obj, nil
		}
	}

	if obj, found, err := m.lookupObjectByID(ctx, ident); err != nil {
		return nil, err
	} else if found {
		return m.canonicalContentAndCheckAccess(ctx, obj, requiredMethod)
	}

	if obj, found, err := m.lookupObjectByAlias(ctx, ident); err != nil {
		return nil, err
	} else if found {
		return m.canonicalContentAndCheckAccess(ctx, obj, requiredMethod)
	}

	if !checksumIdent {
		if obj, found, err := m.lookupObjectByChecksum(ctx, ident, requiredMethod); err != nil {
			return nil, err
		} else if found {
			return obj, nil
		}
	}

	return nil, faults.ErrNotFound
}

func (m *ObjectManager) lookupObjectByChecksum(ctx context.Context, ident string, requiredMethod string) (*models.InternalObject, bool, error) {
	byChecksum, err := m.GetObjectsByChecksum(ctx, ident, requiredMethod)
	if err != nil {
		return nil, false, err
	}
	if len(byChecksum) == 0 {
		if strings.TrimSpace(requiredMethod) != "" {
			allMatches, err := m.GetObjectsByChecksum(ctx, ident, "")
			if err != nil {
				return nil, false, err
			}
			if len(allMatches) > 0 {
				return nil, true, faults.ErrUnauthorized
			}
		}
		return nil, false, nil
	}
	return &byChecksum[0], true, nil
}

func (m *ObjectManager) lookupObjectByID(ctx context.Context, ident string) (*models.InternalObject, bool, error) {
	obj, err := m.db.GetObject(ctx, ident)
	if err == nil {
		return obj, true, nil
	}
	if faults.IsNotFoundError(err) {
		return nil, false, nil
	}
	return nil, false, err
}

func (m *ObjectManager) lookupObjectByAlias(ctx context.Context, ident string) (*models.InternalObject, bool, error) {
	canonicalID, aliasErr := m.db.ResolveObjectAlias(ctx, ident)
	if aliasErr != nil {
		if faults.IsNotFoundError(aliasErr) {
			return nil, false, nil
		}
		return nil, false, aliasErr
	}
	if strings.TrimSpace(canonicalID) == "" {
		return nil, false, nil
	}

	obj, err := m.db.GetObject(ctx, canonicalID)
	if err != nil {
		if faults.IsNotFoundError(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	return obj, true, nil
}

func (m *ObjectManager) canonicalContentAndCheckAccess(ctx context.Context, obj *models.InternalObject, method string) (*models.InternalObject, error) {
	canonical, err := m.canonicalContentForObject(ctx, obj)
	if err != nil {
		return nil, err
	}
	if err := m.requireObjectMethod(ctx, canonical, method); err != nil {
		return nil, err
	}
	return canonical, nil
}

func (m *ObjectManager) canonicalContentForObject(ctx context.Context, obj *models.InternalObject) (*models.InternalObject, error) {
	sha, ok := common.CanonicalSHA256(obj.Checksums)
	if !ok {
		cloned := cloneObject(*obj)
		return &cloned, nil
	}
	siblings, err := m.db.GetObjectsByChecksum(ctx, sha)
	if err != nil {
		return nil, err
	}
	canonical := canonicalizeContentObjects(objectsWithSHA256(siblings, sha))
	if len(canonical) == 0 {
		return nil, faults.ErrNotFound
	}
	return &canonical[0], nil
}

func (m *ObjectManager) GetObjectsByChecksums(ctx context.Context, hashes []string, requiredMethod string) (map[string][]models.InternalObject, error) {
	objectsByChecksum, err := m.db.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	filtered := make(map[string][]models.InternalObject, len(objectsByChecksum))
	for checksum, objects := range objectsByChecksum {
		matching := objectsWithSHA256(objects, checksum)
		filtered[checksum] = m.filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod)
	}
	return filtered, nil
}

func (m *ObjectManager) GetObjectsByChecksum(ctx context.Context, checksum string, requiredMethod string) ([]models.InternalObject, error) {
	objects, err := m.db.GetObjectsByChecksum(ctx, checksum)
	if err != nil {
		return nil, err
	}
	matching := objectsWithSHA256(objects, checksum)
	return m.filterObjectsByMethod(ctx, canonicalizeContentObjects(matching), requiredMethod), nil
}

func (m *ObjectManager) GetBulkObjects(ctx context.Context, ids []string, requiredMethod string) ([]models.InternalObject, error) {
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(objects))
	for _, obj := range objects {
		if sha, ok := common.CanonicalSHA256(obj.Checksums); ok {
			hashes = append(hashes, sha)
		}
	}
	siblingsByChecksum, err := m.db.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	canonical := make([]models.InternalObject, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		resolved := cloneObject(obj)
		if sha, ok := common.CanonicalSHA256(obj.Checksums); ok {
			matching := objectsWithSHA256(siblingsByChecksum[sha], sha)
			family := canonicalizeContentObjects(matching)
			if len(family) == 0 {
				return nil, faults.ErrNotFound
			}
			resolved = family[0]
		}
		if _, ok := seen[resolved.Id]; ok {
			continue
		}
		seen[resolved.Id] = struct{}{}
		canonical = append(canonical, resolved)
	}
	return m.filterObjectsByMethod(ctx, canonical, requiredMethod), nil
}

func (m *ObjectManager) GetPreparedScopedObjects(ctx context.Context, ids []string, organization, project, requiredMethod string) ([]models.InternalObject, error) {
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return m.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
}

func (m *ObjectManager) PrepareScopedObjects(ctx context.Context, objects []models.InternalObject, organization, project, requiredMethod string) ([]models.InternalObject, error) {
	started := time.Now()
	expanded, err := m.expandProjectChecksumSiblingObjects(ctx, objects, organization, project)
	if err != nil {
		return nil, err
	}
	filtered := m.filterObjectsByMethod(ctx, expanded, requiredMethod)
	canonicalStart := time.Now()
	canonical := canonicalizeProjectScopedObjects(filtered, organization, project)
	log.Printf("INFO: syfon_prepare_scoped_objects organization=%s project=%s input_count=%d expanded_count=%d filtered_count=%d output_count=%d canonicalize_scoped_objects_ms=%d duration_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), len(objects), len(expanded), len(filtered), len(canonical), time.Since(canonicalStart).Milliseconds(), time.Since(started).Milliseconds())
	return canonical, nil
}

func (m *ObjectManager) ListPreparedObjectsPageByScope(ctx context.Context, organization, project, requiredMethod, startAfter string, limit, offset int) ([]models.InternalObject, error) {
	if limit <= 0 {
		return []models.InternalObject{}, nil
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
	collected := make([]models.InternalObject, 0, target)
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
			if startAfter != "" && obj.Id <= startAfter {
				continue
			}
			if _, ok := seen[obj.Id]; ok {
				continue
			}
			seen[obj.Id] = struct{}{}
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
		return []models.InternalObject{}, nil
	}
	end := skip + limit
	if end > len(collected) {
		end = len(collected)
	}
	out := collected[skip:end]
	log.Printf("INFO: syfon_list_prepared_objects_page_by_scope organization=%s project=%s start_after=%t limit=%d offset=%d raw_pages=%d raw_ids=%d records=%d duration_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), startAfter != "", limit, offset, rawPages, rawIDs, len(out), time.Since(started).Milliseconds())
	return out, nil
}

func (m *ObjectManager) ListObjectIDsPageByChecksum(ctx context.Context, checksum, checksumType, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	var objects []models.InternalObject
	if strings.TrimSpace(organization) != "" || strings.TrimSpace(project) != "" {
		raw, err := m.db.GetObjectsByChecksum(ctx, checksum)
		if err != nil {
			return nil, err
		}
		scoped := make([]models.InternalObject, 0, len(raw))
		for _, obj := range raw {
			if objectMatchesScope(&obj, organization, project) {
				scoped = append(scoped, obj)
			}
		}
		filtered := m.filterObjectsByMethod(ctx, scoped, requiredMethod)
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
		if checksumType != "" && !common.ObjectHasChecksumTypeAndValue(obj, checksumType, checksum) {
			continue
		}
		if strings.TrimSpace(organization) != "" && !objectMatchesScope(&obj, organization, project) {
			continue
		}
		ids = append(ids, obj.Id)
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

func (m *ObjectManager) ListObjectIDsPageByScope(ctx context.Context, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}

	if lister, ok := m.db.(db.ObjectIDPageLister); ok && canUseUnrestrictedScopePage(ctx, requiredMethod) {
		pageStart := time.Now()
		ids, err := lister.ListObjectIDsPageByScope(ctx, organization, project, startAfter, limit, offset)
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

func (m *ObjectManager) ListObjectIDsPageByURL(ctx context.Context, objectURL, organization, project, requiredMethod, startAfter string, limit, offset int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	objectURL = strings.TrimSpace(objectURL)
	if objectURL == "" {
		return []string{}, nil
	}
	if lister, ok := m.db.(db.ObjectURLPageLister); ok {
		resources, includeUnscoped, restrictToResources := objectMethodResourceFilter(ctx, requiredMethod)
		if access.IsGen3Mode(ctx) && access.IsAuthzEnforced(ctx) && !access.HasAuthHeader(ctx) {
			return []string{}, nil
		}
		return lister.ListObjectIDsPageByURL(ctx, objectURL, organization, project, startAfter, limit, offset, resources, includeUnscoped, restrictToResources)
	}

	ids, err := m.ListObjectIDsByScope(ctx, organization, project, requiredMethod)
	if err != nil {
		return nil, err
	}
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(objects))
	for _, obj := range objects {
		if objectHasAccessURL(&obj, objectURL) {
			out = append(out, obj.Id)
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

func (m *ObjectManager) ListObjectIDsByScope(ctx context.Context, organization, project string, requiredMethod string) ([]string, error) {
	if strings.TrimSpace(organization) == "" && strings.EqualFold(strings.TrimSpace(requiredMethod), objectMethodRead) {
		if ids, ok, err := m.listReadableObjectIDs(ctx); ok || err != nil {
			return ids, err
		}
	}
	listStart := time.Now()
	ids, err := m.db.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	log.Printf("INFO: syfon_list_object_ids_by_scope organization=%s project=%s ids=%d list_scope_ids_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), len(ids), time.Since(listStart).Milliseconds())
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered, err := m.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(filtered))
	for _, obj := range filtered {
		out = append(out, obj.Id)
	}
	return out, nil
}

func (m *ObjectManager) ListObjectsByScope(ctx context.Context, organization, project, requiredMethod string) ([]models.InternalObject, error) {
	if strings.TrimSpace(organization) == "" && strings.EqualFold(strings.TrimSpace(requiredMethod), objectMethodRead) {
		if ids, ok, err := m.listReadableObjectIDs(ctx); ok {
			if err != nil {
				return nil, err
			}
			objects, err := m.db.GetBulkObjects(ctx, ids)
			if err != nil {
				return nil, err
			}
			return m.filterObjectsByMethod(ctx, objects, requiredMethod), nil
		}
	}
	ids, err := m.db.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return m.PrepareScopedObjects(ctx, objects, organization, project, requiredMethod)
}

// ListPhysicalObjectsByScope returns each stored object row in a project scope.
// Callers that repair physical access methods need the row identity and methods
// without the same-checksum canonical merge used by normal reads.
func (m *ObjectManager) ListPhysicalObjectsByScope(ctx context.Context, organization, project, requiredMethod string) ([]models.InternalObject, error) {
	ids, err := m.db.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	return m.filterObjectsByMethod(ctx, objects, requiredMethod), nil
}

// ListMissingScopedSHA256 returns the requested SHA-256 checksums that are not
// registered for the given project. It deliberately uses the indexed checksum
// lookup and does not hydrate complete DRS records or access methods.
func (m *ObjectManager) ListMissingScopedSHA256(ctx context.Context, organization, project string, checksums []string) ([]string, error) {
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization == "" || project == "" || len(checksums) == 0 {
		return nil, faults.ErrUnauthorized
	}
	if err := m.requireScopeMethod(ctx, organization, project, objectMethodRead); err != nil {
		return nil, err
	}

	existingByChecksum, err := m.db.ListScopedObjectIDsByChecksums(ctx, organization, project, checksums)
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

func (m *ObjectManager) expandProjectChecksumSiblingObjects(ctx context.Context, objects []models.InternalObject, organization, project string) ([]models.InternalObject, error) {
	if len(objects) == 0 {
		return []models.InternalObject{}, nil
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
		sha, _ := common.CanonicalSHA256(obj.Checksums)
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
	idsByChecksum, err := m.db.ListScopedObjectIDsByChecksums(ctx, organization, project, checksums)
	if err != nil {
		return nil, err
	}
	listDuration := time.Since(listStart)

	expanded := make([]models.InternalObject, 0, len(objects))
	seenIDs := make(map[string]struct{}, len(objects))
	missingIDs := make([]string, 0)
	missingSeen := make(map[string]struct{})
	for _, obj := range objects {
		if _, seen := seenIDs[obj.Id]; seen {
			continue
		}
		seenIDs[obj.Id] = struct{}{}
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
		siblings, err := m.db.GetBulkObjects(ctx, missingIDs)
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
			if _, seen := seenIDs[obj.Id]; seen {
				continue
			}
			seenIDs[obj.Id] = struct{}{}
			expanded = append(expanded, obj)
		}
	}
	hydrateDuration := time.Since(hydrateStart)
	log.Printf("INFO: syfon_expand_scoped_checksum_siblings organization=%s project=%s checksums=%d input_count=%d missing_ids=%d output_count=%d list_scoped_sibling_ids_ms=%d hydrate_missing_siblings_ms=%d", strings.TrimSpace(organization), strings.TrimSpace(project), len(checksums), len(objects), len(missingIDs), len(expanded), listDuration.Milliseconds(), hydrateDuration.Milliseconds())
	return expanded, nil
}

func objectHasAccessURL(obj *models.InternalObject, objectURL string) bool {
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

func (m *ObjectManager) listReadableObjectIDs(ctx context.Context) ([]string, bool, error) {
	lister, ok := m.db.(db.ObjectIDResourceLister)
	if !ok || !access.IsAuthzEnforced(ctx) {
		return nil, false, nil
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return []string{}, true, nil
	}

	resources := readableResources(ctx)
	ids, err := lister.ListObjectIDsByResources(ctx, resources, true)
	return ids, true, err
}

func (m *ObjectManager) listReadableObjectIDsPage(ctx context.Context, startAfter string, limit, offset int) ([]string, bool, error) {
	pager, ok := m.db.(db.ObjectIDPageLister)
	if !ok || !access.IsAuthzEnforced(ctx) {
		return nil, false, nil
	}
	if access.IsGen3Mode(ctx) && !access.HasAuthHeader(ctx) {
		return []string{}, true, nil
	}

	resources := readableResources(ctx)
	ids, err := pager.ListObjectIDsPageByResources(ctx, resources, true, startAfter, limit, offset)
	return ids, true, err
}

func (m *ObjectManager) canPageScopeRead(ctx context.Context, organization, project string) bool {
	if !access.IsAuthzEnforced(ctx) {
		return true
	}
	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return false
	}
	return access.HasMethodAccess(ctx, objectMethodRead, []string{resource})
}

func readableResources(ctx context.Context) []string {
	return authorizedResources(ctx, objectMethodRead)
}

func (m *ObjectManager) readableChecksumFilter(ctx context.Context, organization, project string) ([]string, bool, bool, bool) {
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
		return syfoncommon.NormalizeAccessResources(access.GetUserAuthz(ctx))
	}
	resources := make([]string, 0, len(privileges))
	for resource, methods := range privileges {
		if methods[method] || methods["*"] {
			resources = append(resources, resource)
		}
	}
	return syfoncommon.NormalizeAccessResources(resources)
}

func (m *ObjectManager) authorizedChecksumIDs(ctx context.Context, checksum, requiredMethod string) ([]string, bool, error) {
	lister, ok := m.db.(db.ObjectAuthorizedLister)
	if !ok {
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

func objectMatchesScope(obj *models.InternalObject, organization, project string) bool {
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

func (m *ObjectManager) filterObjectsByMethod(ctx context.Context, objects []models.InternalObject, method string) []models.InternalObject {
	if strings.TrimSpace(method) == "" {
		return objects
	}
	filtered := make([]models.InternalObject, 0, len(objects))
	for _, obj := range objects {
		if m.hasObjectMethod(ctx, &obj, method) {
			filtered = append(filtered, obj)
		}
	}
	return filtered
}
