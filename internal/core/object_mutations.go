package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/server/drs"
	syfoncommon "github.com/calypr/syfon/common"
	"github.com/calypr/syfon/internal/access"
	"github.com/calypr/syfon/internal/common"
	"github.com/calypr/syfon/internal/db"
	"github.com/calypr/syfon/internal/faults"
	"github.com/calypr/syfon/internal/models"
)

const maxDeniedAccessResources = 25

// RegisterBulk saves multiple internal objects as a single logical operation.
func (m *ObjectManager) RegisterBulk(ctx context.Context, candidates []drs.DrsObjectCandidate) (int, error) {
	now := time.Now().UTC()
	toRegister := make([]models.InternalObject, 0, len(candidates))
	for _, c := range candidates {
		obj, err := CandidateToInternalObject(c, now)
		if err != nil {
			return 0, err
		}
		toRegister = append(toRegister, obj)
	}

	if err := m.RegisterObjects(ctx, toRegister); err != nil {
		return 0, err
	}
	return len(toRegister), nil
}

// DeleteBulkByScope removes all objects matching an organization/project scope after verifying permissions.
func (m *ObjectManager) DeleteBulkByScope(ctx context.Context, organization, project string) (int, error) {
	if err := m.requireScopeMethod(ctx, organization, project, objectMethodDelete); err != nil {
		return 0, err
	}

	ids, err := m.db.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	if lister, ok := m.db.(db.ObjectAuthorizedLister); ok {
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

	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return 0, err
	}
	return m.db.RemoveObjectControlledAccessBulk(ctx, toDelete, resource)
}

func (m *ObjectManager) DeleteObject(ctx context.Context, id string) error {
	return m.DeleteObjectWithOptions(ctx, id, DeleteOptions{})
}

type DeleteOptions struct {
	DeleteStorageData bool
}

func (m *ObjectManager) DeleteObjectWithOptions(ctx context.Context, id string, opts DeleteOptions) error {
	if opts.DeleteStorageData {
		return fmt.Errorf("%w: physical storage deletion is not atomic with catalog mutation", faults.ErrConflict)
	}
	obj, err := m.db.GetObject(ctx, id)
	if err != nil {
		return err
	}
	if err := m.requireAllObjectMethod(ctx, obj, objectMethodDelete); err != nil {
		return err
	}
	if opts.DeleteStorageData && (obj.PublicRead || len(ObjectAccessResources(obj)) > 0) {
		return fmt.Errorf("%w: cannot delete shared content storage without exclusive ownership", faults.ErrConflict)
	}
	return m.db.DeleteObject(ctx, id)
}

func (m *ObjectManager) BulkDeleteObjects(ctx context.Context, ids []string) error {
	return m.BulkDeleteObjectsWithOptions(ctx, ids, DeleteOptions{})
}

func (m *ObjectManager) BulkDeleteObjectsWithOptions(ctx context.Context, ids []string, opts DeleteOptions) error {
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
	return m.db.BulkDeleteObjects(ctx, toDelete)
}

func (m *ObjectManager) deletablePhysicalObjectIDsForBulk(ctx context.Context, ids []string) ([]string, error) {
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*models.InternalObject, len(objects))
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
			canonicalID, resolveErr := m.db.ResolveObjectAlias(ctx, objectID)
			if resolveErr == nil && strings.TrimSpace(canonicalID) != "" {
				return nil, fmt.Errorf("%w: bulk delete requires a physical object UUID; %q is an alias for %q", faults.ErrConflict, objectID, strings.TrimSpace(canonicalID))
			}
			if resolveErr != nil && !errors.Is(resolveErr, faults.ErrNotFound) {
				return nil, resolveErr
			}
			continue
		}
		if !m.hasObjectMethod(ctx, obj, objectMethodDelete) {
			continue
		}
		if err := m.requireAllObjectMethod(ctx, obj, objectMethodDelete); err != nil {
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

func (m *ObjectManager) UpdateObjectAccessMethods(ctx context.Context, objectID string, accessMethods []drs.AccessMethod) error {
	obj, err := m.db.GetObject(ctx, objectID)
	if err != nil {
		return err
	}
	if err := m.requireAllObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return err
	}
	return m.db.UpdateObjectAccessMethods(ctx, objectID, accessMethods)
}

func (m *ObjectManager) BulkUpdateAccessMethods(ctx context.Context, updates map[string][]drs.AccessMethod) error {
	if len(updates) == 0 {
		return nil
	}

	ids := make([]string, 0, len(updates))
	for objectID := range updates {
		ids = append(ids, objectID)
	}
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return err
	}
	byID := make(map[string]*models.InternalObject, len(objects))
	for i := range objects {
		byID[objects[i].Id] = &objects[i]
	}
	for _, objectID := range ids {
		obj, ok := byID[objectID]
		if !ok {
			return faults.ErrNotFound
		}
		if err := m.requireAllObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
			return err
		}
	}
	return m.db.BulkUpdateAccessMethods(ctx, updates)
}

func (m *ObjectManager) RemoveObjectControlledAccess(ctx context.Context, objectID, resource string) (*models.InternalObject, error) {
	obj, err := m.db.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	if err := m.requireObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return nil, err
	}

	normalized := syfoncommon.NormalizeAccessResources([]string{resource})
	if len(normalized) == 0 {
		return nil, fmt.Errorf("resource is required")
	}
	resource = normalized[0]

	resources := ObjectAccessResources(obj)
	found := false
	for _, existing := range resources {
		if strings.TrimSpace(existing) == resource {
			found = true
		}
	}
	if !found {
		return nil, faults.ErrNotFound
	}

	if err := m.db.RemoveObjectControlledAccess(ctx, objectID, resource); err != nil {
		return nil, err
	}

	updated, err := m.db.GetObject(ctx, objectID)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

func (m *ObjectManager) RegisterObjects(ctx context.Context, objs []models.InternalObject) error {
	if err := m.validateExistingContentRead(ctx, objs); err != nil {
		return err
	}
	if err := m.bulkObjectMethodError(ctx, objs, objectMethodCreate); err != nil {
		return err
	}
	return m.db.RegisterObjects(ctx, objs)
}

func (m *ObjectManager) validateExistingContentRead(ctx context.Context, objs []models.InternalObject) error {
	seen := make(map[string]struct{})
	for i := range objs {
		sha, ok := common.CanonicalSHA256(objs[i].Checksums)
		if !ok || sha == "" {
			continue
		}
		if _, done := seen[sha]; done {
			continue
		}
		seen[sha] = struct{}{}
		existing, err := m.db.GetObjectsByChecksum(ctx, sha)
		if err != nil {
			return err
		}
		for j := range existing {
			if existing[j].PublicRead || m.hasObjectMethod(ctx, &existing[j], objectMethodRead) {
				continue
			}
			return faults.ErrUnauthorized
		}
	}
	return nil
}

func (m *ObjectManager) CollapseProjectChecksumDuplicates(ctx context.Context, organization, project string) (int, error) {
	ids, err := m.db.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return 0, err
	}
	if err := m.bulkObjectMethodError(ctx, objects, objectMethodUpdate); err != nil {
		return 0, err
	}

	grouped := make(map[string][]models.InternalObject)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, "")
		if !ok {
			continue
		}
		grouped[key] = append(grouped[key], cloneObject(obj))
	}

	merged := make([]models.InternalObject, 0, len(grouped))
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
			aliasMap[obj.Id] = canonical.Id
			toDelete = append(toDelete, obj.Id)
		}
	}

	if len(merged) == 0 {
		return 0, nil
	}
	if err := m.db.RegisterObjects(ctx, merged); err != nil {
		return 0, err
	}
	for aliasID, canonicalID := range aliasMap {
		if err := m.db.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
			return 0, err
		}
	}
	if err := m.db.BulkDeleteObjects(ctx, uniqueStrings(toDelete)); err != nil {
		return 0, err
	}
	return len(aliasMap), nil
}

func (m *ObjectManager) canonicalizeRegistrationObjects(ctx context.Context, objs []models.InternalObject) ([]models.InternalObject, map[string]string, error) {
	if len(objs) == 0 {
		return nil, nil, nil
	}

	checksums := make([]string, 0, len(objs))
	seenChecksums := make(map[string]struct{}, len(objs))
	for _, obj := range objs {
		if sha, ok := common.CanonicalSHA256(obj.Checksums); ok {
			if _, seen := seenChecksums[sha]; seen {
				continue
			}
			seenChecksums[sha] = struct{}{}
			checksums = append(checksums, sha)
		}
	}

	existingByChecksum, err := m.db.GetObjectsByChecksums(ctx, checksums)
	if err != nil {
		return nil, nil, err
	}

	type registrationGroup struct {
		existingCount int
		objects       []models.InternalObject
	}
	groups := make(map[string]*registrationGroup)
	passthrough := make([]models.InternalObject, 0)

	for _, obj := range objs {
		sha, ok := common.CanonicalSHA256(obj.Checksums)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		resources := projectScopeResources(&obj)
		if len(resources) != 1 {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		key := resources[0] + "|" + sha
		group, ok := groups[key]
		if !ok {
			group = &registrationGroup{}
			for _, existing := range existingByChecksum[sha] {
				existingKey, ok := canonicalProjectChecksumKey(&existing, "")
				if ok && existingKey == key {
					group.objects = append(group.objects, cloneObject(existing))
				}
			}
			group.existingCount = len(group.objects)
			groups[key] = group
		}
		group.objects = append(group.objects, cloneObject(obj))
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	merged := make([]models.InternalObject, 0, len(keys)+len(passthrough))
	aliasMap := make(map[string]string)

	for _, key := range keys {
		state := groups[key]
		group := state.objects
		if len(group) == 0 {
			continue
		}

		hasExisting := state.existingCount > 0
		canonical := cloneObject(group[0])
		latest := canonical
		if hasExisting {
			canonical = cloneObject(group[0])
		}
		for i, obj := range group {
			if hasExisting && i < state.existingCount && canonicalObjectNewer(obj, canonical) {
				canonical = cloneObject(obj)
			}
			if canonicalObjectNewer(obj, latest) {
				latest = cloneObject(obj)
			}
		}
		if !hasExisting {
			canonical = cloneObject(latest)
		}

		collapsed := collapseCanonicalGroup(group)
		collapsed.Id = canonical.Id
		collapsed.SelfUri = "drs://" + canonical.Id
		collapsed.CreatedTime = canonical.CreatedTime
		collapsed.Name = latest.Name
		collapsed.NameAliases = common.NormalizeNameAliases(common.StringVal(collapsed.Name), append(collapsed.NameAliases, common.StringVal(canonical.Name)))
		merged = append(merged, collapsed)

		for _, obj := range group {
			if obj.Id == collapsed.Id || strings.TrimSpace(obj.Id) == "" {
				continue
			}
			aliasMap[obj.Id] = collapsed.Id
		}
	}

	merged = append(merged, passthrough...)
	return merged, aliasMap, nil
}

func (m *ObjectManager) ReplaceObjects(ctx context.Context, objs []models.InternalObject) error {
	return m.db.ReplaceObjects(ctx, objs)
}

func (m *ObjectManager) DeleteObjectsByChecksums(ctx context.Context, hashes []string) (int, error) {
	if lister, ok := m.db.(db.ObjectAuthorizedLister); ok {
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
			objects, err := m.db.GetBulkObjects(ctx, toDelete)
			if err != nil {
				return 0, err
			}
			authorized := make([]string, 0, len(objects))
			for i := range objects {
				if err := m.requireAllObjectMethod(ctx, &objects[i], objectMethodDelete); err != nil {
					continue
				}
				authorized = append(authorized, objects[i].Id)
			}
			if len(authorized) == 0 {
				return 0, nil
			}
			if err := m.db.BulkDeleteObjects(ctx, authorized); err != nil {
				return 0, err
			}
			return len(authorized), nil
		}
	}

	objectsByChecksum, err := m.db.GetObjectsByChecksums(ctx, hashes)
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
			if !m.hasObjectMethod(ctx, &obj, objectMethodDelete) {
				continue
			}
			if err := m.requireAllObjectMethod(ctx, &obj, objectMethodDelete); err != nil {
				continue
			}
			seen[obj.Id] = struct{}{}
			toDelete = append(toDelete, obj.Id)
		}
	}
	if len(toDelete) == 0 {
		return 0, nil
	}
	if err := m.db.BulkDeleteObjects(ctx, toDelete); err != nil {
		return 0, err
	}
	return len(toDelete), nil
}

func (m *ObjectManager) CreateObjectAlias(ctx context.Context, aliasID, canonicalID string) error {
	obj, err := m.db.GetObject(ctx, canonicalID)
	if err != nil {
		return err
	}
	if err := m.requireObjectMethod(ctx, obj, objectMethodUpdate); err != nil {
		return err
	}
	return m.db.CreateObjectAlias(ctx, aliasID, canonicalID)
}

func (m *ObjectManager) deletableObjectIDs(ctx context.Context, ids []string) ([]string, error) {
	return m.deletableObjectIDsForMethod(ctx, ids, true)
}

func (m *ObjectManager) deletableObjectIDsForMethod(ctx context.Context, ids []string, requireAll bool) ([]string, error) {
	objects, err := m.db.GetBulkObjects(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered := m.filterObjectsByMethod(ctx, objects, objectMethodDelete)
	toDelete := make([]string, 0, len(filtered))
	for _, obj := range filtered {
		if requireAll {
			if err := m.requireAllObjectMethod(ctx, &obj, objectMethodDelete); err != nil {
				continue
			}
		}
		toDelete = append(toDelete, obj.Id)
	}
	return toDelete, nil
}

func (m *ObjectManager) RequireObjectResources(ctx context.Context, method string, resources []string) error {
	if strings.TrimSpace(method) == "" {
		return nil
	}
	if access.HasObjectMethodAccess(ctx, method, resources) {
		return nil
	}
	return faults.ErrUnauthorized
}

func (m *ObjectManager) requireScopeMethod(ctx context.Context, organization, project, method string) error {
	resource, err := syfoncommon.ResourcePath(organization, project)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resource) == "" {
		return faults.ErrUnauthorized
	}
	return m.RequireObjectResources(ctx, method, []string{resource})
}

func (m *ObjectManager) requireObjectMethod(ctx context.Context, obj *models.InternalObject, method string) error {
	if m.hasObjectMethod(ctx, obj, method) {
		return nil
	}
	return faults.ErrUnauthorized
}

func (m *ObjectManager) requireAllObjectMethod(ctx context.Context, obj *models.InternalObject, method string) error {
	resources := ObjectAccessResources(obj)
	if len(resources) == 0 {
		return m.RequireObjectResources(ctx, method, resources)
	}
	if access.HasMethodAccess(ctx, method, resources) {
		return nil
	}
	return faults.ErrUnauthorized
}

func (m *ObjectManager) hasObjectMethod(ctx context.Context, obj *models.InternalObject, method string) bool {
	method = strings.TrimSpace(method)
	if method == "" {
		return true
	}
	if strings.EqualFold(method, objectMethodRead) && obj != nil && obj.PublicRead {
		return true
	}
	if strings.EqualFold(method, objectMethodRead) && obj != nil && obj.PublicReadPolicyKnown && len(ObjectAccessResources(obj)) == 0 {
		return false
	}
	return access.HasObjectMethodAccess(ctx, method, ObjectAccessResources(obj))
}

func (m *ObjectManager) bulkObjectMethodError(ctx context.Context, objs []models.InternalObject, method string) error {
	resources := make(map[string]struct{})
	var firstDeniedID string
	deniedRecords := 0
	for i := range objs {
		if m.hasObjectMethod(ctx, &objs[i], method) {
			continue
		}
		deniedRecords++
		if firstDeniedID == "" {
			firstDeniedID = objs[i].Id
		}
		for _, resource := range ObjectAccessResources(&objs[i]) {
			if strings.TrimSpace(resource) == "" {
				continue
			}
			resources[resource] = struct{}{}
		}
	}
	if deniedRecords == 0 {
		return nil
	}

	resourceList := make([]string, 0, len(resources))
	for resource := range resources {
		resourceList = append(resourceList, resource)
	}
	sort.Strings(resourceList)

	truncated := 0
	if len(resourceList) > maxDeniedAccessResources {
		truncated = len(resourceList) - maxDeniedAccessResources
		resourceList = resourceList[:maxDeniedAccessResources]
	}

	return &access.AuthorizationError{
		Method:             method,
		RecordID:           firstDeniedID,
		Resources:          resourceList,
		DeniedRecords:      deniedRecords,
		TotalRecords:       len(objs),
		TruncatedResources: truncated,
	}
}
