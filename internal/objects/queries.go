package objects

import (
	"context"
	"sort"
	"strings"

	"github.com/calypr/syfon/apigen/drs"
	"github.com/calypr/syfon/apigen/errorapi"
	clientaccess "github.com/calypr/syfon/client/access"
	"github.com/calypr/syfon/internal/access"
)

// LookupChecksumQueries preserves input order and duplicate queries.
func (s *Service) LookupChecksumQueries(ctx context.Context, queries []ChecksumQuery, requiredMethod string) ([][]drs.DrsObject, error) {
	values := make([]string, 0, len(queries))
	for _, query := range queries {
		_, value := ParseHashQuery(query.Value, query.Type)
		values = append(values, value)
	}
	objectsByChecksum, err := s.GetObjectsByChecksums(ctx, values, requiredMethod)
	if err != nil {
		return nil, err
	}

	matches := make([][]drs.DrsObject, 0, len(queries))
	for _, query := range queries {
		checksumType, checksum := ParseHashQuery(query.Value, query.Type)
		objects := objectsByChecksum[checksum]
		if checksumType != "" {
			filtered := make([]drs.DrsObject, 0, len(objects))
			for _, obj := range objects {
				if RecordHasChecksumTypeAndValue(obj, checksumType, checksum) {
					filtered = append(filtered, obj)
				}
			}
			objects = filtered
		}
		matches = append(matches, objects)
	}
	return matches, nil
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
	filtered, err := s.prepareScopedRecords(ctx, objects, Scope{Organization: organization, Project: project}, requiredMethod)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(filtered))
	for _, obj := range filtered {
		out = append(out, obj.Id)
	}
	return out, nil
}

// ListPhysicalObjectsByScope returns stored rows without checksum-family merging.
func (s *Service) ListPhysicalObjectsByScope(ctx context.Context, organization, project, requiredMethod string) ([]drs.DrsObject, error) {
	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return nil, err
	}
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	policy, err := s.publicReadPolicy(ctx, objects)
	if err != nil {
		return nil, err
	}
	return filterObjectsByMethod(ctx, objects, requiredMethod, policy), nil
}

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

func (s *Service) expandProjectChecksumSiblingObjects(ctx context.Context, objects []drs.DrsObject, organization, project string) ([]drs.DrsObject, error) {
	if len(objects) == 0 {
		return []drs.DrsObject{}, nil
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
	expanded := make([]drs.DrsObject, 0, len(objects))
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
			if _, seen := seenIDs[obj.Id]; seen {
				continue
			}
			seenIDs[obj.Id] = struct{}{}
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

	resources := authorizedResources(ctx, objectMethodRead)
	ids, err := s.store.ListObjectIDsByResources(ctx, resources, true)
	return ids, true, err
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

func objectMatchesScope(obj *drs.DrsObject, organization, project string) bool {
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

func filterObjectsByMethod(ctx context.Context, objects []drs.DrsObject, method string, publicRead map[string]bool) []drs.DrsObject {
	if strings.TrimSpace(method) == "" {
		return objects
	}
	filtered := make([]drs.DrsObject, 0, len(objects))
	for _, obj := range objects {
		if hasObjectMethod(ctx, &obj, method, publicRead) {
			filtered = append(filtered, obj)
		}
	}
	return filtered
}

func (s *Service) publicReadPolicy(ctx context.Context, objects []drs.DrsObject) (map[string]bool, error) {
	ids := make([]string, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		if obj.Id == "" {
			continue
		}
		if _, ok := seen[obj.Id]; ok {
			continue
		}
		seen[obj.Id] = struct{}{}
		ids = append(ids, obj.Id)
	}
	return s.store.GetPublicReadByIDs(ctx, ids)
}

// GetObject retrieves the canonical record identified by ID, alias, or checksum and validates access.
func (s *Service) GetObject(ctx context.Context, ident string, method string) (*drs.DrsObject, error) {
	if strings.TrimSpace(ident) == "" {
		return nil, errorapi.ErrObjectNotFound
	}
	checksum := NormalizeOID(ident)
	isSHA := checksum != ""
	if isSHA {
		family, err := s.getObjectsByChecksum(ctx, checksum, "")
		if err != nil {
			return nil, err
		}
		if len(family) > 0 {
			policy, err := s.publicReadPolicy(ctx, family)
			if err != nil {
				return nil, err
			}
			if !hasObjectMethod(ctx, &family[0], method, policy) {
				return nil, errorapi.ErrAccessDenied
			}
			return &family[0], nil
		}
	}
	record, err := s.store.GetObject(ctx, ident)
	if err != nil {
		if !errorapi.IsNotFoundError(err) {
			return nil, err
		}
		if isSHA {
			return nil, errorapi.ErrObjectNotFound
		}
		matches, err := s.getObjectsByChecksum(ctx, ident, "")
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, errorapi.ErrObjectNotFound
		}
		policy, err := s.publicReadPolicy(ctx, matches)
		if err != nil {
			return nil, err
		}
		readable := filterObjectsByMethod(ctx, matches, method, policy)
		if len(readable) == 0 {
			return nil, errorapi.ErrAccessDenied
		}
		record = &readable[0]
	}
	if sha, ok := CanonicalSHA256(record.Checksums); ok {
		family, err := s.getObjectsByChecksum(ctx, sha, "")
		if err != nil {
			return nil, err
		}
		if len(family) == 0 {
			return nil, errorapi.ErrObjectNotFound
		}
		record = &family[0]
	} else {
		copy := cloneObject(*record)
		record = &copy
	}
	policy, err := s.publicReadPolicy(ctx, []drs.DrsObject{*record})
	if err != nil {
		return nil, err
	}
	if !hasObjectMethod(ctx, record, method, policy) {
		return nil, errorapi.ErrAccessDenied
	}
	return record, nil
}

func (s *Service) GetObjectsByChecksums(ctx context.Context, hashes []string, requiredMethod string) (map[string][]drs.DrsObject, error) {
	objectsByChecksum, err := s.store.GetObjectsByChecksums(ctx, hashes)
	if err != nil {
		return nil, err
	}
	all := make([]drs.DrsObject, 0)
	for _, objects := range objectsByChecksum {
		all = append(all, objects...)
	}
	policy, err := s.publicReadPolicy(ctx, all)
	if err != nil {
		return nil, err
	}
	filtered := make(map[string][]drs.DrsObject, len(objectsByChecksum))
	for checksum, objects := range objectsByChecksum {
		matching := objectsWithSHA256(objects, checksum)
		filtered[checksum] = filterObjectsByMethod(ctx, canonicalizeContentObjects(matching, policy), requiredMethod, policy)
	}
	return filtered, nil
}

func (s *Service) getObjectsByChecksum(ctx context.Context, checksum string, requiredMethod string) ([]drs.DrsObject, error) {
	checksum = strings.TrimSpace(checksum)
	if checksum == "" {
		return []drs.DrsObject{}, nil
	}
	objectsByChecksum, err := s.GetObjectsByChecksums(ctx, []string{checksum}, requiredMethod)
	if err != nil {
		return nil, err
	}
	return objectsByChecksum[checksum], nil
}

func (s *Service) GetBulkObjects(ctx context.Context, ids []string, requiredMethod string) ([]drs.DrsObject, error) {
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
	all := append([]drs.DrsObject(nil), objects...)
	for _, siblings := range siblingsByChecksum {
		all = append(all, siblings...)
	}
	policy, err := s.publicReadPolicy(ctx, all)
	if err != nil {
		return nil, err
	}
	canonical := make([]drs.DrsObject, 0, len(objects))
	seen := make(map[string]struct{}, len(objects))
	for _, obj := range objects {
		resolved := cloneObject(obj)
		if sha, ok := CanonicalSHA256(obj.Checksums); ok {
			matching := objectsWithSHA256(siblingsByChecksum[sha], sha)
			family := canonicalizeContentObjects(matching, policy)
			if len(family) == 0 {
				return nil, errorapi.ErrObjectNotFound
			}
			resolved = family[0]
		}
		if _, ok := seen[resolved.Id]; ok {
			continue
		}
		seen[resolved.Id] = struct{}{}
		canonical = append(canonical, resolved)
	}
	return filterObjectsByMethod(ctx, canonical, requiredMethod, policy), nil
}
