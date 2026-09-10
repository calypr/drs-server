package objects

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/calypr/syfon/apigen/drs"
	clientaccess "github.com/calypr/syfon/client/access"
	clienthash "github.com/calypr/syfon/client/hash"
)

func canonicalizeProjectScopedObjects(objects []drs.DrsObject, organization, project string, publicRead map[string]bool) []drs.DrsObject {
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

	grouped := make(map[string][]drs.DrsObject)
	passthrough := make([]drs.DrsObject, 0)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, forcedResource)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[key] = append(grouped[key], obj)
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]drs.DrsObject, 0, len(keys)+len(passthrough))
	for _, key := range keys {
		out = append(out, collapseCanonicalGroup(grouped[key], publicRead))
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

func canonicalProjectChecksumKey(obj *drs.DrsObject, forcedResource string) (string, bool) {
	if obj == nil {
		return "", false
	}
	sha, ok := CanonicalSHA256(obj.Checksums)
	if !ok || strings.TrimSpace(sha) == "" {
		return "", false
	}
	resource := strings.TrimSpace(forcedResource)
	if resource == "" {
		resources := AccessResources(obj)
		projectScopes := make([]string, 0, len(resources))
		for _, resource := range resources {
			org, project, ok := clientaccess.ResourceScope(resource)
			if !ok || strings.TrimSpace(org) == "" || strings.TrimSpace(project) == "" {
				continue
			}
			projectScopes = append(projectScopes, resource)
		}
		resources = clientaccess.NormalizeAccessResources(projectScopes)
		if len(resources) != 1 {
			return "", false
		}
		resource = resources[0]
	}
	return resource + "|" + sha, true
}

func canonicalizeContentObjects(objects []drs.DrsObject, publicRead map[string]bool) []drs.DrsObject {
	if len(objects) <= 1 {
		return cloneObjects(objects)
	}
	grouped := make(map[string][]drs.DrsObject)
	passthrough := make([]drs.DrsObject, 0)
	for _, obj := range objects {
		sha, ok := CanonicalSHA256(obj.Checksums)
		if !ok {
			passthrough = append(passthrough, cloneObject(obj))
			continue
		}
		grouped[sha] = append(grouped[sha], obj)
	}
	out := make([]drs.DrsObject, 0, len(grouped)+len(passthrough))
	for _, group := range grouped {
		out = append(out, collapseCanonicalGroup(group, publicRead))
	}
	out = append(out, passthrough...)
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out
}

func objectsWithSHA256(objects []drs.DrsObject, checksum string) []drs.DrsObject {
	target := clienthash.NormalizeOid(checksum)
	if target == "" {
		return objects
	}
	matched := make([]drs.DrsObject, 0, len(objects))
	for _, obj := range objects {
		sha, ok := CanonicalSHA256(obj.Checksums)
		if ok && sha == target {
			matched = append(matched, obj)
		}
	}
	return matched
}

func collapseCanonicalGroup(group []drs.DrsObject, publicRead map[string]bool) drs.DrsObject {
	if len(group) == 0 {
		return drs.DrsObject{}
	}
	canonical := group[0]
	latest := group[0]
	for i := 1; i < len(group); i++ {
		obj := group[i]
		created, canonicalCreated := obj.CreatedTime.UTC(), canonical.CreatedTime.UTC()
		if created.Before(canonicalCreated) || created.Equal(canonicalCreated) && obj.Id < canonical.Id {
			canonical = obj
		}
		when, latestWhen := canonicalObjectSortTime(obj), canonicalObjectSortTime(latest)
		if when.After(latestWhen) || when.Equal(latestWhen) && obj.Id > latest.Id {
			latest = obj
		}
	}

	merged := cloneObject(canonical)
	merged.Name = latest.Name
	merged.Size = pickLatestNonZeroSize(group, canonical.Size)
	merged.Description, merged.Version = pickLatestStrings(group, canonical.Description, canonical.Version)
	updated := canonicalObjectSortTime(latest)
	merged.UpdatedTime = &updated
	merged.Checksums = mergeChecksums(group)
	merged.AccessMethods = mergeAccessMethods(group)
	controlled, public := mergeControlledAccess(group, publicRead)
	publicRead[merged.Id] = public
	if len(controlled) > 0 {
		merged.ControlledAccess = &controlled
	} else {
		merged.ControlledAccess = nil
	}
	nameAliases := mergeNameAliases(merged.Name, group)
	if nameAliases != nil {
		merged.NameAliases = &nameAliases
	} else {
		merged.NameAliases = nil
	}
	merged.Aliases = mergeStringPointerValues(func(obj drs.DrsObject) []string {
		if obj.Aliases == nil {
			return nil
		}
		return *obj.Aliases
	}, group)
	merged.SelfUri = "drs://" + merged.Id
	return merged
}

func canonicalObjectSortTime(obj drs.DrsObject) time.Time {
	if obj.UpdatedTime != nil && !obj.UpdatedTime.IsZero() {
		return obj.UpdatedTime.UTC()
	}
	return obj.CreatedTime.UTC()
}

func cloneObjects(objects []drs.DrsObject) []drs.DrsObject {
	out := make([]drs.DrsObject, 0, len(objects))
	for _, obj := range objects {
		out = append(out, cloneObject(obj))
	}
	return out
}

func cloneObject(obj drs.DrsObject) drs.DrsObject {
	cloned := obj
	cloned.Checksums = append([]drs.Checksum(nil), obj.Checksums...)
	if obj.NameAliases != nil {
		nameAliases := append([]string(nil), (*obj.NameAliases)...)
		cloned.NameAliases = &nameAliases
	}
	if obj.AccessMethods != nil {
		methods := append([]drs.AccessMethod(nil), (*obj.AccessMethods)...)
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
	return cloned
}

func mergeChecksums(group []drs.DrsObject) []drs.Checksum {
	seen := make(map[string]struct{})
	merged := make([]drs.Checksum, 0)
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

func mergeAccessMethods(group []drs.DrsObject) *[]drs.AccessMethod {
	seen := make(map[string]struct{})
	methods := make([]drs.AccessMethod, 0)
	for _, obj := range group {
		if obj.AccessMethods == nil {
			continue
		}
		for _, method := range *obj.AccessMethods {
			url := ""
			if method.AccessUrl != nil {
				url = method.AccessUrl.Url
			}
			key := string(method.Type) + "|" + url
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			accessID := AccessMethodID(string(method.Type), url)
			method.AccessId = &accessID
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

func mergeControlledAccess(group []drs.DrsObject, publicRead map[string]bool) ([]string, bool) {
	resources := make([]string, 0)
	public := false
	for _, obj := range group {
		objectResources := AccessResources(&obj)
		if len(objectResources) == 0 {
			if value, known := publicRead[obj.Id]; known {
				public = public || value
			} else {
				public = true
			}
			continue
		}
		resources = append(resources, objectResources...)
	}
	for _, obj := range group {
		public = public || publicRead[obj.Id]
	}
	return clientaccess.NormalizeAccessResources(resources), public
}

func mergeNameAliases(primary *string, group []drs.DrsObject) []string {
	candidates := make([]string, 0)
	for _, obj := range group {
		if obj.Name != nil {
			candidates = append(candidates, *obj.Name)
		}
		if obj.NameAliases != nil {
			candidates = append(candidates, (*obj.NameAliases)...)
		}
	}
	primaryName := ""
	if primary != nil {
		primaryName = *primary
	}
	return NormalizeNameAliases(primaryName, candidates)
}

func pickLatestNonZeroSize(group []drs.DrsObject, fallback int64) int64 {
	best := fallback
	var bestTime time.Time
	bestID := ""
	for _, obj := range group {
		if obj.Size <= 0 {
			continue
		}
		when := canonicalObjectSortTime(obj)
		if best <= 0 || when.After(bestTime) || when.Equal(bestTime) && obj.Id > bestID {
			best = obj.Size
			bestTime = when
			bestID = obj.Id
		}
	}
	return best
}

func pickLatestStrings(group []drs.DrsObject, description, version *string) (*string, *string) {
	var descriptionTime, versionTime time.Time
	descriptionID, versionID := "", ""
	for _, obj := range group {
		when := canonicalObjectSortTime(obj)
		id := obj.Id
		if value := obj.Description; value != nil && strings.TrimSpace(*value) != "" && (description == nil || when.After(descriptionTime) || when.Equal(descriptionTime) && id > descriptionID) {
			trimmed := strings.TrimSpace(*value)
			description, descriptionTime, descriptionID = &trimmed, when, id
		}
		if value := obj.Version; value != nil && strings.TrimSpace(*value) != "" && (version == nil || when.After(versionTime) || when.Equal(versionTime) && id > versionID) {
			trimmed := strings.TrimSpace(*value)
			version, versionTime, versionID = &trimmed, when, id
		}
	}
	return description, version
}

func mergeStringPointerValues(getter func(drs.DrsObject) []string, group []drs.DrsObject) *[]string {
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

func (s *Service) CollapseProjectChecksumDuplicates(ctx context.Context, organization, project string) (int, error) {
	ids, err := s.store.ListObjectIDsByScope(ctx, organization, project)
	if err != nil {
		return 0, err
	}
	objects, err := s.store.GetBulkObjects(ctx, ids)
	if err != nil {
		return 0, err
	}
	if err := bulkObjectMethodError(ctx, objects, objectMethodUpdate, nil); err != nil {
		return 0, err
	}

	grouped := make(map[string][]drs.DrsObject)
	for _, obj := range objects {
		key, ok := canonicalProjectChecksumKey(&obj, "")
		if !ok {
			continue
		}
		grouped[key] = append(grouped[key], obj)
	}

	merged := make([]drs.DrsObject, 0, len(grouped))
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
		canonical := collapseCanonicalGroup(group, nil)
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
	if err := s.store.RegisterObjects(ctx, merged); err != nil {
		return 0, err
	}
	for aliasID, canonicalID := range aliasMap {
		if err := s.store.CreateObjectAlias(ctx, aliasID, canonicalID); err != nil {
			return 0, err
		}
	}
	if err := s.store.BulkDeleteObjects(ctx, uniqueOverwriteStrings(toDelete)); err != nil {
		return 0, err
	}
	return len(aliasMap), nil
}
