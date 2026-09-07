package records

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	objectmodel "github.com/calypr/syfon/internal/objects"

	clientaccess "github.com/calypr/syfon/client/access"
	clienthash "github.com/calypr/syfon/client/hash"
)

func recordStringPtr(value string) *string { return &value }

func recordStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func recordStringSliceValue(value *[]string) []string {
	if value == nil {
		return nil
	}
	return *value
}

func canonicalizeProjectScopedObjects(objects []objectmodel.Record, organization, project string) []objectmodel.Record {
	if len(objects) <= 1 {
		return cloneObjects(objects)
	}

	forcedResource := ""
	organization = strings.TrimSpace(organization)
	project = strings.TrimSpace(project)
	if organization != "" && project != "" {
		if resource, err := clientaccess.ResourcePath(organization, project); err == nil {
			forcedResource = resource
		}
	}

	grouped := make(map[string][]objectmodel.Record)
	passthrough := make([]objectmodel.Record, 0)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, forcedResource)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[key] = append(grouped[key], cloneObject(obj))
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]objectmodel.Record, 0, len(keys)+len(passthrough))
	for _, key := range keys {
		out = append(out, collapseCanonicalGroup(grouped[key]))
	}
	out = append(out, passthrough...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Id == out[j].Id {
			return canonicalObjectSortTime(out[i]).After(canonicalObjectSortTime(out[j]))
		}
		return out[i].Id < out[j].Id
	})
	return out
}

func canonicalProjectChecksumKey(obj *objectmodel.Record, forcedResource string) (string, bool) {
	if obj == nil {
		return "", false
	}
	sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
	if !ok || strings.TrimSpace(sha) == "" {
		return "", false
	}
	resource := strings.TrimSpace(forcedResource)
	if resource == "" {
		resources := projectScopeResources(obj)
		if len(resources) != 1 {
			return "", false
		}
		resource = resources[0]
	}
	return resource + "|" + sha, true
}

func projectScopeResources(obj *objectmodel.Record) []string {
	resources := objectmodel.AccessResources(obj)
	out := make([]string, 0, len(resources))
	for _, resource := range resources {
		org, project, ok := clientaccess.ResourceScope(resource)
		if !ok || strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
			continue
		}
		out = append(out, resource)
	}
	return clientaccess.NormalizeAccessResources(out)
}

func canonicalizeContentObjects(objects []objectmodel.Record) []objectmodel.Record {
	if len(objects) <= 1 {
		return cloneObjects(objects)
	}
	grouped := make(map[string][]objectmodel.Record)
	passthrough := make([]objectmodel.Record, 0)
	for _, obj := range objects {
		sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[sha] = append(grouped[sha], cloneObject(obj))
	}
	out := make([]objectmodel.Record, 0, len(grouped)+len(passthrough))
	for _, group := range grouped {
		out = append(out, collapseCanonicalGroup(group))
	}
	out = append(out, passthrough...)
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

func objectsWithSHA256(objects []objectmodel.Record, checksum string) []objectmodel.Record {
	target := clienthash.NormalizeOid(checksum)
	if target == "" {
		return objects
	}
	matched := make([]objectmodel.Record, 0, len(objects))
	for _, obj := range objects {
		sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
		if ok && sha == target {
			matched = append(matched, obj)
		}
	}
	return matched
}

func collapseCanonicalGroup(group []objectmodel.Record) objectmodel.Record {
	if len(group) == 0 {
		return objectmodel.Record{}
	}
	canonical := cloneObject(group[0])
	latest := cloneObject(group[0])
	for i := 1; i < len(group); i++ {
		obj := cloneObject(group[i])
		if canonicalObjectOlder(obj, canonical) {
			canonical = obj
		}
		if canonicalObjectNewer(obj, latest) {
			latest = obj
		}
	}

	merged := cloneObject(canonical)
	merged.Name = latest.Name
	merged.Size = pickLatestNonZeroSize(group, canonical.Size)
	merged.Description = pickLatestStringPtr(group, func(obj objectmodel.Record) *string { return obj.Description }, canonical.Description)
	merged.MimeType = pickLatestStringPtr(group, func(obj objectmodel.Record) *string { return obj.MimeType }, canonical.MimeType)
	merged.Version = pickLatestStringPtr(group, func(obj objectmodel.Record) *string { return obj.Version }, canonical.Version)
	updated := canonicalObjectSortTime(latest)
	merged.UpdatedTime = &updated
	merged.Checksums = mergeChecksums(group)
	merged.AccessMethods = mergeAccessMethods(group)
	controlled, public := mergeControlledAccess(group)
	merged.PublicRead = public
	for _, obj := range group {
		if obj.PublicReadPolicyKnown {
			merged.PublicReadPolicyKnown = true
			break
		}
	}
	if len(controlled) > 0 {
		merged.ControlledAccess = &controlled
		merged.Authorizations = clientaccess.ControlledAccessToAuthzMap(controlled)
	} else {
		merged.ControlledAccess = nil
		merged.Authorizations = nil
	}
	merged.NameAliases = mergeNameAliases(merged.Name, group)
	merged.Aliases = mergeStringPointerValues(func(obj objectmodel.Record) []string { return recordStringSliceValue(obj.Aliases) }, group)
	merged.SelfUri = "drs://" + string(merged.Id)
	return merged
}

func canonicalObjectOlder(a, b objectmodel.Record) bool {
	at := a.CreatedTime.UTC()
	bt := b.CreatedTime.UTC()
	if !at.Equal(bt) {
		return at.Before(bt)
	}
	return a.Id < b.Id
}

func canonicalObjectNewer(a, b objectmodel.Record) bool {
	at := canonicalObjectSortTime(a)
	bt := canonicalObjectSortTime(b)
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.Id > b.Id
}

func canonicalObjectSortTime(obj objectmodel.Record) time.Time {
	if obj.UpdatedTime != nil && !obj.UpdatedTime.IsZero() {
		return obj.UpdatedTime.UTC()
	}
	return obj.CreatedTime.UTC()
}

func cloneObjects(objects []objectmodel.Record) []objectmodel.Record {
	out := make([]objectmodel.Record, 0, len(objects))
	for _, obj := range objects {
		out = append(out, cloneObject(obj))
	}
	return out
}

func cloneObject(obj objectmodel.Record) objectmodel.Record {
	cloned := obj
	cloned.Checksums = append([]objectmodel.Checksum(nil), obj.Checksums...)
	cloned.NameAliases = append([]string(nil), obj.NameAliases...)
	if obj.AccessMethods != nil {
		methods := append([]objectmodel.AccessMethod(nil), (*obj.AccessMethods)...)
		cloned.AccessMethods = &methods
	}
	if obj.ControlledAccess != nil {
		controlled := append([]string(nil), (*obj.ControlledAccess)...)
		cloned.ControlledAccess = &controlled
	}
	if obj.Aliases != nil {
		aliases := append([]string(nil), (*obj.Aliases)...)
		cloned.Aliases = &aliases
	}
	if obj.Authorizations != nil {
		authz := make(map[string][]string, len(obj.Authorizations))
		for org, projects := range obj.Authorizations {
			authz[org] = append([]string(nil), projects...)
		}
		cloned.Authorizations = authz
	}
	if obj.Properties != nil {
		props := make(map[string]json.RawMessage, len(obj.Properties))
		for key, value := range obj.Properties {
			props[key] = value
		}
		cloned.Properties = props
	}
	return cloned
}

func mergeChecksums(group []objectmodel.Record) []objectmodel.Checksum {
	seen := make(map[string]struct{})
	merged := make([]objectmodel.Checksum, 0)
	for _, obj := range group {
		for _, checksum := range obj.Checksums {
			key := checksum.Type + "|" + checksum.Checksum
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, checksum)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Type == merged[j].Type {
			return merged[i].Checksum < merged[j].Checksum
		}
		return merged[i].Type < merged[j].Type
	})
	return merged
}

func mergeAccessMethods(group []objectmodel.Record) *[]objectmodel.AccessMethod {
	seen := make(map[string]struct{})
	methods := make([]objectmodel.AccessMethod, 0)
	for _, obj := range group {
		if obj.AccessMethods == nil {
			continue
		}
		for _, method := range *obj.AccessMethods {
			url := ""
			if method.AccessUrl != nil {
				url = method.AccessUrl.Url
			}
			key := method.Type + "|" + url
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			method.AccessId = recordStringPtr(objectmodel.AccessMethodID(method.Type, url))
			methods = append(methods, method)
		}
	}
	if len(methods) == 0 {
		return nil
	}
	sort.Slice(methods, func(i, j int) bool {
		iURL := ""
		jURL := ""
		if methods[i].AccessUrl != nil {
			iURL = methods[i].AccessUrl.Url
		}
		if methods[j].AccessUrl != nil {
			jURL = methods[j].AccessUrl.Url
		}
		if methods[i].Type == methods[j].Type {
			return iURL < jURL
		}
		return methods[i].Type < methods[j].Type
	})
	return &methods
}

func mergeControlledAccess(group []objectmodel.Record) ([]string, bool) {
	resources := make([]string, 0)
	public := false
	for _, obj := range group {
		objectResources := objectmodel.AccessResources(&obj)
		if len(objectResources) == 0 {
			public = public || obj.PublicRead || !obj.PublicReadPolicyKnown
			continue
		}
		resources = append(resources, objectResources...)
	}
	for _, obj := range group {
		public = public || obj.PublicRead
	}
	return clientaccess.NormalizeAccessResources(resources), public
}

func mergeNameAliases(primary *string, group []objectmodel.Record) []string {
	candidates := make([]string, 0)
	for _, obj := range group {
		if obj.Name != nil {
			candidates = append(candidates, *obj.Name)
		}
		candidates = append(candidates, obj.NameAliases...)
	}
	return objectmodel.NormalizeNameAliases(recordStringValue(primary), candidates)
}

func pickLatestNonZeroSize(group []objectmodel.Record, fallback int64) int64 {
	best := fallback
	var bestTime time.Time
	bestID := ""
	for _, obj := range group {
		if obj.Size <= 0 {
			continue
		}
		when := canonicalObjectSortTime(obj)
		if best <= 0 || when.After(bestTime) || when.Equal(bestTime) && string(obj.Id) > bestID {
			best = obj.Size
			bestTime = when
			bestID = string(obj.Id)
		}
	}
	return best
}

func pickLatestStringPtr(group []objectmodel.Record, getter func(objectmodel.Record) *string, fallback *string) *string {
	best := fallback
	var bestTime time.Time
	bestID := ""
	for _, obj := range group {
		value := getter(obj)
		if value == nil || strings.TrimSpace(*value) == "" {
			continue
		}
		when := canonicalObjectSortTime(obj)
		if best == nil || when.After(bestTime) || when.Equal(bestTime) && string(obj.Id) > bestID {
			trimmed := strings.TrimSpace(*value)
			best = &trimmed
			bestTime = when
			bestID = string(obj.Id)
		}
	}
	return best
}

func mergeStringPointerValues(getter func(objectmodel.Record) []string, group []objectmodel.Record) *[]string {
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, obj := range group {
		for _, value := range getter(obj) {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			values = append(values, trimmed)
		}
	}
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return &values
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}
func (m *mutationService) CollapseProjectChecksumDuplicates(ctx context.Context, organization, project string) (int, error) {
	ids, err := m.scope.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	objects, err := m.recordReader.GetBulkObjects(ctx, ids)
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
	if err := m.recordWriter.RegisterObjects(ctx, merged); err != nil {
		return 0, err
	}
	for aliasID, canonicalID := range aliasMap {
		if err := m.aliases.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
			return 0, err
		}
	}
	if err := m.recordWriter.BulkDeleteObjects(ctx, uniqueStrings(toDelete)); err != nil {
		return 0, err
	}
	return len(aliasMap), nil
}

func (m *mutationService) canonicalizeRegistrationObjects(ctx context.Context, objs []objectmodel.Record) ([]objectmodel.Record, map[string]string, error) {
	if len(objs) == 0 {
		return nil, nil, nil
	}

	checksums := make([]string, 0, len(objs))
	seenChecksums := make(map[string]struct{}, len(objs))
	for _, obj := range objs {
		if sha, ok := objectmodel.CanonicalSHA256(obj.Checksums); ok {
			if _, seen := seenChecksums[sha]; seen {
				continue
			}
			seenChecksums[sha] = struct{}{}
			checksums = append(checksums, sha)
		}
	}

	existingByChecksum, err := m.content.GetObjectsByChecksums(ctx, checksums)
	if err != nil {
		return nil, nil, err
	}

	type registrationGroup struct {
		existingCount int
		objects       []objectmodel.Record
	}
	groups := make(map[string]*registrationGroup)
	passthrough := make([]objectmodel.Record, 0)

	for _, obj := range objs {
		sha, ok := objectmodel.CanonicalSHA256(obj.Checksums)
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

	merged := make([]objectmodel.Record, 0, len(keys)+len(passthrough))
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
		collapsed.SelfUri = "drs://" + string(canonical.Id)
		collapsed.CreatedTime = canonical.CreatedTime
		collapsed.Name = latest.Name
		collapsed.NameAliases = objectmodel.NormalizeNameAliases(recordStringValue(collapsed.Name), append(collapsed.NameAliases, recordStringValue(canonical.Name)))
		merged = append(merged, collapsed)

		for _, obj := range group {
			if obj.Id == collapsed.Id || strings.TrimSpace(string(obj.Id)) == "" {
				continue
			}
			aliasMap[string(obj.Id)] = string(collapsed.Id)
		}
	}

	merged = append(merged, passthrough...)
	return merged, aliasMap, nil
}
